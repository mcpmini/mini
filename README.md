# mini

**mini** is an MCP proxy that sits between your AI agent and the tools it calls.

MCP servers are verbose — a GitHub `list_pull_requests` returns PR bodies, avatar URLs, node IDs, assignee objects, merge metadata, and dozens of URL template fields. Most of it your agent never reads. mini strips the noise so only what matters reaches context, saving tokens on every tool call.

> **New to MCP?** [Model Context Protocol](https://modelcontextprotocol.io) is how AI agents connect to external tools. mini sits in front of all of them.

## What it does

**Before** — `list_pull_requests` on `golang/go`, raw (`mini call -r`). The `body` alone is ~6,800 characters of benchmark tables, and every PR repeats the same `head`/`base`/`user` sub-objects:

```json
{
  "data": [
    {
      "number": 79998,
      "title": "internal/bytealg: optimize memequal",
      "body": "Implement vectorization optimization for small size memory comparing.\n\ngoos: linux\ngoarch: riscv64\n[~6,800 chars of benchmark tables]",
      "state": "open",
      "draft": false,
      "merged": false,
      "html_url": "https://github.com/golang/go/pull/79998",
      "user": { "login": "lxq015", "id": 79146446, "profile_url": "https://github.com/lxq015", "avatar_url": "https://avatars.githubusercontent.com/u/79146446?v=4" },
      "head": { "ref": "optimize_memequal", "sha": "bf5f9a21...", "repo": { "full_name": "lxq015/go", "description": "The Go programming language" } },
      "base": { "ref": "master", "sha": "dfc01c32...", "repo": { "full_name": "golang/go", "description": "The Go programming language" } },
      "created_at": "2026-06-13T07:39:36Z",
      "updated_at": "2026-06-13T08:10:43Z"
    },
    { "...": "9 more PRs, same shape" }
  ]
}
```

**After** — the same list through a projection that keeps just the fields you scan in a list view (`include_only: [number, state, draft, title, html_url]`). Default JSON (`mini call -j`):

```json
{
  "data": [
    { "draft": false, "html_url": "https://github.com/golang/go/pull/79998", "number": 79998,
      "state": "open", "title": "internal/bytealg: optimize memequal" },
    { "draft": false, "html_url": "https://github.com/golang/go/pull/79997", "number": 79997,
      "state": "open", "title": "internal/bytealg: optimize indexbyte_riscv64.s" }
  ],
  "excluded": [".[].base", ".[].body", ".[].created_at", ".[].head", ".[].merged", ".[].updated_at", ".[].user"],
  "file": "1792044466301"
}
```

`excluded` lists what the projection removed, and `file` is the key for reading the full response back.

Or [TOON](https://github.com/toon-format/toon) (`mini call -t`): field names once on a header row, then one line per item. Most token-efficient for long lists:

```
data[2]{draft,html_url,number,state,title}:
  false,"https://github.com/golang/go/pull/79998",79998,open,"internal/bytealg: optimize memequal"
  false,"https://github.com/golang/go/pull/79997",79997,open,"internal/bytealg: optimize indexbyte_riscv64.s"
excluded[7]: ".[].base",".[].body",".[].created_at",".[].head",".[].merged",".[].updated_at",".[].user"
file: "1792044466301"
```

You control exactly which fields survive — see [Projection config](#projection-config). `mini call -r` always returns the untouched upstream response when you need it.

## Install

```bash
go install github.com/mcpmini/mini/cmd/mini@latest
```

## Connect to your agent

Every client connects mini the same way — by running `mini connect`. The quickest start is `mini init`:

```bash
mini init
```

It imports the MCP servers you already set up in Claude Code, Codex, Cursor and other agents, lets you pick more from the MCP catalog, logs you in to the ones that use OAuth, and connects mini to your agents, backing up each config it changes. To skip the questions, use `mini init --import` (every agent found), `mini init --from cursor` (one agent or config file), or `mini init --add linear,sentry` (catalog servers); these leave logins and agent configs alone and print what's left to do.

To register mini with a client yourself:

```bash
# Claude Code
claude mcp add mini -- mini connect
```

Any other client: point its MCP config at `mini connect`:

```json
{
  "mcpServers": {
    "mini": { "command": "mini", "args": ["connect"] }
  }
}
```

`mini connect` re-exposes each upstream tool under a namespaced name (`github__list_pull_requests`, `linear__list_issues`, etc.) and trims its response. mini isn't hidden — the tools are served by the `mini` MCP server, so your client lists them under `mini`, and the agent calls them through it.

## Adding servers

### Example: GitHub MCP

```bash
# Export a token first:
export GITHUB_TOKEN=...

# Add the server
mini add github \
  --url https://api.githubcopilot.com/mcp \
  --header "Authorization=Bearer $GITHUB_TOKEN"

# Check it connected
mini status

# Try a call
mini call github list_pull_requests '{"owner":"golang","repo":"go","perPage":5}'
```

mini recognizes GitHub's URL and installs its bundled projections automatically. Every tool is visible by default; use your MCP client's approval settings to control writes.

### Other servers

```bash
mini add linear --url https://mcp.linear.app/mcp   # OAuth: mini add opens the login
mini add sentry --url https://mcp.sentry.dev/mcp
mini add slack  --url https://mcp.slack.com/mcp
mini add atlassian --url https://mcp.atlassian.com/v2/mcp
```

To import every server from an existing agent config at once, use `mini init --from claude-code` (or `claude-desktop`, `codex`, `cursor`, `windsurf`, or a path to a config file).

Bundled projections install when you first add a matching server without your own projection rules. Existing server files keep their rules and URLs when mini is upgraded.

### Bundled server configs

These servers have bundled projections. `mini add` and `mini init` match the upstream's URL or command, rather than the name you give it.

| Server | Projection config | Tools covered |
|---|---|---|
| GitHub | [github.yaml](internal/defaults/projections/github.yaml) | list_pull_requests, list_issues, issue_read, pull_request_read, list_commits, get_commit, search_code, search_repositories, search_issues |
| Linear | [linear.yaml](internal/defaults/projections/linear.yaml) | list_issues, get_issue |
| Atlassian | [atlassian.yaml](internal/defaults/projections/atlassian.yaml) | searchJiraIssuesUsingJql, getJiraIssue, editJiraIssue |

Other servers, including Slack and Sentry, use the global string limit without bundled field rules. Add projections to tune individual tools, or set `default_string_limit: 0` to disable the global string limit.

## How it works

mini is a local process that runs on your machine and sits between your agent and your MCP servers. When your agent calls a tool, mini resolves which upstream server owns it, forwards the call, applies your projection config to the response (trimming fields, capping strings, stripping noise), then returns the result. The agent never connects to upstream servers directly.

```mermaid
sequenceDiagram
    participant Agent
    participant mini
    participant Upstream as GitHub MCP

    Agent->>mini: github__list_pull_requests
    mini->>Upstream: list_pull_requests
    Upstream-->>mini: full response (40+ fields, ~18 KB)
    mini->>mini: apply projection
    mini-->>Agent: trimmed response (8 fields, ~400 bytes)
```

mini re-exposes each upstream tool under a namespaced name (`github__list_pull_requests`, etc.) and serves it from its own MCP server, so in your client the tools appear under `mini` and every call goes through it. mini is the one resolving, forwarding, and trimming — the agent talks to mini, mini talks to the upstreams.

## Daemon mode

`mini connect` auto-detects a running daemon and routes through it, sharing one set of upstream connections across all agent sessions. If no daemon is running, `mini connect` starts one on demand. You usually do not need to manage it yourself; use `mini daemon status` to check whether it is running, or `mini daemon` to start it manually.

```mermaid
graph TB
    subgraph before["Without daemon: each chat owns upstream connections"]
        direction LR
        a1[Claude Code] --> g1[GitHub MCP]
        a1 --> l1[Linear MCP]
        a2[Codex] --> g2[GitHub MCP]
        a2 --> l2[Linear MCP]
        a3[Cursor] --> g3[GitHub MCP]
        a3 --> l3[Linear MCP]
    end

    subgraph after["With daemon: mini connect relays to one shared daemon"]
        direction LR
        b1[Claude Code] --> p1[mini connect]
        b2[Codex] --> p2[mini connect]
        b3[Cursor] --> p3[mini connect]
        p1 --> d[mini daemon]
        p2 --> d
        p3 --> d
        d --> bg[GitHub MCP]
        d --> bl[Linear MCP]
    end
```

## Projection config

Projections are the rules that control what mini keeps, limits, or removes from responses. They live under `projections:` in each server's `~/.mini/servers/<server>.yaml`, keyed by tool name (`"*"` applies to every tool), and are installed automatically for known servers by `mini add` and `mini init`.

For most users the bundled projections are enough. If you want to tune them:

```yaml
# ~/.mini/servers/github.yaml
projections:
  list_pull_requests:
    string_limits:
      body: 1500            # cap at 1500 chars in list view
  pull_request_read:
    exclude: [user.avatar_url]
    string_limits:
      body: 8000            # generous limit for detail view
```

The bar for exclusion is high: keep fields that could reasonably help during a typical month of development, including fork descriptions, identities, revision references and useful links. When in doubt, keep the field. See [docs/default-config-philosophy.md](docs/default-config-philosophy.md) for full guidance.

`exclude` matches exact dotted object paths relative to the response document root. `user.avatar_url` removes only that user's avatar; `description` removes only a root-level field. In proxy mode, each parsed JSON text block or structured-content value is its own document root. Exclusions do not traverse arrays: exclude the whole array by its property path if needed. Array indexes, wildcards and jq expressions are unsupported.

Config directory layout:

```
~/.mini/
  config.yaml              # global settings (see below)
  servers/<name>.yaml      # one file per upstream server, including its projections
  internal/                # machine-managed runtime state
```

### Global config

`~/.mini/config.yaml` controls mini's overall behavior:

```yaml
log_level: info       # debug | info | warn | error
response_format: json # json (default) | toon (see above)
```

**`response_format: toon`** switches projected responses to the TOON format shown above — useful if your agent handles plain text better than structured data.

By default, mini caps strings at 2000 chars to keep responses manageable. You can raise, lower, or disable this with `default_string_limit` in `~/.mini/config.yaml` (set to `0` to disable). Projection configs can override the limit per field with `string_limits`.

### Recovering filtered data

In the default proxy mode, the agent gets the upstream server's own result, trimmed in place: `structuredContent`, JSON and text blocks are projected, while images, other blocks and `_meta` pass through unchanged. With `response_format: toon`, JSON text blocks are re-encoded as TOON. Tools take the upstream's own arguments.

Embedded resource text is limited as text; JSON field rules do not parse objects inside that text.

When projection cuts something, the result carries a `__mini` notice, as a key in the result object or as a final text block:

```json
"__mini": {
  "msg": "Response filtered, some fields were excluded or truncated, use read(<file>, <jq filter>) to fetch full values.",
  "file": "1792180588821",
  "truncated": [{ "path": ".content[0].text | fromjson | .items[0].body", "chars": 444 }]
}
```

The full upstream result is stored in `~/.mini/internal/responses/`. The agent passes `file` to `read`, with a listed path (or any jq filter built on it) as `filter`, to recover just the value it needs. A call can also pass `__mini: {"projection": "raw"}` to get the whole result at once.

Results that projection doesn't change have no notice and no stored file. Response files are cleaned up automatically by TTL and disk budget.

## Auth

`mini init` and `mini add` log you in to servers that support browser login (Linear, Slack, Sentry and others). To log in again later:

```bash
mini auth linear   # opens browser to complete auth handshake
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
mini call -t github list_issues '{"owner":"golang","repo":"go","state":"open","perPage":10}'
mini call -r github get_file_contents '{"owner":"golang","repo":"go","path":"README.md"}'
mini perm-call github create_pull_request '{"owner":"...","repo":"...","title":"..."}'
```

This is useful when:
- You want projection and auth handled for shell scripts or CI pipelines without an agent involved
- You're debugging what a tool actually returns before writing a projection config
- Your agent environment can run subprocesses but has limited MCP support

## Compact tool mode

Compact mode (`mini connect --tool-mode compact`) exposes exactly 4 tools regardless of how many upstream servers you have. It works similarly to `mini call`/`mini perm-call` from the CLI: the agent invokes tools through mini rather than directly:

| Tool | What it does |
|---|---|
| `list` | Discover tools across connected servers |
| `call` | Invoke an `open` tool; returns error for `protected` tools |
| `perm_call` | Invoke a `protected` tool — configure your client to always ask before running this |
| `config` | Add/remove servers, adjust projections, check status |

The `open`/`protected` split only matters in compact mode and the `mini call` / `mini perm-call` CLI. In the default proxy mode, upstream tools are exposed directly and approval is handled by your MCP client, not by mini. Use your client's native per-tool approval settings for writes. `hidden` tools are filtered from normal discovery, but that is visibility control, not a security boundary.

Use compact mode when your client loads every MCP tool schema eagerly at session start and a large catalog of servers is costing you context on every turn. Clients that defer schemas, like Claude Code, get no benefit from compact mode and lose native tool schemas, so stick with the default there. [How Claude Code loads MCP schemas →](docs/claude-code-mcp-loading.md)

## Commands

```
mini connect [--http ADDR] [--standalone] [--tool-mode proxy|compact]   Connect an agent (stdio MCP)
mini daemon                               Run as a shared background daemon
mini daemon status                        Check whether the daemon is running

mini ls                                   List servers and check connections
mini add NAME (--url URL | -- CMD [ARGS...])  Add a server
mini rm NAME                              Remove a server
mini status                               Server health and tool counts
mini test [--timeout T]                   CI health check (exits 1 on any failure)
mini auth NAME                            OAuth2 PKCE flow for a server
mini init [--import | --from AGENT | --add NAMES]  Setup wizard
mini cleanup                              Delete expired response files

mini call [-j|-t|-r] SERVER TOOL [JSON]   Invoke a tool directly
mini perm-call [-j|-t|-r] SERVER TOOL [JSON]  Invoke a protected tool directly
```
