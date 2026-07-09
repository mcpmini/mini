# Evals

End-to-end agent evaluation tests for mini.

## Status: Disabled

Evals are not run in CI and are not part of the standard test suite.

**Why:** LLMs are non-deterministic. A single eval run is not a reliable signal — tool call sequences and response interpretation vary across runs. Meaningful evaluation requires a large sample (N≥20+ runs per scenario) to distinguish real capability changes from variance. That volume of Claude API calls is expensive and not currently practical.

Evals are preserved for manual runs when validating significant behavior changes (e.g. new projection configs, changes to the response envelope format, or major server handler changes).

## Running evals manually

Evals require real upstream MCP servers and a Claude API key:

```bash
export ANTHROPIC_API_KEY=...
go test -tags evals -v -timeout 10m ./evals/...
```

Each eval spawns a full mini instance with real upstream connections and runs Claude as the agent against it.

## What evals test

- `TestEval_TokenBaseline` — schema token cost with no tool calls
- `TestEval_SprintPlanning` — multi-system: Linear + GitHub + Jira
- `TestEval_IncidentTriage` — Sentry → GitHub → Slack
- `TestEval_ReviewPRs` — GitHub: list PRs → read files → comment
- `TestEvalBugFixPipeline` — Jira → Sentry → code fix → GitHub PR (golden eval)

## Projection quality assessment

For projection config validation, use the deterministic heuristic tests in `internal/bench/`:

```bash
go test -race -tags test -run TestProjectionValidation ./internal/bench/...
```

These check token reduction and required key presence against real MCP fixtures without involving an LLM judge.
