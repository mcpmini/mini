---
name: review-pr
description: Adversarial multi-pass PR review — concurrency, security, correctness, tests, then conventions. Assumes bugs exist. Proves findings before reporting. Emits APPROVE / REQUEST CHANGES / REJECT verdict.
argument-hint: <PR-number, PR-URL, or blank for current branch diff>
---

Adversarial review of $ARGUMENTS (or the current branch diff if blank).

**Assume bugs exist. Your job is to find and prove them.**

Do not explain away suspicious patterns — investigate until you have proof or can definitively rule the issue out. Write tests if needed. If high-risk code is undertested, that alone can justify REJECT.

## Non-negotiables (apply to every pass)

1. **No unproven findings.** A suspicion is not a finding. Each pass defines a proof standard; a finding that doesn't meet it gets investigated further or dropped — never reported at HIGH or MEDIUM with "could/might/may" language.
2. **Re-verify before reporting.** For every finding, re-open the cited file at the cited line and confirm the code exists and says what you claim. A finding with a wrong line number or misquoted code is worse than no finding.
3. **Pick up check.sh results.** Do not write the report until the background check suite from Step 0 has finished and you have read its log.
4. **Verdict is mechanical.** Derive the verdict from the findings table using the rules at the end — never from overall impression.

## Step 0 — Gather the diff and check out the PR branch

1. Resolve the PR number from the arguments (`1` from `https://github.com/mcpmini/mini/pull/1`, from `#1`, or bare `1`). If the arguments are blank, review the current branch's diff against main in the current checkout and skip to step 5.
2. Get the PR description, diff, and full file list. Use GitHub through mini's MCP integration or the mini CLI when possible to dogfood this repository's tooling; otherwise fall back to the `gh` CLI.
3. Check out the PR head in a dedicated worktree so you review the PR's actual files (not the diff against your current branch) and run the check suite against the PR's code. If `.agents/worktrees/review-pr-<number>` already exists from a prior review, reuse it; otherwise:
   ```bash
   git fetch origin <head-branch>
   git worktree add .agents/worktrees/review-pr-<number> FETCH_HEAD --detach
   ```
   Detached HEAD works even if another worktree already has the branch checked out.
4. Enter the worktree — with Claude Code use `EnterWorktree(path: ".agents/worktrees/review-pr-<number>")`; otherwise run all subsequent commands from that directory.
5. Fire off the full check suite in the background (`run_in_background: true`) and note the log path, then continue immediately with Pass 1. It covers build, staticcheck, golangci-lint, function length, parameter count, return value checks, and the race-detector test suite. You will be notified when it finishes; pick up the results before writing the report — any failure introduced by the PR is a finding.
   ```bash
   ./check.sh 2>&1 | tee /tmp/review-pr-check-$(date +%s).log
   ```
6. Read every changed file **in full** — not just the diff hunks. A diff shows what changed; the full file shows what it interacts with and what invariants it relies on.

## Pass 1 — Triage

Scan the diff and changed files. Before investigating anything deeply, answer:

1. **Core change**: one sentence — what behavior is added, removed, or modified?
2. **Shared state**: what structs, maps, slices, channels, or package-level vars does the diff touch?
3. **Trust boundaries**: what new inputs arrive from outside (user, config, network, MCP tool args, env vars) and where do they land?
4. **New control paths**: what new error paths, goroutine launches, or auth checks does the change introduce?
5. **Candidate list**: for each of Passes 2a–2c, list specific things to investigate. Be precise — not "check locking" but "check whether `s.authFlows` reads on lines 45–47 are covered by `s.authMu`".
6. **Call-site audit** — for every function or method whose signature, parameters, return contract, or behavior changes in this diff, including new helper functions immediately wired into multiple places:
   a. Grep for *all* call sites — not just the ones visible in the diff hunks.
   b. List every call site explicitly with file:line.
   c. For each call site, note: what conditional/state context surrounds it, what value it produces for the changed parameter/contract under the new behavior, and whether that's correct for *this call site's* purpose.
   d. Carry any call site whose correctness is unclear into Pass 2c.

   A function correct for the call site the author had in mind can be wrong for a call site that existed before the change, or for a sibling call site added in the same diff.

Produce a brief triage note to drive Passes 2–4. Do not write it into the final report.

## Pass 2a — Concurrency

Check suite output from Step 0 already covers race tests, vet, and staticcheck. Work through every candidate from triage.

**Goroutine lifecycle**
- What stops it? A done-channel, context cancellation, or WaitGroup? `context.Background()` passed to a long-lived goroutine is a red flag — there is no way to cancel it, and leaks compound on every call.
- If it panics, is there a `recover`? An unrecovered panic in a goroutine kills the process.
- Does it hold resources (subprocess, listener, ticker, connection) that leak if it never exits?

**Shared state access**
- List every field of every shared struct the diff reads or writes. For each: is every access — including reads — inside the protecting mutex? "Usually protected" is not protected.
- Watch for: partially-protected structs; closures capturing outer mutable state.

**Map and slice concurrent access**
- Concurrent map read+write is a **hard fatal crash** (`concurrent map read and map write`), not a data race the detector catches at test time. Any shared map without full lock coverage on all read paths (including `range`) is a production crash waiting to happen.
- Slices are not crash-safe either — concurrent append on the same backing array corrupts memory.

**Lock discipline**
- If two mutexes are ever both held, is acquisition order consistent at every call site? Inconsistent order → deadlock.
- Are there channel sends, I/O, or external calls inside a held lock? That starves every other waiter.
- `RLock` → `Lock` upgrade on the same mutex in the same goroutine → deadlock.
- Large critical sections that acquire/release the same lock multiple times create race windows between releases.

**Channel safety**
- Sending to a closed channel panics. `select { case ch <- v: default: }` does NOT guard this — `default` fires when the channel is full, not when it is closed.
- Closing a channel must happen exactly once — `sync.Once` if multiple goroutines could close.
- `time.After` in a `select` loop leaks a timer goroutine on every iteration until the duration expires; prefer `time.NewTimer` with an explicit `Stop()`.

**TOCTOU**
- Check-then-act pairs where the check is inside a lock and the act is outside it.
- Two separately-locked operations (e.g. Remove then Add on a shared index) leave a window where concurrent observers see inconsistent state.

**`sync` primitives misuse**
- `WaitGroup.Add` must be called before the goroutine that calls `Done` is launched, not inside it — the goroutine may call `Done` before `Add` is observed.
- `sync.Once` that panics leaves the `Once` permanently poisoned; subsequent calls silently do nothing.
- Copying a `sync.Mutex`, `WaitGroup`, or `Cond` after first use is a bug (`go vet` catches this, but also check structs passed by value or appended to slices).

**Initialization races**
- Package-level variables mutated after `init` are shared global state — any goroutine touching them needs synchronization.
- Lazy initialization (`if x == nil { x = ... }`) without `sync.Once` or atomics is a data race.
- Constructors that start goroutines before returning: callers may not realize the object is "live" the moment `New()` returns.

**Smell test — quick scan for red flags** (grep for these, investigate any hit):
- `go func()` with no done-channel, no context, and no WaitGroup — orphaned goroutine
- `context.Background()` or `context.TODO()` inside a spawned goroutine or blocking call
- `sync.Mutex` in a struct that is passed by value
- `close(ch)` without `sync.Once` nearby
- `select { case ch <- v: default: }` near a `close(ch)` — `default` does not protect against send-on-closed
- Lock acquired, I/O performed, lock released — blocking call inside a lock
- Two mutexes acquired in the same function — verify ordering is consistent everywhere
- `time.Sleep` in production code — usually polling instead of proper signaling
- `atomic.Value` or `atomic.Pointer` storing a struct with pointer fields — the whole value must be swapped atomically; partial field updates still race
- `Close()` method without `sync.Once` — double-close panics channels, corrupts state
- `http.Server.Shutdown(context.Background())` — if any handler can block indefinitely (disabled tool timeout, hanging subprocess), the process never exits; always pass a bounded context
- RLock held across a network call — blocks reconnect from taking the write lock; snapshot the pointer under the lock, release, then call

**Blocking without guaranteed escape** — for every `select` that blocks on channels:
- Trace every code path that closes or sends to each case channel.
- Verify *all* equivalent calling paths — different transports, error returns, shutdown sequences, session eviction — eventually fire one of the cases. Example: a `select` on `done | abort | ctx.Done()` where `abort` is only closed in the stdio path blocks forever on HTTP after session eviction or daemon restart. The race detector will not catch this.
- Check for a deadline on the blocking context as a backstop even when the primary escape looks present.

**Asymmetric cleanup across equivalent paths** — when a lifecycle action (closing a channel, calling a cancel func, calling `markAborted`, setting a flag) runs in one handling path, grep for the equivalent action in every other path that shares the same lifecycle. If `serveLoop` (stdio) calls `markAborted()` on exit but the HTTP handler never does, every HTTP session is stuck after eviction or restart.

**Proof standard**: name the two goroutines, the shared variable, and the specific lock-release points that create the window. Not "could race" — "races when X and Y run concurrently because Z is not held during steps A–B."

## Pass 2b — Security

**Command injection**
- Any user-controlled string reaching `exec.Command`, `sh -c`, `os.Expand`, or string-concatenated into a shell invocation.
- User-controlled means: MCP tool args, config file values, HTTP headers, env vars set by external processes, OAuth redirect parameters.
- The question is: what is the trust model and does the code enforce it?

**Path traversal**
- User-controlled strings in `filepath.Join`, `os.Open`, `os.Create`, or similar. `filepath.Join` normalizes `..` but does not restrict to a base directory — check if the result is validated against the intended root.

**SSRF**
- User-controlled URLs passed to any HTTP client. Does the client use `SSRFSafeDialer` or equivalent?
- Does the HTTP client follow redirects? A redirect from a trusted host to an internal host bypasses allowlists.

**Auth/authz bypass**
- New code paths that reach protected operations: can they be reached without the required permission check?
- Does new code assume a caller has already been validated? Trace from the entry point, not from the guard.

**Secret exposure**
- Tokens, API keys, or user-controlled data written to logs or returned in error messages to callers.

**Crypto misuse**
- `math/rand` used where `crypto/rand` is required. Predictable state, nonce, or PKCE verifier values.

**Proof standard**: trace the data from its source to the dangerous sink, naming every function in the chain. Don't flag patterns that are unreachable or defended upstream.

## Pass 2c — Correctness

**Error handling**
- Errors assigned to `_` or silently ignored: are they genuinely safe to discard, or does ignoring them leave state inconsistent?
- Errors on partial writes or partial updates: if the operation fails mid-way, is the resulting state consistent and recoverable?
- Silent fallback to zero/nil values on failure — the caller proceeds as if nothing happened.

**Nil and zero-value hazards**
- Pointer dereferences without nil checks, especially on values from config, parsed input, or optional struct fields.
- Method calls on interface values that could be nil.
- `defer f.Close()` before a nil check on `f`.

**Logic correctness**
- Off-by-one in ranges, indices, string slicing.
- Wrong comparator (`<=` vs `<`, `!=` vs `==`, negated condition).
- Boundary behavior: empty slice, zero value, MaxInt, empty string, single element.
- **Read the doc string for every changed function and verify the implementation matches what it claims.** Mismatches here are common and dangerous.

**Resource lifecycle**
- `defer f.Close()` must come after the nil/error check. The pattern is: call → check error → defer close.
- HTTP response bodies: `defer resp.Body.Close()` after the nil check on `resp`.
- Connections, listeners, tickers, timers: a close on every exit path including error returns.

**Operational correctness**
- Timeout handling: what happens if a dependency is slow or permanently stuck? Is there a timeout? Does it propagate correctly through context?
- Graceful shutdown: are in-flight requests completed before exit? Are resources (connections, temp files, subprocesses) released?
- Retry logic: is it bounded? Does it back off? Does it retry non-retryable errors (e.g. 400 Bad Request)?
- Backpressure: under sustained load, does the system queue unboundedly? Does it shed load or return pressure to callers?
- Partial failure: if a multi-step operation fails halfway, is persistent state consistent? Is there a recovery path that doesn't require manual intervention?

**Design problems that cause bugs**
- State duplicated in two places that can drift out of sync — one gets updated and the other doesn't.
- Abstraction leaks that force callers to know implementation details: callers constructing internal state, ordering requirements not enforced by the type, "must call X before Y" contracts with no enforcement.
- API contracts easy to misuse: positional parameters where meaning is ambiguous, zero value that silently enables dangerous behavior, optional fields that interact in non-obvious ways.
- Coupling that prevents safe evolution: reloading one thing requires parsing everything; a config change in one package requires coordinated changes in three others.

**Proof standard**: for logic bugs, state the input that triggers the wrong behavior and the actual vs. expected outcome. For resource leaks, identify the specific exit path that skips the close.

## Pass 2d — Structure

**Skip this pass** if the diff is a small fix, a config change, or touches only test files. This pass is for diffs that introduce or modify abstractions: new types, new files, new multi-function flows, or significant restructuring.

Passes 2a–2c work bottom-up: read a function, evaluate it. This pass works top-down: map the flows first, then check whether the code's decomposition matches the domain. Each function can look correct in isolation while the overall decomposition is wrong — bottom-up reading cannot surface that.

**Prioritize this pass** when the diff shows these signals in a changed package:
- Functions with 4+ parameters (a missing domain type)
- Bare `func()` parameters or closure captures (a domain concept without a name)
- High comment density in non-test code (the code needs explaining because the abstractions are wrong)
- Tests with heavy setup boilerplate (boundaries are in the wrong place — testing one concept requires building another)

For each package the diff substantially changes:

1. **Map the flows and name the verbs.** Before reading any function body, trace every execution flow through the package — not just the changed code, but the full flows that pass through it. Name each flow and each action within it as a domain verb phrase ("resolve daemon", "forward request", "refresh expired token"). The verbs are the exercise: as you name actions across flows, the natural methods emerge. Verbs that share state belong on the same type. Verbs that don't share state belong on different types. A verb with no name in the code (inline logic, bare callback) is a missing abstraction. If you can't name a flow without using implementation terms, note that — it's already a signal.

2. **Derive the domain model.** Given the flows and verbs, if you were writing this from scratch, what types would you create? Group verbs by shared state into types. The gap between this blank-slate design and the actual code is the finding.

3. **Compare to actual structure.** Map each domain concept to the types, files, and functions that implement it. Flag:
   - **Mixed concerns**: a new or modified type handles verbs from multiple unrelated domain concepts. Test: changing concept A forces touching code that implements concept B.
   - **Missing abstractions**: bare callbacks, anonymous functions, or inline logic where the flows show a named domain concept. Especially: `func()` parameters or closure captures that represent a real domain action with no name.
   - **Cryptic naming**: new names that don't map to any domain verb or noun — you can't predict the behavior without reading the body.
   - **Wrong boundaries**: the diff draws type/file boundaries that don't align with the domain model.
   - **Over-abstraction**: indirection that doesn't correspond to any domain concept — an interface with one implementation and no test fake, a wrapper that adds no behavior.

**Proof standard:** name the domain concept(s), show where they appear in the flow list, and show the specific mismatch in the code. Not "this could be split" — "these are two independent domain concepts (X and Y) sharing a type because [specific evidence]."

Structural findings are **MEDIUM** by default. **HIGH** only if the mismatch makes a critical flow untestable or forces changes across 3+ unrelated files.

For a deeper standalone structural review, use the `structure-review` skill.

## Pass 3 — Tests

Read the tests for every changed function. For each:

1. Is the happy path tested?
2. Is the main error path tested?
3. Are the edge cases the change introduces tested? New config field → test for zero value and interaction with other fields.
4. Do the assertions actually check the right thing, or do they pass trivially?

**Representative situations** — do tests reflect how real users encounter the code?
- Do they set up pre-existing state where relevant? (e.g. a server that already has tools registered, a config that already has other projections set, a token that is already expired) A test that only runs against a clean slate will miss bugs that only surface with existing data.
- Are the inputs realistic? Fake data that is too simple (single-character strings, empty structs) can mask bugs that appear with real payloads.
- Do tests cover the interaction between the new change and pre-existing behavior, not just the new behavior in isolation?

**Regression value** — will these tests actually catch it if the behavior regresses?
- A test that passes trivially (asserts `err == nil` when the function cannot return an error, or checks the output contains a string that would always be present) adds no regression safety.
- If you deleted the new code, would the new tests fail? If not, they're not testing the change.
- Tests that only cover the happy path for a function that is primarily about error handling provide false confidence.

**Write a test to prove a suspected bug** when code analysis strongly suggests an issue but a test settles it faster than further tracing. Keep it ≤ 25 lines; use the existing test infrastructure (`FakeConnection`, `serve()`, `callTool()` helpers in `server_test.go`). Name it `review_<something>_test.go` so it's easy to find and clean up.

```bash
go test -race -tags test -run TestReview ./path/to/package/... -v
```

**Transport/path symmetry** — if the fix addresses a bug in one code path (e.g. HTTP handler), confirm there is a test that exercises that specific path, not a test that only covers the other path (stdio). A stdio test passing does not prove the HTTP path is fixed.

**Consider REJECT on test coverage alone** if:
- An auth, permission, or token-handling path was modified with no test coverage.
- A goroutine launch or shared-state mutation was added and the race-detector tests don't exercise it.
- Tests were removed or weakened for a high-risk function without justification.

## Pass 4 — Conventions (diff-level only)

This pass works only from the diff — no deep exploration. Flag quickly, one line each.

Convention findings default to **MEDIUM** — the project has strict, explicit rules about comments, naming, and structure (AGENTS.md). Violating them is not cosmetic; it degrades maintainability and readability, which are priority #2 in the project's principles. Reserve LOW only for findings so trivial they border on preference (e.g. a mildly verbose variable name that still communicates correctly).

**Project style violations** (AGENTS.md):
- Boolean or empty-string flags as positional args — `check.sh` catches function length and param count mechanically; this is what it misses

**Comments that shouldn't exist (MEDIUM):**
- Describes what the code does rather than why (rename instead)
- Section dividers in test files (`// --- setup ---`, `// --- act ---`)
- Doc-style comment on a function whose name already conveys the contract

**Naming and design (MEDIUM):**
- Names that don't self-document (force the reader to read the body to understand purpose)
- Abstractions that don't earn their keep (helper with one call site, unnecessary indirection)
- Defensive nil/error checks for values the framework guarantees non-nil/non-error
- Unnecessary intermediate variables whose only purpose is naming an already-clear expression

**Duplication (MEDIUM):** does the new code replicate logic that already exists elsewhere in the codebase? Grep for the pattern before flagging. Identical or near-identical functions/blocks copied across 2+ packages are MEDIUM — extract to a shared helper. Even 2 copies is worth flagging if the logic is non-trivial (> 3 lines); 3+ copies is always MEDIUM. Duplication is not a style nit — it's a correctness risk (one copy gets fixed, the others don't).

## Pre-report gate

Complete every item before writing the report:

1. Read the check.sh log from Step 0 in full. Any failure introduced by the PR is a finding.
2. For each candidate finding, re-read the cited code and confirm all three: the file:line is right, the quoted code matches, and the trigger scenario actually reaches that code. If any of the three can't be confirmed, drop the finding.
3. For each finding, check the diff: is the issue introduced or made worse by this PR, or pre-existing? Pre-existing issues go in a one-line "Pre-existing (not blocking)" note and do not count toward the verdict.
4. Confirm every Pass 1 candidate and every call site from the call-site audit was investigated. Anything skipped must be listed explicitly in the report as not investigated.

## Report

Output the report directly in the conversation. Do **not** post it as a GitHub PR review comment or create any GitHub review artifacts — findings go back to the caller in the conversation only.

```markdown
# PR Review — [title or branch]
**Date:** YYYY-MM-DD
**Verdict:** APPROVE | REQUEST CHANGES | REJECT

## Executive Summary
[One paragraph. Overall quality, biggest risk area, what the verdict hinges on.]

## 🔴 HIGH — [title]
**Pass:** Concurrency | Security | Correctness | Tests
**File:** path/file.go:LINE
**Bug:** What the issue is.
**Proof:** Execution trace, goroutine pair, test output — whatever proves it.
**Trigger:** Concrete scenario that causes it.
**Impact:** Panic / data race / auth bypass / data loss / etc.

## 🟠 MEDIUM — [title]
[same structure]

## 🟡 LOW — [title]
**Pass:** Conventions | Correctness
[One line. What and where. Reserve LOW for truly trivial findings — borderline preference calls, not rule violations.]

## Test coverage verdict
[What is tested, what is missing, whether the gap is a blocker.]

## Summary table
| # | Severity | Pass | File:Line | Finding |
|---|---|---|---|---|
```

**Verdict:**
- **APPROVE** — no HIGH or MEDIUM; LOWs are optional cleanup
- **REQUEST CHANGES** — one or more MEDIUMs that must be fixed before merge
- **REJECT** — any HIGH; or a security/auth path with no test coverage; or a proven race in code exercised by the race-detector suite

Don't pad the report. If the code is correct and well-tested, say so in two sentences and APPROVE.
