---
name: review-pr
description: Adversarial multi-pass PR review — concurrency, security, correctness, tests, then conventions. Assumes bugs exist. Proves findings before reporting. Emits APPROVE / REQUEST CHANGES / REJECT verdict.
argument-hint: <PR-number, PR-URL, or blank for current branch diff>
---

Adversarial review of $ARGUMENTS (or the current branch diff if blank).

**Assume bugs exist. Your job is to find and prove them.**

Do not explain away suspicious patterns — investigate until you have proof or can definitively rule the issue out. Write tests if needed. If high-risk code is undertested, that alone can justify REJECT.

---

## Step 0 — Gather the diff and run checks

Use the GitHub MCP tools if available, otherwise fall back to the `gh` CLI. Get the PR description, diff, and full file list. Then **read every changed file in full** — not just the diff hunks. A diff shows what changed; the full file shows what it interacts with and what invariants it relies on.

Fire off the full check suite in the background — it covers build, staticcheck, golangci-lint, function length, parameter count, return value checks, and the race-detector test suite — then continue immediately with Pass 1:
```bash
./check.sh 2>&1 | tee /tmp/review-pr-check-$(date +%s).log
```
Run this with `run_in_background: true` and note the log path. You will be notified when it finishes. Pick up the results before writing the report — any failure introduced by the PR is a finding.

---

## Pass 1 — Triage

Scan the diff and changed files. Before investigating anything deeply, answer:

1. **Core change**: one sentence — what behavior is added, removed, or modified?
2. **Shared state**: what structs, maps, slices, channels, or package-level vars does the diff touch?
3. **Trust boundaries**: what new inputs arrive from outside (user, config, network, MCP tool args, env vars) and where do they land?
4. **New control paths**: what new error paths, goroutine launches, or auth checks does the change introduce?
5. **Candidate list**: for each of Passes 2a–2c, list specific things to investigate. Be precise — not "check locking" but "check whether `s.authFlows` reads on lines 45–47 are covered by `s.authMu`".

Produce a brief triage note to drive Passes 2–4. Do not write it into the final report.

---

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

**Proof standard**: name the two goroutines, the shared variable, and the specific lock-release points that create the window. Not "could race" — "races when X and Y run concurrently because Z is not held during steps A–B."

---

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

---

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

---

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

**Consider REJECT on test coverage alone** if:
- An auth, permission, or token-handling path was modified with no test coverage.
- A goroutine launch or shared-state mutation was added and the race-detector tests don't exercise it.
- Tests were removed or weakened for a high-risk function without justification.

---

## Pass 4 — Conventions (diff-level only)

This pass works only from the diff — no deep exploration. Flag quickly, one line each.

**Project style violations** (CLAUDE.md):
- Boolean or empty-string flags as positional args — `check.sh` catches function length and param count mechanically; this is what it misses

**Comments that shouldn't exist:**
- Describes what the code does rather than why (rename instead)
- Section dividers in test files (`// --- setup ---`, `// --- act ---`)
- Doc-style comment on a function whose name already conveys the contract

**AI verbosity signals:**
- Defensive nil/error checks for values the framework guarantees non-nil/non-error
- An abstraction or helper with exactly one call site
- Unnecessary intermediate variables whose only purpose is naming an already-clear expression

**Duplication:** does the new code replicate logic that already exists? Grep for the pattern before flagging.

These are LOW severity unless they conceal a real bug. One line each. Move on.

---

## Report

Append a new section to `principal-review-YYYY-MM-DD.md` at the repo root (create the file if it doesn't exist today).

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
[One line. What and where.]

## Test coverage verdict
[What is tested, what is missing, whether the gap is a blocker.]

## Confirmed safe
[Patterns that looked suspicious but were traced and found correct. Include the reason so they aren't re-flagged next time.]

## Summary table
| # | Severity | Pass | File:Line | Finding |
|---|---|---|---|---|
```

**Verdict:**
- **APPROVE** — no HIGH or MEDIUM; LOWs are optional cleanup
- **REQUEST CHANGES** — one or more MEDIUMs that must be fixed before merge
- **REJECT** — any HIGH; or a security/auth path with no test coverage; or a proven race in code exercised by the race-detector suite

Don't pad the report. If the code is correct and well-tested, say so in two sentences and APPROVE.
