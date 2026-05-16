# How Claude Code loads MCP tools

## The short version

Claude Code never sends MCP tool schemas to the API upfront. Instead it sends only tool
*names*, and the model fetches full schemas on demand via a built-in ToolSearch tool.
This means mini's **proxy mode** is the right choice for Claude Code: upstream tools are
exposed directly, Claude defers their schemas through its normal mechanism, and mini trims
responses invisibly.

---

## Two categories of tools

**Local tools** (Read, Edit, Grep, Bash, etc.) are built into Claude Code. Their full
schemas are sent on every API call and the model can call them immediately.

**MCP tools** (`mcp__github__list_pull_requests`, etc.) are never sent to the API until
the model explicitly discovers them. On the first turn, the API call contains only local
tools — no MCP tools at all. The model knows their names (announced in the system context)
but cannot call them yet.

---

## Tool discovery flow

**Turn 1** — model sees local tools + a ToolSearch tool. No MCP schemas present.

```
Client → API:  tools = [Read, Edit, Grep, Bash, ..., ToolSearch]
               (zero MCP tools)
```

**Model calls ToolSearch** (`query: "github list pull requests"`). Claude Code runs
keyword matching locally in TypeScript — no API round-trip. It returns `tool_reference`
blocks, not inline schemas:

```json
{
  "type": "tool_result",
  "content": [{ "type": "tool_reference", "tool_name": "mcp__github__list_pull_requests" }]
}
```

**Turn 2** — Claude Code scans message history for `tool_reference` blocks, finds the
discovered tool, and now includes it in the API call with `defer_loading: true`:

```
Client → API:  tools = [Read, Edit, ..., ToolSearch,
                        mcp__github__list_pull_requests (defer_loading: true)]
```

The Anthropic API sees the `tool_reference` in the message history, expands it with the
full schema at that position, and the model can call the tool.

**Turn 3+** — the tool stays in the tools array for the rest of the session.

---

## What `defer_loading: true` means

The client sends the full JSON schema to the API alongside `defer_loading: true`. The API
receives it but does **not** inject it into the model's base context. The schema only
appears in context at the exact `tool_reference` position in the message history. This
keeps the prompt cache stable and prevents paying for schemas the model never ends up using.

---

## Why this matters for mini

**Standard mode** (`mini serve`, 4-tool interface):

The model calls `mini.list` to discover tools, then `mini.call` for every invocation —
two round-trips per upstream call. Upstream tool names come back as text inside a tool
result message, never entering the API's deferred tool mechanism. Schemas and responses
both land in conversation messages.

**Proxy mode** (`mini proxy`):

mini exposes upstream tools directly (`github__list_pull_requests`, `sentry__list_issues`,
etc.). Claude Code's deferred loading works exactly as designed: schemas defer through the
`defer_loading` + `tool_reference` mechanism, one round-trip per call, responses still
trimmed by mini's projections. The model doesn't know mini is there.

For Claude Code, **proxy mode is strictly better**: correct schema deferral, half the
round-trips, same response trimming.

---

## Tool search modes

Controlled by `ENABLE_TOOL_SEARCH` (default: always defer all MCP tools):

| Value | Behavior |
|---|---|
| unset / `true` | Always defer — all MCP tools discovered via ToolSearch |
| `auto` / `auto:N` | Defer only when schemas would exceed N% of context window (default 10%) |
| `false` | Disabled — all schemas sent upfront (standard mode) |

Only supported on Claude Sonnet 4+ and Opus 4+. Haiku falls back to sending all schemas
upfront automatically.
