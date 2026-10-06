#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
exec go run golang.org/x/vuln/cmd/govulncheck@latest ./...
