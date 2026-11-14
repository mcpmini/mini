# Configuring mini

```
~/.mini/
  config.yaml              # global settings
  servers/<name>.yaml      # one file per upstream server
  internal/                # machine-managed runtime state
```

## Server files

```yaml
# ~/.mini/servers/github.yaml
transport: http
url: https://api.githubcopilot.com/mcp
auth:
  type: bearer
  token: "${GITHUB_TOKEN}"     # expanded from the environment
permissions:
  hidden: [delete_file]        # agents never see this tool
projections:                   # keyed by tool name; "*" = every tool
  list_issues:
    exclude:
      - "issues[].user.avatar_url"  # remove a field from each issue
    string_limits:
      body: 1500               # cap each issue body at 1,500 chars
    array_limits:
      labels: 5                # keep the first 5 labels per issue
  issue_read:
    string_limits:
      body: 25000              # cap a single issue body at 25,000 chars
```

A server file can also set `enabled: false` to turn the server off, or `tool_timeout: 5m` (default 30s).

Servers that log in through a browser (Linear, Slack, Sentry and others) need no `auth:` block. `mini add` and `mini init` log you in, and `mini auth NAME` logs in again.

mini checks `~/.mini/servers/` every few seconds while it runs. A new file connects its server, a deleted file disconnects it, changed settings reconnect it, and projection edits apply to the next call. `config.yaml` is read at startup.

## Projection rules

- `exclude`: remove fields by exact path, such as `user.avatar_url` or `"[].user.avatar_url"`. See [the path rules](default-config-philosophy.md#exclude-paths).
- `include_only`: keep only these top-level fields, or each item's fields when the result is a list.
- `string_limits`: max characters for any field with this name, at any depth. Overrides `default_string_limit`; `0` means no cap.
- `array_limits`: max items for any array with this name, at any depth; `default` applies to every array.
- `strip_markup: true`: strip HTML and Markdown from every string.
- `format`: `json` or `toon` for this tool, overriding `response_format`.

For what the bundled projections keep and why, see [default-config-philosophy.md](default-config-philosophy.md).

## Global settings

```yaml
# ~/.mini/config.yaml
log_level: info            # debug | info | warn | error
response_format: json      # json (default) | toon
default_string_limit: 2000 # max chars per string; 0 disables
```

`response_format: toon` switches projected responses to [TOON](https://github.com/toon-format/toon), which writes a list's field names once on a header row, then one line per item.

## Recovering filtered data

The agent gets the upstream server's own result, trimmed in place. `structuredContent`, JSON text blocks and prose text blocks are projected. Images, other blocks and `_meta` pass through unchanged. Embedded resource text is limited as text; field rules don't parse JSON inside it. With `response_format: toon`, JSON text blocks are re-encoded as TOON.

When projection cuts something, the result carries a `__mini` notice: a key in the result object, the last element of a result list, or a final text block. From a `list_pull_requests` page:

```json
"__mini": {
  "msg": "Response filtered, some fields were excluded or truncated, ...",
  "file": "1794658721344",
  "excluded": [".structuredContent | .[].user.avatar_url"],
  "truncated": [
    { "path": ".structuredContent | .[1].body", "chars": 1933 }
  ]
}
```

The full upstream result is stored in `~/.mini/internal/responses/`. The agent passes `file` to `read`, with a listed path (or any jq filter built on it) as `filter`, to get back just that value. A call can pass `__mini: {"projection": "raw"}` to get the whole result at once.

A result that projection doesn't change has no notice and no stored file. Stored results expire after an hour (`response_ttl`), and the oldest go first past 500 MB (`response_disk_budget_mb`).

Clients such as Claude Code check `structuredContent` against the tool's `outputSchema`. mini relaxes each advertised `outputSchema`, dropping `required`, length and item-count limits and similar checks, so a trimmed result and its notice still validate.
