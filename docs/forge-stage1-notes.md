# Forge Stage 1 — implementation notes

Companion to [forge-prototype.md](forge-prototype.md). Covers what was built, measured overhead,
Deno findings, security limits, and revised estimates for the next steps.

## What shipped

`execute_code(code, input)` on mini's MCP server, gated behind `experimental_code_mode: true`
in `config.yaml` (off by default; exposed in both compact and proxy tool modes when enabled).

- `internal/forge` — the runtime. `forge.Execute(ctx, Params{Code, Input, Timeout})` spawns a
  fresh `deno run --no-prompt --no-remote -` per call, pipes a generated harness module to
  stdin (no temp files), and parses a marker-delimited result protocol.
- `internal/server/code_mode.go` — the MCP handler; ~25 lines, reuses the existing tool-error
  normalization.

### Harness design

The user's code is wrapped as `export default (<code>\n);`, base64-encoded, and dynamically
imported inside the harness via a `data:` URL. This makes syntax errors in user code catchable
(`TypeError` whose message embeds the `SyntaxError` diagnostic) instead of crashing the module,
so all error classification flows through one protocol. The harness's only result channel is a
final synchronous stdout write of `\n<random 16-hex marker>{"ok":...}` or
`{"error":{"kind":...}}`; everything before the last marker occurrence is console output, kept
for diagnostics on failure. Program output cannot spoof the result: the marker is random per run
and the genuine emission is always last.

Error kinds: `syntax`, `runtime`, `timeout`, `cancelled`, `not_serializable`,
`output_too_large`, `runner`. Runtime stack traces are sanitized before returning to the agent:
harness frames and host filesystem paths are stripped, the base64 data URL is collapsed to
`code`, and line numbers align with the user's source (`at default (code:1:34)`).

### Limits (implementation choices, not product decisions)

- Timeout: 30s default, caller-overridable; enforced Go-side via context, process killed.
- stdout capture: 8MB, process killed immediately on overflow (an infinite-print program fails
  in well under a second rather than sitting blocked until timeout). stderr: 64KB.
- Child env: only `PATH`, `HOME`, `DENO_NO_UPDATE_CHECK=1` (defense in depth — user code cannot
  read env anyway).

## Measured overhead

Apple M4 Pro, Deno 2.6.3: **~15.7ms per trivial `execute_code` call**, end-to-end through
`forge.Execute` (10-iteration benchmark; matches raw `deno run` startup, so harness generation
and JSON plumbing are negligible). Comfortably below one model turn by 2–3 orders of magnitude,
which is the comparison that matters for the hypothesis. All 10 acceptance scenarios from the
brief are covered by tests; the package suite runs in ~3s.

## Deno findings

- `deno run -` parses stdin as module TypeScript (`.mts`): top-level await, `import()`, and TS
  syntax all work with zero flags and no temp files.
- `data:` URL dynamic imports need no permission flags, and their syntax errors are catchable
  with a full source snippet in the message.
- `fetch("http://localhost:1")` fails via Deno's built-in bad-ports blocklist
  (`TypeError: ... port 1 [is] blocked`), not the permission system. Other ports/hosts fail
  with `NotCapable`. Net egress is fully blocked either way; only the error shape differs.
- `Deno.stdout.writeSync` + `Deno.exit(0)` flushes reliably and kills dangling
  timers/intervals — a leftover `setInterval` does not delay process exit.
- Sandbox denials (`fs`, `env`, `net`, subprocess, FFI) all throw catchable `NotCapable`
  errors at runtime, so they classify as ordinary runtime failures with clear messages.

## Security limitations

- Deno permissions bound *capabilities*, not *resources*: no CPU or memory ceiling. A busy
  loop is stopped only by our timeout; a program allocating gigabytes without printing is not
  stopped until the timeout. Acceptable for Stage 1 (accidental-harm posture), inadequate for
  hostile code. If this matters later: `--v8-flags=--max-old-space-size` is a cheap partial
  memory bound.
- This is one OS process under the user's own account. No seccomp/namespace/container layer.
  Deno's permission model is the entire boundary, as the prototype doc anticipated.
- `Params.Code` size is uncapped Go-side; a pathological caller can pipe an arbitrarily large
  program. Worth a cheap cap before any exposure beyond trusted local agents.

## Revised estimates

- **Standalone Forge MCP server** (own binary, out of mini): ~1 week. The runtime package is
  self-contained (~350 LOC, one dependency on an internal rand helper), so the cost is mini's
  MCP serve loop: extract a shared transport/server library (2–5 days per the earlier coupling
  review) or hand-roll a minimal stdio server (~2 days). Plus CLI scaffold and config. Staying
  inside mini until Stage 2 proves out remains the cheaper path.
- **Stage 2 (pinned library imports)**: 3–5 days. Deno makes the run-side trivial —
  `--cached-only` plus a lockfile keeps execution offline with pre-resolved deps, and imported
  code inherits the same default-deny permissions. The real work is the resolution step
  (network-enabled `deno install` invoked by the host, not by programs), choosing where the
  per-program lockfile/import map lives, and deciding which registries (jsr/npm) to allow.
  Stage 2 does not disturb the Stage 1 execution path: same harness, one extra flag, plus a
  cache directory.
