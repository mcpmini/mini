---
name: review-pr
description: Adversarial multi-pass PR review — concurrency, security, correctness, structure, duplication, experience, tests, then maintainability. Assumes bugs exist. Proves findings before reporting. Emits APPROVE / REQUEST CHANGES / REJECT verdict. Invoke it generically with only the target (PR, branch, or paths to limit it to). Never pass a design summary, suspected weak spots, angles to check, or earlier findings, since that anchors the reviewer and narrows the review.
argument-hint: <PR-number, PR-URL, branch, or blank for current branch diff> [paths to limit the review to]
---

Adversarial review of $ARGUMENTS (or the current branch diff if blank).

The passes below hold review methods and mini-specific checks; the general Go and testing guidance they rely on lives in `docs/go-guidelines.md` and `docs/testing.md`, which Step 0 has you read. Treat smell checks as investigation prompts; assess findings by reachable behavior and consequences.

**Assume bugs exist. Your job is to find and prove them.**

Do not explain away suspicious patterns — investigate until you have proof or can definitively rule the issue out. Write tests if needed. If high-risk code is undertested, that alone can justify REJECT.

## Non-negotiables (apply to every pass)

1. **No unproven findings.** A suspicion is not a finding. Each pass defines a proof standard; a finding that doesn't meet it gets investigated further or dropped — never reported at HIGH or MEDIUM with "could/might/may" language.
2. **Re-verify before reporting.** For every finding, re-open the cited file at the cited line and confirm the code exists and says what you claim. A finding with a wrong line number or misquoted code is worse than no finding.
3. **Pick up check.sh results.** Do not write the report until the background check suite from Step 0 has finished and you have read its log.
4. **Verdict is mechanical.** Derive the verdict from the findings table using the rules at the end — never from overall impression.
5. **The request's framing is a claim, not a fact.** Statements in the review request or PR description about the design ("built on X", "reuses Y", "no duplication") are things to verify. Angles the requester lists add to the passes; they never narrow them.
6. **Be pragmatic.** Rate every finding as "Weigh every finding" describes. A PR doesn't have to solve every problem: recommend the smallest fix that removes this one, not a redesign, new layers, or type machinery, unless the current shape has already caused a bug or a clear maintenance cost. Small cleanups in code the PR touches are welcome (leave it cleaner than you found it); problems elsewhere that are worth fixing go under "Outside this PR".

## Weigh every finding

Rate each finding's impact and likelihood, then decide.

**Impact**, on the user, the agent, or the next developer:
- **High:** a crash or hang, lost data or credentials, a security hole, mini or a server unusable, or an agent misled into a wrong action.
- **Medium:** a feature misbehaves or fails confusingly, or code so hard to follow that the next change will likely break it, such as a function hundreds of lines long.
- **Low:** cosmetic, or a little confusing but still readable.

**Likelihood**, in normal use. Normal includes events that are rare but routine: slow or flaky networks, upstreams that time out or fail, a user who edits a config and gets it wrong, invalid input, several agents at once. Unlikely means it needs something outside mini's responsibility, such as an MCP server that breaks the spec, or misuse beyond the trust model.

**Decide:**
- Likelihood or impact of medium or above: worth fixing. HIGH when the impact is high and normal use reaches it; otherwise MEDIUM.
- Both low: LOW at most, or leave it out. It isn't worth an issue.
- When it's unclear whether something happens in practice, say so; it can wait for evidence rather than be solved speculatively.

**Breaking changes don't count while mini is v0.x.** Renamed config fields, changed CLI flags or output, removed behavior, and old files that no longer load are not findings. Mention one in a line if users will notice it, so the PR description can say so, but it never blocks the PR.

## Step 0 — Gather the diff and check out the PR branch

1. Resolve the PR number from the arguments (`1` from `https://github.com/mcpmini/mini/pull/1`, from `#1`, or bare `1`). If the arguments are blank, review the current branch's diff against main in the current checkout and skip to step 5. If they name a branch instead, review that branch's diff against main: skip step 2 and use the branch as `<head-branch>` in step 3. Paths after the target limit which changed files the passes cover; still read the code those files interact with.
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
6. Read `docs/go-guidelines.md` and `docs/testing.md` from the repository root. The passes assume their guidance and don't repeat it.
7. Read every changed file **in full** — not just the diff hunks. A diff shows what changed; the full file shows what it interacts with and what invariants it relies on.

## Pass 1 — Triage

Scan the diff and changed files. Before investigating anything deeply, answer:

1. **Core change**: one sentence — what behavior is added, removed, or modified?
2. **Shared state**: what structs, maps, slices, channels, or package-level vars does the diff touch?
3. **Trust boundaries**: what new inputs arrive from outside (user, config, network, MCP tool args, env vars) and where do they land?
4. **New control paths**: what new error paths, goroutine launches, or auth checks does the change introduce?
5. **Candidate list**: for each of Passes 2a–2f, 3 and 4, list specific things to investigate. Be precise — not "check locking" but "check whether `s.authFlows` reads on lines 45–47 are covered by `s.authMu`".
6. **Call-site audit** — for every function or method whose signature, parameters, return contract, or behavior changes in this diff, including new helper functions immediately wired into multiple places:
   a. Grep for *all* call sites — not just the ones visible in the diff hunks.
   b. List every call site explicitly with file:line.
   c. For each call site, note: what conditional/state context surrounds it, what value it produces for the changed parameter/contract under the new behavior, and whether that's correct for *this call site's* purpose.
   d. Carry any call site whose correctness is unclear into Pass 2c.

   A function correct for the call site the author had in mind can be wrong for a call site that existed before the change, or for a sibling call site added in the same diff.
7. **Audiences**: who sees what this change outputs — a CLI user, an agent over MCP, the init wizard, an operator reading logs? List each affected command or tool for Pass 2f.

Produce a brief triage note to drive Passes 2–4. Do not write it into the final report.

## Pass 2a — Concurrency

Check suite output from Step 0 already covers race tests, vet, and staticcheck. Work through every candidate from triage, applying the concurrency and lifecycle guidance in `docs/go-guidelines.md`. Also check what it doesn't cover:

- **Shared state:** list every field of every shared struct the diff reads or writes, and confirm every access, reads included, holds the protecting mutex. "Usually protected" is not protected. Watch closures capturing outer mutable state.
- **Maps and slices:** unsynchronized conflicting map access can crash the process, and the race detector only catches interleavings that ran. Trace slice aliasing, including append, rather than assuming.
- **Lock upgrades:** `RLock` → `Lock` on the same mutex in the same goroutine deadlocks.
- **`sync` misuse:** `WaitGroup.Add` must run before the goroutine that calls `Done` starts. A `sync.Once` that panics stays poisoned and silently does nothing afterward.
- **Initialization:** package-level variables mutated after `init` are shared state. A constructor that starts goroutines makes the object live before `New()` returns.

**Smell test — quick scan for red flags** (grep for these, investigate any hit):
- `go func()` with no done-channel, no context, and no WaitGroup — orphaned goroutine
- `context.Background()` or `context.TODO()` inside a spawned goroutine or blocking call
- `sync.Mutex` in a struct that is passed by value
- `close(ch)` — verify closure ownership and whether sends can overlap it
- `select { case ch <- v: default: }` near a `close(ch)` — `default` does not protect against send-on-closed
- Lock acquired, I/O performed, lock released — blocking call inside a lock
- Two mutexes acquired in the same function — verify ordering is consistent everywhere
- `time.Sleep` in production code — usually polling instead of proper signaling
- `atomic.Value` or `atomic.Pointer` storing a struct with pointer fields — the whole value must be swapped atomically; partial field updates still race
- `Close()` — verify its documented repeat-call behavior and synchronization
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

Apply the errors, resources, and state-transition guidance in `docs/go-guidelines.md`. Also check:

**Silent failure**
- A fallback to zero or nil values on failure, so the caller proceeds as if nothing happened.

**Nil and zero-value hazards**
- Pointer dereferences without nil checks, especially on values from config, parsed input, or optional struct fields.
- Method calls on interface values that could be nil.
- `defer f.Close()` or `defer resp.Body.Close()` before the error or nil check.

**Logic correctness**
- Off-by-one in ranges, indices, string slicing.
- Wrong comparator (`<=` vs `<`, `!=` vs `==`, negated condition).
- Boundary behavior: empty slice, zero value, MaxInt, empty string, single element.
- **Read the doc string for every changed function and verify the implementation matches what it claims.** Mismatches here are common and dangerous.

**Operational correctness**
- Retries of errors a retry can't fix (e.g. 400 Bad Request).
- Backpressure: under sustained load, does the system queue unboundedly, or shed load and return pressure to callers?

**Design problems that cause bugs**
- State duplicated in two places that can drift out of sync — one gets updated and the other doesn't.
- Abstraction leaks that force callers to know implementation details: callers constructing internal state, or "must call X before Y" rules a caller in this codebase gets wrong or easily could.
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

1. **Map the flows and name the verbs.** Trace the full flows through the changed package before judging isolated functions. Read bodies and callers as needed. Name each flow and action using domain language ("resolve daemon", "forward request", "refresh expired token"). Compare state ownership, invariants, and lifecycle to the existing boundaries. Inline operations and callbacks are candidates only when their structure hides a meaningful contract or creates concrete friction.

2. **Derive a candidate domain model.** Group operations by state, invariants, and lifecycle. Compare plausible boundaries against the existing design. A different hypothetical design is not a finding; show the concrete cost or defect the current boundary causes.

3. **Compare to actual structure.** Map each domain concept to the types, files, and functions that implement it. Flag:
   - **Mixed concerns**: a new or modified type handles verbs from multiple unrelated domain concepts. Test: changing concept A forces touching code that implements concept B.
   - **Missing abstractions**: recurring policy or ownership invariants that are difficult to express or enforce. A callback or inline operation alone is insufficient evidence.
   - **Cryptic naming**: new names that don't map to any domain verb or noun — you can't predict the behavior without reading the body.
   - **Wrong boundaries**: the diff draws type/file boundaries that don't align with the domain model.
   - **Over-abstraction**: indirection with no useful contract or boundary. A single implementation does not disqualify an interface; show the cost and what would be preserved by removing it.

**Proof standard:** name the domain concept(s), show where they appear in the flow list, and show the specific mismatch in the code. Not "this could be split" — "these are two independent domain concepts (X and Y) sharing a type because [specific evidence]."

Assess structural findings by demonstrated maintenance cost, testing obstacles, or product risk. File count and disagreement with a hypothetical design do not determine severity.

For a deeper standalone structural review, use the `structure-review` skill.

## Pass 2e — Duplication

This pass explores the whole codebase, not just the diff. For every function, type, predicate, and multi-step flow the diff adds or changes, ask: does something already do this job?

1. **Search by behavior, not text.** Duplicates rarely share lines. Grep for other callers of the functions and APIs the new code calls, for the same constants, error messages, and config fields, and read sibling entry points and packages that handle similar work.
2. **Classify each match:**
   - Copied code: the same lines in two places.
   - Parallel implementation: the same job done with different code.
   - Repeated decision: the same rule or predicate evaluated in two places, which can drift apart.
   - Reinvented helper: new code that redoes something an existing helper or the standard library already provides.
   Also check the diff against itself for blocks repeated across files.
3. **Compare shared and separate forms.** For matching contracts, consider which operation should own the shared rule and how callers would use it. Preserve separate implementations when sharing hides different contracts or creates more coupling than it removes. Justify a finding through observed drift or meaningful maintenance cost.

**Proof standard:** cite every location's file:line, show that they do the same job (same inputs, outputs, and side effects), and give the unification.

Assess duplication by inconsistent behavior or concrete maintenance cost. Similar code alone does not establish a blocking defect; shared abstraction can also hide different contracts.

## Pass 2f — Experience

**Skip this pass** if triage found no audience.

Run each command or tool from triage once, against one fixture that combines every state the diff distinguishes (healthy, each failure kind, disabled, empty). Read the whole output as that audience would; for an agent, read the raw tool result.
- A failure says what failed, why, and what to do next, at the earliest step the audience can act, through a channel they actually see.
- Each fact appears once, is true on every path that prints it, and agrees with the exit status and totals.

**Proof standard:** quote the output, name the audience, and say what they needed to see instead.

## Pass 3 — Tests

Map each changed behavior to new or existing tests. Check the success path, the failure or boundary that matters to this change, and the production entry point the tests actually exercise. Do not require a new test per changed function or every possible permutation.

Apply `docs/testing.md`: realistic setup and pre-existing state, assertions that would catch a regression, perturbing the behavior when an assertion's strength is uncertain, and tests as readable code. Also check:

- Do tests cover the interaction between the new change and pre-existing behavior, not just the new behavior in isolation?
- Tests that only cover the happy path of a function that is mainly about error handling give false confidence.

**Write a test to prove a suspected bug** when code analysis strongly suggests an issue but a test settles it faster than further tracing. Use the existing test infrastructure (`FakeConnection`, `serve()`, `callTool()` helpers in `server_test.go`). Name it `review_<something>_test.go` so it's easy to find and clean up.

```bash
go test -race -tags test -run TestReview ./path/to/package/... -v
```

**Transport/path symmetry** — if the fix addresses a bug in one code path (e.g. HTTP handler), confirm there is a test that exercises that specific path, not a test that only covers the other path (stdio). A stdio test passing does not prove the HTTP path is fixed.

**Investigate a blocking coverage gap** if:
- An auth, permission, or token-handling path was modified with no test coverage.
- A goroutine launch or shared-state mutation was added and the race-detector tests don't exercise it.
- Tests were removed or weakened for a high-risk function without justification.

Name the unprotected contract, realistic failure, existing coverage, and why the missing evidence matters. Missing a new test or a race-detector run alone does not establish a defect or determine the verdict.

## Pass 4 — Maintainability

Read the changed code as an engineer new to it would, and flag where they would misread it or likely break it when changing it. `check.sh` catches function length and parameter count; this pass covers what it can't.

- **Names:** functions are verb phrases that say what they do and predict their effects; types and variables are domain nouns. Flag vague names (`handle`, `process`, `data`, `util`, `manager`) and names that mislead.
- **Shape:** each function does one job, and the normal path reads straight down with early returns. Flag deep nesting, long functions that need scrolling to follow, and boolean or empty-string flags as positional args.
- **Explicitness:** no clever tricks or hidden side effects; steps that must happen in a certain order are obvious from the code. The code says what it means without relying on a comment.
- **Reuse:** the standard library (`slices`, `maps`, `strings`, `errors`, `context`, `sync`) and existing helpers over hand-rolled loops; no layers, interfaces, or helpers that don't make the code easier to read or change.
- **Consistency:** naming, error style, and idioms match the surrounding package.
- **Comments:** they explain why, not what; no section dividers in tests; no doc comments that repeat the name.

**Proof standard:** quote the code, say what a reader would get wrong or what change it makes risky, and give the clearer form. Assess severity by that cost or by an explicit AGENTS.md rule, not by preference alone.

## Pre-report gate

Complete every item before writing the report:

1. Read the check.sh log from Step 0 in full. Any failure introduced by the PR is a finding.
2. For each candidate finding, re-read the cited code and confirm all three: the file:line is right, the quoted code matches, and the trigger scenario actually reaches that code. If any of the three can't be confirmed, drop the finding.
3. For each finding, check the diff: is the issue introduced or made worse by this PR? Anything it didn't goes under "Outside this PR" and doesn't count toward the verdict.
4. Confirm every Pass 1 candidate and every call site from the call-site audit was investigated. Anything skipped must be listed explicitly in the report as not investigated.
5. Confirm you read `docs/go-guidelines.md` and `docs/testing.md` in Step 0.

## Report

Output the report directly in the conversation. Do **not** post it as a GitHub PR review comment or create any GitHub review artifacts — findings go back to the caller in the conversation only.

```markdown
# PR Review — [title or branch]
**Date:** YYYY-MM-DD
**Verdict:** APPROVE | REQUEST CHANGES | REJECT

## Executive Summary
[One paragraph. Overall quality, biggest risk area, what the verdict hinges on.]

## 🔴 HIGH — [title]
**Pass:** Concurrency | Security | Correctness | Experience | Tests | Maintainability
**Rating:** impact High | Medium | Low, likelihood High | Medium | Low
**File:** path/file.go:LINE
**Bug:** What the issue is.
**Proof:** Execution trace, goroutine pair, test output — whatever proves it.
**Trigger:** Concrete scenario that causes it.
**Impact:** Panic / data race / auth bypass / data loss / etc.

## 🟠 MEDIUM — [title]
[same structure]

## 🟡 LOW — [title]
**Pass:** Maintainability | Correctness
[One line. What and where. Reserve LOW for truly trivial findings — borderline preference calls, not rule violations.]

## Outside this PR
[Problems found in code this PR didn't cause and doesn't need to fix. One entry each: file:line, the problem, and why it matters, so the caller can decide whether to file an issue. They don't affect the verdict. Omit the section if there are none.]

## Test coverage verdict
[What is tested, what is missing, whether the gap is a blocker.]

## Summary table
| # | Severity | Pass | File:Line | Finding |
|---|---|---|---|---|
```

**Verdict:**
- **APPROVE** — no HIGH or MEDIUM; LOWs are optional cleanup
- **REQUEST CHANGES** — one or more MEDIUMs that must be fixed before merge
- **REJECT** — any HIGH supported by a reachable failure and consequential impact. Assess coverage gaps by the unprotected contract and risk, and races by their behavior, rather than a categorical test-count rule.

Don't pad the report. If the code is correct and well-tested, say so in two sentences and APPROVE.
