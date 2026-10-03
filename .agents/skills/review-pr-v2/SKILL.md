---
name: review-pr-v2
description: Adversarial PR review organized by investigation method (trace, run, attack, break, compare) rather than by category. Same invocation and report format as review-pr, so the two can run side by side. Assumes bugs exist and proves findings before reporting. Emits APPROVE / REQUEST CHANGES / REJECT. Invoke it generically with only the target (PR, branch, or paths to limit it to). Never pass a design summary, suspected weak spots, angles to check, or earlier findings, since that anchors the reviewer and narrows the review.
argument-hint: <PR-number, PR-URL, branch, or blank for current branch diff> [paths to limit the review to]
---

Adversarial review of $ARGUMENTS (or the current branch diff if blank).

The general Go and testing guidance lives in `docs/go-guidelines.md` and `docs/testing.md`, which Step 0 has you read; this skill doesn't repeat it.

**Assume bugs exist. Your job is to find and prove them.** Do not explain away suspicious patterns: investigate until you have proof or can rule the issue out.

## Non-negotiables

1. **No unproven findings.** A finding meets the proof standard for its kind (below) or is dropped. Never report HIGH or MEDIUM with "could/might/may" language.
2. **Re-verify before reporting.** Re-open every cited file at the cited line and confirm the code says what you claim.
3. **Read the check.sh log** before writing the report.
4. **The verdict is mechanical.** Derive it from the findings using the rules at the end.
5. **The request's framing is a claim, not a fact.** Statements about the design in the request or PR description are things to verify. Listed angles add to the review; they never narrow it.
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

## Step 0 — Check out the change

1. Resolve the target. A PR number comes from `https://github.com/mcpmini/mini/pull/1`, `#1`, or `1`. Blank arguments mean the current branch's diff against main in the current checkout: skip to step 4. A branch name means that branch's diff against main: skip step 2. Paths after the target limit which changed files you review; still read the code those files interact with.
2. Get the PR description, diff, and file list, through mini's MCP integration or the mini CLI when possible, otherwise the `gh` CLI.
3. Check out the head in a dedicated worktree, reusing `.agents/worktrees/review-pr-v2-<number>` if it exists:
   ```bash
   git fetch origin <head-branch>
   git worktree add .agents/worktrees/review-pr-v2-<number> FETCH_HEAD --detach
   ```
   Work from that directory (with Claude Code, `EnterWorktree`).
4. Start the check suite in the background and keep going:
   ```bash
   ./check.sh 2>&1 | tee /tmp/review-pr-v2-check-$(date +%s).log
   ```
5. Read `docs/go-guidelines.md` and `docs/testing.md` from the repository root.
6. Read every changed file in full, not just the hunks.

## Step 1 — Understand

Write a short private note (not part of the report):

1. **Core change:** one sentence on what behavior is added, removed, or modified.
2. **Shared state:** structs, maps, slices, channels, or package-level vars the diff touches.
3. **Trust boundaries:** new inputs from outside (user, config, network, MCP tool args, env vars) and where they land.
4. **New paths:** new error paths, goroutines, auth checks, and every *equivalent* path that should behave the same (stdio and HTTP, startup and reload, CLI and agent, success and error return, shutdown).
5. **Audiences:** who sees this change's output (CLI user, agent over MCP, the init wizard, an operator reading logs) and through which commands or tools.
6. **Call sites:** for every function whose signature, contract, or behavior changes, grep *all* call sites, list them with file:line, and note whether the new behavior is right for each one's purpose.
7. **Risks:** a precise list of things to investigate, each with the method below that will settle it. Not "check locking" but "check whether `s.authFlows` reads at lines 45–47 hold `s.authMu`".

## Step 2 — Investigate

Settle every risk from Step 1 with at least one method. Use the reference checklist in Step 3 for what to look for.

**Trace.** Follow data and control through the code.
- Every call site from Step 1, in its own context.
- Every equivalent path from Step 1: when one path does a lifecycle action (closes a channel, cancels, cleans up, reports), find the same action in each sibling path.
- Untrusted input from its source to every sink it reaches.
- Each changed function against its doc comment.

**Run.** When the change has an audience, build the binary and use each affected command or tool once against one fixture combining every state the diff distinguishes (healthy, each failure kind, disabled, empty). Read the whole output as that audience would; for an agent, read the raw tool result.
- A failure says what failed, why, and what to do next, at the earliest step the audience can act, through a channel they see.
- Each fact appears once, is true on every path that prints it, and agrees with the exit status and totals.

**Attack.** Make the risky parts fail.
- Concurrency: name two goroutines and find the window where a shared value is accessed without the lock, or where blocking has no escape.
- Failure: make a dependency slow, stuck, or fail halfway, and check the state left behind.
- Boundaries: empty, zero, one, duplicate, and malformed inputs.
- Write a focused test when that settles a suspicion faster than tracing. Name it `review_<something>_test.go` and use the existing helpers (`FakeConnection`, `serve()`, `callTool()` in `server_test.go`):
  ```bash
  go test -race -tags test -run TestReview ./path/to/package/... -v
  ```

**Break.** For each changed behavior, perturb it and run the focused tests. One should fail for the expected reason; if none does, the behavior is unprotected. Restore the code afterward.

**Compare.** Read the change against the rest of the codebase.
- Search by behavior for existing code that does the same job: copied code, parallel implementations, the same rule decided in two places, or a reinvented helper.
- When the diff adds types, files, or multi-step flows, map the flows in domain terms and check that types and files follow the domain: no mixed concerns, missing abstractions, cryptic names, or indirection without a contract. Use `structure-review` for a deeper pass.
- Read the changed code as an engineer new to it would, and note where they would misread it or likely break it when changing it. Use the maintainability checklist below.

## Step 3 — Reference checklist

What `docs/go-guidelines.md` and `docs/testing.md` don't already cover.

**Concurrency** (the check suite already runs `-race`, vet and staticcheck)
- Every access to a shared field, reads included, holds its lock. No `RLock`→`Lock` upgrade; no `RLock` held across a network call.
- `context.Background()` in a long-lived goroutine can't be cancelled; `http.Server.Shutdown` needs a bounded context.
- `WaitGroup.Add` before the goroutine starts; a `sync.Once` that panics stays poisoned.
- Every blocking `select` can be woken on every equivalent path (HTTP as well as stdio, after eviction and restart).

**Security**
- User-controlled strings reaching `exec.Command`, a shell, or file paths without a root check.
- User-controlled URLs: `SSRFSafeDialer` or equivalent, and no redirect following.
- New routes to protected operations that skip the permission check; trace from the entry point, not the guard.
- Tokens or secrets in logs, errors, or agent-facing results. `crypto/rand` for state, nonces, and verifiers.

**Correctness**
- Silent fallback to zero values, so the caller proceeds as if nothing happened.
- Nil dereferences on config, parsed input, and optional fields; `defer Close()` before the error check.
- Off-by-one, wrong comparator, negated condition.
- Unbounded queues; retries of errors a retry can't fix.
- State kept in two places that can drift; "call X before Y" rules a caller in this codebase gets wrong or easily could.

**Tests**
- A fix in one path is tested in that path, not only a sibling (HTTP fix, HTTP test).
- High-risk changes (auth, permissions, tokens, goroutines, shared state) with no covering test.

**Maintainability** (`check.sh` catches function length and parameter count)
- Names: functions are verb phrases that say what they do and predict their effects; types and variables are domain nouns. No vague names (`handle`, `process`, `data`, `util`, `manager`) or misleading ones.
- Shape: each function does one job; the normal path reads straight down with early returns. No deep nesting, functions too long to follow, or boolean or empty-string flags as positional args.
- Explicitness: no clever tricks or hidden side effects; steps that must happen in a certain order are obvious from the code.
- Reuse: the standard library (`slices`, `maps`, `strings`, `errors`, `context`, `sync`) and existing helpers over hand-rolled loops; no layers, interfaces, or helpers that don't make the code easier to read or change.
- Consistency: naming, error style, and idioms match the surrounding package.
- Comments explain why, not what; no section dividers in tests; no doc comments that repeat the name.

## Proof standards

- **Concurrency:** the two goroutines, the shared variable, and the lock-release points that open the window.
- **Security:** the chain of functions from the source to the dangerous sink.
- **Correctness:** the triggering input, and the actual versus expected outcome.
- **Experience:** the quoted output, the audience, and what they needed to see.
- **Structure:** the domain concepts, where they appear in the flows, and the specific mismatch.
- **Duplication:** every location with file:line, evidence they do the same job, and the unification.
- **Tests:** the unprotected contract, a realistic regression it would let through, and the perturbation that showed no test fails.
- **Maintainability:** the quoted code, what a reader would get wrong or what change it makes risky, and the clearer form. Assess severity by that cost or an explicit AGENTS.md rule, not preference alone.

## Pre-report gate

1. Read the check.sh log in full. Any failure the PR introduced is a finding.
2. For each finding, confirm the file:line, the quoted code, and that the trigger reaches that code. Drop any you can't confirm.
3. For each finding, check whether this PR introduced or worsened it. Anything it didn't goes under "Outside this PR" and doesn't count toward the verdict.
4. Confirm every risk and call site from Step 1 was settled. List anything not investigated in the report.
5. Confirm you read `docs/go-guidelines.md` and `docs/testing.md` in Step 0.

## Report

Output the report in the conversation only; never post it to GitHub.

```markdown
# PR Review — [title or branch]
**Date:** YYYY-MM-DD
**Verdict:** APPROVE | REQUEST CHANGES | REJECT

## Executive Summary
[One paragraph. Overall quality, biggest risk area, what the verdict hinges on.]

## 🔴 HIGH — [title]
**Pass:** Concurrency | Security | Correctness | Experience | Structure | Duplication | Tests | Maintainability
**Found by:** Trace | Run | Attack | Break | Compare
**Rating:** impact High | Medium | Low, likelihood High | Medium | Low
**File:** path/file.go:LINE
**Bug:** What the issue is.
**Proof:** Execution trace, goroutine pair, command output, test output — whatever proves it.
**Trigger:** Concrete scenario that causes it.
**Impact:** Panic / data race / auth bypass / data loss / misleading output / etc.

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
| # | Severity | Pass | Found by | File:Line | Finding |
|---|---|---|---|---|---|
```

**Verdict:**
- **APPROVE** — no HIGH or MEDIUM; LOWs are optional cleanup
- **REQUEST CHANGES** — one or more MEDIUMs that must be fixed before merge
- **REJECT** — any HIGH supported by a reachable failure and consequential impact

Don't pad the report. If the code is correct and well tested, say so in two sentences and APPROVE.
