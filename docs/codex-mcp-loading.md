# How Codex loads MCP tools

> For the protocol-level lifecycle and how mini sits between the agent and upstreams, see
> [mcp-lifecycle.md](mcp-lifecycle.md). This doc covers Codex's tool-exposure strategy and
> connection behavior.

## Overview

Codex defers **every** MCP tool behind a client-side `tool_search` tool whenever the model
supports tool search, which recent frontier models do. No tool schema is sent upfront,
and there is no tool-count threshold. MCP tools are sent eagerly only for models that cannot
search.

This has been the default since **Codex 0.142.2 (2026-06-24)**:

| Date | Change |
|---|---|
| 2026-04-17 | [openai/codex#18097](https://github.com/openai/codex/pull/18097): opt-in `tool_search_always_defer_mcp_tools` flag defers all MCP tools; default stays "defer once there are 100+ tools" (first in 0.122.0) |
| 2026-05-18 | [openai/codex#23390](https://github.com/openai/codex/pull/23390): removes the carve-out that kept explicitly mentioned app tools direct |
| 2026-06-22 | [openai/codex#29486](https://github.com/openai/codex/pull/29486): always defer when the model supports search; the 100-tool threshold and the flag are removed (first in 0.142.2) |

Earlier versions send every schema eagerly below 100 MCP tools and defer only above that.

---

## Startup: tool list fetch

Codex calls `tools/list` for every configured MCP server at init with pagination, collecting
all schemas into a `HashMap<String, ToolInfo>` keyed by qualified name
(`mcp__<server>__<tool>`). Names exceeding length limits are truncated and suffixed with a
SHA1 hash for uniqueness.

---

## Exposure decision

[`spec_plan.rs`](https://github.com/openai/codex/blob/61f34c35af/codex-rs/core/src/tools/spec_plan.rs#L151-L158) passes the model's
`supports_search_tool` into
[`mcp_tool_exposure.rs`](https://github.com/openai/codex/blob/61f34c35af/codex-rs/core/src/mcp_tool_exposure.rs#L158-L162), which picks one
exposure for all MCP tools:

```rust
let exposure = if search_tool_enabled {
    ToolExposure::Deferred
} else {
    ToolExposure::Direct
};
```

`supports_search_tool` is `true` for every model in the bundled
[`models.json`](https://github.com/openai/codex/blob/61f34c35af/codex-rs/models-manager/models.json#L160). The old `search_tool` feature
flag is marked
[`Stage::Removed`](https://github.com/openai/codex/blob/61f34c35af/codex-rs/features/src/lib.rs#L1303-L1308), so users cannot turn search off.
The models an account can use depend on its plan and may differ from this bundled list.

Exceptions:

- Tools from agent plugins that exceed a per-tool or total schema byte budget are hidden
  rather than deferred.
- `[code_mode] direct_only_tool_namespaces` in Codex config forces the listed namespaces to
  be exposed directly.

---

## Tool search: client-side BM25

[`tool_search.rs`](https://github.com/openai/codex/blob/61f34c35af/codex-rs/core/src/tools/handlers/tool_search.rs)
·
[`tool_search_spec.rs`](https://github.com/openai/codex/blob/61f34c35af/codex-rs/core/src/tools/handlers/tool_search_spec.rs)

Deferred tools are registered as callable handlers but not sent as tool specs. The model
instead gets one `tool_search` tool whose description lists the searchable sources (server
names and descriptions), not individual tools.

When the model calls `tool_search`:

1. BM25 runs in-process (no round trip, no API call) over each tool's name, description and
   schema text
2. Matching tools are returned with `defer_loading: true`; the OpenAI Responses API expands
   those schemas for the next model turn, making the tools callable

Default result limit: [8](https://github.com/openai/codex/blob/61f34c35af/codex-rs/tools/src/tool_discovery.rs#L7).

---

## Tool name format

- Third-party MCP servers: `mcp__<server_name>__<tool_name>` (double-underscore)
- `codex_apps` server (first-party connectors): bare tool names, no `mcp__` prefix
- Names sanitized to `^[a-zA-Z0-9_-]+$`; over-length names truncated + SHA1 suffixed

---

## Context window impact

| Setup | Upfront schema cost | Per-search cost |
|---|---|---|
| Model supports search (recent frontier models) | `tool_search` spec plus one line per server; no tool names or schemas | ~100 tokens (query) + ~200–2000 per schema expanded on discovery |
| Model without search, or Codex before 0.142.2 below 100 tools | Full schemas for all tools | n/a |

Prompt cache: the initial tool list is stable and cacheable per session, but adding or
removing a server changes the `tool_search` source list and busts the cache.

**What this does not fix**: MCP tool *responses*. Large upstream API payloads (lists of
issues, PR diffs, etc.) arrive in context unmodified regardless of whether schemas were
deferred.

---

## Comparison with Claude Code

| Aspect | Codex | Claude Code |
|---|---|---|
| Default schema loading | Always deferred (since 0.142.2) | Always deferred |
| Upfront schema cost | Near-zero (server names/descriptions only) | Near-zero (names/descriptions only) |
| Search mechanism | Client-side BM25 in Rust | Client-side keyword/select matching |
| Search result format | `defer_loading: true` refs (OpenAI Responses API) | `tool_reference` blocks (Anthropic API) |
| Deferred tools visible upfront | No — only their servers are listed | Yes — names announced, schemas withheld |
| Prompt cache | Busted by tool set changes | Preserved — deferred tools outside system prompt prefix |
| Response trimming | None | None |

---

## Connection lifecycle, parallelism & timeouts

Before any tool schema exists, Codex connects to each configured server. This happens in
[`McpConnectionManager::new`](https://github.com/openai/codex/blob/87b808bb570f01f4b6fc8485c5459052fac0e320/codex-rs/codex-mcp/src/connection_manager.rs#L213).

**All servers connect in parallel, unthrottled.** One tokio task is spawned per enabled server
into a `JoinSet`
([line 302](https://github.com/openai/codex/blob/87b808bb570f01f4b6fc8485c5459052fac0e320/codex-rs/codex-mcp/src/connection_manager.rs#L302));
no concurrency cap. Each task emits `Starting` before it begins and `Ready` / `Failed` /
`Cancelled` when it finishes — that drives the TUI status lines — and a detached task joins all
outcomes into one `McpStartupComplete` summary.

**Per server, the sequence is spawn → initialize → `tools/list`.** For stdio that's exactly one
child process (rmcp's `TokioChildProcess`), then an `initialize` handshake (protocol
`2025-06-18`) followed by the startup `tools/list`, all wrapped in a `Shared` future so
concurrent awaiters reuse one init.

**Timeouts:** per-server startup budget is
[`DEFAULT_STARTUP_TIMEOUT = 30s`](https://github.com/openai/codex/blob/87b808bb570f01f4b6fc8485c5459052fac0e320/codex-rs/codex-mcp/src/rmcp_client.rs#L76),
overridable via `startup_timeout_sec`, wrapping the whole startup path. Runtime tool calls get a
separate `DEFAULT_TOOL_TIMEOUT = 120s`.

**Failure isolation:** a server that times out or errors is marked `Failed` and startup
continues — the join only produces a summary, never aborts. The one hard-bail case is *required*
servers: [`session.rs`](https://github.com/openai/codex/blob/87b808bb570f01f4b6fc8485c5459052fac0e320/codex-rs/core/src/session/session.rs#L1208)
`anyhow::bail!`s session init if a required server failed; non-required servers can never sink
the boot.

**No health checks.** Codex does not ping live connections; a dead server is detected reactively
when its transport stream closes or the child exits.

---

## Which mini mode

`mini connect` (proxy) is the default and works everywhere. Codex 0.142.2 and later defers
every MCP schema, so proxy costs nothing upfront at any catalog size, and compact mode saves
no schema tokens. Compact mode (`mini connect --tool-mode compact`) only helps older Codex
versions, which send every schema eagerly below 100 tools.
