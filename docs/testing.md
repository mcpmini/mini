# Testing mini

Tests give us fast evidence that behavior still works while the implementation changes. A useful failure points to a product or contract problem worth investigating. Test count and line coverage cannot tell us whether an assertion would catch a regression.

Early automated unit-test frameworks made small checks cheap to rerun after each edit. As suites grew, the [test pyramid](https://martinfowler.com/bliki/TestPyramid.html) captured the cost of checking every rule through a whole application: broad tests are valuable for wiring, but usually slower and harder to diagnose. The lasting lesson from [xUnit's history](https://martinfowler.com/bliki/Xunit.html) is rapid feedback; the lesson from the pyramid is to test each risk at the level that can expose it clearly. Neither requires writing a failing test before every change or a fixed ratio of test types.

## Choose the evidence a change needs

Start with the observable contract, its realistic entry point, and a plausible way it could fail. Look for existing tests before adding one. Choose the smallest level that can fail for the right reason, then add a wider test when wiring or process behavior is itself the risk. These levels describe what runs together and where the test enters the system; a file's name or directory alone does not establish its level.

| Level | Use it for | Example in mini |
| --- | --- | --- |
| Unit | Exercise one rule or cohesive piece of logic with controlled inputs and collaborators. Cover meaningful input, boundary, and error permutations here. | `projection.Apply` with constructed values; config parsing. |
| Integration | Run multiple real components together and control only the external boundaries. Check their wiring, state transitions, and protocol handling. An in-memory server or command tree belongs here. | Server request handling through registry and projection with `transport.FakeConnection`; HTTP handling with `httptest.Server`; `newRootCmd()` in `cmd/mini/root_test.go`. |
| End-to-end | Launch the built `mini` binary and interact through its public CLI or MCP interface. Check behavior across the process boundary as a black box. | `test/integration/cli_test.go` for CLI behavior; `test/integration/server_test.go` and `proxy_mode_test.go` for MCP journeys with the fake upstream. |

New rules should normally have unit tests. Mini can also have many integration tests: they prove that real components work together and are often fast enough to cover meaningful boundary and state combinations. End-to-end tests should cover critical journeys and process-specific behavior without repeating every unit permutation. A fake upstream keeps mini's end-to-end tests repeatable; it does not make them integration tests or prove compatibility with every external MCP server.

### Decide what to add

Choose tests for the failure a change could cause, not a quota at each level. Behavior that exists only at an integration or process seam does not need a manufactured unit test.

| Change | Start here | Add a wider test when |
| --- | --- | --- |
| Rule, parser, or transformation | Unit cases for distinct inputs, boundaries, and errors. | Config-to-server wiring or another component interaction needs an integration test; process behavior needs end-to-end coverage. |
| Server routing, permissions, or projection | Unit cases for the rule plus integration requests through the relevant handler. | The public MCP wire path or process setup could change the outcome. |
| CLI command or flag | Unit cases for separable logic plus an integration test of the command tree, parsing, and streams. | The outcome depends on `main` or the OS process: actual exit status, direct writes to process streams, startup environment, or child-process behavior. |
| Transport, retry, auth, or lifecycle | Unit cases for the rule plus integration tests at the affected HTTP, state, or timing boundary, including failure and cleanup when relevant. | Stdio framing, daemon wiring, subprocess exit, or a critical journey needs proof. |
| Edited catalog or fixture | Existing load and validation tests, plus a behavioral case if the edit changes a promised outcome. | A user journey or stable external contract changes; routine content edits need no new end-to-end test. |

A bug fix usually deserves a regression that reaches the old failure through a production path. A pure refactor may need only the existing suite. If an existing test already reaches the behavior and would catch the regression, strengthen it when needed instead of adding a duplicate. Higher-level tests may repeat a little behavior to prove a boundary works, but should not replay every component permutation or pin incidental upstream data.

Coverage is a way to find surprising unexercised code, not a score to optimize. There is no global percentage target or routine mutation-testing requirement. A race-detector pass covers only paths and processes actually run with race instrumentation.

## Other kinds of tests

The level says **what runs together and where the test enters**. These terms describe **why or how** it tests; one test can have several of them:

- A **regression test** protects a previously broken behavior. Put it at the narrowest production-reachable boundary that catches the bug.
- A **contract or conformance test** checks a stable external promise, such as MCP message shape, CLI exit behavior, or a specified encoding. Use external fixtures when a standard defines the expected result.
- A **failure, lifecycle, or concurrency test** controls a timeout, disconnect, retry, cancellation, ordering, or cleanup transition. It can run at any of the three levels, depending on where the risk lives.
- A **property or fuzz test** explores many inputs against an invariant, especially for parsers and transformations. It complements examples with decisive expected outcomes.
- A **golden or snapshot test** stores an expected result for a large, stable output. It is an assertion technique; review updates to the expected file as carefully as code changes.
- A **smoke test** checks that a critical journey works at all. Keep it small; focused tests should explain individual rule failures. Benchmarks measure performance and need separate interpretation from correctness tests.

None of these names is a checklist to exhaust for each change. Choose the risks and boundaries that matter, then make the assertions strong enough to catch them.

## Placement and build tags

Keep unit and integration tests near the package they exercise. Put new black-box end-to-end tests in the existing `test/integration` suite, where the shared harness builds and launches the binaries. That directory and its `integration` tag have historical names; the tests are end-to-end when they enter through the built mini binary. Separate tests by the boundary and setup they share, not by labels such as “regression” or “golden.”

| Build tag | What it means here | Standard `check.sh` |
| --- | --- | --- |
| None | Ordinary package test. | Included in the race-enabled `go test -tags test ./...`. |
| `test` | Package tests or test-only helpers that need this tag. It does not mean “unit.” | Included in the same race-enabled run. |
| `integration` | Historical tag for real-binary end-to-end tests and the fake MCP binary. | Runs tests under `test/integration` only; child binaries are not built with `-race`. |
| `live` | Two existing opt-in server test files using real external MCPs. It is an environment selector, not another test level. | Not included; running these server tests currently requires both `live` and `test` tags. |

The repository also uses `evals` to build evaluation tooling and platform selectors such as `!windows`; neither is a test level. Do not add a build tag just to label a test “unit,” “contract,” or “smoke.” A tag alone does not prove the standard gate selects a file: `check.sh` runs the `integration` suite only under `test/integration`, so the tagged tests in `cmd/mini/cli_test.go` are outside that gate. Check the command and the selected tests before claiming coverage.

## Assert behavior that should remain true

Prefer an observable result, error, state transition, outbound call, or forbidden side effect that the product promises. Make the decisive assertion easy to find. A test that only proves setup succeeded, checks a static value, or matches incidental fixture content gives weak protection.

- For a frequently edited JSON catalog, test that it loads and drives the intended behavior. Do not pin its entry count or a particular incidental annotation unless that value is itself a contract.
- To test annotation passthrough or a policy rule, construct the annotation as input and assert what mini does with it. This tests behavior rather than the current contents of a changing file.
- For a transition, establish the relevant precondition first, perform the action, then assert the resulting state. This helps show the action caused the result.
- Negative contracts matter. When denying a call, wait for the denial to complete, then use a recording fake or another observable side effect to confirm no upstream call occurred. An elapsed sleep followed by “still zero” is not a reliable barrier.

A useful failure should identify which contract broke. Avoid exact output snapshots for large or frequently changing data when a small semantic assertion would be clearer. Goldens can help for stable, complex output when their diffs are reviewed and updating them is deliberate.

## Treat tests as code

Keep setup, action, and assertions short and legible. Reuse existing helpers before adding another. Extract repeated protocol framing, config writing, or fake setup into helpers named for the domain operation; call `t.Helper()` in helpers that fail through `*testing.T`. Typed options are clearer than long positional arguments or boolean switches. Keep the behavior and decisive assertion visible in the test: a helper that silently performs both can make a passing test hard to trust.

Use tables when cases share the same flow and differ only in inputs and expected outcomes. Use separate tests when scenarios have different actions or failure mechanisms. Some repetition in broad scenarios is acceptable when sharing it would hide the journey. Do not create a universal harness merely to remove a few similar lines. Do not use comment dividers to organize test files; descriptive test names, subtests, and files do that job.

Tests must own their resources. Isolate config, home, output, sockets, and temporary files; close listeners and response bodies; stop and join child processes and goroutines. A test should not read or write the developer's `~/.mini`.

## Control failures and time

Exercise network errors, delays, cancellation, malformed responses, partial failure, retry, and process exit where the changed contract depends on them. Simulate each failure at the boundary it belongs to: `FakeConnection` for server routing, `httptest.Server` for HTTP behavior, and the fake MCP process for real stdio or subprocess behavior. Add a fake capability when a named product test needs it; prefer scripted responses and observable call events over probability.

Synchronize on events, channels, completion, or fake-clock registration. A bounded real timeout can guard against a hung test; measure wall time only when response latency itself is the contract. Where code already injects a clock, advance the fake clock after the relevant timer or waiter has registered.

## Author and review loop

Before writing a test, inspect nearby tests and helpers, identify the changed behavior and failure risk, and choose the test level. Run the focused test with the right build tags and confirm that it selected the intended cases; a successful `-run` command can select zero tests. Then run the relevant repository checks. Report the commands, results, and any meaningful gap; distinguish an environment failure from a product failure.

Self-review the test as code: does it reach the changed path, show the action, and assert a stable contract? If an assertion's strength is uncertain, temporarily revert or perturb the behavior and rerun the focused test. The test should fail for the expected reason; a compile error or unrelated failure is not evidence. Restore the code afterward. This is an optional confidence check, not mandatory test-first development or a per-test mutation exercise.

A reviewer asks whether the coverage matches the risk, whether the setup is realistic, whether assertions could pass despite a plausible regression, and whether helpers improve readability without hiding behavior. Findings should reflect meaningful product risk or maintenance cost, not a preferred testing style alone. The PR review workflow carries that judgment.
