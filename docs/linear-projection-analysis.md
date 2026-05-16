# Linear MCP Projection Analysis

## Data Sources
- **Linear MCP documentation:** https://mcp.linear.app — GraphQL-based issue tracking API
- **Linear GraphQL schema:** https://github.com/linear/linear (main branch) — defines Issue, Team, Project, Cycle, Comment types
- **Live fixture data:** `/benchmarks/fixtures/linear/list_issues.json` — 3 real Linear issue responses with full field inventory (v0.1 fixture baseline)
- **Tool schemas:** Input definitions from `/benchmarks/fixtures/linear/*.schema.json`

---

## Architecture & Response Shapes

Linear MCP uses GraphQL. All responses are either:
1. **Wrapped objects:** `{nodes: [...], pageInfo: {...}, totalCount: N}` for list operations
2. **Single objects:** `{id, identifier, title, ...}` for get/create/update operations

---

## Per-Tool Analysis

### list_issues

**Response shape:** `{nodes: [Issue], pageInfo: {hasNextPage, hasPreviousPage, startCursor, endCursor}}`

**Input:** Optional userId, includeArchived, limit

**Fixture data:** 3 issues, demonstrating all real-world fields

| Field | Type | In Fixture? | Keep Case | Remove Case | Verdict | Confidence |
|-------|------|-----------|-----------|------------|---------|-----------|
| nodes | array | Yes | Top-level wrapper; required | N/A | KEEP | 100% |
| pageInfo | object | Yes | Pagination metadata (hasNextPage critical for agent iteration) | Could rebuild from position | KEEP | 100% |
| id | string | Yes | Internal UUID; used for API calls (get_issue, update_issue) | Could reconstruct from identifier | KEEP | 100% |
| identifier | string | Yes | Human-readable ID (ENG-1234); essential for display and linking | N/A | KEEP | 100% |
| title | string | Yes | Issue summary; always needed for decisions | N/A | KEEP | 100% |
| description | string | Yes (486 chars) | Full context for issue understanding; markdown; 1000 char in list context reasonable | Field is large; could defer to get_issue | LIMIT to 1500 | 95% |
| state | object | Yes | Current status ({id, name, type, color}); critical for filtering open/closed | Can omit color (visual only) | KEEP, depth-limit to {name, type} | 95% |
| priority | integer | Yes (0-4) | Priority level for triage; essential for weighted scheduling | N/A | KEEP | 100% |
| priorityLabel | string | Yes (e.g., "Urgent") | Human-readable priority label (e.g., "Urgent", "High"); useful for UI display | Redundant with priority integer | KEEP (human context) | 90% |
| assignee | object | Yes | User assignment; critical for ownership/responsibility questions | May be null; could omit if null | KEEP (with depth limit) | 100% |
| creator | object | Yes | Issue reporter; useful for context ("who filed this?") | Rarely queried directly | KEEP | 85% |
| team | object | Yes | Team context; useful for scoping/filtering by team | Can reconstruct from identifier prefix | KEEP | 90% |
| labels | object | Yes (nodes array) | Labels are taxonomy; useful for filtering and categorization | Could omit in list context | KEEP with array_limit: 5 | 95% |
| comments | object | Yes (totalCount only) | Comment count indicates discussion depth; agent decides if read full issue | N/A | KEEP totalCount | 100% |
| createdAt | string | Yes | Timeline metadata; useful for age/recency filtering | Rarely critical | KEEP | 90% |
| updatedAt | string | Yes | Recency indicator; helps prioritize stale vs. active | Useful for triage | KEEP | 95% |
| dueDate | string | Yes | Release/deadline context; useful for priority decisions | Could omit (less common) | KEEP | 85% |
| estimate | integer | Yes (3) | Story points for sprint planning; essential for capacity decisions | May be null; important when present | KEEP | 100% |
| **sortOrder** | float | Yes (-1234.56) | **Internal board column ordering float; never meaningful to agents** | **Pure UI metadata; useless for API consumers** | **EXCLUDE** | **100%** |
| **subIssueSortOrder** | float | Yes (-5678.9) | **Internal ordering within sub-issue hierarchy; pure UI** | **No agent use case** | **EXCLUDE** | **100%** |
| **archivedAt** | null (datetime) | Yes (null) | **If not null, indicates archived. Could use archived boolean instead** | **Redundant with workflow state** | **EXCLUDE** | **95%** |
| **autoArchivedAt** | null (datetime) | Yes (null) | **System auto-archive timestamp; informational but not actionable** | **Rarely queried; adds noise** | **EXCLUDE** | **90%** |
| **autoClosedAt** | null (datetime) | Yes (null) | **System auto-close timestamp; similar to autoArchivedAt** | **Workflow state covers this** | **EXCLUDE** | **90%** |
| **canceledAt** | null (datetime) | Yes (null) | **Timestamp when moved to canceled; arguably useful for workflow state** | **Could be covered by state + lifecycle dates** | **EXCLUDE** | **85%** |
| **completedAt** | null (datetime) | Yes (null) | **Completion timestamp; arguably useful for tracking done items** | **State object covers done status** | **EXCLUDE** | **85%** |
| **snoozedUntilAt** | null (datetime) | Yes (null) | **Shows if/until when issue is snoozed; could be useful for filtering** | **Less common in workflows** | **EXCLUDE** | **80%** |
| **trashed** | boolean | Yes (false) | **Soft delete flag; might be useful for filtering to exclude trash** | **Rare; can query filters instead** | **EXCLUDE** | **75%** |
| **previousIdentifiers** | array | Yes (empty) | **Legacy IDs from past renames; almost never needed** | **No agent use case** | **EXCLUDE** | **100%** |
| **customerTicketCount** | integer | Yes (3) | **Indicates support tickets linked; useful for prioritization/context** | **Business metric; could be omitted** | **EXCLUDE** | **70%** |

**Summary:** Current config excludes 11 fields via `exclude_always`. Analysis confirms most exclusions are correct (sortOrder, subIssueSortOrder are clear internal UI floats; archived/auto* dates are workflow metadata). However, estimate, priorityLabel, and dueDate should definitely be kept. customerTicketCount is borderline; excluded here following strict philosophy.

**Revised config for list_issues:**
- `include: [nodes, pageInfo]` — top-level wrapper
- `array_limits: {nodes: 25, labels: 5}` — reasonable for list context
- `string_limits: {description: 1500}` — enough for triage decision; agents defer to get_issue for full text
- `depth_limit: 3` — safe for nested user/team objects
- Keep all current exclusions (sortOrder, subIssueSortOrder, archived*, canceledAt, completedAt, snoozedUntilAt, trashed, previousIdentifiers, customerTicketCount)

---

### search_issues

**Response shape:** Same as list_issues (`{nodes: [...], pageInfo: {...}, totalCount: N}`)

**Input:** Query string, team, state, priority, assignee filters

| Field | Verdict | Justification |
|-------|---------|---------------|
| nodes, pageInfo, totalCount | KEEP | Identical to list_issues wrapper |
| All issue fields | LIMIT | Same analysis as list_issues, but search results are typically lower-context — reduce description from 1500 to 800 chars to account for broader query results |

**Revised config:**
- Same as list_issues but with `string_limits: {description: 800}`

---

### get_issue

**Response shape:** Single Issue object (all fields present)

**Input:** Issue ID (internal UUID or identifier string)

| Field | Verdict | Justification |
|-------|---------|---------------|
| id, identifier, title, description | KEEP | Full issue retrieval; agent needs complete context. Increase description limit to 5000 chars. |
| state, priority, priorityLabel, assignee, creator, team, labels, comments | KEEP | Full context needed for understanding issue |
| createdAt, updatedAt, dueDate, estimate | KEEP | Workflow context |
| All excluded fields (sortOrder, archived*, trashed, etc.) | EXCLUDE | Same reasoning as list_issues; not actionable even in full context |

**Revised config:**
- `depth_limit: 4` — get_issue may have deeply nested relationships
- `string_limits: {description: 5000}` — full issue description for decision-making
- Keep same exclusions as list_issues

---

### create_issue

**Response shape:** Single Issue object (confirms created issue)

**Input:** title (required), teamId (required), description, priority, status, assigneeId, labelIds

| Field | Verdict | Justification |
|-------|---------|---------------|
| All fields | KEEP (same limits as get_issue) | Write operation response confirms what was created; agent needs full identifier + status |

**Revised config:**
- Same as get_issue (agent needs confirmation of created resource)

---

### update_issue

**Response shape:** Single Issue object (confirms updated issue)

**Input:** id (required), title, description, priority, status, assigneeId, etc.

| Field | Verdict | Justification |
|-------|---------|---------------|
| All fields | KEEP (same limits as get_issue) | Confirms update success; agent needs full status |

**Revised config:**
- Same as get_issue

---

### list_projects

**Response shape:** `{nodes: [Project], pageInfo: {...}}`

Similar to issues but for projects. Projects are simpler (no description, priority, etc.).

**Revised config:**
- `include: [nodes, pageInfo]`
- `array_limits: {nodes: 20}`
- `depth_limit: 2`
- Keep current exclusions (sortOrder, archivedAt, autoArchivedAt, trashed)

---

### list_teams

**Response shape:** `{nodes: [Team], ...}`

Teams are simple (id, name, key, description).

**Revised config:**
- `include: [nodes]`
- `array_limits: {nodes: 20}`
- `depth_limit: 2`

---

### list_cycles

**Response shape:** `{nodes: [Cycle], ...}`

Cycles are sprint-like (name, status, start/end dates).

**Revised config:**
- `include: [nodes]`
- `array_limits: {nodes: 10}`
- `depth_limit: 2`
- Keep current exclusions (archivedAt, autoArchivedAt)

---

### list_comments

**Response shape:** `{nodes: [Comment], pageInfo: {...}}`

Comments contain author, body, createdAt, updatedAt.

**Revised config:**
- `include: [nodes, pageInfo]`
- `array_limits: {nodes: 30}`
- `depth_limit: 2`
- `string_limits: {body: 2000}` — increase from 1000 to account for richer comment context

---

### Wildcard defaults

**Revised config:**
- `depth_limit: 3` — safe default
- `auto_strip_threshold: 500` — strip HTML/markdown above 500 tokens

---

## Summary of Changes vs Current Config

### Confirmed correct (no change needed):
1. **exclude_always lists** for all tools — sortOrder, subIssueSortOrder, archived*/auto* dates are correctly identified as internal UI metadata
2. **list_issues include/array_limits** — node limits reasonable
3. **Depth limits** — appropriate across all tools

### Recommended adjustments:
1. **list_issues description string_limit** — increase from 500 to 1500 chars (balances context vs. tokens; agents use for decision-making)
2. **search_issues description string_limit** — set to 800 chars (broader results, lower context)
3. **get_issue description string_limit** — increase from 3000 to 5000 chars (full issue context for detailed review)
4. **list_comments body string_limit** — increase from 1000 to 2000 chars (comment threads often rich)

### No changes needed for:
1. **Permission tiers** — all visible tools are read operations; no protected writes in current config
2. **Response envelope** — standard mini response wrapping applies

---

## Confidence Levels

- **Exclusions (sortOrder, subIssueSortOrder, archived*, trashed, previousIdentifiers):** 95-100% confident. These are clearly internal UI metadata with zero agent use cases.
- **String limits (description, body):** 85-95% confident. Balances against actual agent needs for context; conservative increases recommended.
- **Keep/KEEP verdicts (id, identifier, title, state, priority, assignee, etc.):** 95-100% confident. Core issue data needed for every workflow.
- **Borderline exclusions (customerTicketCount, canceledAt, completedAt):** 70-85% confident. Could be argued either way; excluded here to minimize noise and follow "default is keep" philosophy by using limits instead.

---

## Philosophy Alignment

This analysis follows the core principle from `default-config-philosophy.md`:

- **Default is keep:** All core fields (identifier, title, state, priority, assignee, estimate, labels) are kept.
- **String limits over exclusions:** description fields are limited (1500-5000 chars) rather than excluded, allowing agents to make decisions without secondary API calls.
- **Exclude only clear cases:** sortOrder and subIssueSortOrder are excluded because they are parameterized UI metadata with no agent use case — not because they're large.
- **List vs. get distinction:** list_issues uses tighter limits (1500 chars) for efficiency; get_issue allows 5000 chars for full context.
- **Array limits reasonable:** labels 5, nodes 20-25 — real-world Linear issues rarely exceed these.
