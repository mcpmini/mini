# mini

**mini** is an MCP proxy that trims tool responses before they reach your agent.

MCP servers are generous — `list_pull_requests` returns full PR bodies, avatar URLs, node IDs, merge metadata. Most of it your agent never reads. mini strips the noise using configurable projection rules, so only the fields that matter arrive in context.

> **New to MCP?** [Model Context Protocol](https://modelcontextprotocol.io) is how AI agents connect to external tools. mini sits in front of all of them.

---

## Install

```bash
go install github.com/mcpmini/mini/cmd/mini@latest
mini init    # imports your existing MCP servers, installs bundled projections
mini status  # verify connections
```

---

## What it does

**Before** — one PR entry from a busy repo:

```json
{
  "number": 275198,
  "title": "Remove layout control toggles from titlebar",
  "body": "Removes layout toggle buttons...\n\n[full body continues]",
  "state": "open",
  "draft": true,
  "html_url": "https://github.com/microsoft/vscode/pull/275198",
  "user": { "login": "Copilot", "id": 198982749, "avatar_url": "https://avatars.githubusercontent.com/..." },
  "assignees": [{ "login": "cwebster-99", "id": 19878031, "avatar_url": "..." }],
  "head": { "ref": "copilot/remove-layout-toggles-navbar", "sha": "73e46e32...", "repo": { "full_name": "microsoft/vscode", ... } },
  "base": { "ref": "respectable-dog", "sha": "a756650...", "repo": { "full_name": "microsoft/vscode", ... } },
  "created_at": "2025-11-04T17:25:38Z",
  "updated_at": "2025-11-04T18:51:15Z"
}
```

**After** — same PR, with mini's bundled GitHub projection applied:

```json
{
  "number": 275198,
  "title": "Remove layout control toggles from titlebar",
  "state": "open",
  "draft": true,
  "user": "Copilot",
  "assignees": ["cwebster-99"],
  "head": "copilot/remove-layout-toggles-navbar",
  "base": "respectable-dog",
  "created_at": "2025-11-04T17:25:38Z"
}
```

Bodies stripped. Avatar URLs gone. Nested objects flattened to the one field that matters. Multiply across 30 PRs.

---

## Connect to your agent

### Claude Code — proxy mode

```bash
claude mcp add mini mini proxy
```

Claude Code defers MCP tool schema loading — it discovers and loads tool schemas on demand rather than upfront. Proxy mode is the right fit: mini exposes upstream tools directly (`github__list_pull_requests`, `sentry__list_issues`, etc.), Claude's schema deferral works as designed, and responses are trimmed transparently. Mini is invisible to the agent.

Two utility tools are also available: `config` (runtime admin: add/remove servers, check status) and `read` (fetch a large response file by path).

→ [How Claude Code loads MCP tools](docs/claude-code-mcp-loading.md)

### Codex, Cursor, Windsurf, and other MCP clients — standard mode

```json
{
  "mcpServers": {
    "mini": { "command": "mini" }
  }
}
```

Standard mode exposes 4 tools to the agent:

| Tool | What it does |
|---|---|
| `list` | Discover all tools across connected servers |
| `call` | Invoke a tool — response is projected and returned |
| `perm_call` | Same as `call` for protected tools (write ops, destructive actions) |
| `config` | Runtime admin: add/remove servers, adjust projections, check status |

Good for clients that load all tool schemas upfront — the fixed 4-tool surface keeps initial context cost predictable regardless of how many upstream servers you have.

See [how Codex loads MCP tools](docs/codex-mcp-loading.md) for Codex-specific guidance.

Restart your agent after adding.

---

## Adding servers

```bash
mini add github --url https://api.githubcopilot.com/mcp --header "Authorization=Bearer $GITHUB_TOKEN"
mini add linear --url https://mcp.linear.app/mcp
mini add sentry --url https://mcp.sentry.io/mcp --header "Authorization=Bearer $SENTRY_TOKEN"
mini add slack  --url https://mcp.slack.com/mcp   --header "Authorization=Bearer $SLACK_TOKEN"
```

Or import from an existing config:

```bash
mini add --from-claude   # Claude Desktop / Claude Code
mini add --from-cursor   # Cursor mcp.json
mini add --from-codex    # Codex config.toml
mini add --from-gemini   # Gemini CLI settings.json
```

Bundled projections for GitHub, Linear, Sentry, Slack, and Jira install automatically when a known server is detected.

---

## Projection config

Projections tell mini which fields to keep, truncate, or drop. They live in `~/.mini/projections/<server>.yaml`:

```yaml
list_pull_requests:
  include: [number, title, state, draft, user, created_at, updated_at, assignees, head, base]
  string_limits:
    body: 300
  array_limits:
    default: 20
    assignees: 3
```

Without a projection, mini applies conservative defaults: strings capped at 1,000 chars, arrays at 3 items, depth at 3 levels. You'll get some trimming but the large reductions come from explicit `include` lists that match what your agent actually needs.

Config lives in `~/.mini/` (override with `--config DIR`):

```
~/.mini/
  config.yaml              # global settings (inline_threshold, log_level, …)
  servers/<name>.yaml      # one file per upstream server
  projections/<name>.yaml  # per-tool field rules
  responses/               # response files, auto-cleaned by TTL and disk budget
```

### Large responses

When a projected response is still large, mini writes it to `~/.mini/responses/` and returns the file path instead. Use `read` (proxy mode) or `config action:read` (standard mode) to fetch the file. Response files are cleaned up automatically.

---

## Permissions

Gate write operations behind `perm_call`:

```yaml
# ~/.mini/servers/github.yaml
permissions:
  protected: [create_pull_request, merge_pull_request, delete_file]
  hidden: [get_authenticated_app, list_app_installations]
```

Three tiers: `open` (default), `protected` (requires `perm_call`), `hidden` (invisible to `list`). Bundled permission defaults install automatically for known servers.

`perm_call` works as a gate via your agent's per-tool permission settings: in Claude Code, allowlist `mcp__mini__call` and leave `mcp__mini__perm_call` unapproved — Claude will prompt before calling protected tools. Codex supports the same via `approval_mode` per tool. **Cursor only supports server-level approval**, so `perm_call` is not a hard gate there — use `hidden` instead for tools that should never be agent-callable in Cursor.

---

## Auth

For servers that require OAuth2 (Linear, Slack):

```bash
mini auth linear   # opens browser for PKCE flow, token stored in ~/.mini/tokens/
```

API key and Bearer token injection are also supported — set them in the server config or via `${ENV_VAR}` references.

---

## Commands

```
mini serve            Standard mode — 4-tool interface (default)
mini proxy            Proxy mode — upstream tools exposed directly
mini daemon           Run as a shared background daemon
mini daemon status    Check daemon health
mini ls               List configured servers
mini add NAME         Add a server
mini rm NAME          Remove a server
mini status           Server health and tool counts
mini auth NAME        OAuth2 PKCE flow for a server
mini init             Setup wizard
mini test             CI health check (exits 1 on any failure)
mini cleanup          Delete expired response files
```
