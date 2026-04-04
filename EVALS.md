# minimcp Evaluation Framework

## Goal

Quantify minimcp's actual token savings for real agent workflows. Produce reproducible numbers comparable across code changes, using a sandboxed Claude instance that executes the same workflow twice — once against raw MCP servers, once through minimcp — and measures token consumption.

## Evaluation scenarios

### 1. Address PR review comments (primary)

**Workflow:** Agent reads a PR, finds review comments, implements fixes, pushes.

Steps:
1. `github.list_pull_requests` — find open PRs
2. `github.get_pull_request` — PR body, diff, metadata
3. `github.list_pull_request_comments` — review threads
4. `github.get_file_contents` × N — read relevant files
5. Edit files (tool calls or code output)
6. `github.create_or_update_file` — push fixes
7. `github.add_reply_to_pull_request_comment` — resolve threads

**Why this matters:** Every step returns verbose payloads. GitHub PR objects alone can be 5–20K tokens raw; minimcp strips `node_id`, URL templates, `gravatar_id`, and other noise that agents never use.

### 2. Incident response: Sentry + code + PR

**Workflow:** Agent reads an error from Sentry, traces it to source, opens a fix PR.

Steps:
1. `sentry.list_issues` — find open errors
2. `sentry.get_issue` — stacktrace, breadcrumbs, user context
3. `github.get_file_contents` × N — read affected files
4. `github.create_pull_request` — open fix PR with description

**Why this matters:** Sentry issue objects include raw event payloads, SDK metadata, and full breadcrumb chains — largely irrelevant to the fix. minimcp's `exclude_always`, `strip_content`, and array limits cut these substantially.

### 3. Log triage: Datadog → code → summary

**Workflow:** Agent reads recent errors from Datadog, identifies pattern, reads source, writes summary.

Steps:
1. `datadog.query_logs` — last N log events matching a filter
2. `datadog.get_log` × N — individual log details
3. `github.search_code` — find the emitting code path
4. Agent produces a structured summary (no PR, pure analysis)

**Why this matters:** Datadog log arrays are extremely large. minimcp's `array_limits` and projection configs reduce what the agent sees to the error message, timestamp, service, and trace ID — enough to reason about patterns without reading kilobytes of request context per event.

### 4. Jira ticket → fix → PR (full loop)

**Workflow:** Agent reads a Jira ticket, understands requirements, implements the change, opens a PR linked back to the ticket.

Steps:
1. `jira.get_issue` — title, description, acceptance criteria, comments
2. `github.search_code` — find relevant source files
3. `github.get_file_contents` × N
4. `github.create_pull_request` — PR body references Jira key
5. `jira.add_comment` — link back to PR

**Why this matters:** Jira issue objects contain rich HTML descriptions, extensive comment chains, and changelog history. minimcp's `strip_content` converts HTML to plain text; `exclude_always` drops changelogs and internal metadata.

## Measurement approach

### Token counting

For each scenario, record:
- **Input tokens:** sum of all tool result payloads sent to the model
- **Output tokens:** model completions (should be roughly equal between raw/mini runs)
- **Tool call count:** identical between runs (same workflow, different payload sizes)
- **Reduction %:** `(raw_input - mini_input) / raw_input × 100`

Token counts come from the Claude API usage field on each response, summed across the conversation.

### Fixture-based testing

To ensure reproducibility without live credentials:

1. **Capture phase** (manual, run once with real credentials): Execute the workflow against real MCP servers, record every tool result to `benchmarks/fixtures/<server>/<tool>.json`.
2. **Replay phase** (CI, runs on every PR): A fake MCP server replays the captured fixtures. Claude runs the workflow against both the fake raw server and minimcp proxying the same fake. Token counts are compared.

```
benchmarks/
  fixtures/
    github/
      list_pull_requests.json
      get_pull_request.json
      list_pull_request_comments.json
      get_file_contents.json
    sentry/
      list_issues.json
      get_issue.json
    datadog/
      query_logs.json
  projections/
    github.yaml    # projection configs used during replay
    sentry.yaml
    datadog.yaml
  scenarios/
    address_comments.yaml   # ordered list of tool calls + expected keys
    incident_response.yaml
    log_triage.yaml
    jira_fix_pr.yaml
  results/
    baseline.json   # committed; updated when raw behavior intentionally changes
```

### Scenario definition format

```yaml
# benchmarks/scenarios/address_comments.yaml
name: address_comments
description: Read PR review comments and implement fixes
steps:
  - server: github
    tool: list_pull_requests
    fixture: list_pull_requests.json
  - server: github
    tool: get_pull_request
    fixture: get_pull_request.json
  - server: github
    tool: list_pull_request_comments
    fixture: list_pull_request_comments.json
  - server: github
    tool: get_file_contents
    fixture: get_file_contents.json
    repeat: 3    # agent reads 3 files
```

### Sandboxed Claude execution

The eval runner:
1. Spins up a fake MCP server serving the fixture files
2. Starts Claude (via API) with the system prompt defining the task
3. Claude issues tool calls; runner responds with fixtures
4. Runner accumulates input token counts per response
5. Repeats with minimcp proxying the same fake server
6. Compares and records results in `benchmarks/results/`

Claude is given a deterministic task prompt so tool call sequences are stable across runs. The system prompt constrains it to the fixture tools only.

### CI integration

```yaml
# .github/workflows/evals.yml
on:
  push:
    branches: [main]
  pull_request:
    paths:
      - 'internal/projection/**'
      - 'internal/response/**'
      - 'benchmarks/**'

jobs:
  evals:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - run: go build -o minimcp ./cmd/minimcp
      - run: go test ./benchmarks/... -v -timeout 120s
        env:
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
```

The eval test fails if token reduction regresses by more than 5% relative to the committed baseline. It warns (not fails) if the baseline itself shifts — this handles intentional changes to projection configs.

## Success criteria

| Scenario | Target reduction |
|---|---|
| Address PR comments | ≥ 60% input token reduction |
| Incident response (Sentry) | ≥ 55% |
| Log triage (Datadog) | ≥ 70% |
| Jira fix PR | ≥ 50% |

These targets are educated estimates. Actual baselines will be established from the first captured run and committed as `benchmarks/results/baseline.json`.

## Implementation plan

### Phase 1: Fixture capture (requires credentials)
- Run each scenario against live MCP servers
- Save raw tool results to `benchmarks/fixtures/`
- Commit fixtures (sanitise any secrets first)

### Phase 2: Replay infrastructure
- Implement `benchmarks/fakeserver/` — a minimal stdio MCP server that reads fixtures
- Write `benchmarks/runner/runner.go` — orchestrates Claude via API, accumulates token counts
- Define scenario YAML format and parser

### Phase 3: Projection configs
- Tune `benchmarks/projections/` YAML configs for each server
- These become the canonical configs shipped with minimcp for these servers

### Phase 4: CI integration
- Add `evals.yml` workflow
- Set baseline from first full run
- Wire regression threshold check

### Phase 5: Reporting
- Add `minimcp bench` subcommand that runs evals locally and prints a comparison table
- Output: scenario name, raw tokens, mini tokens, reduction %, pass/fail vs threshold
