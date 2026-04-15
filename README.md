# mini

**mini** is an MCP proxy that sits between your AI agent and upstream MCP servers (GitHub, Linear, Sentry, Slack, Jira, …). It trims tool responses down to what agents actually need, cutting token usage by 80–99% on the noisiest calls.

> **New to MCP?** [Model Context Protocol](https://modelcontextprotocol.io) is how AI agents like Claude, Cursor, and Gemini connect to external tools — GitHub, Linear, Slack, etc. Each "MCP server" exposes a set of tools the agent can call. mini sits in front of all of them.

## How agents load MCP tools

Understanding where the token cost actually comes from helps explain what mini fixes.

**Claude Code**
- All MCP tools are registered as *deferred* by default — only names and descriptions are sent to the Anthropic API; full schemas are excluded from the context window until needed
- When the model needs a tool, it calls a built-in `ToolSearch` tool (regex or BM25); the API injects the matching schema inline without touching the prompt cache
- Tool *responses*, however, are raw — upstream servers return the full JSON object exactly as the API gave it, with no trimming or projection
- Two MCP `_meta` extensions allow opt-outs: `anthropic/alwaysLoad` disables deferral for a specific tool; `anthropic/searchHint` adds extra search keywords

**Codex**
- All MCP tools are loaded upfront at init and sent to the model with full schemas; no deferral at startup
- A built-in `tool_search` tool runs client-side BM25 over the pre-loaded catalog; matched tools are returned with `defer_loading: true` (OpenAI Responses API) so the model gets lightweight references before committing
- Tool names use `mcp__server__tool` format with double-underscore delimiters; names exceeding the limit are truncated with a SHA1 suffix for uniqueness
- Like Claude Code, Codex receives raw upstream JSON — no projection layer exists in either client

**Where mini fits**

Both clients solve schema-loading efficiency differently, but neither touches response content. A `list_pull_requests` call on a busy repo returns the same 188k-token blob whether the tool schema was deferred or not. mini intercepts that response before it reaches the agent and applies projection — field allowlists, string truncation, array caps — so the agent sees a summary and a trimmed file instead.

For Claude Code, mini's 4-tool schema is itself deferred like any other MCP server; the real saving is response trimming. For Codex, mini also cuts the initial tool list from N schemas to 4, reducing upfront context at startup.

---

## Why

MCP responses are verbose by design — full JSON blobs with every field the API returns. A `list_pull_requests` call on an active repo returns PR bodies, URL fields, avatar links, node IDs, and merge metadata the agent will never read. On a busy repo like microsoft/vscode that's **~188,000 tokens** for a single tool call.

mini applies per-tool rules: field allowlists, string truncation, and array caps. The agent gets a concise summary plus a trimmed JSON file for follow-up reads.

| Tool | Raw | mini | Saved |
|------|-----|------|-------|
| `list_pull_requests` (microsoft/vscode, 30 PRs) | 188,174 tokens | 3,224 tokens | **98%** |
| `list_issues` (30 issues with long bodies) | 80,116 tokens | 246 tokens | **99.7%** |
| `search_code` | 2,186 tokens | 1,721 tokens | **21%** |
| `get_file_contents` | 394 tokens | 169 tokens | **57%** |

Even on quieter repos the savings are significant — 5,000–25,000 tokens trimmed to 500–3,000 is typical.

<details>
<summary>Example — <code>list_pull_requests</code> on microsoft/vscode</summary>

**Without mini** — the agent receives the full response for every PR. One entry looks like:

```json
{
  "number": 275198,
  "title": "Remove layout control toggles from titlebar",
  "body": "Removes layout toggle buttons (Primary Sidebar, Secondary Sidebar, Panel,\nConfigure Layout) from the titlebar navigation bar while preserving all\nfunctionality through existing alternative entry points.\n\n## Changes\n\n**Removed `MenuId.LayoutControlMenu` registrations:**\n- Toggle Primary Sidebar (left/right variants) - `src/vs/workbench/browser/actions/layoutActions.ts`\n- Toggle Secondary Sidebar (left/right variants) - `src/vs/workbench/browser/parts/auxiliarybar/auxiliaryBarActions.ts`\n- Toggle Panel - `src/vs/workbench/browser/parts/panel/panelActions.ts`\n- Configure Layout submenu and Customize Layout button\n\n**Cleaned up unused artifacts:**\n- Removed `IsAuxiliaryWindowContext` imports from all three files\n- Removed unused icon registrations: `panelLeftOffIcon`, `panelRightOffIcon`,\n  `panelIcon`, `panelOffIcon`, `auxiliaryBar*Icon` variants\n\n## Functionality Preserved\n\nAll removed actions remain accessible via:\n- F1 Command Palette\n- View → Appearance menu (`MenuId.MenubarAppearanceMenu`)\n- Keyboard shortcuts (⌘B, ⌘J, ⌘⌥B)\n...\n[full body continues for every PR in the list]",
  "state": "open",
  "draft": true,
  "merged": false,
  "html_url": "https://github.com/microsoft/vscode/pull/275198",
  "user": {
    "login": "Copilot",
    "id": 198982749,
    "profile_url": "https://github.com/apps/copilot-swe-agent",
    "avatar_url": "https://avatars.githubusercontent.com/in/1143301?v=4"
  },
  "assignees": ["cwebster-99", "Copilot"],
  "requested_reviewers": ["cwebster-99"],
  "head": {
    "ref": "copilot/remove-layout-toggles-navbar",
    "sha": "73e46e32690c6ea9cb7356eaa3ae703c1bada93c",
    "repo": { "full_name": "microsoft/vscode", "description": "Visual Studio Code" }
  },
  "base": {
    "ref": "respectable-dog",
    "sha": "a756650538937bf7dc32849d117e6a566cac8f57",
    "repo": { "full_name": "microsoft/vscode", "description": "Visual Studio Code" }
  },
  "created_at": "2025-11-04T17:25:38Z",
  "updated_at": "2025-11-04T18:51:15Z"
}
```

Multiply that by 30 PRs with full bodies = **188,174 tokens**.

**With mini** — the agent's `call` returns immediately with a summary and a pointer to a trimmed file:

```json
{
  "server": "github",
  "tool": "list_pull_requests",
  "ok": true,
  "summary": "30 pull requests (28 open, 2 draft). Latest: #282558 Adding needsinput state (osortega, 2025-12-10), #275198 Remove layout control toggles from titlebar (Copilot → cwebster-99, 2025-11-04), #267874 Implement AI search success tracking telemetry (Copilot → osortega, 2025-10-17)",
  "inline": false,
  "file": "~/.mini/responses/20251104123456.json",
  "estimated_raw_tokens": 188174,
  "estimated_tokens_saved": 184950,
  "latency_ms": 287
}
```

The file contains only the fields the agent needs:

```json
[
  {
    "number": 275198,
    "title": "Remove layout control toggles from titlebar",
    "state": "open",
    "draft": true,
    "user": { "login": "Copilot" },
    "assignees": ["cwebster-99", "Copilot"],
    "head": { "ref": "copilot/remove-layout-toggles-navbar" },
    "base": { "ref": "respectable-dog" },
    "created_at": "2025-11-04T17:25:38Z",
    "updated_at": "2025-11-04T18:51:15Z"
  }
]
```

Body stripped, URL fields stripped, user metadata stripped. **3,224 tokens** instead of 188,174.

</details>

<details>
<summary>Example — <code>get_pull_request</code> reading PR details</summary>

**Without mini** — full PR object with the complete body, all merge metadata, and nested user/repo objects:

```json
{
  "number": 275198,
  "title": "Remove layout control toggles from titlebar",
  "body": "Removes layout toggle buttons (Primary Sidebar, Secondary Sidebar, Panel,\nConfigure Layout) from the titlebar navigation bar...\n\n## Changes\n\n**Removed `MenuId.LayoutControlMenu` registrations:**\n- Toggle Primary Sidebar (left/right variants)...\n\n**Cleaned up unused artifacts:**\n- Removed `IsAuxiliaryWindowContext` imports...\n\n## Functionality Preserved\n\nAll removed actions remain accessible via:\n- F1 Command Palette\n- View → Appearance menu\n- Keyboard shortcuts (⌘B, ⌘J, ⌘⌥B)\n...\n[full markdown body]\n\n---\n✨ Let Copilot coding agent set things up for you — coding agent works faster\nand does higher quality work when set up for your repo.",
  "state": "open",
  "draft": true,
  "merged": false,
  "mergeable_state": "unstable",
  "html_url": "https://github.com/microsoft/vscode/pull/275198",
  "user": {
    "login": "Copilot",
    "id": 198982749,
    "profile_url": "https://github.com/apps/copilot-swe-agent",
    "avatar_url": "https://avatars.githubusercontent.com/in/1143301?v=4"
  },
  "assignees": ["cwebster-99", "Copilot"],
  "requested_reviewers": ["cwebster-99"],
  "head": { "ref": "copilot/remove-layout-toggles-navbar", "sha": "73e46e32...", "repo": { "full_name": "microsoft/vscode", "description": "Visual Studio Code" } },
  "base": { "ref": "respectable-dog", "sha": "a756650...", "repo": { "full_name": "microsoft/vscode", "description": "Visual Studio Code" } },
  "additions": 3,
  "deletions": 130,
  "changed_files": 3,
  "commits": 3,
  "comments": 3,
  "created_at": "2025-11-04T17:25:38Z",
  "updated_at": "2025-11-04T18:51:15Z"
}
```

**With mini:**

```json
{
  "server": "github",
  "tool": "get_pull_request",
  "ok": true,
  "summary": "PR #275198 Remove layout control toggles from titlebar — open draft by Copilot → cwebster-99. +3/−130 across 3 files, 3 commits, 3 comments. Removes Primary Sidebar, Secondary Sidebar, Panel, and Configure Layout toggle buttons from the titlebar navigation bar. All functionality preserved via Command Palette, View menu, and keyboard shortcuts.",
  "inline": true,
  "estimated_raw_tokens": 847,
  "estimated_tokens_saved": 551,
  "latency_ms": 143,
  "data": {
    "number": 275198,
    "title": "Remove layout control toggles from titlebar",
    "state": "open",
    "draft": true,
    "merged": false,
    "user": { "login": "Copilot" },
    "assignees": ["cwebster-99", "Copilot"],
    "head": { "ref": "copilot/remove-layout-toggles-navbar", "sha": "73e46e32..." },
    "base": { "ref": "respectable-dog" },
    "body": "Removes layout toggle buttons (Primary Sidebar, Secondary Sidebar, Panel, Configure Layout) from the titlebar navigation bar while preserving all functionality through existing alternative entry points.\n\nChanges\n\nRemoved MenuId.LayoutControlMenu registrations:\n- Toggle Primary Sidebar (left/right variants)...",
    "additions": 3,
    "deletions": 130,
    "changed_files": 3,
    "created_at": "2025-11-04T17:25:38Z",
    "updated_at": "2025-11-04T18:51:15Z"
  }
}
```

Body is kept (it's useful for a single PR) but truncated to 2,000 chars. Avatar URLs, merge commit SHA, `mergeable_state`, and all the GitHub API URL fields are gone.

</details>

---

## How it works

mini exposes **4 tools** to your agent:

| Tool | What it does |
|------|-------------|
| `list` | Discover all tools across all connected upstream servers |
| `call` | Invoke a tool — response is projected and returned as a summary + trimmed JSON |
| `perm_call` | Same as `call` but for tools marked as protected (write operations, destructive actions) |
| `config` | Runtime admin: add/remove servers, adjust projections, check status |

Agents call `list` once to discover what's available, then use `call`/`perm_call` for every tool invocation. mini handles routing, projection, and response storage transparently.

## Quickstart

```bash
# Install
go install github.com/mcpmini/mini/cmd/mini@latest

# Run the setup wizard — imports your existing MCP servers automatically
mini init

# Verify everything connected
mini status
```

Connect mini to Claude Code:

```bash
claude mcp add mini mini
```

Or add it to any agent that supports MCP (Cursor, Windsurf, Gemini CLI):

```json
{
  "mcpServers": {
    "mini": { "command": "mini" }
  }
}
```

Restart your agent. It will now route all MCP calls through mini.

## Bundled projections

mini ships with projection configs for the most popular MCP servers. They're installed automatically when you run `mini add` or `mini init` and a known server is detected.

| Server | Bundled | Notes |
|--------|---------|-------|
| GitHub | ✓ | PRs, issues, commits, file contents, code search |
| Linear | ✓ | Issues, projects, teams |
| Sentry | ✓ | Issues, events, stacktraces |
| Slack | ✓ | Channel history, messages |
| Jira | ✓ | Issues, sprints |

## Configuration

Config lives in `~/.mini/` by default (override with `--config DIR`):

```
~/.mini/
  config.yaml              # global settings
  servers/<name>.yaml      # one file per upstream server
  projections/<name>.yaml  # projection rules (tool → field config)
  responses/               # trimmed response files (TTL-managed)
```

**Adding a server:**

```bash
mini add github --url https://api.githubcopilot.com/mcp --header "Authorization=Bearer $GITHUB_TOKEN"
mini add linear --url https://mcp.linear.app/mcp
```

**Projection rules** control what gets kept per tool. Example for `list_pull_requests`:

```yaml
list_pull_requests:
  include: [number, title, state, draft, user, created_at, updated_at, assignees, head, base]
  string_limits:
    body: 300
  array_limits:
    default: 20
    assignees: 3
  depth_limit: 2
```

**Permission tiers** let you gate write operations behind `perm_call`:

```yaml
# servers/github.yaml
permissions:
  protected: [create_pull_request, merge_pull_request, delete_file]
```

Agents must use `perm_call` for protected tools — `call` returns a clear error explaining why.

## Default behavior with no projection config

mini still applies global defaults (strings capped at 1,000 chars, arrays at 3 items, depth at 3 levels) but does no per-tool field selection. You'll get some trimming but not the large reductions that come from `include` lists and targeted `exclude_always` rules.

## Commands

```
mini serve            Start the MCP proxy (default, uses daemon if running)
mini daemon           Run as a shared background daemon
mini daemon status    Check daemon health
mini ls               List configured servers
mini add NAME         Add a server
mini rm NAME          Remove a server
mini status           Show server health and tool counts
mini test             CI-safe health check (exits 1 on any failure)
mini auth NAME        OAuth2 PKCE flow for a server
mini init             Interactive setup wizard
mini cleanup          Delete expired response files
```
