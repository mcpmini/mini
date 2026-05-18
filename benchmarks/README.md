# Benchmarks

Fixtures and projection validation tests for mini's bundled default configurations.

## Structure

```
benchmarks/
  fixtures/
    <server>/
      <tool>.json        # response fixture
      <tool>.live.json   # fixture captured from a real live MCP session
      <tool>.schema.json # input argument schema (not a response fixture)
```

## Fixture capture requirements

**Fixtures MUST come from real MCP tool responses, not raw REST API calls.**

The MCP server processes and transforms API data before returning it. Field names, nesting structure, and included fields can all differ from what the underlying REST API returns. A fixture from `curl https://api.github.com/...` is not a valid substitute for a fixture from the GitHub MCP tool.

### How to capture a fixture

```bash
# Build mini
go build -o mini ./cmd/mini

# Add the server if not already configured
./mini add github --url https://api.githubcopilot.com

# Call the tool with raw output flag — saves the raw MCP response
./mini call -r github list_pull_requests owner=mcpmini repo=mini

# The raw response is in ~/.mini/responses/<timestamp>.raw.json
# Copy it to benchmarks/fixtures/<server>/<tool>.json
```

Use the `.live.json` suffix to mark fixtures from live MCP sessions so they're distinguishable from manually-authored approximations.

## Running validation tests

```bash
go test -race -tags test -run TestProjectionValidation ./internal/bench/...
```

The test applies each bundled default projection to its fixture and asserts:
- Token reduction meets the minimum threshold
- Required keys survive the projection
- The projected output is not empty

## Adding a new fixture

1. Capture a real MCP response using the instructions above
2. Add it to `benchmarks/fixtures/<server>/<tool>.json`
3. Add a `validateCase` entry to `fixtureValidations` in `internal/bench/validate_test.go`
4. Check that the tool name matches the key in the projection YAML (`internal/defaults/projections/<server>.yaml`)

If the tool name in the fixture doesn't match any named key in the projection config, only the `"*"` wildcard applies. The test will log a warning.

## Known gaps

| Server | Tool | Status |
|--------|------|--------|
| atlassian | jira_search | Needs real mcp-atlassian fixture |
| atlassian | jira_get_issue | Needs real mcp-atlassian fixture |
| atlassian | confluence_get_page | Needs real mcp-atlassian fixture |
| linear | get_issue | Needs fixture |
| sentry | get_issue_details | Needs fixture |
| slack | search_messages | Needs fixture |

The `jira/search_issues` fixture uses old tool naming (`search_issues`) from a previous projection config version. The current atlassian projection uses `jira_search`. Once a real mcp-atlassian fixture is captured, replace `jira/search_issues` with `atlassian/jira_search`.
