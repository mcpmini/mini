# Fix: params linter threshold

## Status

- `maxParams` lowered from 8 to 6. Done.
- `//nolint` / `//nolint:params` support added to `tools/params/main.go`, mirroring
  `tools/funclen`. Done.
- All 12 functions that violated the threshold of 6 were fixed by extracting params
  structs (none needed `//nolint:params` — all were genuine violations). Done.

## Remaining work (out of scope for this PR)

`maxParams` is still 6, not the originally intended 4. Lowering it further to 4 will
surface significantly more violations (~140+ functions across the codebase) and is
left as future work. When that work is picked up:

1. Run `go run ./tools/params/main.go .` with `maxParams` temporarily set to 4 to find
   all violations.
2. For each violation, extract a params struct where it's a real violation, or add
   `//nolint:params` where genuinely justified (e.g. constructors, functions whose
   params are forced by an interface).
3. Once all violations are addressed, lower `maxParams` to 4 and confirm `check.sh`
   passes.
