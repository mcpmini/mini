# How Codex loads MCP tools

## Overview

Codex takes the opposite approach to Claude Code: **all MCP tools are loaded upfront at initialization** with full schemas sent to the model before any conversation begins. A built-in `tool_search` tool then lets the model surface relevant tools from the already-loaded catalog using fuzzy (BM25) search, with matched tools returned as lightweight deferred references for subsequent use.

---

## Startup: full upfront load

During initialization, Codex calls `tools/list` for every configured MCP server and fetches the complete tool list with pagination. All tools are converted into `ResponsesApiTool` objects for the OpenAI Responses API and added to the initial request with `defer_loading` unset — every schema is included in full.

The `codex_apps` server is a special case: its tool list is cached per-user to avoid re-fetching on every startup.

The `ResponsesApiTool` struct has a `defer_loading` field that serializes only when explicitly set to `true`. For the initial load, it remains `None` (absent), so every tool's full schema goes into context:

```rust
// Source: codex-rs/core/src/client_common.rs
pub struct ResponsesApiTool {
    pub(crate) name: String,
    pub(crate) description: String,
    pub(crate) strict: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) defer_loading: Option<bool>,
    pub(crate) parameters: JsonSchema,
}
```

→ [permalink](https://github.com/openai/codex/blob/d34bc6646/codex-rs/core/src/client_common.rs#L275-L287)

---

## Tool name format

MCP tools are presented to the model with a qualified name (`mcp_connection_manager.rs`):

- Standard format: `mcp__<server_name>__<tool_name>` (double-underscore delimiter)
- Exception: `codex_apps` server tools use bare names without the `mcp__` prefix
- Names are sanitized to `^[a-zA-Z0-9_-]+$`
- Names that exceed the length limit are truncated and suffixed with a SHA1 hash for uniqueness

→ [permalink](https://github.com/openai/codex/blob/d34bc6646/codex-rs/core/src/mcp_connection_manager.rs#L155-L199)

---

## Tool spec assembly

All MCP tools are added to the initial tool list using `mcp_tool_to_openai_tool()`, which sets `defer_loading: None`:

```rust
// Source: codex-rs/core/src/tools/spec.rs
if let Some(mcp_tools) = mcp_tools {
    for (name, tool) in entries {
        match mcp_tool_to_openai_tool(name.clone(), tool.clone()) {
            Ok(converted_tool) => {
                push_tool_spec(&mut builder, ToolSpec::Function(converted_tool), ...);
                builder.register_handler(name, mcp_handler.clone());
            }
        }
    }
}
```

→ [permalink](https://github.com/openai/codex/blob/d34bc6646/codex-rs/core/src/tools/spec.rs#L3042-L3062)

---

## Tool search: client-side BM25

Codex ships a built-in `tool_search` tool that implements client-side fuzzy matching over the pre-loaded catalog. When the model calls it:

1. The handler holds a `HashMap` of all pre-loaded MCP tools in memory
2. BM25 scoring runs against tool names, descriptions, and parameter names/descriptions
3. Results are grouped by namespace (server prefix) and returned to the model

The critical detail: results from `tool_search` are serialized using `mcp_tool_to_deferred_openai_tool()` — a separate converter that sets `defer_loading: Some(true)`:

```rust
// Source: codex-rs/core/src/tools/spec.rs
pub(crate) fn mcp_tool_to_deferred_openai_tool(
    name: String,
    tool: rmcp::model::Tool,
) -> Result<ResponsesApiTool, serde_json::Error> {
    Ok(ResponsesApiTool {
        defer_loading: Some(true),  // deferred reference
        parameters: input_schema,
        output_schema: None,        // no output schema for deferred refs
        ...
    })
}
```

→ [permalink](https://github.com/openai/codex/blob/d34bc6646/codex-rs/core/src/tools/spec.rs#L2334-L2364)

→ [tool_search handler](https://github.com/openai/codex/blob/d34bc6646/codex-rs/core/src/tools/handlers/tool_search.rs)

The OpenAI Responses API then expands these `defer_loading: true` references back into full schema definitions before the model uses the tool. The model gets a lightweight ref back from search, then the full schema is injected inline on use.

---

## Two-phase deferral

Codex's approach is two-phase:

1. **Phase 1 — full upfront load**: all schemas arrive in context at the start of every session
2. **Phase 2 — search returns deferred refs**: when the model calls `tool_search`, results come back as `defer_loading: true` references rather than re-emitting the full schema again

Phase 2 optimizes *subsequent turns* (the model doesn't repeat full schema text in its responses), but Phase 1 means the initial context cost is always the sum of all schemas. The catalog is always fully in context; search doesn't reduce it.

---

## Context window impact

With 200 MCP tools at typical schema sizes:

| | Cost |
|---|---|
| Upfront schema cost | ~40,000–80,000 tokens (all schemas, all servers) |
| Per-`tool_search` call | ~100–300 tokens (search query + deferred refs returned) |
| Prompt cache | Initial list is cacheable; any change to the tool set busts the cache |

For small tool sets (< 20 tools), the upfront load is negligible. For large multi-server setups, the initial context cost is the dominant expense, and `tool_search` does not reduce it — the model already has all schemas when it runs a search.

**What this optimizes**: accuracy and latency on individual tool calls. The model can inspect any schema without a round-trip because everything is already present. **What it does not fix**: the same as Claude Code — raw MCP responses arrive with no trimming or projection, so large API responses land in context in full.

---

## Comparison with Claude Code

| Aspect | Codex | Claude Code |
|---|---|---|
| Schema loading | All upfront at init | Deferred by default (names only in context) |
| Upfront schema cost | High — full schemas for all tools | Near-zero — names/descriptions only |
| Search mechanism | Client-side BM25 in Rust | Client-side keyword/select in TypeScript |
| Search result format | `defer_loading: true` references (OpenAI Responses API) | `tool_reference` blocks (Anthropic API) |
| Prompt cache | Cacheable but busted by tool set changes | Preserved — deferred tools outside system prompt prefix |
| Response trimming | None | None |
