# AGENTS.md

This file provides guidance to coding agents and contributors working in this repository.

## Commands

```bash
export PATH="/opt/homebrew/bin:$(go env GOPATH)/bin:$PATH"   # Go 1.26.1 + staticcheck

go build ./...
go test -race -tags test ./...

# Integration tests (build real binaries, spawn subprocesses)
go test -tags integration,test ./test/integration/...

# Run a single test
go test -tags test ./internal/server/... -run TestExecuteRoutesToUpstream -v

# Build the binary
go build -o mini ./cmd/mini

# Run all checks (build + lint + race tests) — same as CI
./check.sh
```

## Philosophy

mini has two goals, in order:

1. **Reduce token overhead from MCP responses** — upstream tool responses are often enormous; agents pay for every token in both input and output processing
2. **Allow agents to be more autonomous** — fewer interruptions, better signal-to-noise, agents can act confidently on trimmed responses rather than drowning in irrelevant data

Every feature decision should serve one of these two goals.

## Code style

### Size limits

- Functions: aim for ≤ 15 lines; anything > 25 lines must be broken into smaller functions
- Files: aim for ≤ 300 lines; > 450 lines warrants a refactor; > 500 lines is a critical priority

### Function signatures

- ≤ 3 parameters: positional args are fine
- ≥ 4 parameters: use a params/options struct so every call site is self-documenting (`foo(FooParams{Timeout: 5*time.Second, Retry: true})` is far easier to read than `foo(5*time.Second, true, "", nil)`)
- Never add optional/flag parameters (booleans, empty strings) as bare positional args — a struct field with a clear name is always preferable

### Principles (in priority order)

1. **Correctness** — behavior must be right first
2. **Maintainability / Readability / Testability** — explicit, easy-to-reason-about code; self-documenting names
3. **Simplicity** — leverage stdlib, reuse existing code, refactor often; no unnecessary abstractions
4. **Performance** — avoid obvious inefficiencies, but not at the cost of the above

### Comments

Comments exist for one reason only: to explain *why* a non-obvious behavior is necessary, when that reason cannot be expressed through better naming.

**Remove comments that:**
- Describe what the code is doing — rename the function or variable instead
- Act as section dividers or structural markers — split into smaller functions or files instead
- Repeat information already in the function name, type, or signature
- Document function behavior that is obvious from the name (e.g. `// writeConfig writes config.yaml`)

**Keep comments that:**
- Explain a non-obvious constraint or invariant (e.g. why a particular value is chosen, why a branch exists)
- Warn about a subtle interaction that cannot be captured in a name

When you feel the urge to add a comment that describes *what*, improve the name instead. Doc-style comments on functions are only warranted when the function name alone cannot convey the non-obvious contract.

### Testing

- Cover happy paths, argument permutations, error cases, and edge cases
- Prefer fakes over mocks (`transport.FakeConnection` is the established pattern)
- If something is hard to test, the abstraction is probably wrong — fix the design, not the test
- Tests are the safety net for refactoring; they must be rock solid

**Test structure — no comment separators:**

Do not use `// --- section ---` style comment dividers in test files. They are brittle and go stale.

Instead:
- Use `t.Run("descriptive name", func(t *testing.T) {...})` to group related cases within one function
- Use separate test files per component (`pending_test.go`, `prefix_writer_test.go`) — the filename is the grouping
- Use table-driven tests with a `name` field for permutation coverage
- Write test function names descriptively enough that no separator is needed

## Review workflow

For code reviews, use the `review-pr` skill as the detailed guide. When reviewing your own output, spin up an adversarial limited-context subagent with a red-team mindset so it can give neutral feedback without being anchored by the implementation thread.

## Architecture

mini is a context-optimizing MCP proxy. Agents talk to it via stdio; it routes calls to one or more upstream MCP servers (stdio or HTTP/SSE).

**4 tools exposed to agents:** `list` (discover), `call` (execute), `perm_call` (execute protected), `config` (runtime admin)

**Request flow:**

```
agent → Serve() → session handler → handleList / handleExecute / handleExecuteProtected / handleConfigure
                                         ↓
                                   registry.Lookup()   — resolves server+tool, checks permission
                                         ↓
                                   upstream.callTool()  — dispatches to upstream connection
                                         ↓
                                   projection.Apply()   — trim/strip/limit the response
                                         ↓
                                   response.Builder.Build() — inline or write to file
```

### Key packages

| Package | Role |
|---|---|
| `internal/transport` | `Connection` interface; `StdioConnection` (subprocess), `HTTPConnection` (SSE/streamable), `FakeConnection` (tests); `pendingMap` (in-flight request tracking) |
| `internal/config` | `Config`, `ServerConfig`, `ProjectionConfig`, `ActionConfig` types + YAML loader; `ValidServerName` regex |
| `internal/registry` | Tool index keyed by `"server.tool"`; permission resolution; action (virtual tool) support |
| `internal/server` | `Server` struct + `Serve()` loop + handlers; `Session` (per-connection projection overrides) |
| `internal/projection` | `Apply(value, cfg, defaults)` → `Result{Summary, ExcludedKeys, Passthrough}`; HTML/MD stripping |
| `internal/response` | `Builder`/`Store`; always-inline projected data; TTL-based raw file lifecycle |
| `internal/auth` | API key + OAuth2 PKCE token storage |
| `internal/daemon` | Background daemon HTTP server; port file management (liveness via HTTP healthz, not PID) |
| `internal/proxy` | stdio→HTTP bridge; connects agent stdio to daemon HTTP |

### Response envelope

When projection removes or truncates data, `call`/`perm_call` prepends a header:
```
[Projected — .secret, .internal excluded; .body truncated (420 chars)]
File: <epoch>_<hash8>.json
{
  "id": 1,
  "name": "widget"
}
```
- `excluded`: jq-style paths of fields removed entirely by projection
- `truncated[].chars`: characters (runes) removed from a string field
- `truncated[].items`: items removed from an array field by array limit
- When no data is removed, the response is plain JSON with no projection header
- Raw recovery files written to `~/.mini/responses/<epoch>_<hash8>.json` when data is lost

### Config directory layout

```
~/.mini/
  config.yaml              # global Config
  servers/<name>.yaml      # one ServerConfig per upstream
  projections/<name>.yaml  # ProjectionConfig map (tool → config)
  actions/<name>.yaml      # ActionConfig (virtual tools with pre-filled args)
  responses/               # auto-managed response store
  tokens/                  # OAuth token cache
```

Projections can be embedded inline in a server's YAML under `projections:`. A `"*"` key is a wildcard applying to all tools on that server.

### Permissions

Three tiers: `open` (default), `protected` (requires `perm_call`), `hidden` (invisible to `list`). Defined per-server in `permissions.protected`, `permissions.hidden`, `permissions.default`.

### Security defaults

- Server names validated against `^[a-zA-Z0-9_-]+$` at all input boundaries (CLI, MCP protocol, config load)
- Runtime `add_server` via MCP only allows HTTP transports; stdio opt-in via `dangerous_allow_runtime_stdio: true`
- HTTP upstreams block redirects (session token exfiltration prevention)
- Response bodies capped at 64MB; error bodies at 4KB

### Unit testing pattern

```go
fake := fakeConn("toolA", "toolB")
fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
srv.AddConnection(context.Background(), config.ServerConfig{Name: "myserver"}, fake)

resp := serve(t, srv, callTool("call", map[string]any{"server": "myserver", "tool": "toolA"}))
text := toolResultText(t, resp)
```

`serve()` in `server_test.go` automatically prepends the `initialize` handshake. For transport-level tests, `makePipeConn()` in `stdio_test.go` creates a pipe-backed `StdioConnection` without spawning a subprocess.

### CLI subcommands

`mini [--config DIR] <command>`: `serve` (default), `daemon`, `ls`, `add`, `rm`, `status`, `cleanup`, `auth`, `test`, `init`

- `serve [--http ADDR] [--standalone]` — stdio proxy; optionally also serves HTTP on ADDR; skips daemon detection if `--standalone`
- `daemon [--port N]` — run as shared HTTP daemon (background)
- `daemon status` — show whether the daemon is running
