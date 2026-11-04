#!/usr/bin/env bash
set -euo pipefail

export PATH="/opt/homebrew/bin:$(go env GOPATH)/bin:$PATH"

# CI must fail on unformatted code, so only local runs fix it; golangci-lint run reports what's left.
if [[ -z "${CI:-}" ]]; then
  GOFLAGS="${GOFLAGS:-} -tags=integration,test" go run ./tools/structlint -fix ./...
  golangci-lint fmt
fi
go build ./...
staticcheck ./...
golangci-lint run
scripts/lint.sh
testbin=$(mktemp -d)
scripts/test-bins.sh "$testbin" > "$testbin/env"
while IFS= read -r line; do export "$line"; done < "$testbin/env"
go test -race -tags test ./...
go test -race -tags integration,test -run '^TestIntegration' -timeout 180s ./...
