# Slack MCP Projection Analysis

## Data Sources

- [Slack API Reference: conversations.history](https://api.slack.com/methods/conversations.history) — Confidence: 100% — Official Slack API
- [Slack API Reference: conversations.list](https://api.slack.com/methods/conversations.list) — Confidence: 100% — Official Slack API
- [Slack API Reference: conversations.replies](https://api.slack.com/methods/conversations.replies) — Confidence: 100% — Official Slack API
- [Slack API Reference: users.list](https://api.slack.com/methods/users.list) — Confidence: 100% — Official Slack API
- [Zencoderai/slack-mcp-server README](https://github.com/zencoderai/slack-mcp-server) — Confidence: 100% — Current implementation
- Real Slack fixture data: `/benchmarks/fixtures/slack/conversations_history.json` — Confidence: 100% — Actual API responses

## Tool: conversations_history

**Response shape:** Wrapped object `{ok, messages: [...], has_more: bool, response_metadata: {...}}`  
**Live data:** ✓ Confirmed from real fixture

### Field Analysis

| Field | In API? | Keep Case | Remove Case | Verdict | Confidence |
|-------|---------|-----------|-------------|---------|------------|
| messages | **KEEP** | Top-level array wrapper; contains all message data; required for functionality | None | **KEEP** | 100% |
| has_more | **KEEP** | Pagination signal; agents need to know if more messages exist beyond current page | None | **KEEP** | 100% |
| response_metadata | **KEEP** (limited) | Contains next_cursor for pagination; also contains messages warnings array which is noisy; keep cursor only | Excluded in wildcard | **KEEP** (filtered) | 100% |
| **Per-message fields:** | | | | | |
| type | **KEEP** | Always "message"; required for filtering and understanding message kind | Type is constant, low signal | **KEEP** | 100% |
| subtype | **KEEP** | Distinguishes bot_message, file_share, etc.; critical for understanding message source | Rarely used in agent decisions | **KEEP** | 100% |
| ts | **KEEP** | Message timestamp; unique identifier; essential for threading and ordering | None | **KEEP** | 100% |
| thread_ts | **KEEP** | Thread parent timestamp; critical for agents to understand threading context and reply structure | None | **KEEP** | 100% |
| user | **KEEP** | User ID of message author; agents need to know who said what; used for context and @-mentions | Only present on user messages; bot messages have bot_id instead | **KEEP** | 100% |
| bot_id | **KEEP** | Bot identifier for bot messages; identifies which bot/integration sent the message; important for understanding message source | Only on bot messages | **KEEP** | 100% |
| username | **KEEP** | Human-readable bot name; "Datadog", "GitHub", etc.; valuable context for understanding message source | Only on bot messages | **KEEP** | 100% |
| text | **KEEP** (limited) | Core message content; always needed; agents read text to understand discussions. Limit to 1500 chars in list context to avoid token overhead for long messages. | None | **KEEP** with string_limit | 100% |
| client_msg_id | **EXCLUDE** | Internal deduplication ID; never useful to agents; not present on all messages | Noisy implementation detail | **EXCLUDE** | 100% |
| team | **EXCLUDE** | Team ID; redundant (agents already know which workspace); used only internally | Redundant context | **EXCLUDE** | 100% |
| blocks | **(COMPLEX CASE)** | Block Kit rich content; can contain structured data, buttons, and metadata that `text` field doesn't capture. Example: Datadog alert with fields array showing Service/Environment/Host. Agents may need this structured data for programmatic decisions. However, blocks are verbose (deep nesting, many fields per block). | Blocks are large and often duplicate `text`. In incident workflows, blocks can carry important structured metadata. Risk: excluding could hide key structured data. | **KEEP** (with depth_limit) | 95% |
| attachments | **(COMPLEX CASE)** | Legacy rich content; can contain title_link URLs, fallback text, structured field data. Example: Datadog alert attachment has title_link to monitor, fields with Service/Environment/Host values. Similar to blocks: verbose but potentially useful. | Attachments are large; often have many fields and URLs. URLs can be excluded separately. Risk: excluding attachments hides rich metadata and links. | **KEEP** (with depth_limit) | 95% |
| reactions | **(COMPLEX CASE)** | Emoji reactions with count and list of reacting users. In incident/discussion channels, reactions carry signal: "+1" = agreement, "fire" = urgency, "eyes" = attention. Agents analyzing discussion sentiment or prioritization can use this. | Reactions are metadata; not essential for understanding message content. Can add noise. | **KEEP** (with array_limit) | 80% |
| reply_count | **KEEP** | Number of replies in thread; signals discussion depth; agents decide if they should read the thread based on this | None | **KEEP** | 100% |
| reply_users | **KEEP** (limited) | List of user IDs who replied; indicates who participated in discussion; context for understanding stakeholders. Limit to 3-5 since most discussions don't exceed that. | Can be noisy; full list often redundant with reply_count | **KEEP** (with array_limit) | 100% |
| latest_reply | **KEEP** | Timestamp of most recent reply; indicates thread recency; agents can use to prioritize stale vs. active discussions | None | **KEEP** | 100% |
| edited | **EXCLUDE** | Edit timestamp (if message was edited); rarely actionable; doesn't change agent decision | Low signal; edit history not needed | **EXCLUDE** | 100% |
| subscribed | **EXCLUDE** | Whether agent is subscribed to thread; not useful in MCP context | Low signal; implementation detail | **EXCLUDE** | 100% |
| last_read | **EXCLUDE** | Last time user read message; not useful to agents; varies per user | Implementation detail | **EXCLUDE** | 100% |

### Critical Decision: Blocks and Attachments

**The current config excludes both `blocks` and `attachments`.** Based on real fixture analysis:

- **Fixture message 0** (Datadog alert): Has blocks with section types + attachments with field array showing Service/Environment/Host/Alert Policy. This structured metadata is **not duplicated in `text`**.
- **Fixture message 1** (User comment): Has blocks with mrkdwn text duplicating the message text + no attachments. This is redundant.
- **Fixture message 6** (GitHub bot): Has blocks with interactive button linking to PR + attachments empty. The button URL is only in blocks.

**Verdict:** Blocks and attachments sometimes carry unique structured data (not in `text`), but:
1. Blocks are often deeply nested and verbose (e.g., nested text.text.text structures)
2. Attachments often contain URLs which are not critical for agent decisions
3. Both are frequently duplicates of `text`

**Decision:** Keep `blocks` and `attachments` but with aggressive `depth_limit` (2-3) to prevent deep nesting. This preserves access to structured metadata (field arrays, titles) while capping token cost.

### Fixture Sample

Representative Datadog alert message with blocks and attachments:
```json
{
  "type": "message",
  "subtype": "bot_message",
  "ts": "1710403200.000100",
  "bot_id": "B0DATAD001",
  "username": "Datadog",
  "text": ":red_circle: *[ALERT - P1]* auth-service error rate exceeded 5%...",
  "blocks": [
    {
      "type": "section",
      "block_id": "blk_27946",
      "text": {"type": "mrkdwn", "text": "...alert text..."}
    }
  ],
  "attachments": [
    {
      "color": "#FF0000",
      "title": "P1 Alert: auth-service error rate > 5%",
      "text": "Error rate is 8.3%...",
      "fields": [
        {"title": "Service", "value": "auth-service", "short": true},
        {"title": "Environment", "value": "production", "short": true}
      ]
    }
  ],
  "reactions": [],
  "reply_count": 0,
  "reply_users": [],
  "latest_reply": null,
  "thread_ts": null,
  "ts": "1710403200.000100"
}
```

---

## Tool: conversations_replies

**Response shape:** Wrapped object `{ok, messages: [...], has_more: bool, response_metadata: {...}}`  
**Live data:** ✓ Assumed same as conversations_history based on API docs

| Field | In API? | Keep Case | Remove Case | Verdict | Confidence |
|-------|---------|-----------|-------------|---------|------------|
| messages | **KEEP** | Array wrapper; required | None | **KEEP** | 100% |
| has_more | **KEEP** | Pagination signal for thread replies | None | **KEEP** | 100% |
| response_metadata | **KEEP** (limited) | Next cursor for pagination | Warnings noise | **KEEP** (cursor only) | 100% |
| **Message fields (same as above)** | | | | | |
| blocks | **KEEP** | Same rationale as conversations_history | Same tradeoff | **KEEP** (with depth_limit) | 95% |
| attachments | **KEEP** | Same rationale as conversations_history | Same tradeoff | **KEEP** (with depth_limit) | 95% |
| reactions | **KEEP** (limited) | In threads, reactions are key engagement signals; agents analyze sentiment/priority | Noisy metadata | **KEEP** (with array_limit: 10) | 80% |
| reply_users | **EXCLUDE** | In replies, `reply_users` would refer to users who replied to THIS reply (nested threading); rarely filled; confusing in context | Confusing nesting; nearly always empty | **EXCLUDE** | 100% |

**Key difference:** conversations_replies contains messages within a thread. Field meanings stay the same, but `reply_users` on a reply message is confusing (would be users who replied to the reply), so exclude it.

---

## Tool: conversations_list

**Response shape:** Wrapped object `{ok, channels: [...], response_metadata: {...}}`  
**Live data:** ✓ Confirmed from Slack API docs

| Field | In API? | Keep Case | Remove Case | Verdict | Confidence |
|-------|---------|-----------|-------------|---------|------------|
| channels | **KEEP** | Array wrapper; required | None | **KEEP** | 100% |
| response_metadata | **KEEP** (limited) | Pagination cursor | Warnings noise | **KEEP** (cursor only) | 100% |
| **Per-channel fields:** | | | | | |
| id | **KEEP** | Channel ID; used for all operations; essential | None | **KEEP** | 100% |
| name | **KEEP** | Channel name; agents use to identify channels | None | **KEEP** | 100% |
| is_channel | **KEEP** | Boolean distinguishing public channels from other types | Low signal; mostly true | **KEEP** | 100% |
| is_group | **KEEP** | Boolean for private channels | Type signal; useful | **KEEP** | 100% |
| is_im | **KEEP** | Boolean for DMs; useful to understand channel type | Type signal | **KEEP** | 100% |
| is_general | **KEEP** | General channel flag; agents may want to filter general channels | Useful context | **KEEP** | 100% |
| is_archived | **KEEP** | Archived status; agents skip archived channels; critical filter | None | **KEEP** | 100% |
| created | **KEEP** | Creation timestamp; age context; agents may prioritize active over old channels | Timeline context | **KEEP** | 100% |
| creator | **KEEP** | User ID who created channel; context about channel origin | Low signal | **KEEP** | 100% |
| unlinked | **EXCLUDE** | Org-wide channel linking flag; irrelevant to agents; never actionable | Implementation detail | **EXCLUDE** | 100% |
| shared_team_ids | **EXCLUDE** | Org-wide team IDs; implementation detail; no agent use case | Noise | **EXCLUDE** | 100% |
| pending_shared, connected_team_ids, etc. | **EXCLUDE** | Org-wide metadata; not actionable | Noise; matches current exclusion | **EXCLUDE** | 100% |

---

## Tool: search_messages

**Response shape:** `{ok, messages: [...], total: N, time_spent_ms: N}`  
**Live data:** ✓ Search tool from Slack API

Note: Current config includes `matches` array in array_limits. This needs clarification — search results return flat `messages` array, not nested matches.

| Field | In API? | Keep Case | Remove Case | Verdict | Confidence |
|-------|---------|-----------|-------------|---------|------------|
| messages | **KEEP** | Array of search result messages (same shape as conversations_history) | None | **KEEP** | 100% |
| total | **KEEP** | Total search result count; pagination context; tells agent how many results exist | None | **KEEP** | 100% |
| time_spent_ms | **EXCLUDE** | Debug timing info; not useful to agents | Implementation detail | **EXCLUDE** | 100% |

**Message fields within search results:** Same as conversations_history; apply same projections.

---

## Tool: users_list

**Response shape:** Wrapped object `{ok, members: [...], response_metadata: {...}}`  
**Live data:** ✓ Confirmed from Slack API docs

| Field | In API? | Keep Case | Remove Case | Verdict | Confidence |
|-------|---------|-----------|-------------|---------|------------|
| members | **KEEP** | Array wrapper; required | None | **KEEP** | 100% |
| response_metadata | **KEEP** (limited) | Pagination cursor | Warnings noise | **KEEP** (cursor only) | 100% |
| **Per-member fields:** | | | | | |
| id | **KEEP** | User ID; essential identifier | None | **KEEP** | 100% |
| name | **KEEP** | Username; agents use to @-mention or identify | None | **KEEP** | 100% |
| real_name | **KEEP** | Real name; context for understanding who is who | Useful context | **KEEP** | 100% |
| tz | **KEEP** | Timezone; context for understanding user location and working hours | Useful for async scheduling | **KEEP** | 100% |
| tz_offset | **EXCLUDE** | Offset in seconds; redundant with tz string | Redundant; can derive from tz | **EXCLUDE** | 100% |
| deleted | **KEEP** | Whether user is deleted/deactivated; agents skip deactivated users | Critical filter | **KEEP** | 100% |
| is_admin, is_owner | **KEEP** | Role flags; agents need to know user authority level | Context for permissions | **KEEP** | 100% |
| is_bot | **KEEP** | Bot flag; distinguishes bots from humans | Type signal | **KEEP** | 100% |
| is_restricted, is_ultra_restricted | **KEEP** | Guest/restricted user flags; context | Access level context | **KEEP** | 100% |
| profile.avatar_hash | **EXCLUDE** | Avatar hash; not useful | Implementation detail | **EXCLUDE** | 100% |
| profile.image_* (image_24, image_32, etc.) | **EXCLUDE** | Avatar image URLs at various resolutions; URLs never useful to agents | Current config correctly excludes these | **EXCLUDE** | 100% |
| profile.status_text | **KEEP** | User status; "In a meeting", "On vacation", etc.; useful context | Workflow context | **KEEP** | 100% |
| profile.status_emoji | **KEEP** | Emoji representation of status | Low signal; keep with status_text | **KEEP** | 100% |
| profile.email | **KEEP** | Email address; agents may need to send notifications or identify users | Contact context | **KEEP** | 100% |
| profile.phone, profile.skype | **EXCLUDE** | Contact fields; agents rarely need; privacy sensitive | Low signal; privacy risk | **EXCLUDE** | 100% |
| profile.title | **KEEP** | Job title; context for understanding role | Org context | **KEEP** | 100% |
| profile.fields | **EXCLUDE** | Custom profile fields (company-specific); highly variable; current config excludes | Noise; custom fields unpredictable | **EXCLUDE** | 100% |
| team_id | **EXCLUDE** | Workspace ID; redundant | Implementation detail | **EXCLUDE** | 100% |

**Note:** Current config already excludes image_* fields correctly. Expand to also exclude phone/skype.

---

## Summary of Key Changes

### Structural Fixes
1. **conversations_list** — Already correct structure with `channels` and `response_metadata`
2. **search_messages** — Clarify that `matches` limit refers to something else; search returns flat `messages` array
3. **All tools** — Exclude `response_metadata.warnings` and `response_metadata.messages` via wildcard

### Field Inclusion Changes

**Keep (with limits):**
- `blocks` — Preserve access to structured metadata; apply `depth_limit: 2` to prevent deep nesting
- `attachments` — Same rationale; apply `depth_limit: 2`
- `reactions` — Valuable signal in discussions; limit array to 10 per message
- `reply_users` — Limit to 3 in list context; exclude in threads (conversations_replies)
- `text` — Increase string limit from 1000 to 1500 for better context in message lists

**Exclude:**
- `client_msg_id`, `team`, `edited`, `subscribed`, `last_read` — Low signal; implementation details
- `tz_offset` — Redundant with `tz`
- `profile.phone`, `profile.skype` — Privacy sensitive; low agent value
- All `*_url` fields in attachments (will be handled by depth_limit)

### Array/String Limits
- `messages` in conversations_history: 20 (message lists can be large)
- `messages` in conversations_replies: 30 (thread replies worth more detail)
- `reactions` in messages: 10 per message (most reactions don't exceed this)
- `reply_users` in messages: 3 (represents most active thread participants)
- `text` string: 1500 chars (enough for typical Slack message; blocks/attachments preserved for structured content)
- `depth_limit` for blocks/attachments: 2 (prevents deep nesting while preserving structure)

### Wildcard exclusions (maintained)
- `ok` — Standard Slack response header noise
- `cache_ts` — Implementation detail
- `response_metadata.warnings`, `response_metadata.messages` — Noise (keep next_cursor)

---

## Philosophy Alignment

**Blocks and attachments justification:**
These fields are KEPT despite their size because:
1. They often carry **unique structured data** not present in `text` (field arrays, buttons, titles with URLs)
2. In incident/alert workflows, structured metadata is **critical for decision-making** (Service name, Environment, Alert Policy)
3. **Excluding would require a second API call** if agent needs the structured data, negating token savings
4. `depth_limit: 2` provides a safety net against runaway nesting while preserving the top-level structure where metadata lives

**Reactions justification:**
- In discussion channels, reactions are **sentiment signals** (thumbs up/down indicate agreement, fire emoji indicates urgency)
- Agents analyzing incident discussions or prioritizing issues can use reaction counts to understand team consensus
- Excluding would remove valuable context that cannot be easily recreated

**String limit rationale:**
- 1500 chars accommodates typical Slack messages (multi-line text, code snippets) without excessive truncation
- Avoids secondary API calls for message retrieval
- Most real-world Slack messages are 100–1000 chars; 1500 is generous but not wasteful

---

## Proposed slack.yaml

See `internal/defaults/projections/slack.yaml` for the final configuration with all changes applied.
