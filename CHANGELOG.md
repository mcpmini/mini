# Changelog

## v0.3.0 — unreleased

- **`mini init` is a full-screen setup** — it imports the MCPs you already use in Claude Code, Claude Desktop, Codex, Cursor and Windsurf (or any config file, OpenClaw's included, with `--from`), offers more from a curated MCP catalog, runs browser logins, and connects mini to your agents, backing up each config it changes. Servers that need a token or your own OAuth app get the steps as a comment in their file. `--import`, `--from` and `--add` do the same without asking. See [docs/auth.md](docs/auth.md) for the steps init can't do for you.
- **Proxy mode returns the upstream's own results** — trimmed in place instead of wrapped in `{"data": …}`, preserving `structuredContent`, prose, images, resources and `_meta`. Tools take the upstream's own arguments, not an `args` wrapper. A trimmed result carries a `__mini` notice whose paths are `read` filters against the stored result. Compact mode and `mini call` keep their result contracts.
- **TOON replaces the mini response format** — `response_format: toon` and `mini call -t` encode responses as [TOON](https://github.com/toon-format/toon): lossless, and much smaller than JSON for lists of records.
- **Changes apply without a restart** — a running mini connects servers added to `servers/`, reconnects a server whose file changes, drops a removed one, applies projection edits, and connects a server once `mini auth` or `mini add` saves its login. Agents get a tool-list change notice.
- **Faster, sturdier startup** — servers connect concurrently with a deadline (`handshake_timeout` in the server's file, default 30s), mini serves agents at once, and the first tool list waits briefly for servers still starting. A broken or misconfigured server file only skips that server, and `config status` says which servers are starting or unavailable, and why.
- **OAuth logins keep working** — tokens refresh before they expire and after a 401, and an expired upstream session is re-initialized. mini identifies itself with a client ID metadata document before falling back to dynamic registration, honors registered client secrets, sends the RFC 8707 `resource`, and validates the authorization server's issuer. A saved login is bound to the server URL, token endpoint and client it was issued for, so editing any of them asks for a new login instead of sending the token elsewhere.
- **Projections live in the server's file** — under `projections:` in `servers/<name>.yaml`. The `config` tool gains `get_projection`.
- **Updated bundled defaults** — no tools are hidden or protected by default. GitHub, Linear and Atlassian projections use current tool names; stale Slack and Sentry rules are removed. New Atlassian catalog additions use the v2 endpoint.
- **Projection option errors are reported** — misspelled or removed options are rejected in server files and `set_projection`, with the offending key and valid options in the error.
- **`mini call` runs the MCP handshake** before calling, so servers that require it work from the CLI.
- **Quieter CLI** — log lines go to `internal/cli.log` (and `internal/init.log` for `mini init`) instead of the terminal.

### Upgrading from v0.2

- Log in again to every OAuth server with `mini auth <name>`: v0.2 token files lack the server binding and are refused.
- Move the rules in each `servers/<name>.proj.yaml` under `projections:` in `servers/<name>.yaml`, then delete the `.proj.yaml` file. mini no longer reads them.
- Move any `servers:` entries in `config.yaml` into their own `servers/<name>.yaml` files. mini ignores them without a warning.
- `mini init --yes` is gone; use `--import`, `--from` or `--add` for unattended setup.
- `response_format: mini` is now `response_format: toon`, and `mini call -m` is now `-t`.
- `passthrough` is removed. List fields you want to retain in `include_only` instead. Unknown projection options now produce an error; in a server file, invalid projections are skipped while the server remains available.
- Bundled defaults apply to newly added servers. Existing files retain their projections, permissions and URLs; edit them if you want the new defaults. Changing an OAuth server's URL requires a fresh login.
- `${VAR}` is expanded only in `headers`, `env`, `auth.token` and `auth.client_secret`. A server with `${VAR}` in `url`, `command` or `args` is skipped with a warning.
- In proxy mode, call tools with their own arguments; the v0.2 `args` wrapper is now passed through to the upstream as an ordinary argument.
- Proxy recovery filters address the full upstream result, for example `.content[0].text | fromjson | .items`. Embedded resource text is limited as text rather than parsed for JSON field rules.
- `mini add --from-gemini` is gone; use `mini init --from` with an agent name or config file.

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
