#!/usr/bin/env bash
# Builds the binaries that integration tests run, into DIR, and prints the env lines that point at them.
# Usage: scripts/test-bins.sh DIR, then export each printed line.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: scripts/test-bins.sh DIR" >&2
  exit 2
fi
dir=$(cd "$1" && pwd)

go build -o "$dir/echomcp" ./cmd/echomcp >&2
go build -race -tags test -ldflags "-X github.com/mcpmini/mini/internal/version.buildRevision=integration-test" -o "$dir/mini" ./cmd/mini >&2
go build -tags integration -o "$dir/fakemcp" ./test/fakemcp >&2
go build -o "$dir/structlint" ./tools/structlint >&2

echo "ECHOMCP_BIN=$dir/echomcp"
echo "MINIMCP_BIN=$dir/mini"
echo "FAKEMCP_BIN=$dir/fakemcp"
echo "STRUCTLINT_BIN=$dir/structlint"
