# How Claude Code loads MCP tools

> For the protocol-level lifecycle and how mini sits between the agent and upstreams, see
> [mcp-lifecycle.md](mcp-lifecycle.md). This doc covers Claude Code's tool-exposure strategy and
> connection behavior at a high level.

## The short version

Claude Code never sends MCP tool schemas to the API upfront. Instead it sends only tool
*names*, and the model fetches full schemas on demand via a built-in ToolSearch tool.
This means mini's default **proxy mode** is the right choice for Claude Code: upstream
tools are exposed directly, Claude defers their schemas through its normal mechanism, and
mini trims responses invisibly. Just run `mini connect`.

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
keyword matching locally on the client — no API round-trip. It returns `tool_reference`
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

**Proxy mode** (`mini connect`, the default):

mini exposes upstream tools directly (`github__list_pull_requests`, `sentry__list_issues`,
etc.). Claude Code's deferred loading works exactly as designed: schemas defer through the
`defer_loading` + `tool_reference` mechanism, one round-trip per call, responses still
trimmed by mini's projections. The model doesn't know mini is there.

**Compact mode** (`mini connect --tool-mode compact`, 4-tool interface):

The model calls `mini.list` to discover tools, then `mini.call` for every invocation —
two round-trips per upstream call. Upstream tool names come back as text inside a tool
result message, never entering the API's deferred tool mechanism. Schemas and responses
both land in conversation messages.

For Claude Code, **proxy mode is strictly better**: correct schema deferral, half the
round-trips, same response trimming. Reach for `--tool-mode compact` only if your client
loads all schemas upfront at session start.

---

## Connection lifecycle (high level)

Before tool discovery, Claude Code connects to each configured MCP server at startup. The
observable behavior:

- **Servers connect in parallel**, with a bounded number of simultaneous connections (local
  stdio servers are connected at a lower concurrency than remote HTTP/SSE servers, since each
  stdio server spawns a child process).
- **Each connection is time-boxed** by a per-server connect timeout on the order of tens of
  seconds, and a server that fails or times out is **isolated** — the others still load and the
  session is never blocked by one bad server. There is no "required server aborts startup"
  behavior.
- **No health checks.** Claude Code does not ping live connections; a dropped server is detected
  reactively (its transport closing or a later call failing). Remote transports are retried with
  backoff; local stdio servers are not auto-reconnected.
- **Failures surface to the user, not the model.** Dead-server state appears in the MCP status
  UI; the model just sees the affected tools disappear from the next turn, or gets an error when
  it calls a tool on a server that has gone away.

This is the behavioral contract mini must be compatible with — see
[mcp-lifecycle.md](mcp-lifecycle.md) for how mini sits in front of these connections.
