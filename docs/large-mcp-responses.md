# How clients handle large MCP responses

> Companion to [claude-code-mcp-loading.md](claude-code-mcp-loading.md) and
> [codex-mcp-loading.md](codex-mcp-loading.md), which cover tool schemas. This doc covers what
> happens to a `CallToolResult` once a client receives it. Checked 2026-10-22.

## Size limits

| Client | Limit | Past the limit |
|---|---|---|
| Claude Code | 50,000 chars | Saves the full result to a file in the session's `tool-results` directory and gives the model the path; the model reads it in chunks. Warns at 10K tokens; hard cap `MAX_MCP_OUTPUT_TOKENS`, default 25K tokens. A tool can raise its own limit to 500K chars with `_meta["anthropic/maxResultSizeChars"]`. Error results over ~11K chars keep the first and last 5,000. |
| Codex | Model `truncation_policy`, 10K tokens (~40K chars) for current models; per-server `output_token_limit` overrides | Middle truncation: keeps the head and the tail and drops the middle. No path to the dropped part. |
| OpenCode | 2,000 lines or 50 KB | Keeps the first lines, saves the full output to a file, appends `[showing lines 1-N of M; full output saved to <file>]`. A single-line JSON result over 50 KB shows no lines. |
| Pi (pi-mcp-adapter) | 2,000 lines or 50 KB | Keeps the head, saves the full output to a temp file. |

Sources: [Claude Code MCP docs](https://code.claude.com/docs/en/mcp), the `openai/codex`
truncation policy, OpenCode `packages/core/src/tool-output.ts`, pi-mcp-adapter
`mcp-output-guard.ts`.

## Which fields reach the model

- Claude Code: `structuredContent` replaces text blocks when present; image and resource blocks
  still show. On `isError` only `content` is used.
- Codex: `structuredContent` alone when present, otherwise every content block.
- OpenCode and Pi: every content block; `structuredContent` is not shown.
- No client shows result `_meta`, and none validates `structuredContent` against `outputSchema`.
- `outputSchema` is not shown to the model by Claude Code with tool search or by Codex by
  default. Codex code mode renders it into TypeScript return types.
- Two text blocks: both clients show both. Claude Code joins them with no separator.

## What this means for mini

- Codex sets the bound: 10K tokens for the whole result. Markup and JSON run about 3 chars per
  token, so a 50K-char page is ~15K tokens. Codex drops its middle, and since mini cut nothing,
  there is no stored file to read it back from.
- Bundled rules therefore cap whole documents and single-item gets at 25K chars (~8K tokens),
  which leaves room for the rest of the result and the notice. 25K is the 90th percentile of
  sampled public Confluence pages.
- Strings with no rule get the 2K default (~500–700 tokens). A cut only saves tokens when the
  agent usually stops at it, which holds for lists and search but not for single-item gets.
- `read` returns the stored field unprojected. A jq string slice (`.body[5000:]`) fetches only
  the part that was cut.
