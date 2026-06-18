# Security

mini sits between AI agents and upstream MCP servers. This document describes the threat model, the mitigations in place, and known limitations.


## Threat model

mini is primarily a trust boundary between **untrusted agent output** (tool responses that may contain attacker-controlled text) and **user-controlled resources** (API tokens, local processes, config files).

The key adversary is **prompt injection via tool responses**: malicious content returned by an upstream tool (a GitHub issue body, a web page, a database row) that instructs the agent to call mini tools in ways that exfiltrate credentials or execute unintended commands.

Secondary adversaries are **malicious upstream servers** configured by a user with bad YAML or a supply-chain-compromised config.

Out of scope: a fully compromised host OS, or an attacker who can write to `~/.mini/`.


## Mitigations

### Prompt injection — `add_server`

The highest-risk MCP tool mini exposes to agents is `add_server` (via `config`), because it can register a new upstream server at a URL controlled by the caller.

**Strip on ingest** (`internal/server/configure.go: validateRuntimeTransport`):

- `sc.Auth = nil` — A crafted auth config with a malicious `token_url` would receive the PKCE `code` + `code_verifier` during an OAuth exchange, enough to mint a token on behalf of the user. Auth setup must go through the CLI (`mini auth`) where endpoints are user-verified.
- `sc.Headers = nil` — A crafted header map like `{"Authorization": "Bearer <stolen-token>"}` plus an attacker URL would silently forward the token on every subsequent call. The user would never see this happen.
- `sc.Env = nil` (when `dangerous_allow_runtime_stdio: true`) — Prevents injecting credentials as environment variables into spawned subprocesses.

**SSRF blocking** (`internal/transport/ssrf.go: ValidateURL`):

Runtime `add_server` URLs are validated before use (config-file URLs are admin-authored and trusted):
- Scheme must be `http` or `https`
- Loopback hostnames (`localhost`, `*.localhost`) are blocked
- Private TLDs (`.local`, `.internal`) are blocked
- Direct IP references to private ranges are blocked: `127.0.0.0/8`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16` (IMDS), `100.64.0.0/10`, `::1/128`, `fc00::/7`, `fe80::/10`, `0.0.0.0/8`
- IPv4-in-IPv6 addresses (`::ffff:127.0.0.1`) are unmapped before range checks

**Limitation**: DNS-resolution-time SSRF (where a hostname resolves to a private IP at connect time, after validation) is not blocked. A custom `DialContext` that re-checks the resolved IP is the correct fix and is tracked as a future improvement.

**Mitigation**: To defend against DNS rebinding and late-resolution SSRF, deploy mini behind a network policy or firewall that blocks egress to private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8) from the mini process. This is the recommended defense for production deployments.

**Redirect blocking** (`internal/transport/http.go`):

HTTP upstreams follow zero redirects. A redirect to an attacker URL would bypass the SSRF check performed at config time.

### Server name validation

Server names are validated against `^[a-zA-Z0-9_-]+$` at every input boundary: CLI flags, MCP protocol `add_server` calls, and config file loading. This prevents path traversal via names like `../../../etc` that could escape `~/.mini/servers/`.

### HTTP transport only for runtime `add_server`

Agents can only register HTTP upstreams via `add_server` by default. Stdio subprocess execution from agent-controlled commands requires `dangerous_allow_runtime_stdio: true` in global config — an explicit opt-in that surfaces the risk to the user.

**Warning**: When `dangerous_allow_runtime_stdio: true` is set, agents can supply arbitrary `command` and `args` to `add_server`. Because `exec.CommandContext` does not use a shell, shell metacharacter injection is not possible, but **there is no allowlist** — any binary on the host can be executed. This flag grants the agent near-arbitrary command execution on the host. Only enable it when you fully trust every agent that connects to this mini instance.

`dangerous_allow_private_urls: true` disables SSRF URL validation on `add_server`, allowing upstreams that resolve to private/loopback addresses. This is intended for test environments where upstream MCPs run on localhost. Do not enable in production.

### Header injection

HTTP header values containing CR, LF, or NUL characters are rejected by Go's `net/http` package before being forwarded to upstreams, preventing header injection attacks.

### HTTP mode binding

`mini serve --http ADDR` listens for HTTP MCP connections. By default:

- A bare port (`4857`) or colon-prefixed port (`:4857`) binds to `127.0.0.1` (loopback only).
- Any other host component (e.g. `0.0.0.0:4857`) is considered non-loopback and **requires** passing `--dangerous-nonloopback-http` to `mini serve`. Without the flag, the server exits with an error.
- When `--dangerous-nonloopback-http` is passed, a warning is logged and the server starts; the operator is responsible for ensuring all network clients are trusted (e.g. via firewall rules or VPN).

The daemon (`mini daemon`) always binds to `127.0.0.1` and does not accept a host override.

### Session ID

HTTP session IDs are generated with `crypto/rand` (not PID- or time-based) and validated on receipt: regex `^[a-f0-9-]{32,128}$` (minimum 32 chars, hex + hyphens) plus a programmatic check requiring ≥ 16 hex characters to prevent all-hyphen IDs with no real entropy. This prevents session store DoS via unbounded unique IDs.

### Request body size

Incoming requests from agents are capped at 1MB (`internal/server/http.go`). Tool arguments should be kilobytes; a 1MB limit is a 1000× safety margin that still prevents heap exhaustion from malformed/injected inputs.

### Response body size

Upstream response bodies are capped at 64MB. Error bodies from upstreams are capped at 4KB.

### Response file security

Response files written to `~/.mini/responses/` use:
- `0700` directory permissions (restricts access to the running user)
- `0600` file permissions
- Timestamp-based filenames (e.g. `20060102150405123.json`). Names are not cryptographically random, but the `0700` directory permission limits access to the owning user, so unguessable names are not required.

The `read` tool (proxy mode) validates requested paths using `filepath.EvalSymlinks` before checking they are within the response directory. Symlinks inside the store that point outside it are rejected — a symlink escape would allow reading arbitrary files accessible to the process user.


## Permission tiers

| Tier | Visibility | Callable via |
|------|-----------|--------------|
| `open` (default) | `list` shows it | `call` or `perm_call` |
| `protected` | `list` shows it | `perm_call` only |
| `hidden` | `list` hides it (unless `list(hidden:true)`) | `perm_call` only |

**Note**: hidden tools are enumerable via `list(hidden:true)` unless `disable_list_hidden: true` is set in global config. Do not rely on `hidden` as a hard access control boundary without this flag.


## OAuth security

The PKCE flow in `internal/auth/oauth.go` correctly:
- Generates a fresh `code_verifier` and `state` nonce per flow
- Validates `state` on callback before accepting the code
- Closes the callback server immediately after receiving the code
- Flushes the HTTP response before signaling (prevents truncation when the server closes)

OAuth endpoints (`auth_url`, `token_url`) in server config files are user-managed and are not validated for SSRF — these are trusted user-authored values, not agent-provided ones. If an agent adds a server via `add_server`, `sc.Auth` is stripped entirely before the server config is stored (see Prompt injection section above).


## Daemon

The daemon uses a port file (`~/.mini/daemon.port`) for discovery. Liveness is verified via HTTP healthz before reporting the daemon as running — a stale port file left after a crash does not prevent a new daemon from starting.

The daemon HTTP server sets `ReadHeaderTimeout: 5s` to prevent slowloris-style attacks. No `WriteTimeout` is set because per-call tool timeouts are enforced via `context.WithTimeout` — a fixed write timeout would silently truncate responses for tools with long but legitimate timeouts.


## Reporting vulnerabilities

Please open a GitHub issue with the `security` label, or email the maintainer directly. Do not disclose publicly before a fix is available.
