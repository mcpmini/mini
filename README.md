# mini

**mini** is an MCP proxy that sits between your AI agent and your MCP servers, trimming tool responses down to what the agent actually needs.

MCP responses are verbose by design — full JSON blobs with every field the API returns. PR bodies, avatar URLs, node IDs, merge metadata — most of it the agent will never read. mini strips the noise before any of it reaches your agent's context window. [See an example.](#how-it-works)

For list-heavy tools like `list_pull_requests` or `list_issues`, response sizes drop dramatically with a projection config. Read-heavy tools like `get_file_contents` see more modest savings. Results vary by upstream server and config.

> **New to MCP?** [Model Context Protocol](https://modelcontextprotocol.io) is how AI agents connect to external tools. Each "MCP server" exposes a set of tools the agent can call. mini sits in front of all of them.

---

## Install

```bash
go install github.com/mcpmini/mini/cmd/mini@latest
mini init    # imports your existing MCP servers and installs bundled projections
mini status  # verify everything connected
```

---

## Connect to your agent

### Claude Code

```bash
claude mcp add mini mini proxy
```

Claude Code doesn't load MCP tool schemas upfront — it discovers them on demand using a built-in ToolSearch mechanism. This means **proxy mode is the right choice**: mini exposes your upstream tools directly (`github__list_pull_requests`, `sentry__list_issues`, etc.), Claude's schema deferral works exactly as designed, and responses are still trimmed by mini in the background. One round-trip per tool call, and Claude has no idea mini is there.

Standard mode works, but it's less efficient — every upstream call requires two round-trips (`mini.list` then `mini.call`), and tool schemas end up in conversation messages instead of Claude's native deferred-loading layer.

→ [How Claude Code loads MCP tools](docs/claude-code-mcp-loading.md)

### Codex

Add to your project's `codex.json`:

```json
{
  "mcpServers": {
    "mini": { "command": "mini" }
  }
}
```

Codex has its own deferred loading (BM25 search, kicks in at 100+ tools), but below that threshold it loads all schemas upfront. Standard mode keeps the surface to 4 tools regardless, so the initial context cost stays predictable. See [how Codex loads MCP tools](docs/codex-mcp-loading.md).

### Cursor

Open Settings → MCP and add:

```json
{
  "mcpServers": {
    "mini": { "command": "mini" }
  }
}
```

### Windsurf, Gemini CLI, and other MCP clients

```json
{
  "mcpServers": {
    "mini": { "command": "mini" }
  }
}
```

Restart your agent after adding. Use proxy mode (`"command": "mini proxy"`) only if your agent defers MCP tool schemas natively — otherwise standard mode is the safer choice.

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

## How it works

### Standard mode (`mini serve`, default)

mini exposes 4 tools to the agent:

| Tool | What it does |
|------|-------------|
| `list` | Discover all tools across connected servers |
| `call` | Invoke a tool — response is projected and returned as a summary + trimmed JSON |
| `perm_call` | Same as `call` for protected tools (write ops, destructive actions) |
| `config` | Runtime admin: add/remove servers, adjust projections, check status |

### Proxy mode (`mini proxy`)

mini exposes each upstream tool directly as `server__tool` (e.g. `github__list_pull_requests`, `sentry__list_issues`). Projections still apply — the agent gets trimmed data without knowing mini is there. Two utility tools are also available: `mini_config` (admin) and `mini_read` (fetch a response file by path when a response is too large to inline).

<details>
<summary>Example — <code>list_pull_requests</code> before and after</summary>

**Without mini** — one PR entry from a busy repo:

```json
{
  "number": 275198,
  "title": "Remove layout control toggles from titlebar",
  "body": "Removes layout toggle buttons...\n\n## Changes\n\n**Removed `MenuId.LayoutControlMenu` registrations:**\n- Toggle Primary Sidebar (left/right variants)...\n[full body continues]",
  "state": "open",
  "draft": true,
  "merged": false,
  "html_url": "https://github.com/microsoft/vscode/pull/275198",
  "user": { "login": "Copilot", "id": 198982749, "avatar_url": "https://avatars.githubusercontent.com/..." },
  "assignees": ["cwebster-99", "Copilot"],
  "head": { "ref": "copilot/remove-layout-toggles-navbar", "sha": "73e46e32...", "repo": { "full_name": "microsoft/vscode" } },
  "base": { "ref": "respectable-dog", "sha": "a756650...", "repo": { "full_name": "microsoft/vscode" } },
  "created_at": "2025-11-04T17:25:38Z",
  "updated_at": "2025-11-04T18:51:15Z"
}
```

30 PRs with full bodies = **188,174 tokens**.

**With mini** (standard mode) — the agent receives a summary and a pointer to a trimmed file:

```json
{
  "server": "github", "tool": "list_pull_requests", "ok": true,
  "summary": "30 pull requests (28 open, 2 draft). Latest: #282558 Adding needsinput state (osortega), #275198 Remove layout control toggles (Copilot → cwebster-99), #267874 AI search telemetry (Copilot → osortega)",
  "inline": false,
  "file": "~/.mini/responses/20251104123456.json",
  "estimated_raw_tokens": 188174,
  "estimated_tokens_saved": 184950
}
```

The file contains only the fields that matter — body stripped, URLs stripped, user metadata stripped. **3,224 tokens** instead of 188,174.

**With mini** (proxy mode) — the agent receives the trimmed data directly:

```json
[
  {
    "number": 275198,
    "title": "Remove layout control toggles from titlebar",
    "state": "open", "draft": true,
    "user": { "login": "Copilot" },
    "assignees": ["cwebster-99"],
    "head": { "ref": "copilot/remove-layout-toggles-navbar" },
    "base": { "ref": "respectable-dog" },
    "created_at": "2025-11-04T17:25:38Z"
  }
]
```

Same trimming, no wrapper envelope.

</details>

---

## Configuration

Config lives in `~/.mini/` (override with `--config DIR`):

```
~/.mini/
  config.yaml              # global settings (inline_threshold, log_level, …)
  servers/<name>.yaml      # one file per upstream server
  projections/<name>.yaml  # per-tool field rules
  responses/               # trimmed response files (auto-cleaned by TTL)
```

**Tuning projections** — example for `list_pull_requests`:

```yaml
list_pull_requests:
  include: [number, title, state, draft, user, created_at, updated_at, assignees, head, base]
  string_limits:
    body: 300
  array_limits:
    default: 20
    assignees: 3
```

Without a projection config, mini applies conservative global defaults (strings capped at 1,000 chars, arrays at 3 items, depth at 3 levels). You'll get some savings but not the large reductions that come from explicit `include` lists.

**Permission tiers** — gate write operations behind `perm_call`:

```yaml
# servers/github.yaml
permissions:
  protected: [create_pull_request, merge_pull_request, delete_file]
```

**OAuth** — for servers that require OAuth2 (Linear, Slack):

```bash
mini auth linear   # opens browser for PKCE flow, token stored in ~/.mini/tokens/
```

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
