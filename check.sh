#!/usr/bin/env bash
set -euo pipefail

export PATH="/opt/homebrew/bin:$(go env GOPATH)/bin:$PATH"

go build ./...
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
