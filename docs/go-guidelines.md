# Go engineering in mini

Read this when writing or reviewing Go. Follow [AGENTS.md](../AGENTS.md) for project rules, [testing.md](testing.md) for test design, and the review skills for detailed checks. External style preferences do not override mini policy.

## Design around contracts and ownership

- Trace the production entry point, callers, state transitions, external effects, and cleanup, including relevant failure and concurrent paths. Find existing operations and helpers before adding another implementation.
- For substantial changes, compare approaches by correctness, maintainability, simplicity, then performance. Name the invariant: a denied call never reaches upstream; stale reconnect work cannot resurrect a removed server.
- Put policy and lifecycle operations with the state they govern. Keep CLI and protocol handlers focused on translation. Judge boundaries by invariants, testing obstacles, and concrete maintenance cost, not file counts or a hypothetical preferred design.
- Prefer concrete types; define small interfaces at consuming boundaries when substitution, testing, or a meaningful contract warrants them. One implementation can suffice. Share code when contracts match; avoid wrappers, generics, and helpers that add complexity without useful behavior.

## Preserve Go and protocol semantics

- Handle failure early and keep the normal path readable. Extract meaningful operations when meeting repository size limits.
- Make zero values useful where practical; require construction when it enforces an invariant. Check typed nils: an interface containing a nil pointer is not nil.
- Use pointer receivers for mutation and structs containing locks or other no-copy state. Respect no-copy contracts after first use.
- Decide whether maps, slices, and pointers are borrowed, transferred, or copied. Shallow copies and atomically published pointers do not isolate reachable mutable state.
- Preserve missing versus present, zero versus unspecified, and JSON `null` versus `[]`. Apply defaults at a clear boundary.

## Errors, resources, and state transitions

- Return useful, secret-safe errors for expected failures. Use `errors.Is`/`As` for classification; wrap with `%w` when exposing the cause is part of the contract. Handle or report an error where a decision belongs; justify ignored errors, especially for writes and cleanup.
- Give every acquired resource an owner and cleanup path, including partial startup failure. Register cleanup promptly; loop-scoped acquisitions may need a smaller function because `defer` waits for function return.
- For persisted and live state, define success and the state left by each failure. Check rollback, retries, and concurrent removal or replacement. Locking individual steps does not make a transition atomic. Before replaying a request, establish whether its side effects may already have occurred.
- Trace reachable panic paths. Routine input and network failures should return errors; recover only at an intentional containment boundary that reports failure and preserves invariants.

## Concurrency and lifecycle

- Give each goroutine an owner, stop condition, and completion signal. Cancellation requests a stop; it does not prove completion. Unblock I/O and channel operations, and join workers before releasing state they use.
- Propagate request contexts; give background services their own lifecycle. Bound interactive waits, shutdown, retries, and fan-out according to the contract.
- Protect conflicting accesses and whole transitions. Check lock order, callbacks or I/O under locks, and the validity of snapshots after unlocking. Choose mutexes, channels, or atomics to match the invariant.
- Own channel closure and exclude overlapping sends. `sync.Once` only coordinates closure; a `select` default does not make sending to a closed channel safe.
- Check version-dependent behavior against `go.mod` and current API documentation. Check mini's injected clock separately; its behavior need not match every standard-library timer guarantee.

## Prove behavior through the right boundary

Validate untrusted input and enforce permissions at their owning boundary, consistently across CLI, configuration, and MCP entry points. Check framing, limits, paths, redirects, credentials, and output filtering where relevant. Read applicable MCP specifications and compatibility versions for wire changes.

Distinguish protocol errors from tool execution errors. Preserve the response content and metadata required by the client contract.

Use the testing guide to cover observable contracts through production paths. Force relevant concurrency orderings with events or controlled dependencies; establish completion before checking absent side effects. Verify changed callers and equivalent transports. Run appropriate repository checks and report meaningful gaps. Findings need reachable triggers and consequences; pattern matches and style preferences alone do not prove bugs.

## References for specific questions

- Idioms and design: [Effective Go](https://go.dev/doc/effective_go), [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments), [Google Go Style](https://google.github.io/styleguide/go/), [Uber Go Style Guide](https://github.com/uber-go/guide).
- Concurrency: [memory model](https://go.dev/ref/mem), [pipelines and cancellation](https://go.dev/blog/pipelines), current standard-library documentation.
- Error contracts: [whether to wrap errors](https://go.dev/blog/go1.13-errors#Whether_to_Wrap).
- Specialized verification: [fuzzing](https://go.dev/doc/security/fuzz/), [testing/synctest and its limits](https://pkg.go.dev/testing/synctest), [Go security practices](https://go.dev/doc/security/best-practices). Use when the changed risk warrants it; preserve existing test infrastructure when it fits.
