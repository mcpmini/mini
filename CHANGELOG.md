# Changelog

## v0.3.0 — 2026-11-13

- **`mini init` is a full-screen setup** — it imports the MCPs you already use in Claude Code, Claude Desktop, Codex, Cursor and Windsurf, offers more from a curated catalog, runs browser logins, and connects mini to your agents, backing up each config it changes. `--import`, `--from` and `--add` set up servers without asking.
- **Agents get the upstream's own results** — trimmed in place, keeping `structuredContent`, images, resources and `_meta`. A trimmed result carries a `__mini` notice listing what was cut, and `read` fetches it back.
- **Trimmed results pass client schema checks** — mini advertises a relaxed `outputSchema`, so clients that validate results, such as Claude Code, accept trimmed ones.
- **MCP 2026-07-28 support** — for agents and upstreams that speak the new revision, including tool-list lifetimes. Older revisions are negotiated properly on both sides.
- **Changes apply without a restart** — a running mini picks up added, edited and removed server files, projection edits and new logins, and tells agents their tool list changed.
- **Faster, sturdier startup** — servers connect concurrently, mini serves agents at once, and a broken server file only skips that server. `config status` says which servers are still starting or unavailable, and why.
- **More reliable OAuth** — tokens refresh before they expire and after a 401. mini identifies itself with a client ID metadata document, falls back to dynamic registration, and checks the authorization server's issuer. The browser page reports success only once mini holds the token, and says so when access was denied.
- **TOON output** — `response_format: toon` and `mini call -t` encode responses as [TOON](https://github.com/toon-format/toon): lossless, and much smaller than JSON for lists of records.
- **`mini ls` shows connection state** — ready, needs auth or failed for each server, and `mini ls <server>` lists its tools with their annotation hints.
- **Projections live in the server's file** — under `projections:` in `servers/<name>.yaml`, with nested exclusions written as paths such as `[].user.avatar_url`.
- **Compact mode and `protected` are removed** — agents get each upstream tool directly, and the MCP client decides which calls need approval. `permissions.hidden` still hides tools.

## v0.2.0 — 2026-07-08

- **One command replaces `serve` and `proxy`** — use `mini connect`. The default exposes upstream tools directly as `server__tool`; pass `--tool-mode compact` for the four-meta-tool interface (`list`/`call`/`perm_call`/`config`). Both old commands are removed.
- **Bare `mini` prints help and exits 0** — it no longer starts a server.
- **Projected results stay inline** — when fields are removed or truncated, mini returns the projected data with a `__mini` recovery hint and stores the raw response under `~/.mini/internal/responses/`. The new `read` tool accepts the bare recovery key and an optional jq filter.
- **Stable proxy call contract** — upstream arguments live under `args`; `__mini.projection` selects projected or raw output without colliding with an upstream tool's own fields.
- **Tool aliases** — projection configs can rename verbose upstream tool names while mini still routes calls to the original tool.
- **More complete auth and config support** — OAuth follows the 2025-11-25 MCP auth updates, known providers can be detected automatically, and server configs support `${VAR}` environment interpolation.
- **Stronger daemon behavior** — the daemon now uses a private Unix socket plus bearer authentication, and `mini connect` can recover when it dies before handling a request.
- **Dynamic tool-list refresh** — upstream `notifications/tools/list_changed` events refresh mini's registry and relay a tool-list change notification to connected sessions.
- **More complete MCP compatibility** — HTTP/SSE upstreams now follow `tools/list` pagination, pass tool annotations through in proxy mode, and return structured JSON-RPC errors for daemon HTTP failures.

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
