#!/usr/bin/env bash
# The convention checks and vet shared by check.sh and CI's lint job.
set -euo pipefail

go run ./tools/funclen .
go run ./tools/params .
go run ./tools/returns .
go run ./tools/clocklint .
go run ./tools/testkind .
go run ./tools/fileiolint .
GOFLAGS="${GOFLAGS:-} -tags=integration,test" go run ./tools/structlint ./...
# go vet skips files behind build tags unless they're named; these cover the prod code and every test.
go vet -tags integration,test ./...
