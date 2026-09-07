#!/usr/bin/env bash
# Build test helper binaries into the given output directory.
# Usage: test/build.sh <outdir>
set -euo pipefail

outdir="${1:?usage: test/build.sh <outdir>}"
go build -o "$outdir/echomcp" ./cmd/echomcp
