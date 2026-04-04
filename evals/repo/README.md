# acme/webapp

A Go HTTP API server for managing cached API responses.

## Architecture

- `response/` — response cache store (file-backed, budget-limited)
- `server/` — HTTP handlers

## Known issues

- `response/storage.go` `WritePair` does not check available disk space before
  writing, causing **"disk quota exceeded"** errors in production (Sentry: WEBAPP-ABC).
  Fix: check current usage against budget before writing.

## Development

```bash
go build ./...
go test ./...
```
