#!/usr/bin/env bash
set -euo pipefail

export PATH="/opt/homebrew/bin:$(go env GOPATH)/bin:$PATH"

go build ./...
staticcheck ./...
golangci-lint run
go run ./tools/funclen .
go run ./tools/params .
go run ./tools/returns .
go test -race -tags test ./...
