# GitHub MCP Projection Analysis

## Overview

The current GitHub projection config was built against old REST API fixtures. The live fixtures (`.live.json` files) show the actual data shapes from the current GitHub MCP implementation, which uses a mix of REST and GraphQL APIs.

**Critical discovery:** `list_issues` returns a wrapped object `{issues, totalCount, pageInfo}`, not a flat array. The current config's `include: [number, title, ...]` would incorrectly filter at the top level and drop the `issues` array entirely.

---

## Per-Tool Analysis

### list_pull_requests
**Response shape:** Flat array of PR objects  
**Live data:** ✓ Confirms REST array format

| Field | Verdict | Justification | In Live Data |
|-------|---------|---------------|--------------|
| number | **KEEP** | Essential for identification and reconstruction of PR URL | ✓ |
| title | **KEEP** | Core information, always needed | ✓ |
| state | **KEEP** | Open/closed status crucial for filtering/decisions | ✓ |
| draft | **KEEP** | Important for workflow - agents should know if PR is ready | ✓ |
| merged | **KEEP** | Workflow-critical (draft PRs show `merged: false`, good to confirm) | ✓ |
| user | **KEEP** | Author identification enables @-mentions, context | ✓ |
| created_at | **KEEP** | Timeline information for weekly workflows | ✓ |
| updated_at | **KEEP** | Recency indicator; helps agents prioritize stale PRs | ✓ |
| labels | **KEEP** | Taxonomy, filtering, and context (limit to 5 per philosophy) | ✓ |
| assignees | **KEEP** | Shows ownership; agents need to know who's responsible (limit to 3) | ✓ |
| head | **KEEP** | Branch ref + sha + repo context needed for checkout/inspection | ✓ |
| base | **KEEP** | Base branch context; needed for merge/rebase decisions | ✓ |
| milestone | **KEEP** | Release/sprint planning context | ✓ |
| body | **EXCLUDE** | Not in live list_pull_requests fixture; get_pull_request exists for details | ✗ |
| requested_reviewers | **EXCLUDE** | Not in live data; rarely shown in lists | ✗ |
| html_url | **EXCLUDE** | Can be reconstructed: `github.com/{repo}/pull/{number}` | ✓ |

**Revised include:** `[number, title, state, draft, merged, user, created_at, updated_at, labels, assignees, head, base, milestone]`

---

### list_issues
**Response shape:** `{issues: [...], totalCount: N, pageInfo: {...}}`  
**Live data:** ✓ Confirms GraphQL-style wrapper

| Field | Verdict | Justification | In Live Data |
|-------|---------|---------------|--------------|
| issues | **KEEP** | Top-level array wrapper; must be included | ✓ |
| totalCount | **KEEP** | Pagination awareness; agents need to know if more results exist | ✓ |
| pageInfo | **KEEP** (limited) | `hasNextPage` + `hasPreviousPage` tell agent if can paginate; keep only bool flags | ✓ |
| number | **KEEP** | Issue identification | ✓ |
| title | **KEEP** | Core information | ✓ |
| state | **KEEP** | OPEN/CLOSED status crucial | ✓ |
| user | **KEEP** | Reporter identification | ✓ |
| created_at | **KEEP** | Timeline | ✓ |
| updated_at | **KEEP** | Recency | ✓ |
| labels | **KEEP** | Taxonomy and context (limit to 5) | ✓ |
| assignees | **KEEP** | Ownership (limit to 3) | ✓ |
| milestone | **KEEP** | Release/sprint planning | ✓ |
| comments | **KEEP** | Indicates discussion depth; helps agent decide if read-full-issue is needed | ✓ |
| body | **EXCLUDE** | Rarely shown in lists; get_issue exists for full details | ✗ |

**Critical fix:** The response structure is NOT a flat array. Remove `include` list entirely and instead control via field-level `array_limits` and `string_limits`. The `pageInfo` object should be preserved but depth-limited to boolean fields only.

---

### get_issue
**Response shape:** Single issue object with all fields  
**Note:** No live fixture; using schema from old fixture

| Field | Verdict | Justification | In Live Data |
|-------|---------|---------------|--------------|
| number | **KEEP** | Identification | Assumed ✓ |
| title | **KEEP** | Core information | Assumed ✓ |
| state | **KEEP** | Status | Assumed ✓ |
| user | **KEEP** | Reporter context | Assumed ✓ |
| created_at | **KEEP** | Timeline | Assumed ✓ |
| updated_at | **KEEP** | Timeline | Assumed ✓ |
| labels | **KEEP** | Taxonomy (limit to 5) | Assumed ✓ |
| assignees | **KEEP** | Ownership (limit to 3) | Assumed ✓ |
| milestone | **KEEP** | Release context | Assumed ✓ |
| body | **KEEP** (limited) | Full issue retrieval warrants body; agents often need to understand issue in detail. Increase limit to 5000 to avoid secondary API call. | Assumed ✓ |
| comments | **KEEP** | Discussion depth indicator | Assumed ✓ |

---

### get_pull_request
**Response shape:** Single PR object  
**Note:** No live fixture; extrapolated from list_pull_requests shape

| Field | Verdict | Justification | In Live Data |
|-------|---------|---------------|--------------|
| number | **KEEP** | Identification | Extrapolated ✓ |
| title | **KEEP** | Core information | Extrapolated ✓ |
| state | **KEEP** | Status | Extrapolated ✓ |
| draft | **KEEP** | Workflow flag | Extrapolated ✓ |
| user | **KEEP** | Author context | Extrapolated ✓ |
| created_at | **KEEP** | Timeline | Extrapolated ✓ |
| updated_at | **KEEP** | Timeline | Extrapolated ✓ |
| labels | **KEEP** | Taxonomy (limit to 5) | Extrapolated ✓ |
| assignees | **KEEP** | Ownership (limit to 3) | Extrapolated ✓ |
| head | **KEEP** | Branch context | Extrapolated ✓ |
| base | **KEEP** | Base branch context | Extrapolated ✓ |
| body | **KEEP** (limited) | Full PR retrieval warrants body; code review workflows need this. 2000 char limit balances completeness vs. token cost. | Extrapolated ✓ |
| mergeable | **KEEP** | Merge conflict detection; critical for automation | Extrapolated ✓ |
| merged | **KEEP** | State info | Extrapolated ✓ |
| additions | **KEEP** | Size signal; helps agents decide on review strategy | Extrapolated ✓ |
| deletions | **KEEP** | Size signal | Extrapolated ✓ |
| changed_files | **KEEP** | Size signal; agents may want to avoid huge PRs | Extrapolated ✓ |
| milestone | **EXCLUDE** | Not critical for full PR view when you have head+base | Extrapolated ✗ |

---

### list_pull_request_files
**Response shape:** Flat array of file objects  
**Note:** No live fixture; using schema from current config

| Field | Verdict | Justification | In Live Data |
|-------|---------|---------------|--------------|
| filename | **KEEP** | File identification | Assumed ✓ |
| status | **KEEP** | Indicates added/modified/deleted | Assumed ✓ |
| additions | **KEEP** | Change size signal | Assumed ✓ |
| deletions | **KEEP** | Change size signal | Assumed ✓ |
| changes | **KEEP** | Total change count; redundant but harmless | Assumed ✓ |
| patch | **KEEP** (limited) | Unified diff; essential for code review. 3000 char is reasonable limit. | Assumed ✓ |

---

### search_code
**Response shape:** `{total_count, incomplete_results, items: [...]}`  
**Live data:** ✓ Confirms wrapper + nested repository per item

| Field | Verdict | Justification | In Live Data |
|-------|---------|---------------|--------------|
| total_count | **KEEP** | Pagination context; agents need to know result size | ✓ |
| incomplete_results | **KEEP** | Rate-limit indicator; important for reliability | ✓ |
| items | **KEEP** | Result array wrapper | ✓ |
| name | **KEEP** | Filename; crucial for code discovery | ✓ |
| path | **KEEP** | Full path context | ✓ |
| sha | **KEEP** | Commit identification for retrieving full file | ✓ |
| repository | **KEEP** (limited) | Repo context; keep `id`, `name`, `full_name` but exclude all the _url fields | ✓ |
| text_matches | **KEEP** (limited) | Search context highlights; limit to 3 matches, 500 char per fragment | ✓ |
| score | **EXCLUDE** | Search relevance signal; not actionable by agents; always excluded | ✓ (excluded) |
| url, git_url, download_url | **EXCLUDE** | Verbose REST URLs; can reconstruct from name, path, repository | ✓ (marked excluded) |
| html_url | **EXCLUDE** | Can be reconstructed: `github.com/{full_name}/blob/{sha}/{path}` | ✓ (marked excluded) |

**Revised include:** `[total_count, incomplete_results, items]` at top level; field-level controls for nested data.

---

### get_file_contents
**Response shape:** Single file object with base64 encoded content  
**Live data:** ✓ Confirms structure

| Field | Verdict | Justification | In Live Data |
|-------|---------|---------------|--------------|
| type | **KEEP** | File vs. directory distinction | ✓ |
| name | **KEEP** | Filename | ✓ |
| path | **KEEP** | Full path context | ✓ |
| size | **KEEP** | Size signal; helps agent decide if worth downloading | ✓ |
| sha | **KEEP** | Commit identification | ✓ |
| content | **KEEP** | The actual file content (base64 encoded) | ✓ |
| encoding | **KEEP** | Decoding hint | ✓ |
| url, git_url, html_url, download_url | **EXCLUDE** | Verbose REST URLs; reconstructible | ✓ (implied) |
| _links | **EXCLUDE** | Never useful; always excluded by wildcard | ✓ (implied) |

---

### list_commits
**Response shape:** Flat array of commit objects  
**Live data:** ✓ Confirms array format

| Field | Verdict | Justification | In Live Data |
|-------|---------|---------------|--------------|
| sha | **KEEP** | Commit identification | ✓ |
| commit | **KEEP** | Nested object with message, author, committer; essential context | ✓ |
| author | **KEEP** | User info for commit; may differ from committer (cherrypicks, etc.) | ✓ |
| committer | **KEEP** | User info; tracks who applied the commit | ✓ |
| message | **LIMIT** | Commit message in `commit.message`; 200 char limit balances context vs. tokens | ✓ |
| html_url | **EXCLUDE** | Can be reconstructed: `github.com/{repo}/commit/{sha}` | ✓ |

---

### get_commit
**Response shape:** Single commit object with stats and files  
**Live data:** ✓ Confirms structure with nested `commit`, `author`, `committer`, `stats`, `files`

| Field | Verdict | Justification | In Live Data |
|-------|---------|---------------|--------------|
| sha | **KEEP** | Commit identification | ✓ |
| commit | **KEEP** | Full commit metadata including message | ✓ |
| author | **KEEP** | Author context | ✓ |
| committer | **KEEP** | Committer context | ✓ |
| stats | **KEEP** | Summary of `additions`, `deletions`, `total` | ✓ |
| files | **KEEP** (limited) | File-by-file changes; limit array to 50 files | ✓ |
| message | **LIMIT** | Full message; 500 char limit for single commit context | ✓ |
| patch | **LIMIT** | Unified diff per file; 2000 char limit | ✓ |
| html_url | **EXCLUDE** | Can be reconstructed | ✓ |

---

### list_repository_contents
**Response shape:** Flat array of file/dir objects  
**Note:** No live fixture; directory listing endpoint

| Field | Verdict | Justification | In Live Data |
|-------|---------|---------------|--------------|
| name | **KEEP** | File/dir name | Assumed ✓ |
| path | **KEEP** | Full path | Assumed ✓ |
| type | **KEEP** | file/dir distinction | Assumed ✓ |
| size | **KEEP** | Size signal | Assumed ✓ |
| sha | **KEEP** | Hash for retrieval | Assumed ✓ |

---

### search_repositories
**Response shape:** `{total_count, incomplete_results, items: [...]}`  
**Live data:** ✓ Confirms wrapper structure; items have repo metadata

| Field | Verdict | Justification | In Live Data |
|-------|---------|---------------|--------------|
| total_count | **KEEP** | Result count; pagination context | ✓ |
| incomplete_results | **KEEP** | Rate-limit signal | ✓ |
| items | **KEEP** | Repository array | ✓ |
| name | **KEEP** | Repo name | ✓ |
| full_name | **KEEP** | owner/repo needed for cloning, PR creation, etc. | ✓ |
| description | **KEEP** (limited) | Repo purpose; 200 char limit balances context vs. tokens | ✓ |
| language | **KEEP** | Tech stack signal | ✓ |
| stargazers_count | **KEEP** | Popularity indicator; relevant for discovery | ✓ |
| forks_count | **KEEP** | Community adoption signal | ✓ |
| open_issues_count | **KEEP** | Activity indicator; helps agents filter stale vs. active repos | ✓ |
| created_at | **KEEP** | Age context | ✓ |
| updated_at | **KEEP** | Recency indicator | ✓ |
| topics | **KEEP** (limited) | Taxonomy; limit to 5 to avoid bloat | ✓ |
| private | **KEEP** | Access restriction flag | ✓ |
| fork | **KEEP** | Fork status; context for contribution discussions | ✓ |
| archived | **KEEP** | Maintenance status; agents should skip archived repos | ✓ |
| default_branch | **KEEP** | Branch to clone; often not `main` | ✓ |
| html_url | **EXCLUDE** | Can be reconstructed: `github.com/{full_name}` | ✓ |
| id, node_id | **EXCLUDE** | Internal identifiers; never needed by agents | ✓ |
| All *_url fields | **EXCLUDE** | Verbose REST URLs; no agent use case | ✓ (in data) |

**Revised include:** `[total_count, incomplete_results, items]` at top level.

---

## Summary of Key Changes

### Structural Fixes
1. **list_issues** - Remove `include` filter entirely; use field-level limits instead since response is wrapped (`{issues, totalCount, pageInfo}`)
2. **search_code** - Confirm top-level `[total_count, incomplete_results, items]` and limit nested repository + text_matches
3. **search_repositories** - Confirm top-level structure, remove verbose fields from repository items

### Field Inclusions/Exclusions
- Add `merged` to **list_pull_requests** for explicit state confirmation
- Remove `milestone` from **get_pull_request** (can use head/base for sprint context)
- Keep `body` on **get_issue** and **get_pull_request** but with appropriate string limits (5000 and 2000 respectively)
- Exclude all `*_url` fields globally where not critical
- Exclude `score` from search results (never actionable)
- Exclude all `node_id` fields (internal GraphQL identifiers; not needed)

### Array/String Limits
- `text_matches`: 3 matches per item in search results
- `fragment` (text match context): 500 chars
- `patch` (file-level diffs): 2000-3000 chars depending on tool
- `message` (commit): 200-500 chars depending on context (list vs. full)
- `description` (repo): 200 chars
- Standard node limits: labels 5, assignees 3, topics 5, files 50

---

## Philosophy Alignment

These changes align with the core principle: **"If a developer could plausibly use a tool once a week, include it."**

- **Include body/description with limits** - Agents often need summary context to decide on follow-up actions; including trimmed versions avoids a second API call and saves tokens overall
- **Exclude _url fields** - Agents never need these; URLs can be reconstructed or aren't actionable
- **Keep state/status fields** - Essential for every decision workflow
- **Preserve nested context** (head/base, author/committer) - Developers need this to understand changes without fetching again
- **Limit highly repetitive arrays** (labels, assignees) - Real-world data rarely exceeds these limits anyway
