# Stage 2 — use libraries

Branch: `forge-02-packages` (was `forge-stage2`). Capability: the agent's code can use npm/jsr
packages it declares.

## What the user gets

The agent can say "I need zod and csv-parse" and use them in its code — real libraries for
parsing, validation, formatting, transforming. There's still no internet at runtime: packages
are fetched safely before the locked box starts, and the code can only import what it declared.

## Behavior (shipped)

- `packages` param on `execute_code`: `npm:`/`jsr:` specifiers only, max 8. Versions are
  optional (unpinned is allowed).
- Packages are resolved and downloaded host-side (with network) before the sandbox starts, then
  the run executes with `--cached-only` so nothing new can be pulled at runtime. A run with no
  packages gets `--no-remote --no-npm` so it can't reach the registry at all.
- Importing a package grants it no extra authority — it runs with exactly the run's permissions.
- Each declared package set gets its own private download cache, so a run can only import that
  set's packages and their dependencies — never something another run or the developer's own
  Deno work happened to leave lying around.

## Requirements — next build

1. **Cache location** — the package caches move out of the system temp directory into the app's
   own state directory (`<config>/internal/forge/cache/`). Rationale in
   the open-questions reference; note this coordinates with the stage-5 scratch move and the
   sweeper.

## Known gaps (accepted)

- A declared package's own dependencies are importable too — unavoidable when you declare a
  package. A dependency that is buggy or has been tampered with upstream is a real concern;
  pinning/lockfiles are the eventual answer, but Deno's lockfile support was judged too immature
  to lean on today. See the open-questions reference.
- "Latest" freezes: an unpinned package resolves to whatever was current when its cache was
  first built and stays there until the cache is swept (~7 days). This is Deno's own
  cache behavior, not ours. Forcing exact versions was ruled out — models tend to invent version
  numbers. See the open-questions reference (`~/proj/forge-open-questions-reference.md`).

## Decisions log

Executing agents: append entries here.
