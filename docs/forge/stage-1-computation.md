# Stage 1 — run code

Branch: `forge-01-run-code` (was `forge-stage1`). Capability: an agent can submit a TypeScript
function and get its result back, with nothing else possible.

## What the user gets

The agent can compute: parse, filter, group, transform data it was handed, and return a
JSON-compatible result. The code runs in a locked box — no internet, no files, no environment
variables, no processes, no packages, no tools. Every run starts fresh; nothing carries over
between runs.

## Behavior (shipped)

- `execute_code(code, input)`: `code` is an async function source, `input` is the JSON value
  passed to it; the function's return value is the result (missing return → `null`).
- Fresh `deno run --no-prompt --no-config -` subprocess per call; program piped via stdin; user
  code imported from a data: URL so syntax errors are catchable and line numbers align.
- Errors come back classified (syntax, runtime, timeout, cancellation, oversized output,
  non-serializable result, runner failure) with sanitized stack traces.
- Cancelling the MCP call kills the subprocess. Output capped (8MB stdout, 64KB stderr, with
  drain past the cap so noisy programs can't hang the run). 30s wall-clock timeout.
- Concurrent runs don't interfere; one run can't observe another's state.

## Requirements — next build

Nothing new lands here in the current round. This stage is the foundation everything else
restacks onto; it must stay green through the restructure.

## Known gaps (accepted)

- No CPU or memory ceiling — the timeout and output caps are the only resource bounds. Fine for
  a local single-user tool; revisit if Forge ever runs anywhere shared.
- Stack traces can include fragments of the data: module URL after certain errors — noisy, not
  a leak. Tighten when error ergonomics becomes a focus.

## Decisions log

Executing agents: append entries here (date, what was decided, alternatives considered and why
they lost). Keep it short and honest — this is the record the next agent reads first.
