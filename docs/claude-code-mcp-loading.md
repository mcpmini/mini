# How Claude Code loads MCP tools

## Overview

Claude Code connects to MCP servers at startup, fetches their tool lists over the MCP `tools/list` RPC, and converts each tool into an Anthropic API tool definition. The defining design decision is **all MCP tools are deferred by default**: their full JSON schemas are withheld from the context window until the model explicitly searches for and needs them.

---

## What "deferred" means at the API level

The Anthropic API supports a `defer_loading: true` flag on tool definitions. When a tool is sent with this flag:

- The tool name and description travel in the API request as usual
- The `input_schema` (the full JSON schema) is **not** included in the context window
- The API holds the schema server-side and injects it inline only when the model discovers the tool through a search call

This is a first-party Anthropic API feature, enabled via beta headers (`advanced-tool-use-2025-11-20` for first-party integrations, `tool-search-tool-2025-10-19` for third-party MCP servers like mini).

Deferred tool definitions are also excluded from the **system prompt prefix**, which means they don't interfere with prompt caching. The cache is preserved across turns even as new tools are discovered.

---

## Which tools are deferred

By default, every MCP tool is deferred. A small set of built-in tools are always loaded immediately:

- The ToolSearch tool itself (used to discover everything else)
- The Brief tool
- The Agent tool (when a feature flag is enabled)
- SendUserFile (when the REPL bridge is active)

Two MCP `_meta` extensions let upstream servers influence this behavior:

- `_meta['anthropic/alwaysLoad']` — opts a specific tool out of deferral so it is always included with full schema
- `_meta['anthropic/searchHint']` — provides extra keywords to improve discoverability when the model searches

---

## How the model discovers deferred tools

Claude Code ships a built-in **ToolSearch tool** that the model calls when it needs to find something. It supports two query forms:

- **select:** — exact lookup by name: `select:Read,Edit,Grep`
- **keyword search** — fuzzy match over tool names, descriptions, and argument names/descriptions: `github list pull requests`

When the model calls ToolSearch, it returns `tool_reference` blocks — lightweight pointers to the deferred tools:

```json
{
  "type": "tool_result",
  "tool_use_id": "toolu_...",
  "content": [
    { "type": "tool_reference", "tool_name": "mcp__github__list_pull_requests" }
  ]
}
```

The Anthropic API intercepts these blocks and expands them inline into full `input_schema` definitions before the model sees the result. The model can then call the discovered tool immediately.

The full list of deferred tool names is announced to the model at the start of each conversation so it knows what is searchable.

The Anthropic API also provides two server-side built-in variants that work the same way without any client code:
- `tool_search_tool_regex_20251119` — Claude constructs Python `re.search()` patterns
- `tool_search_tool_bm25_20251119` — Claude writes natural language queries

Claude Code uses its own client-side implementation rather than these built-ins, which gives it more control over ranking and the select-by-name shortcut.

---

## Tool name format

MCP tools are presented to the model with a qualified name: `mcp__<server>__<tool>`. The double-underscore delimiter distinguishes them from built-in tools. Names are sanitized to `^[a-zA-Z0-9_-]+$`.

---

## Tool search modes

Three modes are available:

| Mode | Behavior |
|---|---|
| `tst` (default) | Always defer all MCP tools |
| `tst-auto` | Defer only when tool schemas would exceed a configurable percentage of the context window (default 10%) |
| `standard` | No deferral — all schemas sent inline |

The auto-threshold calculates deferred token cost using the API's token counter, falling back to a character-based heuristic if the counter is unavailable.

---

## Context window impact

A typical multi-server setup (GitHub, Slack, Sentry, Jira, Linear) with 200+ tools might have schemas totalling 30,000–60,000 tokens. With deferral:

| | Without deferral | With deferral |
|---|---|---|
| Upfront schema cost | 30,000–60,000 tokens | ~0 (names only, not in context prefix) |
| Per-search cost | n/a | ~100 tokens (search call) + ~200–2,000 tokens per schema expanded |
| Prompt cache | Busted by any schema change | Preserved (deferred tools outside prefix) |

For a 5-server setup, Anthropic's own numbers put the savings at over 85% on schema overhead, typically loading only the 3–5 tools the model actually needs per request.

**What deferral does not fix**: tool *response* content. A `list_pull_requests` call on a busy GitHub repo returns the full API JSON blob — tens of thousands of tokens — regardless of whether the schema was deferred. Deferral solves schema overhead; it has no effect on response size.

---

## Model compatibility

Deferred loading requires model support for `tool_reference` blocks:

- Supported: Claude Sonnet 4+, Opus 4+
- Not supported: Haiku models (by default; can be overridden)
