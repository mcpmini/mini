---
name: concurrency-review
description: Deep adversarial concurrency audit of Go code — phases by impact: crashes, hangs, leaks, races, correctness, tests. Assumes bugs exist. Reports findings in the conversation.
argument-hint: [file/package paths, or blank for full repo]
---

Adversarial concurrency audit of $ARGUMENTS (default: the full repo). **Assume bugs exist. Work through every phase.** Output findings directly in the conversation.

Read [Go engineering in mini](../../../docs/go-guidelines.md). Check version-dependent claims against `go.mod`, runtime settings, and current API documentation. Grep hits identify candidates, not defects.

**Proof standard for every finding:** name the shared state, the two (or more) goroutines involved, and the concrete interleaving or blocking scenario — which paths run concurrently, what timing, what input. "Could race" or "might deadlock" is not a finding; investigate until you can state the scenario, or record it under Non-issues with the reason it's safe.

In the grep commands below, `$PKGS` means the directories under review — the paths in $ARGUMENTS, or `./internal ./cmd` for a full-repo audit. Always pass explicit paths; `grep -rn 'pattern'` with no path hangs reading stdin.

---

## Phase 1 — Automated tools (always run first)

```bash
export PATH="/opt/homebrew/bin:$(go env GOPATH)/bin:$PATH"
go test -race -tags test ./...
go vet -tags integration,test ./...
staticcheck ./...   # if available
golangci-lint run   # if available
```

Record every failure verbatim. The race detector catches runtime races that manual review misses — but only on exercised paths. Continue immediately with Phase 2 while waiting for results.

---

## Phase 2 — Crash-severity (immediate process death, no recovery)

### Concurrent map access
Unsynchronized conflicting map access is a data race and can cause a fatal runtime error. The race detector can catch exercised races but cannot establish coverage of every interleaving. Verify synchronization or exclusive ownership for reads, writes, and iteration.

```bash
rg -n 'range ' $PKGS   # maps ranged — are they shared across goroutines?
```

For slices, trace header mutation and backing-array aliasing. Conflicting accesses require synchronization; disjoint element access can be safe.

### Send to / close of closed channel
- Sending to a closed channel panics. It is recoverable in the sending goroutine, but recovery does not repair the ownership defect.
- Closing an already-closed channel panics.
- `select { case ch <- v: default: }` does **not** guard against send-on-closed — `default` fires when the channel is **full**, not closed. In a `select`, a nil channel disables the case without panicking — it never fires. Sending to a nil channel outside a select blocks forever.
- Coordinate potential closers so closure occurs at most once. Single ownership, a mutex-protected state transition, or `sync.Once` can provide this. Also exclude concurrent sends; `sync.Once` alone does not do that.

### Goroutine panic without recover
An unrecovered panic in any goroutine kills the entire process. Trace operations that can actually panic and the reachable input or invariant violation. Ordinary network and JSON errors do not justify blanket recovery. If a containment boundary is required, check how it reports failure and preserves state.

---

## Phase 3 — Logical hangs (silent, hardest to diagnose)

No crash, no error — the service stops responding. The race detector and vet cannot catch these.

### Deadlocks
- **Lock ordering:** two mutexes ever held simultaneously? Acquisition order must be consistent at every call site. Map all (A→B) and (B→A) pairs.
- **Lock recursion:** Go mutexes are not reentrant — holding and re-acquiring the same lock deadlocks.
- **RLock → Lock upgrade:** attempting to acquire a write lock while holding a read lock on the same mutex deadlocks immediately.
- **Channel send inside a held lock:** if the receiver also needs the lock, both sides block forever.

### Blocking select without guaranteed escape
For every `select` that blocks on channels: trace every code path that closes or sends to each case channel. Are **all** equivalent calling paths — different transports (stdio vs HTTP), error returns, shutdown sequences, session eviction — guaranteed to eventually fire one of the cases?

A `select` on `done | abort | ctx.Done()` where `abort` is only closed in the stdio path (not the HTTP path) blocks forever for HTTP sessions after eviction or restart. The race detector will not catch this.

**Asymmetric cleanup:** when a lifecycle action (close channel, cancel func, `markAborted`, flag reset) runs in one handling path, grep for the equivalent action in every other path sharing that lifecycle.

**Backstop:** is there a `context.WithTimeout` as a fallback even if the primary escape might not fire?

### Shutdown without deadline
`http.Server.Shutdown(context.Background())` waits forever for in-flight handlers. Any handler that can block indefinitely (tool timeout disabled, hanging subprocess) prevents the process from exiting cleanly. Always pass a bounded context to `Shutdown`.

### sync.Cond Signal vs Broadcast
`Signal()` wakes one waiter, if any; `Broadcast()` wakes all. Choose from the predicate transition and which waiters can make progress. Multiple waiters alone do not require Broadcast. Check the predicate in a loop under the associated lock: `for !ready { cond.Wait() }`.

---

## Phase 4 — Resource leaks (gradual degradation)

No immediate crash — the service degrades over time or under repeated calls.

### Goroutine leaks
Grep for every `go ` launch:
- What stops it? A done-channel, context cancellation, or WaitGroup?
- `context.Background()` or `context.TODO()` passed to a goroutine that blocks on I/O or a channel: no way to cancel, leaks compound on every call.
- **User-interactive goroutines** (waiting for OAuth URL visit, webhook, user confirmation) must have their own explicit timeout — `context.WithTimeout` — because the user may never act.
- Does it hold a subprocess, TCP listener, file handle, or ticker? If the goroutine leaks, so do those resources.

### Timer lifetime and allocation
Modern Go can collect unreferenced timers and tickers. `time.After` does not launch a goroutine per call; `time.Tick` is not an automatic permanent leak. Check whether timers remain reachable, allocation matters on the actual path, or the operation requires explicit stopping. Use NewTimer or NewTicker when lifecycle control or reuse warrants it. See [timer semantics](https://go.dev/wiki/Go123Timer) and mini's clock implementation.

### `defer` inside a loop
`defer` fires on function return, not loop iteration. Deferred calls (file closes, mutex unlocks, connection closes) accumulate for the entire loop duration — a silent resource exhaustion bug at high volume.

```bash
rg -n 'defer ' $PKGS   # look for defers inside for/range blocks
```

### Fan-out without bounded concurrency
Goroutines launched per-request with no semaphore, pool, or work queue grow unboundedly under load. Look for `go func()` or `go someFunc()` inside a loop or per-request handler without a limiting channel or worker pool.

### Mutex held across I/O
Network calls, file I/O, and channel sends inside a held lock starve every other waiter for the duration of that I/O. This includes file operations in eviction/cleanup paths, not just network calls.

---

## Phase 5 — Data races (intermittent corruption)

The race detector covers these at runtime, but only on exercised paths. Manual review catches the rest.

### Shared state without full lock coverage
Find every struct accessed from multiple goroutines. For each field: is every access — including reads — inside the same protecting mutex? Common miss: most fields under a lock, one "obviously safe" field accessed bare.

### TOCTOU (check-then-act)
Check inside a lock, act outside it — not atomic. Also: paired separately-locked operations (Remove then Add on a shared index, budget-check then budget-update) leave a consistency window. Budget counters and size limits are especially prone to transient overshoot under concurrent writes.

### Compound atomic operations
Separate atomic operations do not make a compound invariant atomic. For example, `if x.Load() > 0 { x.Add(-1) }` can produce a negative counter under competing decrements. This is a logical race even when each memory access is synchronized. Use CAS, a mutex, or another design when the contract requires an indivisible transition; ordinary unsynchronized single-word accesses are not generally safe.

### Timer reset race
`time.NewTimer`: the safe pattern depends on Go version.

With modern synchronous timer channels, Stop and Reset prevent subsequent receives of stale values. Stop does not mean "drain the channel"; an unconditional receive after Stop can block. Go 1.23–1.26 could select legacy buffered behavior with `GODEBUG=asynctimerchan=1`; [Go 1.27 removed that setting](https://go.dev/doc/go1.27#runtime). Check the supported version, receiver ownership, and whether the timer uses a channel or callback before prescribing a recipe. Injected clocks may have different semantics.

### sync.Pool: objects not zeroed before reuse
After Put, the previous owner must stop using the object. Before reuse, reset the state required by the consumer's contract; this can happen before Put or after Get. Check retained references and sensitive data separately. Pool reuse does not universally require clearing every field.

### Happens-before across multiple variables
A channel send/receive establishes a happens-before edge only for that communication. Variables written before the send are visible to the receiver — but variables written after the send, or on a different goroutine, are not. Don't assume a channel sync makes all memory globally visible.

---

## Phase 6 — Correctness under concurrency

### Thundering herd without a single-winner guard
When multiple goroutines simultaneously detect the same failure and all attempt recovery, without a `CompareAndSwap` or similar guard the recovery runs N times. Look for concurrent error-handling paths and verify there is a single-winner mechanism.

### Receive from closed channel
Receiving from a closed channel returns the zero value with `ok=false`. Code that ignores `ok` silently processes zero/nil values and may propagate corrupted state. Check all `<-ch` calls where the channel could be closed.

### errgroup context propagation
`errgroup.WithContext` returns a derived `ctx` that is cancelled when the first goroutine errors. Code that shadows the original context with this derived one (`ctx, _ = errgroup.WithContext(ctx)`) will have unrelated operations cancelled by the first error — often not the intent. Keep the original context for work that should outlive group errors.

### sync primitives misuse
- `WaitGroup.Add` must be called before the goroutine that calls `Done` is launched — not inside it.
- `sync.Once` that panics leaves the Once permanently poisoned; subsequent calls silently do nothing.
- Copying a `sync.Mutex`, `sync.WaitGroup`, or `sync.Cond` after first use is a bug — `go vet` catches struct-level copies but misses copies hidden in `append` or map value assignment.

### Initialization races
- Package-level variables mutated after `init` are shared global state — any goroutine touching them needs synchronization.
- Concurrent lazy initialization needs synchronization or exclusive ownership; inspect the full access pattern before prescribing a primitive.
- Constructors that launch goroutines before returning: the object is live the moment `New()` returns.

---

## Phase 7 — Test-specific concurrency bugs

### Test failures from workers
FailNow and Fatal terminate the calling goroutine and must be called from the goroutine running the test. They do not stop child workers. Collect worker results and assert from the test goroutine. Stop and join workers during cleanup so they cannot report errors or use test resources after the test completes.

### Goroutine leaks between tests
Goroutines launched by one test that don't exit leak into subsequent tests, causing interference that looks like flakiness. The race detector does not catch this. Use `goleak` (`go.uber.org/goleak`) to assert no unexpected goroutines survive:
```go
defer goleak.VerifyNone(t)
```
Prefer completion signals for test-owned workers. Global goroutine counts include unrelated runtime activity and do not prove that a particular worker exited. Leak detectors need deliberate configuration and should not create a new dependency without a demonstrated need.

### Package-level state in parallel tests
Tests run with `t.Parallel()` share package-level state. Check conflicting accesses and isolation; synchronization can make shared state safe, and sync.Pool itself supports concurrent use. Even synchronized mutation can cause logical interference between tests. The race detector covers only executed conflicting accesses.

---

## Smell-test greps — run these, investigate every hit

```bash
rg -n 'go func\(' $PKGS                    # no WaitGroup, done-channel, or context → orphan?
rg -n 'context\.(Background|TODO)\(\)' $PKGS  # inside spawned goroutine or blocking call?
rg -n 'time\.After\(' $PKGS                # lifetime or repeated-allocation issue?
rg -n 'time\.Tick\(' $PKGS                 # needs explicit stopping?
rg -n 'defer ' $PKGS                       # inside for/range → accumulates until return?
rg -n 'Shutdown\(context\.Background' $PKGS  # blocks forever if handler hangs
rg -n '\.RLock\(\)' $PKGS                  # followed by network/file I/O before RUnlock?
rg -n 'sync\.Pool' $PKGS                   # ownership and reset contract?
rg -n '\.Load\(\)' $PKGS | rg '\.Store|\.Add'  # compound atomic — needs CAS?
rg -n 'close\(' $PKGS                      # closure ownership and send overlap?
rg -n '\.Signal\(\)' $PKGS                 # should this be Broadcast()?
rg -n '^\s*go ' $PKGS                      # inside a for loop or per-request handler → bounded?
```

---

## Pre-report gate

1. Collect the Phase 1 tool output; quote failures verbatim in the report.
2. For each finding, re-open the cited file and confirm the line number and quoted code are correct.
3. Check each finding against the proof standard: shared state named, goroutines named, interleaving stated. Anything that fails goes back for investigation or into Non-issues — not the report.
4. Confirm every phase was worked, including Phase 7 (tests). If a phase surfaced nothing, say so in one line rather than omitting it.

## Report format

```markdown
# Concurrency Review — DATE
**Scope:** [packages reviewed]

## Summary
[Overall verdict. Tool output summary. Count by severity.]

## Phase N — SEVERITY: title
**File:** path/file.go:LINE
**Bug:** what the race/hang/leak/crash is and what shared state is involved
**Trigger:** concrete scenario — which goroutines, what timing, what input
**Impact:** crash / hang / leak / data corruption / transient error

## Non-issues confirmed safe
[Patterns investigated and ruled out, with one-line reason each.
This section prevents re-flagging the same patterns in future reviews.]
```

**Severity:** HIGH (crash, hang, data corruption) → MEDIUM (leak, race with user-visible effect) → LOW (benign, self-correcting, narrow window).

Always write the non-issues section. Confirming safe patterns is as valuable as finding bugs.
