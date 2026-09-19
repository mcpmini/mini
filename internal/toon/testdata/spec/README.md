# Vendored TOON spec fixtures

Encode test fixtures from the [TOON specification](https://github.com/toon-format/spec), release [v4.1.1](https://github.com/toon-format/spec/tree/62f16b369408180f1faf1cba7da1b46d1f336f12).

## Provenance

| | |
|---|---|
| Source | https://github.com/toon-format/spec |
| Release | v4.1.1 |
| Commit | `62f16b369408180f1faf1cba7da1b46d1f336f12` |
| Vendored | `tests/fixtures/encode/*.json` → `encode/`, `LICENSE` → `LICENSE` |
| License | MIT (see [LICENSE](LICENSE)) |

Decode fixtures (`tests/fixtures/decode/`) are intentionally not vendored — mini's toon package is encode-only.

## Updating

To update fixtures to a new spec release, replace the files in `encode/` from the upstream `tests/fixtures/encode/` directory and update this README with the new commit hash and release tag.
