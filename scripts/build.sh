#!/usr/bin/env bash
# Build the mini binary with an accurate version string.
#
# debug.ReadBuildInfo's embedded VCS info is unreliable for builds done from a
# git worktree (golang/go#58218, #64772), so we compute the revision via git
# directly and inject it with -ldflags.
#
# Usage:
#   ./scripts/build.sh [-o OUTPUT]

set -euo pipefail

OUT="mini"
if [[ "${1:-}" == "-o" ]]; then
  OUT="$2"
fi

REV=$(git rev-parse --short=7 HEAD)
if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
  REV="${REV}+dirty"
fi

go build -ldflags "-X github.com/mcpmini/mini/internal/version.buildRevision=${REV}" -o "$OUT" ./cmd/mini
