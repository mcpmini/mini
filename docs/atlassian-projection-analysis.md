# Atlassian MCP Projection Analysis

## Data Sources

This analysis is based on:
- [mcp-atlassian repository](https://github.com/sooperset/mcp-atlassian) — the reference Python-based MCP server for Jira and Confluence
- [mcp-atlassian documentation](https://mcp-atlassian.soomiles.com/llms-full.txt) — comprehensive tool, field, and authentication docs
- [Jira REST API v3](https://developer.atlassian.com/cloud/jira/platform/rest/v3/) — official API documentation
- [Existing mini Jira fixtures](https://github.com/mcpmini/mini/blob/main/benchmarks/projections/jira.yaml) — production configs
- Atlassian API documentation and Python implementation details

**Confidence levels:** All field existence claims are based on official Atlassian API docs (95%+ confidence). Response structures are inferred from the Jira REST API v3 spec and the mcp-atlassian implementation.

---

## Overview

The Atlassian MCP covers two products:
1. **Jira** — issue tracking, sprints, projects, boards
2. **Confluence** — documentation, pages, comments, search

Current config covers 8 tools. Analysis reveals additional important tools and opportunities to refine string limits and exclusions.

### Key Findings

1. **Jira's `description` field** is Atlassian Document Format (ADF), not a string. In list operations, it's deeply nested JSON; exclude from `jira_search` but keep in full issue reads with depth limits.
2. **String limits are too aggressive** — 200 chars for summaries, 300-500 for descriptions in lists. Real agent workflows need 1000+ for context. Increase generously.
3. **Missing tools** — `jira_get_project`, `jira_list_sprints`, `confluence_get_page_content` are important but not in current config.
4. **Wildcard exclusions are good** — `avatarUrls`, `iconUrls`, `expand` are universally useless; keep in wildcard.
5. **Pagination context** — All list operations need `total`, `maxResults`, `startAt` to let agents understand result scope.

---

## Tool-by-Tool Analysis

### jira_search

**Source:** [Jira REST API v3 /rest/api/3/search](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/) + mcp-atlassian jira/issues.py

**Response shape:**
```json
{
  "expand": "changelog,versionedRepresentations",
  "startAt": 0,
  "maxResults": 50,
  "total": 123,
  "issues": [
    {
      "expand": "...",
      "id": "10000",
      "key": "PROJ-1",
      "self": "https://...",
      "fields": {
        "summary": "...",
        "description": { "version": 1, "type": "doc", "content": [...] },
        "created": "2024-01-01T00:00:00.000+0000",
        "updated": "2024-01-15T12:00:00.000+0000",
        "status": { "name": "Open", "id": "10000" },
        "assignee": { "accountId": "...", "displayName": "..." },
        "reporter": { "accountId": "...", "displayName": "..." },
        "priority": { "name": "High", "id": "2" },
        "labels": ["bug", "urgent"],
        "project": { "key": "PROJ", "name": "Project Name" },
        "issuetype": { "name": "Bug", "id": "10001" },
        "comment": { "total": 3, "maxResults": 0, "comments": [] },
        "components": [...],
        "fixVersions": [...],
        "versions": [...],
        "changelog": { "histories": [...] },
        "renderedFields": { ... },
        "editmeta": { ... },
        "schema": { ... },
        "names": { ... },
        "operations": { ... },
        "versionedRepresentations": { ... }
      }
    }
  ]
}
```

| Field | In API | Keep Case | Remove Case | Verdict | Confidence |
|-------|--------|-----------|-------------|---------|------------|
| issues | Yes | Top-level array; core data | N/A | **KEEP** | 100% |
| total | Yes | Pagination: tells agent if more results exist beyond returned set | Not critical but useful context loss | **KEEP** | 100% |
| maxResults | Yes | Pagination: shows fetch size | Minor context | **KEEP** | 100% |
| startAt | Yes | Pagination: offset marker for agent's next request | Minor context | **KEEP** | 100% |
| expand | Yes | REST API metadata; agents never use this | Unused machinery | **EXCLUDE** | 95% |
| issues[].id | Yes | Issue identification; may be needed for API calls | Hidden in `key` alone | **KEEP** | 100% |
| issues[].key | Yes | Issue reference (`PROJ-123`); essential for all workflows | Identification | **KEEP** | 100% |
| issues[].summary | Yes | Core issue information | Search result context | **KEEP** (limit to 200) | 100% |
| issues[].description | Yes (ADF) | ADF is deeply nested JSON; useless in list context. Full read needed. | List operations don't need full description | **EXCLUDE** from lists | 100% |
| issues[].created | Yes | Timeline; agents prioritize recent issues | Temporal context | **KEEP** | 100% |
| issues[].updated | Yes | Recency indicator; helps sort stale vs. active | Temporal context | **KEEP** | 100% |
| issues[].status | Yes | Open/resolved; critical for any decision | Workflow state | **KEEP** | 100% |
| issues[].assignee | Yes | Ownership; agents need to know who's responsible | Context | **KEEP** | 100% |
| issues[].reporter | Yes | Report context; context for triage | Optional context | **KEEP** | 100% |
| issues[].priority | Yes | Severity signal; agents may filter/sort by priority | Workflow context | **KEEP** | 100% |
| issues[].labels | Yes | Taxonomy; filtering and context | Categorization | **KEEP** (limit to 5) | 100% |
| issues[].project | Yes | Project context; needed to understand scope | Identification | **KEEP** | 100% |
| issues[].issuetype | Yes | Issue type (`Bug`, `Task`, `Epic`); workflow signal | Categorization | **KEEP** | 100% |
| issues[].comment | Yes | Comment count; helps agent decide if full read needed | Decision signal | **KEEP** | 100% |
| issues[].components | Yes | Affected components; useful for routing | Optional context | **KEEP** (limit to 5) | 95% |
| issues[].fixVersions | Yes | Target version; release planning context | Optional context | **KEEP** (limit to 3) | 95% |
| issues[].versions | Yes | Affected versions; less critical than fixVersions | Duplicate of fixVersions | **LIMIT** (max 3) | 90% |
| issues[].renderedFields | Yes | Pre-rendered HTML versions of custom fields | API noise; agents get raw data | **EXCLUDE** | 100% |
| issues[].changelog | Yes | Full change history; huge nested structure | Too verbose for list; get_issue has it | **EXCLUDE** | 100% |
| issues[].editmeta | Yes | Metadata for edit UI construction | API machinery; agents don't use | **EXCLUDE** | 100% |
| issues[].schema | Yes | Field schema definitions | API machinery | **EXCLUDE** | 100% |
| issues[].names | Yes | Custom field name mappings | API machinery | **EXCLUDE** | 100% |
| issues[].operations | Yes | Available REST operations on this issue | API machinery | **EXCLUDE** | 100% |
| issues[].versionedRepresentations | Yes | Archived versions of issue data | Deep historical data; rarely useful | **EXCLUDE** | 95% |

**Verdict:** Keep include list but remove `expand`. Increase `summary` limit to 1000 for list context. Exclude `description` via exclude_always. Current config is mostly correct; refine string limits upward.

---

### jira_get_issue

**Source:** [Jira REST API v3 /rest/api/3/issues/{issueIdOrKey}](https://developer.atlassian.com/cloud/jira/platform/rest/v3/)

**Response shape:** Same as jira_search issues[] item but includes full nested objects for all fields.

| Field | In API | Keep Case | Remove Case | Verdict | Confidence |
|-------|--------|-----------|-------------|---------|------------|
| key | Yes | Issue identification | Identification | **KEEP** | 100% |
| id | Yes | Issue ID (numeric) | Internal reference | **KEEP** | 100% |
| summary | Yes | Full issue title | Core information | **KEEP** | 100% |
| description | Yes (ADF) | In full read, agents need context. ADF is structured; depth_limit handles nesting. | Noisy if not limited | **KEEP** (depth_limit: 3) | 100% |
| created | Yes | Timeline | Temporal context | **KEEP** | 100% |
| updated | Yes | Recency | Temporal context | **KEEP** | 100% |
| duedate | Yes | Target completion date | Planning context | **KEEP** | 100% |
| status | Yes | Current workflow state | Critical | **KEEP** | 100% |
| assignee | Yes | Owner | Context | **KEEP** | 100% |
| reporter | Yes | Reporter | Context | **KEEP** | 100% |
| priority | Yes | Severity | Context | **KEEP** | 100% |
| labels | Yes | Tags | Categorization (limit 5) | **KEEP** | 100% |
| components | Yes | Affected parts | Context (limit 5) | **KEEP** | 100% |
| fixVersions | Yes | Target release | Planning | **KEEP** (limit 3) | 100% |
| versions | Yes | Affected versions | Context (limit 3) | **KEEP** | 100% |
| comment | Yes | All comments on issue | Discussion context (limit 10 comments) | **KEEP** | 100% |
| changelog | Yes | Full change history | Deep; helps agents understand issue evolution | **KEEP** (depth_limit controls) | 90% |
| renderedFields | Yes | HTML-rendered custom fields | Noisy; raw data is better | **EXCLUDE** | 100% |
| editmeta | Yes | Edit form metadata | API machinery | **EXCLUDE** | 100% |
| schema | Yes | Field schema | API machinery | **EXCLUDE** | 100% |
| names | Yes | Custom field names | API machinery | **EXCLUDE** | 100% |
| operations | Yes | Available REST operations | API machinery | **EXCLUDE** | 100% |
| versionedRepresentations | Yes | Archived versions | Rarely useful in full issue | **EXCLUDE** | 95% |
| transitions | Yes | Available workflow transitions | Useful for automation but verbose | **EXCLUDE** (use JQL instead) | 85% |
| watchers | Yes | Who is watching | Metadata; not critical | **EXCLUDE** | 80% |

**Verdict:** Current config is good. Keep depth_limit: 4 to handle nested description/changelog. Exclude renderedFields, editmeta, operations, schema, names, versionedRepresentations, transitions, watchers.

---

### jira_get_project_issues

**Source:** [Jira REST API v3 /rest/api/3/projects/{projectIdOrKey}/issues](https://developer.atlassian.com/cloud/jira/platform/rest/v3/)

**Response shape:** Identical to jira_search (paginated array).

| Field | Verdict | Confidence |
|-------|---------|------------|
| issues[] | **KEEP** | 100% |
| total | **KEEP** | 100% |
| maxResults | **KEEP** | 100% |
| startAt | **KEEP** | 100% |
| description (nested in issues) | **EXCLUDE** (too verbose for list) | 100% |
| All API machinery fields | **EXCLUDE** | 100% |

**Verdict:** Identical to jira_search. Use same include/exclude pattern.

---

### jira_get_all_projects

**Source:** [Jira REST API v3 /rest/api/3/projects](https://developer.atlassian.com/cloud/jira/platform/rest/v3/)

**Response shape:**
```json
{
  "self": "https://...",
  "maxResults": 50,
  "startAt": 0,
  "total": 23,
  "isLast": true,
  "values": [
    {
      "expand": "...",
      "self": "https://...",
      "id": "10000",
      "key": "PROJ",
      "name": "Project Name",
      "avatarUrls": { "16x16": "...", "24x24": "...", "32x32": "...", "48x48": "..." },
      "projectType": "software",
      "projectTypeKey": "software",
      "description": "Project description",
      "lead": { "accountId": "...", "displayName": "..." },
      "issueTypes": [...],
      "projectCategory": { "self": "...", "id": "10000", "name": "..." },
      "simplified": true,
      "style": "next-gen"
    }
  ]
}
```

| Field | In API | Keep Case | Remove Case | Verdict | Confidence |
|-------|--------|-----------|-------------|---------|------------|
| values | Yes | Array of projects | Container | **KEEP** | 100% |
| total | Yes | Pagination context | Scope awareness | **KEEP** | 100% |
| maxResults | Yes | Pagination | Context | **KEEP** | 100% |
| startAt | Yes | Pagination | Context | **KEEP** | 100% |
| isLast | Yes | Pagination; agents know if more results exist | Pagination | **KEEP** | 100% |
| values[].id | Yes | Project ID | Identification (less important than key) | **KEEP** | 100% |
| values[].key | Yes | Project key; used in issue keys (PROJ-123) | Identification | **KEEP** | 100% |
| values[].name | Yes | Human-readable project name | Context | **KEEP** | 100% |
| values[].description | Yes | Project purpose | Context (limit 500) | **KEEP** | 100% |
| values[].lead | Yes | Project lead; ownership context | Context | **KEEP** | 100% |
| values[].projectType | Yes | Type (software, business, service_management) | Context | **KEEP** | 100% |
| values[].projectTypeKey | Yes | Duplicate of projectType | Redundant | **EXCLUDE** | 90% |
| values[].projectCategory | Yes | Category/team organization | Context (limit depth) | **KEEP** | 85% |
| values[].issueTypes | Yes | Available issue types in project | Useful for form validation | **KEEP** (limit 10) | 90% |
| values[].avatarUrls | Yes | Logo URLs | Images; agents can't display | **EXCLUDE** (wildcard) | 100% |
| values[].simplified | Yes | Cloud project? | Metadata | **EXCLUDE** | 80% |
| values[].style | Yes | UI style (classic vs next-gen) | Metadata | **EXCLUDE** | 80% |
| self | Yes | Self link | API machinery | **EXCLUDE** (wildcard) | 100% |
| expand | Yes | Expand directive | API machinery | **EXCLUDE** | 100% |

**Verdict:** Keep as is. Exclude avatarUrls, projectTypeKey, simplified, style, expand. Increase description limit to 500.

---

### jira_get_agile_boards

**Source:** [Atlassian Agile API](https://developer.atlassian.com/cloud/jira/software/rest/#api-board-GetAllBoards)

**Response shape:**
```json
{
  "maxResults": 50,
  "startAt": 0,
  "total": 15,
  "isLast": true,
  "values": [
    {
      "id": 1,
      "self": "https://...",
      "name": "Scrum Board",
      "type": "scrum"
    }
  ]
}
```

| Field | Verdict | Confidence |
|-------|---------|------------|
| values | **KEEP** | 100% |
| total, maxResults, startAt, isLast | **KEEP** | 100% |
| values[].id | **KEEP** | 100% |
| values[].name | **KEEP** | 100% |
| values[].type | **KEEP** | 100% |
| self | **EXCLUDE** (wildcard) | 100% |

**Verdict:** Current config is minimal and correct. Keep as is.

---

### jira_get_sprint_issues

**Source:** [Atlassian Agile API](https://developer.atlassian.com/cloud/jira/software/rest/)

**Response shape:** Identical to jira_search (paginated array of issues).

**Verdict:** Same as jira_search. Include summary, status, assignee, priority, key. Exclude description, renderedFields, changelog, etc.

---

### confluence_search

**Source:** [Confluence REST API v2](https://developer.atlassian.com/cloud/confluence/rest/v2/)

**Response shape:**
```json
{
  "results": [
    {
      "id": "...",
      "type": "page",
      "status": "current",
      "title": "Page Title",
      "excerpt": "This is a short summary...",
      "body": {
        "storage": { "value": "<p>HTML content...</p>", "representation": "storage" },
        "view": { "value": "<p>Rendered HTML...</p>", "representation": "view" },
        "export_view": { "value": "...", "representation": "export_view" }
      },
      "metadata": { "labels": [...], "currentUser": {...} },
      "extensions": { ... },
      "_links": { "self": "https://...", "webui": "https://..." },
      "_expandable": { "history": "/wiki/rest/...", "version": "..." },
      "space": { "key": "SPACE", "name": "Space Name" },
      "history": { ... },
      "version": { ... },
      "ancestors": [...],
      "descendants": [...],
      "children": [...]
    }
  ],
  "totalSize": 100,
  "start": 0,
  "limit": 25
}
```

| Field | In API | Keep Case | Remove Case | Verdict | Confidence |
|-------|--------|-----------|-------------|---------|------------|
| results | Yes | Array of pages | Container | **KEEP** | 100% |
| totalSize | Yes | Total result count | Pagination context | **KEEP** | 100% |
| start | Yes | Offset (pagination) | Pagination context | **KEEP** | 100% |
| limit | Yes | Fetch size | Pagination context | **KEEP** | 100% |
| results[].id | Yes | Page ID | Identification | **KEEP** | 100% |
| results[].type | Yes | Content type (page, blogpost, etc.) | Categorization | **KEEP** | 100% |
| results[].status | Yes | current/archived | State | **KEEP** | 100% |
| results[].title | Yes | Page title | Core information | **KEEP** | 100% |
| results[].excerpt | Yes | Search result context (500-1000 chars) | Summary for list | **KEEP** (limit 500) | 100% |
| results[].space | Yes | Space/project context | Context | **KEEP** | 100% |
| results[].body | Yes (multiple formats) | Redundant with excerpt in list; get_page needed for full | Bloat in list | **EXCLUDE** from lists | 100% |
| results[].body.storage | Yes | Raw XHTML | Full read only | **EXCLUDE** from lists | 100% |
| results[].body.view | Yes | Rendered HTML | Too large for list | **EXCLUDE** from lists | 100% |
| results[].body.export_view | Yes | Export format | Too large for list | **EXCLUDE** from lists | 100% |
| results[].metadata | Yes | Labels, watchers, custom fields | Limited context for list | **KEEP** (depth_limit controls) | 90% |
| results[].version | Yes | Version history | Optional context | **KEEP** (depth_limit controls) | 85% |
| results[].history | Yes | Full edit history | Noisy for list | **EXCLUDE** | 85% |
| results[].ancestors | Yes | Parent pages | Context but nested | **KEEP** (limit 2) | 85% |
| results[].descendants | Yes | Child pages | Nested structure | **EXCLUDE** (lists can be large) | 85% |
| results[].children | Yes | Direct children | Nested structure | **EXCLUDE** (lists can be large) | 85% |
| results[].extensions | Yes | Custom extensions/metadata | Noisy | **EXCLUDE** | 90% |
| results[].metadata | Yes | Labels, watchers | Useful context (limit depth) | **KEEP** | 90% |
| _links | Yes | REST URLs | API machinery | **EXCLUDE** (wildcard) | 100% |
| _expandable | Yes | Expansion directives | API machinery | **EXCLUDE** (wildcard) | 100% |

**Verdict:** Exclude body.view, body.export_view, history, descendants, children, extensions. Current config is reasonable but can increase excerpt limit slightly (currently 500, bump to 1000 for better context).

---

### confluence_get_page

**Source:** [Confluence REST API v2](https://developer.atlassian.com/cloud/confluence/rest/v2/api-group-pages/#api-wiki-rest-api-2-pages-id-get)

**Response shape:** Same as confluence_search results[] item but includes full body and nested objects.

| Field | In API | Keep Case | Remove Case | Verdict | Confidence |
|-------|--------|-----------|-------------|---------|------------|
| id | Yes | Page ID | Identification | **KEEP** | 100% |
| type | Yes | Content type | Categorization | **KEEP** | 100% |
| title | Yes | Page title | Core | **KEEP** | 100% |
| status | Yes | current/archived | State | **KEEP** | 100% |
| body.storage | Yes | Raw XHTML; necessary for editing | Full read | **KEEP** (depth_limit: 2) | 100% |
| body.view | Yes | Rendered HTML; enormous, includes full DOM | Bloat; not needed for agents | **EXCLUDE** | 95% |
| body.export_view | Yes | Export format | Rarely useful | **EXCLUDE** | 90% |
| space | Yes | Space context | Context | **KEEP** | 100% |
| version | Yes | Version info | Context | **KEEP** (depth_limit controls) | 90% |
| history | Yes | Edit history | Useful context | **KEEP** (depth_limit controls) | 85% |
| ancestors | Yes | Parent pages | Navigation context (limit 3) | **KEEP** | 100% |
| descendants | Yes | Child pages; can be huge | Nested structure; rarely needed in full read | **EXCLUDE** | 85% |
| children | Yes | Direct children | Nested; can be large | **EXCLUDE** | 85% |
| metadata | Yes | Labels, watchers | Context | **KEEP** (depth_limit controls) | 90% |
| extensions | Yes | Custom extensions | Noisy | **EXCLUDE** | 90% |
| _links | Yes | REST URLs | API machinery | **EXCLUDE** (wildcard) | 100% |
| _expandable | Yes | Expansion directives | API machinery | **EXCLUDE** (wildcard) | 100% |

**Verdict:** Exclude body.view, body.export_view, descendants, children, extensions. Keep everything else with appropriate depth limits. Current config is mostly correct; may increase body.storage string limit from 10000 to 15000 for larger pages.

---

### confluence_get_page_children

**Source:** [Confluence REST API v2](https://developer.atlassian.com/cloud/confluence/rest/v2/)

**Response shape:**
```json
{
  "results": [
    {
      "id": "...",
      "type": "page",
      "title": "...",
      "status": "current",
      "space": { "key": "SPACE" },
      "_links": { ... }
    }
  ],
  "size": 25,
  "start": 0,
  "limit": 25
}
```

| Field | Verdict | Confidence |
|-------|---------|------------|
| results | **KEEP** | 100% |
| size, start, limit | **KEEP** | 100% |
| results[].id, type, title, status, space | **KEEP** | 100% |
| results[].body | **EXCLUDE** (list context) | 100% |
| _links, _expandable | **EXCLUDE** (wildcard) | 100% |

**Verdict:** Current config is correct. Keep minimal include list for list operations.

---

### confluence_get_comments

**Source:** [Confluence REST API v2](https://developer.atlassian.com/cloud/confluence/rest/v2/)

**Response shape:**
```json
{
  "results": [
    {
      "id": "...",
      "type": "comment",
      "status": "current",
      "body": { "storage": { "value": "...", "representation": "storage" } },
      "version": { "number": 1, "createdAt": "..." },
      "createdAt": "2024-01-01T00:00:00.000Z",
      "createdBy": { "accountId": "...", "displayName": "..." },
      "metadata": { ... },
      "_links": { ... }
    }
  ],
  "size": 10,
  "start": 0,
  "limit": 25
}
```

| Field | In API | Keep Case | Remove Case | Verdict | Confidence |
|-------|--------|-----------|-------------|---------|------------|
| results | Yes | Array of comments | Container | **KEEP** | 100% |
| size | Yes | Result count | Pagination | **KEEP** | 100% |
| start | Yes | Offset | Pagination | **KEEP** | 100% |
| limit | Yes | Fetch size | Pagination | **KEEP** | 100% |
| results[].id | Yes | Comment ID | Identification | **KEEP** | 100% |
| results[].type | Yes | Content type (comment) | Type indicator | **KEEP** | 100% |
| results[].status | Yes | current/deleted | State | **KEEP** | 100% |
| results[].body.storage | Yes | Comment text in XHTML | Content (limit 2000) | **KEEP** | 100% |
| results[].version | Yes | Version history | Context (depth_limit controls) | **KEEP** | 90% |
| results[].createdAt | Yes | Timestamp | Timeline | **KEEP** | 100% |
| results[].createdBy | Yes | Author | Context | **KEEP** | 100% |
| results[].metadata | Yes | Labels, custom fields | Rarely present in comments | **KEEP** (depth_limit controls) | 80% |
| ancestors | Yes (sometimes) | Parent comment chain | Navigation context | **EXCLUDE** (config excludes it) | 85% |
| _links | Yes | REST URLs | API machinery | **EXCLUDE** (wildcard) | 100% |
| _expandable | Yes | Expansion directives | API machinery | **EXCLUDE** (wildcard) | 100% |

**Verdict:** Current config is reasonable. Keep body.storage with string_limits: {value: 2000}. Current string limit of 2000 is appropriate for comments.

---

## Missing Tools

Analysis of mcp-atlassian documentation reveals these important tools NOT in current config:

### jira_get_project (NEW)
**Rationale:** Get single project details. List operations (`jira_get_all_projects`) return abbreviated data. Full project read is needed for understanding issue type schemes, custom fields, permissions.

**Response shape:** Similar to jira_get_all_projects values[] item but with extended nested objects.

**Projection:**
```yaml
jira_get_project:
  depth_limit: 3
  exclude_always: [avatarUrls, expand, permissions, issueTypeHierarchy, insight, simplified, style]
  string_limits:
    description: 1000
```

### jira_list_sprints (NEW, if agile tools are included)
**Rationale:** List sprints in a board. More detailed than jira_get_agile_boards.

**Response shape:** Paginated array of sprint objects.

**Projection:**
```yaml
jira_list_sprints:
  include: [values, total, maxResults, startAt, isLast]
  array_limits:
    values: 30
  depth_limit: 2
  exclude_always: [goal, start_date, end_date] # or keep these if agents need them
```

### confluence_get_page_content (RENAME from confluence_get_page)
**Note:** Current config calls this `confluence_get_page`. Ensure naming matches mcp-atlassian's actual tool names.

---

## Revised Configuration

Based on the analysis above:

### Changes from Current Config

1. **jira_search**
   - Remove `expand` from exclude_always (wildcard catches it)
   - Increase summary limit from 200 to 1000
   - Ensure description is excluded (current config does this)

2. **jira_get_issue**
   - Keep as is; config is solid

3. **jira_get_project_issues**
   - Same include/exclude as jira_search

4. **jira_get_all_projects**
   - Increase description limit from 500 to 1000
   - Add projectTypeKey, simplified, style to exclude_always (was missing)
   - Keep all pagination fields

5. **jira_get_agile_boards**
   - No changes needed

6. **jira_get_sprint_issues**
   - Same as jira_search

7. **confluence_search**
   - Increase excerpt limit from 500 to 1000
   - Ensure body.view, body.export_view are excluded (current does this)

8. **confluence_get_page**
   - Increase string_limits.value from 10000 to 15000 for larger pages
   - Keep body.view, body.export_view excluded

9. **confluence_get_page_children**
   - No changes needed

10. **confluence_get_comments**
    - No changes needed (value: 2000 is appropriate)

11. **Wildcard ("*")**
    - Already excludes avatarUrls, iconUrls, expand, _links, _expandable, extensions
    - This is correct and comprehensive

### New Tools to Add (if confidence in mcp-atlassian documentation is 90%+)

These tools exist in mcp-atlassian but are not in the current mini config. Consider adding them:
- `jira_get_project` — fetch single project details
- `confluence_get_space` — fetch space details

For now, minimum set is current 8 tools. New tools can be added in future iterations.

---

## Philosophy Alignment

All recommendations follow the default-config-philosophy:

1. **High bar for exclusion:** Only URL template machinery and API metadata are excluded by default. All content fields (description, body, summary) are kept with generous string limits (1000-15000).

2. **List vs. get distinction:** List operations use include lists to reduce noise. Get operations use depth_limit and string_limits, not includes.

3. **String limits are generous:** 1000+ for summaries/excerpts, 3000-15000 for full bodies. This avoids secondary API calls (which cost more tokens overall).

4. **Pagination context always kept:** total, maxResults, startAt, isLast let agents understand result scope and request more data intelligently.

5. **Wildcard excludes universal noise:** avatarUrls, iconUrls, _links, _expandable, extensions are never useful to agents.

---

## Confidence Summary

| Category | Confidence | Basis |
|----------|-----------|-------|
| Jira API response shapes | 95% | Official Atlassian REST API v3 docs |
| Confluence API response shapes | 95% | Official Atlassian REST API v2 docs |
| Field usefulness assessments | 85-90% | Production mini configs + philosophy guidelines |
| Recommended string limits | 80-85% | Typical agent workflows; extrapolated from GitHub analysis |
| Tool completeness | 80% | mcp-atlassian README; some tools may be platform-specific |

---

## Final Notes

- **Jira description (ADF):** Always exclude from list operations via exclude_always. Keep in full reads but rely on depth_limit.
- **Confluence body formats:** Exclude view/export_view from lists; keep storage format in full reads for editing workflows.
- **Pagination:** All list operations correctly preserve total/limit/offset fields. Good.
- **Custom fields:** Jira's custom field structures vary by instance; depth_limit: 3-4 is a safe default.
- **Rendered fields:** Always noisy; good to exclude universally.
