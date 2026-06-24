---
name: calibrate-projections
description: Calibrate mini projection defaults for an MCP server using real tool responses, fixtures, and validation tests.
argument-hint: <server-name>
---

Create or update a default projection config for MCP server `$ARGUMENTS`.

The goal is a projection YAML that reduces token noise without silently dropping information an agent would need. Every decision must be grounded in real MCP tool responses, not upstream REST API guesses.

## Confirm server and tools

```bash
# Ensure Go and project lint tools are available on PATH before validation.
./mini ls $ARGUMENTS
./mini call list server=$ARGUMENTS
```

Group tools into list, detail/get, and write operations.

## Capture real fixtures

For representative read-only tools, call the MCP tool with raw output:

```bash
./mini call -r $ARGUMENTS <tool> [arg=value ...]
```

Copy the corresponding raw response into `benchmarks/fixtures/$ARGUMENTS/<tool>.live.json`.

Capture at least one list operation, one detail/get operation, and any tool the user specifically cares about. Do not substitute direct REST API responses for MCP responses.

Only commit fixtures produced from public, synthetic, or fully redacted data. Never commit raw live responses from private or authenticated workspaces. Before copying a live response into `benchmarks/fixtures`, inspect it for tokens, private URLs, emails, usernames, org/project names, and account metadata. If public data is unavailable, keep the fixture under an ignored private path such as `benchmarks/fixtures/$ARGUMENTS/private/` or name it `*.private.json`, and do not commit it.

## Analyze response shape

Inspect the actual top-level keys and wrapped collection shapes. For each field decide:

- URL templates, always-null fields, deprecated fields, and deeply nested metadata agents do not act on can be excluded.
- Identifiers, URLs, titles, names, states, timestamps, authors, and actionable status fields should generally stay.
- Content fields such as body, description, message, and markdown should stay with string limits.
- When in doubt, keep the field.

Count rough token savings before and after projection.

## Write projection YAML

Read `docs/default-config-philosophy.md` first.

For list operations:

- Use an explicit `include` list that matches top-level response keys.
- Use `string_limits` for large content fields, usually around 1500 chars.
- Use generous `array_limits` for nested arrays that aid triage.

For detail/get and write operations:

- Avoid `include` filters unless the response shape is proven safe.
- Use larger string limits, usually around 5000 chars.
- Use `exclude` only for fields proven to be useless noise.

Do not add `depth_limit` to defaults. Do not set `strip_markup` unless the specific tool embeds raw HTML that adds no value.

## Register and validate

Add a matcher in `internal/ops/projection.go` when the server can be detected from URL or command.

Add permissions defaults for write or destructive tools when appropriate.

Add fixture validation in `internal/bench/validate_test.go`, then run:

```bash
go test -race -tags test -run TestProjectionValidation ./internal/bench/... -v
go test -race -tags test ./...
```

Report covered tools, token reduction, excluded fields and reasons, and any tools intentionally left without a named projection.
