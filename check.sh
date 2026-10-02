#!/usr/bin/env bash
set -euo pipefail

export PATH="/opt/homebrew/bin:$(go env GOPATH)/bin:$PATH"

# CI must fail on unformatted code, so only local runs fix it; golangci-lint run reports what's left.
if [[ -z "${CI:-}" ]]; then
  golangci-lint fmt
fi
go build ./...
# go vet ./... doesn't run against all tags by default
# specifying the tags here runs it over all the prod code AND the tests
go vet -tags integration,test ./...
staticcheck ./...
golangci-lint run
go run ./tools/funclen .
go run ./tools/params .
go run ./tools/returns .
go run ./tools/clocklint .
go run ./tools/testkind .
testbin=$(mktemp -d)
go build -o "$testbin/echomcp" ./cmd/echomcp
export ECHOMCP_BIN="$testbin/echomcp"
go test -race -tags test ./...
go test -race -tags integration,test -run '^TestIntegration' -timeout 180s ./...
