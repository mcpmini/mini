---
name: concurrency-review
description: Deep adversarial concurrency audit of Go code — phases by impact: crashes, hangs, leaks, races, correctness, tests. Assumes bugs exist. Reports findings in the conversation.
argument-hint: [file/package paths, or blank for full repo]
allowed-tools: Read, Grep, Glob, Bash, Write
---

Adversarial concurrency audit of $ARGUMENTS (default: the full repo). **Assume bugs exist. Work through every phase.** Output findings directly in the conversation.

---

## Phase 1 — Automated tools (always run first)

```bash
export PATH="/opt/homebrew/bin:$(go env GOPATH)/bin:$PATH"
go test -race -tags test ./...
go vet ./...
staticcheck ./...   # if available
golangci-lint run   # if available
```

Record every failure verbatim. The race detector catches runtime races that manual review misses — but only on exercised paths. Continue immediately with Phase 2 while waiting for results.

---

## Phase 2 — Crash-severity (immediate process death, no recovery)

### Concurrent map access
`concurrent map read and map write` is a **hard fatal crash**, not a data race the detector reliably catches in all test configurations. Find every map accessed from multiple goroutines and verify lock coverage on **all** read paths including `range` iteration.

```bash
grep -n 'range ' ...   # maps ranged — are they shared?
```

Slices sharing a backing array under concurrent append are the same class.

### Send to / close of closed channel
- Sending to a closed channel panics with no recovery.
- Closing an already-closed channel panics.
- `select { case ch <- v: default: }` does **not** guard against send-on-closed — `default` fires when the channel is **full**, not closed. Only a nil channel is unconditionally safe to send to.
- Every channel with multiple potential closers must use `sync.Once`.

### Goroutine panic without recover
An unrecovered panic in any goroutine kills the entire process. Any goroutine launched with `go func()` that performs network I/O, JSON parsing, or type assertions without a `recover` at its top level is a latent crash.

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
`Signal()` wakes exactly one waiter. Under load with multiple goroutines waiting on the same condition, `Signal()` can leave others permanently blocked. Use `Broadcast()` unless you can guarantee exactly one waiter. The predicate must always be checked in a loop: `for !ready { cond.Wait() }`.

---

## Phase 4 — Resource leaks (gradual degradation)

No immediate crash — the service degrades over time or under repeated calls.

### Goroutine leaks
Grep for every `go ` launch:
- What stops it? A done-channel, context cancellation, or WaitGroup?
- `context.Background()` or `context.TODO()` passed to a goroutine that blocks on I/O or a channel: no way to cancel, leaks compound on every call.
- **User-interactive goroutines** (waiting for OAuth URL visit, webhook, user confirmation) must have their own explicit timeout — `context.WithTimeout` — because the user may never act.
- Does it hold a subprocess, TCP listener, file handle, or ticker? If the goroutine leaks, so do those resources.

### Timer leaks
- `time.After(d)` inside a `select` loop allocates a new timer on every iteration. The timer lives until `d` expires regardless of whether the select case fired. Under load this creates unbounded timer accumulation. Use `time.NewTimer` with explicit `Stop()`.
- `time.Tick` (not `time.NewTicker`) leaks the ticker permanently — it cannot be stopped.

### `defer` inside a loop
`defer` fires on function return, not loop iteration. Deferred calls (file closes, mutex unlocks, connection closes) accumulate for the entire loop duration — a silent resource exhaustion bug at high volume.

```bash
grep -n 'defer' ... # look for defers inside for/range blocks
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
`atomic.Load()` then `atomic.Store()` separately is not atomic — there is a race window between them. Any check-then-act on atomic values (`if x.Load() > 0 { x.Add(-1) }`) requires `CompareAndSwap` or a mutex. Single-word reads and writes are safe; compound operations are not.

### Timer reset race
`time.NewTimer`: calling `Stop()` then `Reset()` is racy if the timer already fired. The safe pattern:
```go
if !t.Stop() {
    <-t.C  // drain the already-fired value
}
t.Reset(d)
```
Without the drain, the next `select` picks up the stale value immediately, as if Reset was never called.

### sync.Pool: objects not zeroed before reuse
Objects returned to a `sync.Pool` must be zeroed before `Put`. The pool may give the object to another goroutine immediately. Unzeroed fields silently carry data from the previous owner.

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
- Lazy initialization (`if x == nil { x = ... }`) without `sync.Once` or atomics is a data race.
- Constructors that launch goroutines before returning: the object is live the moment `New()` returns.

---

## Phase 7 — Test-specific concurrency bugs

### t.Fatal / t.Error from a goroutine after the test ends
`t.FailNow()` only exits the **test goroutine**, not child goroutines. A goroutine that outlives the test and calls `t.Error` or `t.Log` panics with "testing: t.Fatal called after test finished". Use `t.Cleanup` to stop goroutines before the test exits, or collect results through a channel and check them in the test goroutine.

### Goroutine leaks between tests
Goroutines launched by one test that don't exit leak into subsequent tests, causing interference that looks like flakiness. The race detector does not catch this. Use `goleak` (`go.uber.org/goleak`) to assert no unexpected goroutines survive:
```go
defer goleak.VerifyNone(t)
```
Or check `runtime.NumGoroutine()` before and after.

### Package-level state in parallel tests
Tests run with `t.Parallel()` share package-level state. Any global variable, package-level map, or `sync.Pool` mutated in a parallel test is a data race — `go test -race` will catch most, but only if the race window is actually exercised.

---

## Smell-test greps — run these, investigate every hit

```bash
grep -rn 'go func()'               # no WaitGroup, done-channel, or context → orphan?
grep -rn 'context\.Background()'   # inside spawned goroutine or blocking call?
grep -rn 'time\.After('            # inside select loop → timer leak?
grep -rn 'time\.Tick('             # always leaks — should be NewTicker
grep -rn 'defer'                   # inside for/range → accumulates until return?
grep -rn 'Shutdown(context\.Background' # blocks forever if handler hangs
grep -rn '\.RLock()'               # followed by network/file I/O before RUnlock?
grep -rn 'sync\.Pool'              # objects zeroed before Put?
grep -rn '\.Load()' | grep '\.Store\|\.Add'  # compound atomic — needs CAS?
grep -rn 'close(ch\|done\|abort)'  # without sync.Once nearby?
grep -rn '\.Signal()'              # should this be Broadcast()?
grep -rn 'go .*{' | grep 'for '   # fan-out in loop — bounded?
```

---

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
