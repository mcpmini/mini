# Shared fixtures

`fixtures/` contains MCP response data shared by the eval runner, fake MCP integration tests, and bundled-projection shape regression tests. It includes captured responses and hand-written examples used to exercise representative tool shapes.

Keep each fixture at `fixtures/<server>/<tool>.json`. Use the `.live.json` suffix for a response captured from a live MCP session and `.schema.json` for an input schema. Shape regression tests protect required fields in bundled projections; they do not measure token savings or agent task quality. Use the evals for end-to-end behavior assessment.

For a captured response, call the tool through mini with raw output enabled and copy the saved MCP response from mini's internal response store. The upstream MCP server may transform API data, so raw REST responses are not interchangeable with MCP responses.
