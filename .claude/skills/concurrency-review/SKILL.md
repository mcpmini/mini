---
name: concurrency-review
description: Thorough concurrency review of Go code — races, goroutine leaks, lock coverage, channel safety, deadlocks. Produces a dated findings report.
argument-hint: [path or blank for full repo]
allowed-tools: Read, Grep, Glob, Bash, Write
---

Perform a thorough concurrency review of $ARGUMENTS (default: the full repo).
Write findings to `concurrency-review-<YYYY-MM-DD>.md` in the repo root.

## Step 1 — Run the tools first

```bash
go test -race ./...
go vet ./...
```

If `staticcheck` or `golangci-lint` are available, run them too. The race detector
catches runtime data races that static analysis misses; read every failure carefully
before moving to manual review. Record any tool findings verbatim in the report.

## Step 2 — Manual review

Work through the bug categories below. For each one, grep and read relevant code.
Do not limit yourself to these — they are a starting point, not a checklist.

### Goroutine lifecycle and leaks

Every goroutine must have a clear, reachable termination path. Grep for `\bgo ` and for each launch ask:
- What stops it? A `done` channel, a context cancellation, or an error return?
- What context is it given? `context.Background()` or `context.TODO()` inside a spawned goroutine is a red flag — if that goroutine blocks on I/O or a channel, there is no way to cancel it. Leaks compound: each repeated call spawns another.
- Does it hold resources (subprocess, TCP listener, file handle, ticker)? If it leaks, those leak too.
- If it panics, is there a `recover`? An unrecovered panic in a goroutine kills the whole process.

### Data races on shared state

Find every struct that is shared across goroutines. For each field: who reads it, who writes it, and is every access covered by the same mutex?

Common misses:
- A field written once after a goroutine is launched — the goroutine may read before the write is visible without a happens-before edge (channel send, mutex unlock, or `sync/atomic`).
- Partially protected structs: most fields under a lock, but one "obviously safe" field accessed bare.
- Closures capturing loop variables or outer mutable state.
- `go vet` catches mutex copying; `-race` catches the rest at runtime.

### Map and slice concurrent access

Any concurrent read during a write on a Go map causes a fatal runtime crash (not a data race — a hard crash with `concurrent map read and map write`). Slices are not crash-safe either. Confirm every map/slice shared across goroutines has lock coverage on all read and write paths, including iteration (`range` over a map is a read).

### Lock discipline

- **Lock ordering:** if two mutexes are ever both held, is the acquisition order consistent everywhere? Inconsistent ordering → deadlock.
- **Lock recursion:** Go mutexes are not reentrant. A goroutine that holds a lock and calls a function that tries to acquire the same lock will deadlock.
- **RLock upgrade:** holding `RLock` and calling `Lock` on the same mutex deadlocks.
- **Holding locks across I/O or channel ops:** a goroutine blocked on I/O while holding a lock starves every other goroutine that needs it. Look for network calls, file writes, or channel sends inside lock regions.
- **Large critical sections:** functions that acquire and release the same lock multiple times are hard to reason about — each gap is a potential race window.

### Channel safety

- Sending to a closed channel panics. `select { case ch <- v: default: }` does NOT protect against this — `default` fires when the channel is full, not when it is closed. Only a nil channel is safe to send to without risk of panic.
- Closing a closed or nil channel panics. Closing must be done exactly once; wrap with `sync.Once` if multiple goroutines could close.
- After a channel is closed, any struct field holding it should be set to nil under the protecting mutex before any other goroutine can observe it. A non-nil field pointing to a closed channel is a panic waiting to happen.
- `time.After` in a `select` leaks a timer goroutine until the duration expires; prefer `time.NewTimer` with an explicit `Stop()`.

### Blocking without guaranteed escape

For every `select` that blocks waiting on channels: trace every code path that closes or sends to each case channel. Ask: are **all** equivalent calling paths — error returns, alternate transports (stdio vs HTTP), shutdown sequences — guaranteed to eventually fire one of the cases?

A `select` waiting on `done | abort | ctx.Done()` where `abort` is only closed in one of two equivalent code paths (e.g. stdio `Serve` loop but not HTTP handler) blocks forever on the unguarded path. The race detector will not catch this — it is a logical hang, not a data race.

Checklist:
- For each channel in the select, who closes/sends to it? List every call site.
- Is there a code path that reaches this select without any of those senders being reachable? Error path? Alternate transport? Session eviction?
- Is there a deadline on the blocking context (`context.WithTimeout`) as a backstop even if the primary escape is missing?

**Asymmetric cleanup** is the same class: when a lifecycle action (closing a channel, calling a cancel func, setting a flag, calling `markAborted`) runs in one handling path, grep for the equivalent action in all other paths that share the same lifecycle. If `serveLoop` calls `markAborted()` on exit but the HTTP handler path never does, every session created via HTTP is permanently stuck in `waitInitialized` after eviction or daemon restart.

### TOCTOU (check-then-act) races

A lock protects only what is inside it. A check inside a lock and an action outside it are not atomic:

```go
mu.Lock(); ok := m[k] != nil; mu.Unlock()
// <-- another goroutine can change m here -->
m[k] = newValue  // race
```

Also look for paired operations that must be atomic but aren't — two separately-locked calls (e.g., `Remove` then `Add` on a shared index) leave a window where the state is inconsistent and concurrent observers see a partial update.

### sync primitives misuse

- `WaitGroup.Add` must be called before the goroutine that calls `Done` is launched, not inside it.
- `sync.Once` functions that panic leave the Once in a broken state — subsequent calls silently do nothing.
- Copying a `sync.Mutex`, `sync.WaitGroup`, or `sync.Cond` after first use is a bug (`go vet` catches this).

### Initialization races

- Package-level variables mutated after `init` are shared global state — any goroutine touching them needs synchronization.
- Lazy initialization (`if x == nil { x = ... }`) without `sync.Once` or atomics is a data race.
- Constructor functions that start goroutines or background loops before returning — callers may not realize the object is "live" the moment `New()` returns.

## Smell tests — quick scan for red flags

These patterns do not always indicate a bug but warrant a closer look:

- `go func()` with no `done` channel, no context, and no `WaitGroup` — orphaned goroutine
- `context.Background()` or `context.TODO()` passed into a long-lived goroutine or blocking call
- `sync.Mutex` embedded in a struct that is passed by value
- `close(ch)` without `sync.Once` nearby
- `select { case ch <- v: default: }` near a `close(ch)`
- Lock acquired, I/O performed, lock released — holding lock across blocking call
- Two mutexes acquired in the same function — check ordering
- `time.Sleep` in production code — usually indicates polling without proper signaling
- `atomic.Value` or `atomic.Pointer` storing a struct with pointer fields — the whole value must be swapped atomically; partial field updates still race
- A `Close()` method without `sync.Once` — double-close panics channels, corrupts state
- `http.Server.Shutdown(context.Background())` — if any handler can block indefinitely (tool timeout disabled, hanging subprocess), the daemon never exits; always pass a bounded context
- RLock held across a network call — blocks reconnect from taking the write lock while TCP zombies are in-flight; snapshot the pointer under the lock, release, then call

## Report format

```markdown
# Concurrency Review — DATE

## Summary

## Issue N — PRIORITY: title
**File:** path/file.go:LINE
**Bug:** what the race is and what shared state is involved
**Trigger:** concrete scenario (slow request, concurrent calls, reconnect, etc.)
**Impact:** panic / goroutine leak / data corruption / transient error

## Non-issues confirmed safe
Brief note on patterns investigated and found correct, with one-line reason.
```

Priority: **HIGH** (panic, crash, leak, corruption) → **MEDIUM** (user-visible errors, fragile invariants) → **LOW** (benign, self-correcting, very narrow window).

Always include the non-issues section — confirming safe patterns prevents false positives in future reviews and builds confidence in the code.
