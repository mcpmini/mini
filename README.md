# mini

**mini** is an MCP proxy that sits between your AI agent and the tools it calls.

MCP servers are verbose — a GitHub `list_pull_requests` returns PR bodies, avatar URLs, node IDs, assignee objects, merge metadata, and dozens of URL template fields. Most of it your agent never reads. mini strips the noise so only what matters reaches context, saving tokens on every tool call.

> **New to MCP?** [Model Context Protocol](https://modelcontextprotocol.io) is how AI agents connect to external tools. mini sits in front of all of them.

## Install

```bash
go install github.com/mcpmini/mini/cmd/mini@latest
mini init    # imports your existing MCP configs, installs bundled projections
mini status  # verify all servers connected
```

`mini init` detects your existing Claude Desktop, Claude Code, Cursor, Codex, and Gemini configs and imports them automatically. Bundled projection configs for GitHub, Linear, Sentry, Slack, and Jira install alongside.

## What it does

**Before** — one PR from the GitHub MCP:

```json
{
  "number": 275198,
  "title": "Remove layout control toggles",
  "body": "Removes layout toggle buttons...\n\n[4,800 more chars]",
  "user": { "login": "Copilot", "avatar_url": "https://avatars.githubusercontent.com/...", "id": 198982749, "node_id": "U_...", "gravatar_id": "", "url": "https://api.github.com/users/...", ... },
  "assignees": [{ "login": "dev-user", "avatar_url": "...", "node_id": "...", ... }],
  "head": { "ref": "fix/toggle", "sha": "73e46e32...", "repo": { "full_name": "microsoft/vscode", "node_id": "...", ... } },
  "labels_url": "https://api.github.com/...",
  "commits_url": "https://api.github.com/...",
  ...40 more fields
}
```

**After** — same PR, through mini:

```json
{
  "number": 275198,
  "title": "Remove layout control toggles",
  "state": "open",
  "draft": true,
  "body": "Removes layout toggle buttons...[first 1500 chars]",
  "user": { "login": "Copilot", "profile_url": "https://github.com/Copilot" },
  "html_url": "https://github.com/microsoft/vscode/pull/275198",
  "created_at": "2025-11-04T17:25:38Z",
  "updated_at": "2025-11-04T18:51:15Z"
}
```

Avatar URLs gone. Node IDs gone. URL templates gone. Body capped at 1500 chars. Multiply across 20 PRs — the savings are significant.

Mini has three output modes. The same `list_pull_requests` call:

**Default** — projected JSON. Noisy fields stripped, strings capped, structure preserved. What agents receive in standard mode:
```bash
mini call github list_pull_requests '{"owner":"golang","repo":"go","perPage":2}'
```
```json
[
  { "number": 68851, "title": "net/http: fix connection reuse after timeout",
    "state": "open", "user": { "login": "gopherbot" }, "created_at": "2024-03-15T10:22:33Z" },
  { "number": 68849, "title": "cmd/go: add workspace vendor support",
    "state": "open", "user": { "login": "mvdan" }, "created_at": "2024-03-14T14:11:09Z" }
]
```

**Mini** (`-m` / `response_format: mini`) — field names hoisted to a single header row; values follow one per line. Most token-efficient for long lists since field names aren't repeated per item. Good for agents that handle plain text well, or when you want to squeeze more results inline:
```bash
mini call -m github list_pull_requests '{"owner":"golang","repo":"go","perPage":2}'
```
```
number title state user_login created_at
68851 net/http: fix connection reuse after timeout open gopherbot 2024-03-15T10:22:33Z
68849 cmd/go: add workspace vendor support open mvdan 2024-03-14T14:11:09Z
```

**Raw** (`-r`) — full upstream response, no projection. Use this to inspect what a server actually returns, debug a projection config, or check which fields are available:
```bash
mini call -r github list_pull_requests '{"owner":"golang","repo":"go","perPage":2}'
```
```json
[{ "number": 68851, "node_id": "PR_kwDOAGrz984...", "title": "net/http: fix...",
   "user": { "login": "gopherbot", "id": 8566187, "avatar_url": "https://avatars...",
             "gravatar_id": "", "url": "https://api.github.com/users/gopherbot", ... },
   "labels_url": "https://api.github.com/repos/golang/go/issues/68851/labels{/name}",
   "commits_url": "https://api.github.com/repos/golang/go/pulls/68851/commits",
   ... 40 more fields }]
```

## How it works

Mini is a local process that runs on your machine and sits between your agent and your MCP servers:

```
agent  ──MCP──▶  mini  ──MCP──▶  GitHub MCP
                              ──MCP──▶  Linear MCP
                              ──MCP──▶  Sentry MCP
```

When your agent calls a tool, mini: resolves which upstream server owns it, forwards the call, applies your projection config to the response (trimming fields, capping strings, stripping noise), then returns the result — either inline or as a file path if the response is large. The agent never connects to upstream servers directly.

**Serve mode** (`mini serve`) — mini acts as an MCP server itself, exposing 4 fixed tools (`list`, `call`, `perm_call`, `config`). The agent discovers tools via `list` and calls them via `call`.

**Proxy mode** (`mini proxy`) — mini re-exposes upstream tools directly under namespaced names (`github__list_pull_requests`, etc.). The agent sees them as ordinary tools with their native schemas. This is how Claude Code connects.

**Daemon mode** — instead of each agent session spawning its own mini process, a single shared daemon holds the upstream connections. Sessions route through it automatically. Connections stay warm across agent restarts.

## Connect to your agent

### Claude Code

```bash
claude mcp add mini mini proxy
```

Mini runs in **proxy mode** for Claude Code, exposing your upstream tools directly as `github__list_pull_requests`, `sentry__list_issues`, etc. Claude Code's schema deferral works as normal — it only loads schemas for tools it actually needs, keeping context tight. Responses are trimmed transparently; mini is invisible to the agent.

Claude Code has specific behaviour around how it loads and caches MCP tool schemas that affects how you should configure any MCP proxy. [Read the full explanation](docs/claude-code-mcp-loading.md) if you run into tools that aren't appearing or schemas that look stale.

### Codex

Codex loads all MCP tool schemas upfront at session start, which means the number of tools exposed directly affects your token budget before any work begins. Mini's standard mode exposes exactly 4 tools regardless of how many upstream servers you have — `list`, `call`, `perm_call`, `config` — keeping that fixed cost predictable.

Add to `~/.codex/config.toml`:

```toml
[mcp_servers.mini]
command = "mini"
```

See [how Codex loads MCP tools](docs/codex-mcp-loading.md) for more detail on schema loading behaviour and when proxy mode might be preferable.

### Cursor, Windsurf, and others

```json
{
  "mcpServers": {
    "mini": { "command": "mini" }
  }
}
```

Standard mode exposes the same 4-tool surface:

| Tool | What it does |
|---|---|
| `list` | Discover all tools across connected servers |
| `call` | Invoke a tool — response is projected and returned |
| `perm_call` | Same as `call` but for protected tools (write ops, destructive actions) |
| `config` | Runtime admin: add/remove servers, adjust projections, check status |

**Cursor only supports server-level approval** — use `hidden` for tools that must never run without human review.

### Daemon mode — multiple agents sharing one connection

If you run multiple agent sessions simultaneously (several Claude Code windows, Claude Code + Cursor, etc.), each normally spawns its own mini process and its own upstream connections. The daemon avoids that:

```bash
mini daemon          # start once, runs in the background
mini daemon status   # confirm it's running
```

Once running, any `mini serve` or `mini proxy` invocation automatically detects the daemon and routes through it. Upstream connections are shared across all sessions; projections and permissions remain per-session. The daemon binds to `127.0.0.1` only and survives agent restarts.

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

Bundled projection and permission configs for known servers install automatically.

### Bundled server configs

These servers have projection and permission defaults built in — they're installed automatically when `mini add` or `mini init` detects a matching server name.

| Server | Projection config | Tools covered |
|---|---|---|
| GitHub | [github.yaml](internal/defaults/projections/github.yaml) | list_pull_requests, list_issues, get_issue, get_pull_request, list_commits, get_commit, search_code, search_repositories, search_issues, get_file_contents, list_repository_contents, list_pull_request_files |
| Slack | [slack.yaml](internal/defaults/projections/slack.yaml) | conversations_history, conversations_replies, conversations_list, search_messages, users_list |
| Linear | [linear.yaml](internal/defaults/projections/linear.yaml) | list_issues, search_issues, get_issue, create_issue, update_issue, list_projects, list_teams, list_cycles, list_comments |
| Sentry | [sentry.yaml](internal/defaults/projections/sentry.yaml) | list_issues, get_issue_details, list_events, list_projects, list_organizations |
| Atlassian | [atlassian.yaml](internal/defaults/projections/atlassian.yaml) | Jira: search, get_issue, get_project_issues, get_all_projects, get_project, get_agile_boards, get_sprint_issues — Confluence: search, get_page, get_page_children, get_comments |

For servers not in this list, mini is a transparent proxy — responses pass through unchanged until you add a projection config.

## Projection config

Projections are the rules that control what mini keeps, limits, or removes from responses. They live in `~/.mini/projections/<server>.yaml` and are installed automatically for known servers by `mini init`.

For most users the bundled projections are enough. If you want to tune them:

```yaml
# ~/.mini/projections/github.yaml

list_pull_requests:
  exclude_always: [avatar_url]   # strip provably-useless fields
  string_limits:
    body: 1500                   # cap at 1500 chars in list view

get_pull_request:
  string_limits:
    body: 8000                   # generous limit for detail view
```

The bar for exclusion is high — only strip fields that are **never** useful in any realistic agent workflow (URL template strings, image URLs, deprecated empty fields). When in doubt, keep the field. See [docs/default-config-philosophy.md](docs/default-config-philosophy.md) for full guidance.

Config directory layout:

```
~/.mini/
  config.yaml              # global settings (see below)
  servers/<name>.yaml      # one file per upstream server
  projections/<name>.yaml  # per-tool field rules
  responses/               # auto-managed response files
  tokens/                  # OAuth token cache
```

### Global config

`~/.mini/config.yaml` controls mini's overall behavior:

```yaml
log_level: info       # debug | info | warn | error
response_format: json # json (default) | mini (see below)
```

**`response_format: mini`** switches all agent responses to the compact header:values format shown above — useful if your agent handles plain text better than structured data, or if you want to squeeze more responses inline. It has no effect on responses that are too large to inline (those go to file regardless).

There is no global string truncation by default. Truncation only applies when a projection config is present — either the bundled ones installed by `mini init`, or ones you write yourself.

### Large responses

When mini has projected a response and it is still large, it writes the response to `~/.mini/responses/` and returns a file path instead. The agent fetches it with `read` (proxy mode) or `config action:read` (standard mode).

**This only happens when a projection config is active.** For the bundled servers (GitHub, Slack, Linear, Sentry, Jira), `mini init` installs projections automatically so trimming and file handling work out of the box. For servers you add that aren't in the bundled set, responses pass through unchanged until you write a projection config — mini is a transparent proxy for anything it has no rules for.

**What the agent receives inline vs from a file:**

- **Inline** — the projected JSON, same structure as the upstream response but with excluded fields and string limits applied
- **File** — a more compact form: nested objects flattened (`user.login` → `user_login`), URL-template fields stripped, a `_meta` block added with a field list and index for quick scanning. A `.raw.json` file alongside always has the full original upstream response.

**When does a response go to file?** By default, when it's larger than a typical list of 5–10 items. A list of 5 pull requests stays inline; a large code file or a 50-item search result goes to disk.

Tune this with `inline_threshold` in `config.yaml`:

- **Raise it** if agents are fetching response files too often (fewer round trips, more context used)
- **Lower it** to keep context tighter (more round trips, smaller agent context)

Response files are cleaned up automatically by TTL and disk budget.

## Permissions

Gate write operations behind `perm_call` so agents have to ask before making changes:

```yaml
# ~/.mini/servers/github.yaml
permissions:
  protected: [create_pull_request, merge_pull_request, delete_file]
  hidden: [get_authenticated_app, list_app_installations]
```

Three tiers:

| Tier | Visible in `list` | Callable via |
|---|---|---|
| `open` (default) | Yes | `call` or `perm_call` |
| `protected` | Yes | `perm_call` only |
| `hidden` | No | `perm_call` only |

In Claude Code: allowlist `mcp__mini__call` and leave `mcp__mini__perm_call` requiring approval — Claude will prompt before calling protected tools. Codex supports the same via `approval_mode`. **Cursor only supports server-level approval**, so use `hidden` for tools that must never run without human review.

## Auth

For servers that require OAuth2 (Linear, Slack):

```bash
mini auth linear   # opens browser for PKCE flow, token stored in ~/.mini/tokens/
```

For servers using API keys or Bearer tokens, set them in the server config or reference an env var:

```yaml
# ~/.mini/servers/github.yaml
auth:
  type: bearer
  token: "${GITHUB_TOKEN}"
```

## Using mini from the CLI

You don't have to connect mini to an agent via MCP. `mini call` works as a standalone command — pipe it from scripts, use it in CI, or have your agent invoke it as a subprocess rather than connecting via MCP at all:

```bash
mini call github list_pull_requests '{"owner":"golang","repo":"go","perPage":3}'
mini call -m github list_issues '{"owner":"golang","repo":"go","state":"open","perPage":10}'
mini call -r github get_file_contents '{"owner":"golang","repo":"go","path":"README.md"}'
mini perm-call github create_pull_request '{"owner":"...","repo":"...","title":"..."}'
```

This is useful when:
- You want projection and auth handled for shell scripts or CI pipelines without an agent involved
- You're debugging what a tool actually returns before writing a projection config
- Your agent environment can run subprocesses but has limited MCP support

## Commands

```
mini serve [--http ADDR] [--standalone]   Standard mode (4-tool interface)
mini proxy [--http ADDR]                  Proxy mode (upstream tools exposed directly)
mini daemon [--port N]                    Run as a shared background daemon
mini daemon status                        Check whether the daemon is running

mini ls                                   List configured servers
mini add NAME [flags]                     Add a server
mini rm NAME                              Remove a server
mini status                               Server health and tool counts
mini test [--timeout T]                   CI health check (exits 1 on any failure)
mini auth NAME                            OAuth2 PKCE flow for a server
mini init [--yes]                         Setup wizard
mini cleanup                              Delete expired response files

mini call [-j|-m|-r] SERVER TOOL [JSON]   Invoke a tool directly
mini perm-call [-j|-m|-r] SERVER TOOL [JSON]  Invoke a protected tool directly
```
