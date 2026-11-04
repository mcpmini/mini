# Contributing

## Setup

You need the Go version in `go.mod`, [golangci-lint](https://golangci-lint.run/) v2.14.0 (the version CI pins) and [staticcheck](https://staticcheck.dev/).

Install the pre-push hook once per clone. It lints each commit you push the way CI's lint job does and refuses the push if one fails:

```bash
ln -s "$PWD/scripts/pre-push" "$(git rev-parse --git-common-dir)/hooks/pre-push"
```

## Checks

`./check.sh` runs the build, lint and race tests that CI runs. Run locally, it also applies formatting fixes before it lints, so commit what it changes.

Build, test and style details are in [AGENTS.md](AGENTS.md).
