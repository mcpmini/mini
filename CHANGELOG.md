# Changelog

## v0.1.0 — 2026-06-01

> Pre-release fixes applied after the May 16 feature-complete build. All changes listed here are included in the v0.1.0 tag.

### Bug fixes

- **OAuth PKCE callback** — callback handler now returns HTTP 400 when the `code` parameter is absent (previously wrote "Authorized!" and silently failed token exchange)
- **Redirect URI consistency** — extracted `LoopbackCallbackPath` constant shared by dynamic registration and PKCE flow; divergence between the two paths would have caused `redirect_uri_mismatch` errors on strict OAuth servers
- **Response mutation** — `injectRawPath` in the response store was mutating the caller's `_meta` map in place; fixed with explicit copies to prevent hidden side effects on retry loops
- **Shutdown quiescence** — `maybeReconnect` now checks `upstream.ctx.Err()` before calling `reconnectWg.Add(1)`, preventing a narrow window where `Close()` could return while a reconnect goroutine was still launching
- **Tool-not-found error type** — calling `call` or `perm_call` with an unknown server or tool now returns a tool result with `isError: true` instead of an MCP protocol error (`-32602`); agents can handle this gracefully in their flow without treating it as a protocol fault
- **MCP cancellation** — mini now accepts `notifications/cancelled` from agents and propagates cancellation to in-flight upstream calls via per-request context; parallel requests to the same server are unaffected

### Security (pre-release)

- `dangerous_allow_runtime_stdio` now also strips `sc.Env` from runtime `add_server` calls (prevents credential injection via environment variables into spawned subprocesses)
- SSRF dialer blocks IPv4-in-IPv6 addresses (`::ffff:127.0.0.1`) that could bypass IP range checks
- Session ID validation requires ≥ 16 hex characters to prevent all-hyphen IDs with no real entropy

### Testing

- Added concurrency stress test for `Close()` vs `maybeReconnect` shutdown race
- Added schema compliance test suite (MCP 2025-03-26 spec)
- Added regression tests for all security fixes above

---

## v0.1.0 — 2026-05-16 (feature complete)

Introducing mini, the minifying MCP proxy.

### Features

- **Core proxy** — routes tool calls across multiple upstream MCP servers (stdio and HTTP/SSE/streamable); `server.tool` namespacing
- **Proxy mode** — `mini proxy` exposes upstream tools directly as `server__tool` names; projections still apply transparently
- **Context optimization** — field inclusion/exclusion, array limits, string limits, depth limits, auto HTML/MD stripping, response file store (large responses written to `~/.mini/responses/` with TTL and disk budget)
- **Access control** — three permission tiers per tool: `open`, `protected` (`perm_call`), `hidden`
- **Auth** — API key and Bearer token injection (static or `${ENV_VAR}`); OAuth2 PKCE flow with token persistence
- **Reliability** — retry with exponential backoff on HTTP 429/503; configurable timeouts; automatic stdio reconnect with backoff
- **Daemon mode** — `mini daemon` shared background HTTP daemon; `mini serve` auto-proxies through it transparently
- **CLI** — `add`, `rm`, `ls`, `status`, `auth`, `test`, `init`, `call`, `perm-call`, `cleanup`, `daemon status`
- **Importers** — `--from-claude`, `--from-cursor`, `--from-codex`, `--from-gemini` to pull existing MCP configs
- **Bundled projections** — GitHub, Slack, Linear, Sentry auto-installed when a known server is detected
- **Observability** — `estimated_raw_tokens`, `estimated_tokens_saved`, `latency_ms` in every call response; session stats via `config status`

### Security

- SSRF blocking on all runtime `add_server` URLs (private IPs, loopback, `.local`/`.internal`)
- Redirect blocking on HTTP upstreams
- Server name validation at all input boundaries (`^[a-zA-Z0-9_-]+$`)
- `add_server` strips `auth` and `headers` to prevent prompt-injection credential exfiltration
- Response file `read` tool uses `filepath.EvalSymlinks` — symlink escapes from the store directory are rejected
- Concurrent `add_server`/`remove_server` serialized to prevent transient registry corruption
- Response body caps: 64MB normal, 4KB error; request body cap: 1MB
- `dangerous_allow_runtime_stdio: true` required to permit agent-controlled subprocess execution (default: off)
