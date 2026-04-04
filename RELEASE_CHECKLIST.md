# Release Checklist

Run through this before tagging any release. Each section is ordered from fastest to slowest.

---

## 1. Build & lint

```bash
export PATH="/opt/homebrew/bin:$(go env GOPATH)/bin:$PATH"
./check.sh          # build + staticcheck + race tests — must be green
```

If `check.sh` is not available:
```bash
go build ./...
go vet ./...
staticcheck ./...
go test -race -tags test ./... -timeout 120s
```

---

## 2. Unit tests

```bash
go test -race -tags test ./... -timeout 120s
```

Expected: all packages pass, no data race warnings.

Key packages and what their tests cover:

| Package | What's tested |
|---|---|
| `internal/server` | MCP protocol, tool routing, projections, permissions, rate limiting, reconnect, auth flows |
| `internal/transport` | Stdio/HTTP connections, retry, SSRF validation, scanner, pending map |
| `internal/projection` | Field inclusion/exclusion, depth/array/string limits, HTML/MD stripping |
| `internal/response` | Envelope building, file store, TTL eviction, token estimation |
| `internal/config` | YAML loading, server/projection config parsing, ValidServerName |
| `internal/auth` | OAuth PKCE flow, token save/load |
| `internal/registry` | Tool indexing, permission resolution, search |

---

## 3. Integration tests

Integration tests build real binaries and spawn subprocesses. Require `npx` on PATH for filesystem MCP.

```bash
go test -tags integration,test ./test/integration/... -timeout 180s -v
```

Covers: full stdio/HTTP proxy flows, daemon mode, permissions, session isolation, auth header injection, CLI commands, concurrency under load.

---

## 4. CLI smoke tests

```bash
# These are integration-tagged; run with the integration suite above.
# To run in isolation:
go test -tags integration,test ./cmd/mini/... -v
```

Manual smoke test (takes < 5 min):

```bash
# Build the binary
go build -o /tmp/mini-test ./cmd/mini

# Empty config
/tmp/mini-test --config /tmp/mini-cfg ls                  # "no servers configured"

# Add and remove a server
/tmp/mini-test --config /tmp/mini-cfg add testserver --url https://httpbin.org/anything
/tmp/mini-test --config /tmp/mini-cfg ls                  # shows testserver
/tmp/mini-test --config /tmp/mini-cfg rm testserver
/tmp/mini-test --config /tmp/mini-cfg ls                  # no servers

# Init
/tmp/mini-test --config /tmp/mini-init init --yes         # creates subdirs

# Health check (no servers)
/tmp/mini-test --config /tmp/mini-cfg test                # exits 0

# Bad server name
/tmp/mini-test --config /tmp/mini-cfg add "bad/name" --url https://x.com  # exits non-zero
```

---

## 5. Benchmarks

Benchmarks compare token savings across projection modes. Run to check for regressions.

```bash
go test -tags test -bench=. -benchtime=3s ./internal/projection/... ./internal/server/...
```

Or use the CLI bench tool (requires fixture data in `benchmarks/`):
```bash
go run ./cmd/mini bench --bench-dir benchmarks
```

Expected: `lines` mode should save ≥ 40% vs raw on the GitHub fixtures. If savings drop significantly, a regression in the projection or render pipeline occurred.

---

## 6. Evals (requires Claude CLI)

Evals run real agent sessions. They require `claude` CLI on PATH and consume API tokens (~$1-5 per full run).

```bash
go test -tags evals ./evals/... -v -timeout 600s
```

What each eval covers:

| Eval | Flow | Key assertions |
|---|---|---|
| `TestEval_TokenBaseline` | No tool calls, 5 servers loaded | Non-empty response; measures schema token overhead |
| `TestEval_SprintPlanning` | Linear → GitHub → Jira | All 3 servers called, specific tools called, response references issue IDs |
| `TestEval_IncidentTriage` | Sentry → GitHub → Slack | `list_issues`, `post_message` called; response mentions auth/JWT errors from fixtures |
| `TestEval_ReviewPRs` | GitHub: list PRs → read files → comment | `list_pull_requests`, `get_file_contents`, `add_issue_comment` called; PR numbers in response |
| `TestEvalBugFixPipeline` | Jira → Sentry → code fix → GitHub PR | All services called, code actually modified, PR created, WEBAPP-441 ticket referenced |

Run a single eval for faster iteration:
```bash
go test -tags evals ./evals/... -run TestEvalBugFixPipeline -v -timeout 300s
```

---

## 7. Manual MCP verification

If you have mini running locally (e.g. connected to Claude Code):

1. **list** — call `list` with no args; confirm all connected servers appear
2. **call** — call a read-only tool (`github.list_pull_requests`); confirm trimmed response
3. **perm_call** — attempt a protected tool via `call`; confirm rejection; then via `perm_call`; confirm success
4. **config** — call `config` with `action: status`; confirm server health report
5. **config set_projection** — tune a projection live; call the tool again; confirm change applied
6. **config add_server / remove_server** — add a test HTTP server; verify it appears in `list`; remove it; verify it disappears
7. **Response files** — call a tool with a large response; confirm `inline: false` and a file path is returned; confirm the file exists and is valid JSON

---

## 8. Security checks

```bash
# Run the dedicated security test file
go test -tags test ./internal/server/... -run TestSecurity -v

# SSRF: confirm private URLs are rejected
go test -tags test ./internal/transport/... -run TestSSRF -v

# Check no obvious secrets in the binary
strings $(go build -o /tmp/mini-sec ./cmd/mini && echo /tmp/mini-sec) | grep -E "ghp_|sk-|AKIA" | head -5
```

Manual checks:
- Token files exist at `~/.mini/tokens/` with `0600` permissions
- Response files at `~/.mini/responses/` with `0600` permissions and `0700` directory
- `mini serve --http 0.0.0.0:4857` requires `--dangerous-nonloopback-http` flag

---

## 9. Pre-tag checklist

- [ ] `./check.sh` passes (build + lint + race tests)
- [ ] Integration tests pass
- [ ] No `TODO`/`FIXME`/`HACK` comments introduced since last release
- [ ] ROADMAP.md updated if any roadmap items were completed
- [ ] SECURITY.md accurate (no claims about non-existent mitigations)
- [ ] Version constant updated (`internal/transport/mcp.go: Version`)
- [ ] Git tag created: `git tag v0.X.Y && git push origin v0.X.Y`
