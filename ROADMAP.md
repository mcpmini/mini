# minimcp Roadmap

## What is minimcp (v0.1)

minimcp is an MCP proxy that sits between AI agents (Claude, Cursor, etc.) and upstream MCP servers. Instead of agents connecting directly to every tool server, they connect to minimcp once and get a unified, controlled interface.

### Capabilities in v0.1

**Core proxy**
- Routes tool calls across multiple upstream MCP servers (`server.tool` namespacing)
- Supports both stdio subprocesses and HTTP/SSE/streamable upstreams
- Runtime server registration and removal (HTTP transports by default; stdio opt-in)

**Context optimization**
- Response projection: trim large responses by field inclusion/exclusion, array limits, string limits, depth limits
- Content stripping: HTML and Markdown stripped from large text fields automatically
- Response file store: large responses written to `~/.mini/responses/` with configurable TTL and disk budget, returning a file path agents can `cat`/`jq` instead of receiving walls of text inline
- Per-tool and server-wide default projection configs

**Access control**
- Three permission tiers per tool: `open`, `protected` (requires `perm_call`), `hidden`
- Per-server permission configs with default tier support

**Auth**
- API key and Bearer token injection (static or `${ENV_VAR}` references)
- OAuth2 PKCE flow with token persistence

**Reliability**
- Retry with exponential backoff on HTTP 429/503, respecting `Retry-After` headers (up to 3 attempts)
- Configurable per-tool timeouts and HTTP client timeouts
- Automatic upstream reconnection with backoff on stdio connection failure

**Developer tooling**
- `mini add` / `mini rm` for managing server configs
- `mini auth <server>` for OAuth flows
- `--from-claude` import from Claude Desktop and Claude Code config files
- `--from-cursor` import from Cursor `mcp.json`
- `--from-codex` import from Codex `config.toml` (TOML format)
- `--from-gemini` import from Gemini CLI `settings.json`
- `config` tool for runtime introspection and control
- `mini call` / `mini perm-call` — invoke upstream tools directly from the CLI without an agent
- `mini test` — CI-safe health check (connects to each upstream, exits 1 on failure)
- `mini init` — interactive setup wizard

**HTTP mode and daemon**
- `mini serve --http :4857` to also accept HTTP/SSE connections alongside stdio
- `mini daemon` — shared background daemon; multiple agents connect via HTTP without spawning new subprocesses
- stdio `serve` auto-detects a running daemon and proxies through it (transparent to agents)

**Observability**
- Per-call: `estimated_raw_tokens`, `estimated_tokens_saved`, `latency_ms` in every call response envelope
- Per-session: call count, error count, total latency, estimated tokens saved (via `config status`)
- Per-upstream: same stats surfaced in status report
- All token counts clearly labeled as estimates

**Security**
- Server name validation at all input boundaries (prevents path traversal)
- SSRF blocking on all upstream URLs: private IPs, loopback, `.local`/`.internal` TLDs, link-local ranges
- Redirect blocking on HTTP upstreams (session token exfiltration prevention)
- Response body size caps (64MB normal, 4KB error bodies); request body cap at 1MB
- `dangerous_allow_runtime_stdio` flag (off by default) gates runtime subprocess execution
- `add_server` strips `auth` and `headers` to prevent prompt-injection credential exfiltration
- See SECURITY.md for full threat model and mitigation details


## Roadmap

Priority is impact on developer workflows — making agents faster, cheaper, safer, and more capable.


### v0.2 — Per-Server Proxy & Observability

**Tool name aliasing in projection configs**
- Allow projection configs to declare short aliases for tool names: `list_pull_requests: {alias: list_prs, ...}`
- Agent sees `list_prs` in the tool list instead of `list_pull_requests`, saving schema tokens on every turn
- Aliases are per-server and user-configurable; bundled defaults can ship opinionated aliases for verbose tool names
- *Code changes*: `ProjectionConfig.Alias string`; registry wraps tool name on list/call; inverse map maintained for routing

### v0.2 — Per-Server Proxy & Observability

**Per-server transparent proxy (`mini proxy <server>`)**
- `mini proxy github` starts a single-server proxy that exposes upstream tools directly without any server prefix or mini branding — Claude Code sees `list_pull_requests`, not `github__list_pull_requests`
- Users register each server independently: `claude mcp add github -- mini proxy github`
- No `config` or `read` tools exposed — pure passthrough, mini invisible to the agent
- All projection, permission, and token-optimization logic still applies via the mini daemon
- *Code changes*: `runProxy` accepts optional positional server name; new `server.WithSingleServerProxy(name)` option; proxy tool listing strips server prefix; routing adds it back internally

### v0.2 — Observability & Broader MCP Coverage

**Default projection configs for top developer MCPs**
- Expand bundled projections from the v0.1 set (GitHub, Slack, Jira, Linear, Sentry) to cover the 10 most commonly used MCP servers in developer workflows
- Candidates: Notion, Postgres, Puppeteer/Playwright, Brave Search, Atlassian Confluence, Datadog, PagerDuty, GitLab, Bitbucket
- Each config ships only after being validated against real MCP tool responses (not raw REST API fixtures) — see `benchmarks/README.md`
- *Code changes*: add projection YAMLs to `internal/defaults/projections/`; update `knownServers` in `add_projection.go`; add fixture + `fixtureValidations` entry

**Jira custom field resolver**
- Jira responses include dozens of `customfield_*` keys that are always null or useless for most agents, but the names vary across Jira instances and can't be safely excluded by name in a bundled config
- A custom field resolver would fetch the Jira field schema on first use, identify which custom fields map to meaningful names (e.g. `Story Points`, `Sprint`, `Epic Link`), and build a per-instance projection that excludes unmapped custom fields and renames the useful ones
- This is the right fix for the 105KB Jira search response problem — the current wildcard `depth_limit: 3` achieves 89% reduction, but meaningful per-field projection requires knowing which fields matter on the user's instance
- *Code changes*: new `ops/jira_fields.go`; cache schema in `~/.mini/cache/<server>-jira-fields.json`; inject as post-projection transform

**Local usage tracking (`local_usage`)**
- Track per-(server, tool) call counts, error rates, and total estimated tokens saved — stored locally in `~/.mini/usage.json`, never sent anywhere
- Surfaced via `config action=status` and `mini status` with a ranked "most-used tools" view
- Named `local_usage` deliberately (not "telemetry" or "analytics") — it's your own data about your own agent sessions
- Primary value: know which tools get called most so you can prioritize writing projection configs for them; future versions can suggest projections automatically for noisy tools
- *Code changes*: atomic counters per `(server, tool)` in `upstreamServer`; flush to `~/.mini/usage.json` on shutdown and periodically; expose in `stats()`; no new dependencies

**`/metrics` endpoint on the daemon**
- Expose per-tool call counts, error rates, and latency histograms as JSON at `GET /metrics`
- Builds on per-session and per-upstream counters already tracked in v0.1
- Format: human-readable JSON by default; optional `?format=prometheus` for Prometheus text format
- *Code changes*: HTTP handler on daemon's existing port; no new dependencies for JSON mode

**Hot reload**
- `SIGHUP` or `config reload` reloads server configs without dropping existing connections
- Adds/removes servers that changed; existing connections untouched
- *Code changes*: `server.Reload(newCfg)` that diffs old/new server lists and calls `AddUpstream`/`RemoveServer`

**Request/response logging**
- Optional structured log of every tool call: server, tool, params hash, duration, response size
- Configurable log level and output (file, stderr, syslog)
- *Code changes*: add call logging in `callUpstream`; log file rotation via `lumberjack`

**CI setup**
- GitHub Actions: `go build`, `go test`, `go vet`, `staticcheck` on every PR
- Race detector run (`-race`) on test suite
- Coverage report posted to PR as comment
- *Code changes*: `.github/workflows/ci.yml`; add `//go:build ignore` guards on integration tests requiring `npx`


### v0.3 — Response Caching

Agents frequently call the same read-only tools with identical parameters (fetching the same GitHub issue, the same file listing). Every call burns tokens and latency.

**Deterministic caching**
- Cache key: `hash(server + tool + canonicalJSON(params))`
- Configurable TTL per tool (e.g. `cache_ttl: 5m`) and global default
- Cache stored in the existing response store (reuse TTL/eviction infrastructure)
- Tools marked `no_cache: true` in projection config bypass it
- *Code changes*: `internal/cache` package wrapping the response store; check in `callUpstream` before upstream call; cache writes on success

**Cache invalidation**
- `config` action `invalidate_cache` clears by server, tool, or all
- Write-through invalidation: calling a mutating tool (configurable list) clears related cache entries
- *Code changes*: tag entries with server+tool in store; `Store.InvalidateByTag(tag)`

**Impact**: Read-heavy workloads (code review agents, documentation agents) could see 50-80% reduction in upstream calls.


### v0.4 — Request & Response Pipeline

Agents send whatever the tool schema asks for. But often you want to rewrite inputs or outputs systematically — add a default repo, strip a wrapper object, rename a field the agent keeps getting wrong.

**Input transformation**
- Per-tool `transform_input` config: add/rename/remove params before forwarding
- Template values: `${session.user}`, `${env.MY_VAR}`, literal overrides
- *Code changes*: `internal/transform` package; apply in `callUpstream` before `conn.Call`

**Output transformation**
- Per-tool `transform_output` config: rename fields, wrap/unwrap, inject static fields
- Complements projection (projection trims, transform restructures)
- *Code changes*: apply in `buildEnvelope` after projection

**Conditional tool visibility**
- `visible_if` config: tool only appears in `list` if a condition is met (e.g. `${env.FEATURE_FLAG} == "true"`)
- *Code changes*: filter in `registry.All()` / `registry.Search()` based on session/env context

**Tool chaining (actions v2)**
- Action can call multiple tools in sequence, passing outputs between them
- Simple pipeline DSL in YAML: `steps: [{tool: X, output_as: result}, {tool: Y, params: {id: "${result.id}"}}]`
- *Code changes*: `internal/pipeline` package; `ActionConfig` grows `Steps []PipelineStep`; new `executePipeline` in handlers


### v0.5 — Multi-Agent & Session Isolation

v0.1 has one shared session per MCP connection. As teams use minimcp with multiple agents simultaneously, they need isolation.

**HTTP listen mode**
- minimcp listens on a port; multiple agents connect via HTTP/SSE
- Each connection gets an isolated session with its own projections and identity
- *Code changes*: `internal/server/http_server.go`; session keyed by connection, not global; `config.yaml` `listen_mode: http`

**Per-session rate limits**
- Max calls/minute per session, per tool, per upstream server
- Returns a structured error with retry guidance when exceeded
- *Code changes*: `internal/ratelimit` package (token bucket); checked in `callUpstream`

**Named sessions**
- Sessions can declare identity (`X-Session-Id` header or stdio init params)
- Projections and permissions can be scoped to session identities
- *Code changes*: `Session` struct grows `ID string`; projection lookup checks session identity


### v0.6 — Audit & Cost Tracking

Once agents run autonomously, you need to know what they did and what it cost.

**Audit log**
- Append-only JSONL log: timestamp, session, server, tool, param fingerprint, response size, duration, ok/error
- Never logs param values by default (only hashes); opt-in param logging with redaction rules
- *Code changes*: `internal/audit` package; write in `callUpstream`; rotate via size/time

**Cost estimation**
- Token count per call (input params + response) estimated using the existing `EstimateTokens`
- Running totals per session and per tool, surfaced in `config status`
- Budget limits: `max_tokens_per_session: 100000` rejects calls when exceeded
- *Code changes*: accumulate in `Session`; check budget gate in `callUpstream`

**PII redaction**
- Configurable regex/field-name rules that scrub values before audit logging and response storage
- Built-in rules for common patterns (API keys, emails, credit card numbers)
- *Code changes*: `internal/redact` package; applied to audit log writes and optionally to response files


### v0.7 — Reliability & Failover

**Fallback servers**
- `fallback: secondary-github` in server config; if primary returns 5xx or times out, retry on fallback
- Transparent to the agent
- *Code changes*: `ServerConfig.Fallback string`; `callUpstream` retries on fallback server on connection error

**Circuit breaker**
- After N consecutive failures, open the circuit for a cooldown period (no requests sent)
- Half-open state probes with one request; closes on success
- *Code changes*: `internal/breaker` package; wraps `upstream.callTool`

**Upstream health checks**
- Periodic background `Health()` calls to detect dead upstreams before agents try to use them
- `config status` shows last health check result and latency
- *Code changes*: ticker in `upstreamServer`; store last health result; expose in `stats()`

**Connection pooling for HTTP upstreams**
- Reuse HTTP connections across calls (already done by Go's `http.Transport`, but make it explicit and configurable)
- Max idle connections, keep-alive timeout configurable per server
- *Code changes*: `NewHTTPConnection` creates a shared `http.Transport` with tunable pool settings


### v0.8 — Security Hardening

**Human-in-the-loop approval**
- Protected tools can require a human `yes/no` before execution (currently the `approval` package is internal-only)
- Web UI approval queue: agent call arrives, minimcp holds it, human approves/denies via browser
- *Code changes*: expose `internal/approval` via HTTP endpoint; `perm_call` blocks on approval channel

**Request signing**
- HMAC-sign outbound HTTP requests with a shared secret; upstreams can verify
- *Code changes*: `ServerConfig.SigningKey`; `doPost` adds `X-Minimcp-Signature` header

**Secret scanning**
- Warn (log + optional block) when tool params contain strings matching secret patterns
- Prevents agents from accidentally forwarding secrets they've read to external APIs
- *Code changes*: `internal/secrets` package; scan params in `callUpstream` before forwarding

**mTLS for upstream HTTP**
- `ServerConfig` supports `tls_cert` / `tls_key` / `tls_ca` for mutual TLS to upstreams
- *Code changes*: `NewHTTPConnection` builds custom `tls.Config` from cert paths

**OS keychain integration for OAuth tokens**
- Currently tokens are stored as plaintext JSON at `~/.mini/tokens/<server>.json` with `0600` permissions — the same approach used by AWS CLI (`~/.aws/credentials`), kubectl (`~/.kube/config`), and npm. This is safe against other users on the same machine but not against malware running as the same user.
- Replace with OS-native secret storage via `zalando/go-keyring` (wraps macOS Keychain, Linux Secret Service / KWallet, Windows Credential Manager), falling back to the current file approach in headless/CI environments where no keyring is available (detected via `MINI_NO_KEYRING=1` or keyring unavailability).
- `gh` CLI uses this exact pattern: keychain in interactive sessions, file fallback for CI.
- *Code changes*: `internal/auth/token.go` `Save`/`Load` switch on keyring availability; add `zalando/go-keyring` dependency; document `MINI_NO_KEYRING` env var.


### v0.9 — Ecosystem & Distribution

**Package distribution**
- Homebrew formula (`brew install minimcp`)
- `apt`/`yum` packages via goreleaser
- Docker image (`ghcr.io/minimcp/minimcp`)
- *Code changes*: `.goreleaser.yml`; Dockerfile; package scripts

**Plugin system**
- Go plugin or subprocess-based extensions that can add custom auth providers, transform functions, or approval handlers
- *Code changes*: `internal/plugin` package with defined interface; load from `~/.mini/plugins/`

**MCP registry integration**
- `mini search <query>` searches a public registry of MCP servers
- `mini install <server>` fetches config and adds it
- *Code changes*: `cmd/mini/search.go`; registry client against a hosted API or community repo

**Import from more sources** *(Cursor, Codex, Gemini CLI done; remaining: Windsurf, Zed, Continue, pi.dev)*
- Windsurf, Zed, Continue, and pi.dev config file formats
- *Code changes*: extend existing `--from-*` flags pattern in `cmd/mini/add.go`


### v1.0 — Production Ready

- Stable config format with documented backwards-compatibility guarantees
- Comprehensive user documentation (hosted docs site)
- Security audit by a third party
- Performance benchmarks (calls/sec, p99 latency, memory at scale)
- `mini validate` command that checks config for common mistakes
- Integration test suite against real public MCP servers (GitHub, filesystem) run in CI

**Internationalisation (i18n)**
- User-facing strings (error messages, list output, CLI help) extracted to locale files
- `MINIMCP_LANG` env var or `language: en` config key selects locale; falls back to `en`
- Embedded locale files via `embed.FS` — no external dependency
- At minimum: English (baseline), Japanese, and Simplified Chinese locale files
- CI validates locale files are complete against the English baseline
- *Code changes*: `internal/i18n` package; `T(key, args...)` helper used throughout; `cmd/minimcp/locales/` directory


## CI Story

**Immediate (add now)**

```yaml
# .github/workflows/ci.yml
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.26' }
      - run: go build ./...
      - run: go vet ./...
      - run: go test ./... -race -count=1 -timeout 120s
      - run: go test ./... -coverprofile=coverage.out
      - uses: codecov/codecov-action@v4
```

**Tag integration tests** that require `npx` or network access with `//go:build integration` so the fast CI path skips them. Run integration tests nightly or on release branches.

**Linting**: add `staticcheck` or `golangci-lint` to catch issues the compiler misses (unused exports, shadowed variables, printf format mismatches).


---

## Unscheduled / Future Consideration

Features that are clearly useful but haven't been slotted into a version yet.

**Upstream tool schema refresh**
- When mini connects to an upstream it lists available tools once and caches that list in-memory for the lifetime of the connection. Upstreams that run for days/weeks may add, remove, or rename tools without mini noticing.
- Options: periodic background refresh (`tools/list` every N minutes, configurable per server), `notifications/tools/list_changed` support (MCP spec supports server-push change notifications — subscribe and refresh on receipt), or a `config action:refresh_tools server:<name>` command for manual triggers.
- The notification-based approach is lowest overhead and most correct; the periodic refresh is simpler to implement and works with servers that don't emit notifications.
- *Code changes*: `upstreamServer` subscribes to `notifications/tools/list_changed` if the upstream supports it; fallback periodic ticker; `registerTools` called again on refresh to update the registry; sessions get a `tools_changed` notification so agents can re-list.

---

## Feature Priority Summary

| Version | Theme | Developer Impact |
|---|---|---|
| v0.2 | Observability + CI | Debug issues; `local_usage` shows which tools cost most |
| v0.3 | Caching | Dramatically fewer upstream calls; faster agents |
| v0.4 | Pipeline | Agents need less prompt engineering to use tools correctly |
| v0.5 | Multi-agent | Teams can share one proxy; agents don't interfere |
| v0.6 | Audit + Cost | Know what agents did and what it cost; enforce budgets |
| v0.7 | Reliability | Upstreams go down; agents should degrade gracefully |
| v0.8 | Security | Autonomous agents doing sensitive work need guardrails |
| v0.9 | Ecosystem | Lower barrier to adoption; community growth |
| v1.0 | Production | Enterprise and team adoption |
