# mini — Test Scenarios

Behavior-focused. Each scenario is "given this setup, when the user does X, the result should be Y." Ordered by importance to real users. Internal/technical investigations are in a separate section at the end.

---

## S1 — Agent calls upstream tool through proxy (most common flow)

**Setup:** `mini proxy` connected to GitHub MCP. Agent is Claude Code.

**When:** Agent calls `github__list_pull_requests(owner:"microsoft", repo:"vscode")`

**Then:**
- Response arrives as trimmed JSON — only the configured fields (number, title, state, draft, user, head, base, created_at)
- Body, avatar_url, node_id, html_url are absent
- No wrapper envelope — agent sees the data directly, not `{"data": ..., "elided": [...]}`
- mini is completely invisible to the agent

**When:** Agent calls `github__create_pull_request(...)` (write operation, open by default)

**Then:** Succeeds — proxy mode does not apply the `perm_call` gate

**When:** Mini adds a new upstream server at runtime via `config add_server`

**Then:** Agent immediately receives `notifications/tools/list_changed` and the new tools appear in the next `tools/list`

**When:** Agent calls a tool on a server that was just removed

**Then:** Error returned — "server not connected: X"

---

## S2 — Trimmed response is still too large (file store)

**Setup:** Proxy mode, GitHub MCP, `inline_threshold: 500` (tokens).

**When:** Agent calls `github__list_pull_requests` on a busy repo (30+ PRs)

**Then:**
- Response is written to `~/.mini/responses/<timestamp>.json` (slim file)
- Companion `<timestamp>.raw.json` contains the original upstream response
- Agent receives: `File: ~/.mini/responses/<timestamp>.json` (proxy format) or `[github.list_pull_requests] file:~/.mini/responses/<timestamp>.json` (mini format)
- Slim file is valid JSON with `_meta` (field list, count, index) and `items` array
- Agent can call `read(path:"~/.mini/responses/<timestamp>.json")` and receives slim content

**When:** Agent calls `read` with a path outside `~/.mini/responses/`

**Then:** Rejected with "path must be within mini response directory"

**When:** Agent calls `read` with a path like `../../etc/passwd`

**Then:** Rejected — path traversal blocked

---

## S3 — Mini format (compact text output)

**Setup:** Projection configured with `format: mini` for `list_issues`.

**When:** Agent calls `github__list_issues(owner:"golang", repo:"go")`

**Then:**
- Response is compact text, not JSON:
  ```
  [github.list_issues]
  number state title user_login
  79443 OPEN cmd/asm: unrecognized failures gopherbot
  79442 OPEN cmd/asm/arch: failures gopherbot
  ```
- Header row shows field names; each subsequent row is one item
- No JSON braces, quotes, or commas
- If response went to file: `[github.list_issues] file:/path` (not bare `File: /path`)

**When:** Global `response_format: mini` is set in config.yaml

**Then:** All tool calls use mini format without needing per-tool projection

---

## S4 — Standard mode: agent uses 4-tool interface

**Setup:** `mini serve` (standard mode), GitHub + Linear connected.

**When:** Agent calls `list()`

**Then:** Returns all tools from all servers: `github.list_pull_requests`, `github.list_issues`, `linear.list_issues`, etc.

**When:** Agent calls `list(query:"issues")`

**Then:** Returns only tools matching "issues"

**When:** Agent calls `call(server:"github", tool:"list_issues", params:{...})`

**Then:** Returns projected response — trimmed JSON inline (small) or `{"data":..., "file":"/path"}` (large)

**When:** Agent calls `call(server:"github", tool:"list_issues")` but no projection file exists for github

**Then:** Succeeds — restriction only activates once an operator has written a projections file for that server

**When:** Agent calls `call(server:"github", tool:"list_issues")` and a projections file exists but list_issues has no entry

**Then:** Error — "tool has no projection configured — use perm_call to proceed, or add a projection entry"

**When:** Agent calls `perm_call(server:"github", tool:"list_issues")` for the above case

**Then:** Succeeds with raw (unprojected) response — opt-in to large responses

---

## S5 — Permission tiers

**Setup:** GitHub server with `protected: [create_pull_request, merge_pull_request]`, `hidden: [get_authenticated_app]`.

**When:** Agent calls `list()`

**Then:** `github.get_authenticated_app` is absent from the list

**When:** Agent calls `call(server:"github", tool:"get_authenticated_app")`

**Then:** Error — tool not found (hidden tools invisible to list AND call)

**When:** Agent calls `perm_call(server:"github", tool:"get_authenticated_app")`

**Then:** Error — "tool not found". Hidden tools are completely inaccessible through mini. If the tool is needed, use `protected` instead of `hidden`.

**When:** Agent calls `call(server:"github", tool:"create_pull_request")`

**Then:** Error — "tool is protected — use perm_call instead"

**When:** Agent calls `perm_call(server:"github", tool:"create_pull_request")`

**Then:** Succeeds

**When:** Agent calls `perm_call(server:"github", tool:"list_issues")` (open tool with projection)

**Then:** Error — "tool is not protected — use call instead"

**When:** Agent calls `list(hidden:true)`

**Then:** Returns ALL tools including hidden ones

**When:** `disable_list_hidden: true` is in config and agent calls `list(hidden:true)`

**Then:** Error — listing hidden tools disabled by server configuration

---

## S6 — Session-only projection customisation

**Setup:** Two Claude Code windows, both using `mini proxy` via the same daemon.

**When:** Window A calls `config(action:"set_projection", server:"github", tool:"list_issues", projection:{exclude_always:[body]}, session_only:true)`

**Then:** `{"ok": true, "scope": "session"}`

**When:** Window A calls `github__list_issues`

**Then:** Response has no `body` field

**When:** Window B calls `github__list_issues` (no projection set)

**Then:** Response includes `body` — Window A's projection did not leak to Window B

**When:** Window A calls `config(action:"reload")`

**Then:** Server-level projections reloaded from disk; Window A's session-only projections are unaffected

**When:** Window A calls `config(action:"set_projection", session_only:false, ...)`

**Then:** Projection persisted to `~/.mini/projections/github.yaml`; survives session restart

---

## S7 — Runtime server management

**Setup:** Daemon running, agent connected.

**When:** Agent calls `config(action:"add_server", config:{name:"sentry", transport:"http", url:"https://mcp.sentry.io/mcp", headers:{Authorization:"Bearer $TOKEN"}})`

**Then:** Error — auth/headers stripped at runtime for security. Operator must configure auth in the server YAML file.

**When:** Agent calls `config(action:"add_server", config:{name:"internal", url:"http://192.168.1.1/mcp"})`

**Then:** Error — "URL host resolves to a private/loopback address"

**When:** Operator calls `config(action:"add_server", config:{name:"newserver", url:"https://api.newserver.com/mcp"})`

**Then:** Connection established, tools listed, `notifications/tools/list_changed` sent to all proxy-mode sessions

**When:** Operator calls `config(action:"remove_server", server:"newserver")`

**Then:** Tools removed, sessions closed for that server, notification sent. Returns `{"ok":true}` even if server wasn't found (idempotent).

---

## S8 — Import from existing agent configs

**When:** User runs `mini add --from-claude`

**Then:** Reads `~/.claude.json`, imports all `mcpServers` entries, writes one YAML per server to `~/.mini/servers/`. If a known server (GitHub, Slack) is detected, bundled projection installed automatically.

**When:** Source config has a server named `my bad server` (invalid characters)

**Then:** Skipped with warning — names must match `^[a-zA-Z0-9_-]+$`

**When:** Claude Code and Claude Desktop both have a server named `github`

**Then:** First-seen kept, warning printed for the duplicate

**When:** User runs `mini add --from-cursor`, `--from-codex`, `--from-gemini`

**Then:** Same behavior — server YAMLs written, bundled projections installed where applicable

---

## S9 — OAuth authentication

**When:** User runs `mini auth linear` for a server with `auth.type: oauth2`

**Then:** PKCE flow starts, browser opens to `https://linear.app/oauth/authorize?...` with proper state and S256 challenge

**When:** User completes OAuth in browser

**Then:** Token stored at `~/.mini/tokens/linear.json` with `0600` permissions

**When:** Agent calls a Linear tool after auth

**Then:** Request includes `Authorization: Bearer <token>` — transparent to agent

**When:** Token expires and refresh token is present

**Then:** Token refreshed automatically on next call — agent unaware

**When:** OAuth discovery endpoint returns `token_endpoint: "http://169.254.169.254/token"`

**Then:** Error — SSRF blocked, attacker-controlled endpoint rejected

---

## S10 — Per-session upstream connections (stateful tools)

**Setup:** Playwright server configured with `session_mode: per_session`.

**When:** Two agents call Playwright tools concurrently

**Then:** Each agent gets its own browser instance — Agent A's page state does not affect Agent B

**When:** Agent A's session is idle for longer than `max_idle` duration

**Then:** Session evicted, connection closed, browser instance freed

**When:** Agent A's Playwright connection drops mid-session

**Then:** Only Agent A's connection is evicted; Agent B continues unaffected; Agent A gets a fresh connection on next call

---

## S11 — Reconnect after upstream failure

**When:** Upstream stdio server crashes mid-session

**Then:** mini detects transport error, starts reconnect loop with exponential backoff. In-flight calls fail with error. Once reconnected, new calls succeed. Agent can retry.

**When:** Upstream returns JSON-RPC error `-32602` (invalid params)

**Then:** NOT treated as transport failure — no reconnect. Error returned to agent as tool error.

**When:** Tool call exceeds `tool_timeout: "30s"`

**Then:** Context cancelled, error returned. Upstream still alive — no reconnect.

**When:** `tool_timeout: "not-a-duration"` set in server config

**Then:** Warning logged ("invalid tool_timeout spec, no timeout applied"), call proceeds without timeout rather than failing

---

## S12 — Security: SSRF and network isolation

**When:** Agent calls `config add_server` with `url: "http://127.0.0.1/mcp"`

**Then:** Rejected — "URL host resolves to a private/loopback address"

**When:** Agent calls `config add_server` with `url: "http://169.254.169.254/"` (AWS metadata)

**Then:** Rejected — link-local blocked

**When:** Agent calls `config add_server` with `url: "http://internal.company.local/mcp"`

**Then:** Rejected — `.local` hostname blocked

**When:** HTTP client sends POST to `/mcp` with header `Origin: http://evil.com`

**Then:** 403 — cross-origin request rejected

**When:** mini serves HTTP on `127.0.0.1:4857` (default)

**Then:** Only loopback connections accepted

**When:** User runs `mini serve --http 0.0.0.0:4857` without `--dangerous-nonloopback-http`

**Then:** Server exits with error — non-loopback binding requires explicit opt-in flag

---

## S13 — Disk budget and cleanup

**When:** Total response files exceed `response_disk_budget_mb`

**Then:** Oldest files evicted automatically to bring usage within budget. Newly written file is always kept (never evicted to make room for itself).

**When:** Response files older than TTL

**Then:** Cleaned up automatically by the background cleanup loop

**When:** User runs `mini cleanup`

**Then:** Expired files deleted immediately, usage stats printed

---

---

## Internal / Technical Investigations

These don't map directly to user-visible behavior but affect correctness and reliability.

| # | Area | What to investigate |
|---|---|---|
| I1 | `evictOvershoot` (25% covered) | Budget enforcement loop never exercised in tests — write a test with tiny budget |
| I2 | File store writes (`writeSlimFile` 45%, `writeRawFile` 40%) | Error injection paths uncovered — disk write failure cleanup correct? |
| I3 | `writeExclusive` (54%) | Concurrent write collision handling — is O_EXCL retry loop correct under load? |
| I4 | `cancelAuthFlows` (50%) | Auth goroutine drain — is authWg.Wait() draining correctly after Close()? |
| I5 | `installUpstreamLocked` (62%) | Inline projections branch — projections embedded in server YAML applied on connect? |
| I6 | TOCTOU regression | Add test: concurrent add_server + remove_server, verify generation counter makes remove win |
| I7 | Plain array mini format | Verify header-row layout handles all array shapes including non-uniform items |
| I8 | Session eviction | After idle eviction, leaked state? New session truly fresh? |
