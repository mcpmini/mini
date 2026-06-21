# Changelog

## Unreleased

### Features

- **Experimental pipes** — opt-in with `enable_pipes: true`; YAML-defined multi-step tool sequences are exposed as virtual tools on the `user` server and support input validation, expression interpolation (`{{ inputs.x }}`, `{{ steps.id.result.field }}`), conditional steps (`if:`), set steps, `continue_on_error`, and an `output:` block for structured results
- **`mini pipe list`** — lists loaded pipes with step counts and descriptions; surfaces load/validation errors with non-zero exit
- **`mini pipe run <name> [--args '{}']`** — executes a pipe directly from the CLI
- **`mini pipe check [name]`** — validates pipe YAML and expressions before install; exits non-zero on any error
- Permission inheritance for pipes: a pipe calling any protected step is automatically protected; explicit `permission: protected/hidden` override available

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
