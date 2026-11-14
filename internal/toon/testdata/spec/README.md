# Vendored TOON spec fixtures

Encode test fixtures from the [TOON specification](https://github.com/toon-format/spec), release [v4.4.0](https://github.com/toon-format/spec/tree/d09c3d33e37389345a74bd96981eb96dd9be6c5e).

## Provenance

| | |
|---|---|
| Source | https://github.com/toon-format/spec |
| Release | v4.4.0 |
| Commit | `d09c3d33e37389345a74bd96981eb96dd9be6c5e` |
| Vendored | `tests/fixtures/encode/*.json` → `encode/`, `LICENSE` → `LICENSE` |
| License | MIT (see [LICENSE](LICENSE)) |

Decode fixtures (`tests/fixtures/decode/`) are intentionally not vendored — mini's toon package is encode-only.

## Updating

To update fixtures to a new spec release, replace the files in `encode/` from the upstream `tests/fixtures/encode/` directory and update this README with the new commit hash and release tag.
