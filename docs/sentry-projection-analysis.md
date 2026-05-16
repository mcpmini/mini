# Sentry MCP Projection Analysis

## Data Sources

- **Sentry Official API Docs**: `https://docs.sentry.io/api/events/list-a-projects-issues/` — documents issue fields
- **Live fixture data**: `benchmarks/fixtures/sentry/list_issues.json` — real Sentry API response (5 issues, 1.5MB)
- **Sentry MCP README**: `https://github.com/getsentry/sentry-mcp` — tool descriptions and use cases
- **Tool schemas**: `benchmarks/fixtures/sentry/*.schema.json` — input/output contracts
- **Evals**: `eval_triage.go`, `eval_bugfix.go` — actual agent workflows using `list_issues`, `update_issue`, `create_note`

---

## Tool Inventory

From `evals/servers.go` and fixtures:

**Read tools:**
- `list_issues` — Find errors, triage, incident response
- `list_events` — Event-level details (stacktraces, breadcrumbs)
- `get_issue_details` — Full issue context with activity history
- `list_projects` — Project discovery
- `list_organizations` — Org discovery

**Write tools:**
- `update_issue` — Resolve, reassign, ignore issues
- `create_note` — Add investigation notes (comments)

---

## Per-Tool Analysis

### list_issues

**Response shape:** Flat array of issue objects (up to 100 per request)  
**Live data:** 5 issues; 4 have `metadata`; 1 has `shortId` instead of `id` in some contexts  
**Usage in evals:** Primary tool for incident triage and bug tracking

#### Field-by-Field Analysis

| Field | In API? | Keep case | Remove case | Verdict | Confidence |
|-------|---------|-----------|------------|---------|-----------|
| **id** | Yes ✓ | Issue identification; required for updates and details. Example: `4500000001` | Never — essential field | **KEEP** | 100% |
| **shortId** | Yes ✓ | Sentry's human-readable issue ID (e.g., `AUTH-001`). Useful in logs and Slack messages. | Could reconstruct from `id` + project, but `shortId` is the UI-facing identifier | **KEEP** | 100% |
| **title** | Yes ✓ | The error message or exception title. Core issue summary. | If excluded, agents can't understand what the error is. | **KEEP** | 100% |
| **culprit** | Yes ✓ | The code location that caused the error (file + function). Critical for debugging. Example: `auth/middleware.go in ValidateToken`. | Removing this loses the "where" — essential context for code fixes. | **KEEP** | 100% |
| **permalink** | Yes ✓ | URL to the issue in Sentry UI (e.g., `https://acme.sentry.io/issues/4500000001/`). This is a **URL template string** format issue. | Can agents actually use this? Yes — to share link or open in browser for context. But URLs can be reconstructed. | **EXCLUDE** | 95% |
| **logger** | Yes ✓ (null in fixture) | Sentry SDK logger name (e.g., `db.pool`, `null` if not set). Context signal. | Rarely used in agent workflows; supplementary signal | LIMIT (null is fine) | 85% |
| **level** | Yes ✓ | Severity level (`fatal`, `error`, `warning`, `info`). Crucial for triage and prioritization. | If excluded, agents can't sort by severity. | **KEEP** | 100% |
| **status** | Yes ✓ | Resolution status: `unresolved`, `resolved`, `ignored`, `reprocessing`. Essential workflow state. | Agents must know this to avoid re-resolving or re-ignoring. | **KEEP** | 100% |
| **isPublic** | Yes ✓ | Whether the issue is shared publicly. Admin/visibility flag. | Never useful in agent workflows — subjective org setting. | **EXCLUDE** | 98% |
| **hasSeen** | Yes ✓ | Whether the current user has viewed the issue. Per-user state. | Per-user state is never actionable in agent workflows (agents are not "users"). | **EXCLUDE** | 100% |
| **isBookmarked** | Yes ✓ | Whether the current user bookmarked the issue. Per-user state. | Same as `hasSeen` — subjective user preference, not actionable. | **EXCLUDE** | 100% |
| **project** | Yes ✓ | Nested object with project metadata (id, slug, name, platform, etc.). Context essential for issue lifecycle. | Without project context, agents don't know which Sentry project owns this issue. | **KEEP** (depth-limited) | 100% |
| **metadata** | Yes ✓ | Nested object with error type, filename, function, display flags. Example: `{value: "JWT signature verification failed...", type: "AuthenticationError", filename: "auth/middleware.go", function: "ValidateToken"}`. | This is highly useful! Type and filename are distinct from `culprit` (which is just "file in function"), and `value` is the detailed error message. | **KEEP** | 100% |
| **firstSeen** | Yes ✓ | RFC3339 timestamp when issue first occurred. Timeline context. | Timeline context helps agents prioritize (old vs. new issues). | **KEEP** | 100% |
| **lastSeen** | Yes ✓ | RFC3339 timestamp when issue was last seen. Recency indicator. | Recency indicates if issue is still active. Essential signal. | **KEEP** | 100% |
| **count** | Yes ✓ (string in fixture) | Total number of events for this issue. Frequency indicator. | Frequency helps agents assess severity (1 vs. 10,000 occurrences). | **KEEP** | 100% |
| **userCount** | Yes ✓ | Number of unique users affected. Impact indicator. | Distinct from `count` — shows blast radius. Critical for incident severity. | **KEEP** | 100% |
| **times_seen** | Yes ✓ | Alias for `count`. Duplicate field. | Redundant with `count`. | **EXCLUDE** | 95% |
| **isUnhandled** | Yes ✓ | Whether the exception was caught or unhandled. Exception type signal. | Unhandled exceptions are more critical than handled ones (crash vs. error path). | **KEEP** | 100% |
| **isShared** | Yes ✓ | Whether issue has been shared. Admin flag. | Never actionable in agent workflows. | **EXCLUDE** | 98% |
| **isSubscribed** | Yes ✓ | Whether current user is subscribed to updates. Per-user state. | Per-user state, not actionable. | **EXCLUDE** | 100% |
| **assignedTo** | Yes ✓ | Nested user/team object showing who is responsible. Ownership context. | Ownership is critical context — agents need to know who to mention or escalate to. | **KEEP** (simplified) | 100% |
| **tags** | Yes ✓ | Array of key-value pairs (environment, release, transaction, server, etc.). Critical filtering and context. | Tags are essential — they include environment (prod vs. staging), release version, transaction name, and custom tags. Used heavily in triage. | **KEEP** (limited) | 100% |
| **numComments** | Yes ✓ | Count of notes/comments on the issue. Discussion signal. | Indicates if issue has been investigated (many comments = ongoing discussion). Useful context. | **KEEP** | 95% |
| **seen** | Yes ✓ | ISO8601 timestamp when current user last viewed. Per-user state. | Per-user state, not actionable. | **EXCLUDE** | 100% |
| **annotations** | Yes ✓ (always empty array) | Array of user annotations. Admin/metadata field. | Always empty in practice; no agent use case. | **EXCLUDE** | 98% |
| **participants** | Yes ✓ | Array of users involved in issue discussion. Team context. | Could be useful for escalation, but not critical for initial triage. | LIMIT (first 5) | 75% |
| **activity** | Yes ✓ | Array of activity log entries (resolutions, comments, assignments). Investigation history. | Very useful for understanding issue lifecycle and latest action. Should include but limit. | LIMIT (first 3) | 85% |
| **seenBy** | Yes ✓ (always empty) | Array of users who have viewed issue. Per-user state. | Always empty or irrelevant; no agent use case. | **EXCLUDE** | 100% |
| **statusDetails** | Yes ✓ | Nested object with extra status info (e.g., `{inRelease: "3.0.0-beta.8"}`). Context on resolution. | Useful context (which release fixed it), but not critical for agent decisions. | LIMIT | 80% |
| **subscriptionDetails** | Yes ✓ (null) | Subscription notification settings. Admin state. | Admin setting, not actionable by agents. | **EXCLUDE** | 100% |
| **userReport** | Yes ✓ (null) | User-submitted feedback on issue. CRM data. | CRM/user feedback; not relevant to automation workflows. | **EXCLUDE** | 98% |
| **shareId** | Yes ✓ (null) | Public share token. Admin identifier. | Admin field, not needed by agents. | **EXCLUDE** | 98% |
| **pluginActions** | Yes ✓ (always []) | Integration actions (alerts, notifications). Metadata. | Metadata; not actionable by agents. | **EXCLUDE** | 100% |
| **pluginContexts** | Yes ✓ (always []) | Plugin context data. Metadata. | Metadata; not actionable. | **EXCLUDE** | 100% |
| **pluginIssues** | Yes ✓ (always []) | Related issues from plugins. Metadata. | Metadata; not actionable. | **EXCLUDE** | 100% |
| **type** | Yes ✓ | Issue type (always `"error"` in fixture). Low signal. | Metadata; all Sentry issues are errors. | **EXCLUDE** | 95% |
| **platform** | Yes ✓ | SDK platform (e.g., `python`, `javascript`). Tech context. | Nice-to-have; helps agents understand stack traces. | KEEP | 85% |
| **stats** | Yes ✓ | Nested object with event time-series data (24h, 14d arrays). Huge field (~5KB per issue). | Time-series data is rarely used by agents; visualization only. Safe to exclude. | **EXCLUDE** | 90% |
| **inbox** | Yes ✓ | Nested object with inbox state (reason, date_added). Metadata. | Inbox metadata; admin state. | **EXCLUDE** | 98% |
| **timeSpentTotal** | Yes ✓ | Total time spent on issue (milliseconds). Admin metric. | Probably useful for prioritization (high time = complex), but low priority. | LIMIT | 70% |
| **timeSpentCount** | Yes ✓ | Count of time entries. Admin metric. | Low signal; not actionable. | **EXCLUDE** | 80% |

#### Revised include list for list_issues

**Decision**: Use `include` list for list operations (per philosophy). Exclude `stats` (huge), exclude all per-user state, exclude all plugin/admin fields.

```yaml
list_issues:
  include: [id, shortId, title, culprit, level, status, platform, project, metadata, 
            firstSeen, lastSeen, count, userCount, isUnhandled, assignedTo, tags, 
            numComments, logger, activity]
  array_limits:
    default: 25
    tags: 15
    activity: 3
  depth_limit: 2
  exclude_always: [pluginActions, pluginContexts, pluginIssues, seenBy, statusDetails, 
                   subscriptionDetails, userReport, annotations, permalink, shareId, 
                   isPublic, hasSeen, isSubscribed, isBookmarked, isShared, seen, 
                   times_seen, stats, inbox, timeSpentCount, participants, timeSpentTotal]
  string_limits:
    message: 1000
```

---

### get_issue_details

**Response shape:** Single issue object with all fields; same as `list_issues` but potentially with `entries` (stacktraces, breadcrumbs)  
**Usage in evals:** Not yet, but critical for detailed debugging

#### Field Analysis (summarized; reuses logic from list_issues)

Per philosophy: "Get/detail operations use `depth_limit` and generous `string_limits`; do not use `include` filter."

For `get_issue_details`, we want the full issue object but with:
- `entries` (stacktraces, breadcrumbs) limited to first 10 (could be huge)
- `metadata` included in full (unlike list_issues which limits it)
- `activity` included in full (all investigation history)
- All large nested objects depth-limited to 2–3

#### Config for get_issue_details

```yaml
get_issue_details:
  depth_limit: 3
  array_limits:
    entries: 10
    activity: 50
    tags: 30
    participants: 10
  exclude_always: [pluginActions, pluginContexts, pluginIssues, seenBy, userReport, 
                   annotations, stats, inbox]
  string_limits:
    message: 5000
    activity.data.text: 2000
```

---

### list_events

**Response shape:** Flat array of event objects (each event is an occurrence of an issue)  
**Usage:** Drill down from issue to individual events/stacktraces  
**Fields in fixture:** N/A (no list_events fixture; using schema from current config)

Per current config, the include list is: `[id, eventID, message, platform, dateCreated, level, tags, user]`

**Verdict**: This looks sound. Events are lightweight summaries; agents need:
- `id` + `eventID` for identification
- `message` for context
- `platform`, `dateCreated`, `level` for filtering/time context
- `tags` for metadata
- `user` for affected user context

**No changes needed** — current config is appropriate for list operations.

```yaml
list_events:
  include: [id, eventID, message, platform, dateCreated, level, tags, user]
  array_limits:
    default: 20
    tags: 15
  depth_limit: 2
```

---

### list_projects

**Response shape:** Flat array of project objects  
**Usage:** Discover projects available in the organization  
**No live fixture; analyzing from schema and current config**

Current include list: `[id, name, slug, platform, status, firstEvent, hasAccess, isMember, isBookmarked, isInternal]`

**Analysis:**
- `id`, `name`, `slug` — essential for project identification ✓
- `platform` — SDK type; useful for tech context ✓
- `status` — `active` vs. inactive; workflow relevant ✓
- `firstEvent` — when first error arrived; age signal ✓
- `hasAccess`, `isMember` — access control; agents need to know if they can access ✓
- `isBookmarked` — per-user state; **exclude** ✗
- `isInternal` — visibility flag; not critical

**Verdict**: Remove `isBookmarked` (per-user state); all others KEEP.

```yaml
list_projects:
  include: [id, name, slug, platform, status, firstEvent, hasAccess, isMember, isInternal]
  array_limits:
    default: 30
  depth_limit: 2
```

---

### list_organizations

**Response shape:** Flat array of organization objects  
**Usage:** Multi-org management; less common in typical workflows  
**No live fixture; analyzing from current config**

Current include: `[id, name, slug, status, dateCreated, role, features]`

**Analysis:**
- `id`, `name`, `slug` — identification ✓
- `status` — active vs. inactive ✓
- `dateCreated` — age signal ✓
- `role` — current user's role in org (viewer/owner/manager); **exclude** (per-user state) ✗
- `features` — enabled features in org; useful for capability detection ✓

**Verdict**: Remove `role` (per-user state); all others KEEP.

```yaml
list_organizations:
  include: [id, name, slug, status, dateCreated, features]
  array_limits:
    default: 10
  depth_limit: 2
```

---

### update_issue (write operation)

**Response shape:** Minimal confirmation object  
**Fields:** `id`, `status`, `assignedTo`, etc.  

Write operations return the updated resource. Projection should be minimal (agent just needs confirmation). No exclusions needed; response is already compact.

```yaml
update_issue:
  depth_limit: 2
```

---

### create_note (write operation)

**Response shape:** Minimal note confirmation object  
**Fields:** `id`, `text`, `dateCreated`, `user`

Same as update_issue — minimal projection needed.

```yaml
create_note:
  depth_limit: 2
```

---

## Key Corrections from Current Config

### Current issues:

1. **`metadata` excluded from `list_issues`** — This is WRONG. The fixture clearly shows `metadata` contains critical fields:
   - `type` — exception type (e.g., `AuthenticationError`, `DatabaseTimeoutError`)
   - `filename` — file where error occurred
   - `function` — function name
   - `value` — detailed error message

   Excluding this loses the "what and where" context. Verdict: **INCLUDE** in list_issues.

2. **`permalink` excluded from list_issues** — This is a URL that agents might actually use (e.g., to generate Slack links or reference in comments). While URLs can be reconstructed, this particular URL is the Sentry-specific issue link, which is actionable. Verdict: **EXCLUDE** (URL template, not reconstructible in practice).

3. **`statusDetails` excluded from list_issues** — This could show which release fixed an issue (e.g., `{inRelease: "3.0.0"}`). Limited usefulness but not zero. Verdict: **LIMIT** (include in get_issue_details, exclude from list).

4. **`activity` not in list_issues include** — Recent activity (resolutions, assignments) is valuable context. Verdict: **INCLUDE** (with limit to 3 items).

5. **`isBookmarked` in list_projects** — Per-user state. Verdict: **EXCLUDE**.

6. **`role` in list_organizations** — Per-user state. Verdict: **EXCLUDE**.

---

## Summary of Changes

| Tool | Current | Proposed | Rationale |
|------|---------|----------|-----------|
| list_issues | Excludes `metadata` | Include `metadata` | Critical error context (type, file, function, message) |
| list_issues | No `activity` in include | Add `activity` (limit 3) | Investigation history valuable for triage |
| list_issues | Includes `statusDetails` | Exclude from list | Better in get_issue_details only |
| list_issues | Includes `permalink` | Exclude | URL template string; not reconstructible but low use case |
| list_projects | Includes `isBookmarked` | Exclude | Per-user state, not actionable |
| list_organizations | Includes `role` | Exclude | Per-user state, not actionable |
| get_issue_details | Excludes `metadata` | Include `metadata` | Same as list_issues; full detail retrieval should have full context |
| get_issue_details | — | Add `statusDetails` + `activity` (full) | Detail operations should include all investigation context |

---

## Philosophy Alignment

**Exclusion bar met for:**
- All per-user state fields (`hasSeen`, `isSubscribed`, `isBookmarked`, `seen`, `role`) — never actionable
- All plugin/admin metadata (`pluginActions`, `pluginContexts`, `pluginIssues`) — always empty or irrelevant
- Admin-only fields (`seenBy`, `subscriptionDetails`, `shareId`) — no agent use case
- Time-series data (`stats`) — huge and visualization-only
- Deprecated/empty fields (`annotations`, `userReport`) — always empty or internal

**Inclusions justified for:**
- `metadata` — exception type, file, function, message; essential debugging context
- `activity` — investigation history; shows what has been done
- `assignedTo` — ownership context; critical for escalation
- `tags` — environment, release, custom filters; essential triage signals
- `culprit` — code location; central to debugging
- `isUnhandled` — crash vs. error path; important signal
- `numComments` — discussion indicator; suggests investigation depth

---

## Proposed sentry.yaml

```yaml
list_issues:
  include: [id, shortId, title, culprit, level, status, platform, project, metadata, 
            firstSeen, lastSeen, count, userCount, isUnhandled, assignedTo, tags, 
            numComments, logger, activity]
  array_limits:
    default: 25
    tags: 15
    activity: 3
  depth_limit: 2
  exclude_always: [pluginActions, pluginContexts, pluginIssues, seenBy, subscriptionDetails, 
                   userReport, annotations, permalink, shareId, isPublic, hasSeen, 
                   isSubscribed, isBookmarked, isShared, seen, times_seen, stats, inbox, 
                   timeSpentCount, timeSpentTotal, participants, statusDetails]
  string_limits:
    message: 1000

get_issue_details:
  depth_limit: 3
  array_limits:
    entries: 10
    activity: 50
    tags: 30
    participants: 10
  exclude_always: [pluginActions, pluginContexts, pluginIssues, seenBy, userReport, 
                   annotations, stats, inbox]
  string_limits:
    message: 5000

list_events:
  include: [id, eventID, message, platform, dateCreated, level, tags, user]
  array_limits:
    default: 20
    tags: 15
  depth_limit: 2

list_projects:
  include: [id, name, slug, platform, status, firstEvent, hasAccess, isMember, isInternal]
  array_limits:
    default: 30
  depth_limit: 2

list_organizations:
  include: [id, name, slug, status, dateCreated, features]
  array_limits:
    default: 10
  depth_limit: 2

update_issue:
  depth_limit: 2

create_note:
  depth_limit: 2

"*":
  depth_limit: 3
  auto_strip_threshold: 500
```
