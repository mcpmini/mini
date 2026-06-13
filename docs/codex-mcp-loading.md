# How Codex loads MCP tools

## Overview

Codex uses a **threshold-based strategy** controlled by a single constant:

- **Under 100 tools** (or `search_tool` disabled): all schemas sent eagerly upfront, no
  deferred loading
- **100+ tools** with `search_tool` enabled: a small pinned set is sent eagerly; everything
  else is deferred and discoverable only via a client-side BM25 `tool_search` tool

---

## Startup: tool list fetch

Codex calls `tools/list` for every configured MCP server at init with pagination, collecting
all schemas into a `HashMap<String, ToolInfo>` keyed by qualified name
(`mcp__<server>__<tool>`). Names exceeding length limits are truncated and suffixed with a
SHA1 hash for uniqueness.

---

## Threshold split

[`mcp_tool_exposure.rs`](https://github.com/openai/codex/blob/d34bc6646/codex-rs/core/src/mcp_tool_exposure.rs)

```rust
pub(crate) const DIRECT_MCP_TOOL_EXPOSURE_THRESHOLD: usize = 100;

pub(crate) fn build_mcp_tool_exposure(...) -> McpToolExposure {
    // collect all non-codex-apps tools (third-party MCP servers)
    let mut deferred_tools = filter_non_codex_apps_mcp_tools_only(all_mcp_tools);
    // also add any codex-apps connector tools
    if let Some(connectors) = connectors {
        deferred_tools.extend(filter_codex_apps_mcp_tools(...));
    }

    // below threshold or search disabled → everything is direct
    if !tools_config.search_tool || deferred_tools.len() < DIRECT_MCP_TOOL_EXPOSURE_THRESHOLD {
        return McpToolExposure { direct_tools: deferred_tools, deferred_tools: None };
    }

    // above threshold → only explicitly enabled connectors are direct
    let direct_tools = filter_codex_apps_mcp_tools(all_mcp_tools, explicitly_enabled_connectors, config);
    McpToolExposure { direct_tools, deferred_tools: Some(deferred_tools) }
}
```

Key detail: the `explicitly_enabled_connectors` for the direct set are Codex's own
first-party connectors ("codex apps"), not arbitrary third-party MCP servers. In practice,
most third-party MCP server tools end up in the deferred bucket when the threshold is hit.

---

## Wiring into the API call

[`tool_registry_plan.rs`](https://github.com/openai/codex/blob/d34bc6646/codex-rs/tools/src/tool_registry_plan.rs)

**Direct tools** (lines 473–492) — converted with `mcp_tool_to_responses_api_tool()`, pushed
as full tool specs (no `defer_loading`), available to the model immediately:

```rust
match mcp_tool_to_responses_api_tool(name.clone(), tool) {
    Ok(converted_tool) => {
        plan.push_spec(ToolSpec::Function(converted_tool), ...);
        plan.register_handler(name, ToolHandlerKind::Mcp);
    }
}
```

**Deferred tools** (lines 251–280) — NOT pushed as specs. Instead, a `tool_search` spec is
added, and each deferred tool is registered as a handler (so it can be called once
discovered):

```rust
if config.search_tool && let Some(deferred_mcp_tools) = params.deferred_mcp_tools {
    plan.push_spec(create_tool_search_tool(&search_source_infos, ...), ...);
    plan.register_handler(TOOL_SEARCH_TOOL_NAME, ToolHandlerKind::ToolSearch);

    for tool in deferred_mcp_tools {
        // handler registered but NO spec pushed — tool invisible to model until searched
        plan.register_handler(
            ToolName::namespaced(tool.tool_namespace, tool.tool_name),
            ToolHandlerKind::Mcp,
        );
    }
}
```

---

## Tool search: client-side BM25

[`tool_search.rs`](https://github.com/openai/codex/blob/d34bc6646/codex-rs/core/src/tools/handlers/tool_search.rs)
·
[`tool_discovery.rs`](https://github.com/openai/codex/blob/d34bc6646/codex-rs/tools/src/tool_discovery.rs)

At startup a `SearchEngine<usize>` (BM25, English) is built over all deferred tools. Each
tool's corpus document is:

```
qualified_name + callable_name + server_name + title + description +
connector_name + connector_description + plugin_display_names +
input_schema property names
```

When the model calls `tool_search`:

1. BM25 runs in-process (no round-trip, no API call)
2. Results are grouped by server namespace
3. Each matched tool is returned via `mcp_tool_to_deferred_responses_api_tool()` which sets
   `defer_loading: Some(true)` — the OpenAI Responses API then expands those schemas inline
   for the next model turn, making the tools callable

Default result limit: 8. The `computer-use` server gets a special carve-out (limit 20,
and if it appears in results the search re-runs at the higher limit).

---

## Tool name format

- Third-party MCP servers: `mcp__<server_name>__<tool_name>` (double-underscore)
- `codex_apps` server (first-party connectors): bare tool names, no `mcp__` prefix
- Names sanitized to `^[a-zA-Z0-9_-]+$`; over-length names truncated + SHA1 suffixed

---

## Context window impact

| Setup | Upfront schema cost | Per-search cost |
|---|---|---|
| < 100 tools | Full schemas for all tools | n/a |
| ≥ 100 tools, search enabled | Full schemas for pinned connectors only; deferred tools: names announced, schemas withheld | ~100 tokens (query) + ~200–2000 per schema expanded on discovery |

Prompt cache: the initial tool list is stable and cacheable per session, but adding or
removing a server busts the cache.

**What this does not fix**: MCP tool *responses*. Large upstream API payloads (lists of
issues, PR diffs, etc.) arrive in context unmodified regardless of whether schemas were
deferred.

---

## Comparison with Claude Code

| Aspect | Codex | Claude Code |
|---|---|---|
| Default schema loading | Eager (< 100 tools) / threshold-deferred (≥ 100) | Always deferred |
| Upfront schema cost | Zero to full depending on count | Near-zero (names/descriptions only) |
| Search mechanism | Client-side BM25 in Rust | Client-side keyword/select in TypeScript |
| Search result format | `defer_loading: true` refs (OpenAI Responses API) | `tool_reference` blocks (Anthropic API) |
| Deferred tools visible upfront | No — not in spec list at all | Yes — names announced, schemas withheld |
| Prompt cache | Busted by tool set changes | Preserved — deferred tools outside system prompt prefix |
| Response trimming | None | None |

---

## Which mini mode

`mini connect` (passthrough) is the default and works everywhere. Codex defers schemas once
it crosses 100 tools, so passthrough costs nothing upfront at scale. Below that threshold
(or with `search_tool` disabled) Codex sends every schema eagerly — if you have a large
catalog of servers and want a constant upfront cost regardless of count, use
`mini connect --tool-mode compact` to collapse them behind the four meta-tools.
