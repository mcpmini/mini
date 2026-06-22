# Changelog

## v0.2.0 — <date>

### Added

- **Tool aliases** — projection configs can rename verbose upstream tool names, so agents see shorter names while mini still routes to the original upstream tool.
- **OAuth 2025-11-25 support** — OAuth flows now cover updated MCP discovery, dynamic client registration, PKCE, client information metadata, and resource parameter handling.
- **Config env interpolation** — server configs can use `${VAR}` references for values that should come from the environment.
- **Build/version identity** — `mini version` / `--version`, outbound version metadata, and release builds now report a coherent mini version.
- **Daemon docs and diagnostics** — daemon behavior, recovery, and connection lifecycle are documented, with improved session lifecycle logging and daemon log rotation.

### Changed

- **One command replaces `serve` and `proxy`** — use `mini connect`. The default is **proxy** mode (upstream tools exposed directly as `server__tool`, responses minified); pass `--tool-mode compact` for the four-meta-tool interface (`list`/`call`/`perm_call`/`config`). Both `serve` and `proxy` are removed with no aliases.
- **Bare `mini` prints help and exits 0** — it no longer starts a server.
- **Wire format** — the `initialize` params signal changed from `_mini_proxy_mode: true` to `_mini_tool_mode: "compact"`. Proxy is the daemon's zero-value default and injects nothing.
- **Repository layout** — public docs were consolidated under `docs/`, stale example configs and internal analysis notes were removed, and README now leads with install, connect, server setup, output modes, and daemon behavior.

### Fixed

- **Daemon recovery** — `mini connect` now recovers from a dead daemon by respawning it and replaying initialization when it can prove the failed request did not reach the daemon.
- **Daemon/session hangs** — fixed daemon restart, shutdown, and stale-session hangs, including cancellation over HTTP and bounded failure for stale sessions.
- **MCP `tools/list` pagination** — HTTP/SSE upstreams now follow `nextCursor` and surface pagination errors instead of silently truncating tool lists.
- **Proxy metadata** — upstream tool annotations pass through unchanged in proxy mode.
- **Response file writes** — projected responses that elide or truncate fields now write response files when needed instead of incorrectly staying inline.
- **Auth browser behavior** — browser opening is shared by CLI and server auth flows, with configurable browser command handling and safer URL argument passing.

### Security

- **Daemon trust boundary** — daemon HTTP traffic now requires a bearer token, loopback Host validation, and Unix-socket access controls.
- **Daemon self-healing token reuse** — daemon tokens persist across daemon restarts so already-connected proxy sessions can recover instead of breaking on token rotation.

## v0.1.0 — 2026-06-01

Initial release of mini, the minifying MCP proxy.

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
- IPv4-in-IPv6 SSRF bypass blocked (`::ffff:127.0.0.1`)
- Session ID validation requires ≥ 16 hex characters
