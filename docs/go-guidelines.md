# Go engineering in mini

Read this when writing or reviewing Go. Follow [AGENTS.md](../AGENTS.md) for project rules, [testing.md](testing.md) for test design, and the review skills for detailed checks. External style preferences do not override mini policy.

## Design around contracts and ownership

- Trace the production entry point, callers, state transitions, external effects, and cleanup, including relevant failure and concurrent paths. Find existing operations and helpers before adding another implementation.
- For substantial changes, compare approaches by correctness, maintainability, simplicity, then performance. Name the invariant: a denied call never reaches upstream; stale reconnect work cannot resurrect a removed server.
- Put policy and lifecycle operations with the state they govern. Judge boundaries by invariants, testing obstacles, and concrete maintenance cost, not file counts or a hypothetical preferred design.
- Prefer concrete types; define small interfaces at consuming boundaries when substitution, testing, or a meaningful contract warrants them. One implementation can suffice. Share code when contracts match; avoid wrappers, generics, and helpers that add complexity without useful behavior.

## One implementation behind every way in

- Each mini operation has one implementation, and every way in calls it. The ways in are the CLI commands in `cmd/mini`, the MCP server agents use (`internal/server`), and any UI. A way in only translates. It turns flags, wire requests or form input into resolved values, calls the operation, and renders the result or error. An operation reimplemented in a way in drifts from the others; fix the shared operation instead.
- A shared operation lives in the package for its concept (`auth`, `config`, `projection`, `response`), not in a way in and not in a generic operations package. When a second way in needs an operation that lives inside the first, move it out rather than importing that way in.
- Shared operations don't parse flags or write to the terminal. They take resolved values, plus the writers or callbacks their output goes to.
- Wire long-lived dependencies once, at the top of the process, and pass them in. Expensive ones are built on first use: a command that only reads config doesn't dial upstreams or start a daemon. Carry per-call options in a params struct. Prefer constructors that take their dependencies, so a test builds the production object with fakes instead of swapping package state.
- Keep the long-lived set to shared dependencies. It turns into a god object when it collects per-operation state, or when it grows a field or method for every operation.
- Dependencies point one way: ways in import shared packages, never the reverse, and one way in doesn't import another. The process entry point (`main` in `cmd/mini`) may import every way in to host it. If two packages need each other, move the shared concept into a third package named for it.
- Name a package for what it provides (`projection`, `transport`). A bare catch-all (`util`, `common`, `misc`, `types`, `api`) or a layer word (`manager`, `service`, `ops`) says where code sits, not what it is, so unrelated code collects there; a focused helper like `testutil` is fine. Don't repeat the package name in its identifiers (`transport.Connection`, not `transport.TransportConnection`).
- Split a package when it gains an unrelated reason to change, or a dependency its other users don't need. Don't split by file count or to match a diagram.

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
- Package boundaries: [package names](https://go.dev/blog/package-names), [SOLID Go design](https://dave.cheney.net/2016/08/20/solid-go-design), [how one service wires main, dependencies and tests](https://grafana.com/blog/2024/02/09/how-i-write-http-services-in-go-after-13-years/).
- Concurrency: [memory model](https://go.dev/ref/mem), [pipelines and cancellation](https://go.dev/blog/pipelines), current standard-library documentation.
- Error contracts: [whether to wrap errors](https://go.dev/blog/go1.13-errors#Whether_to_Wrap).
- Specialized verification: [fuzzing](https://go.dev/doc/security/fuzz/), [testing/synctest and its limits](https://pkg.go.dev/testing/synctest), [Go security practices](https://go.dev/doc/security/best-practices). Use when the changed risk warrants it; preserve existing test infrastructure when it fits.
