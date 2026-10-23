# Default Configuration Philosophy

This document explains the principles behind mini's bundled projection configs. Anyone adding or updating a bundled default should read this first.

---

## Tool visibility

mini ships no visibility defaults: every upstream tool is visible, and none is protected. Approval is the client's job, and mini passes each tool's `readOnlyHint` and `destructiveHint` through unchanged so the client can use them. Users who want a tool hidden or protected list it in their own server file:

```yaml
permissions:
  hidden: [delete_file]
  protected: [merge_pull_request] # compact mode needs perm_call; the CLI needs mini perm-call
```

---

## Projection defaults

Projection configs reduce the token cost of tool responses. The goal is to trim noise while keeping the information agents need for realistic workflows. **When in doubt, keep the field.**

### The bar for exclusion is very high

Every excluded field is a potential loss. Agents use responses for context, and context that seems irrelevant often isn't. Even things like emoji reactions on a comment can carry signal. Exclude a field only when there is a strong, tool-specific reason it is **almost never useful in normal development workflows**. A field name or URL shape alone is not enough; consider the values the tool returns and how an agent could use them.

Keep a field if it could reasonably help during a typical month of development. Repetition is not enough to justify exclusion: a pull request's head repository may be a fork, and its description can explain the source of a change. Preserve descriptions, identities, profile links, revision references, and navigation information unless a specific workflow establishes otherwise.

Fields that clear the bar, once checked against the tool's real values:

- **URL templates**: strings with `{placeholder}` parts, such as `following_url: ".../following{/other_user}"`. They are not usable URLs.
- **GraphQL `node_id`s** on REST responses, when no exposed tool accepts one.
- **Always-empty or internal flags**, such as GitHub's `gravatar_id`, `site_admin` and `user_view_type`.
- **Display-only fields**, such as label colors.
- **Profile avatar URLs**: the account's login, id and profile link remain. This does not justify excluding screenshots, attachments, or other images that carry task context.

Concrete API URLs and label ids do not clear it yet; keep them until agent-flow testing shows they go unused. Never exclude a field the tool lets the caller request by name (for example through a `fields` argument). A null or empty sample does not establish that a field is useless.

Every excluded field that matched is listed in the response's `__mini` notice. On a small single-object response, a few excludes can add more bytes than they remove; measure, and prefer string limits there.

### List vs get vs write operations have different needs

**List operations** (returning many items): these responses can contain dozens of objects, so excludes save the most here. Keep what an agent needs to pick an item without fetching it individually.

**Get/detail operations** (single resource): minimal projection with generous `string_limits`. Don't use `include_only`; it's too easy to drop a field the agent needs.

**Write/mutate operations** (create, update, delete): the response typically confirms what was done, including the created resource. Projection needs are minimal — the response is usually already compact, and the agent needs the identifier (id, number, sha, html_url) to reference what it just created. Apply the same rules as get operations.

### Array limits

Bundled defaults preserve returned collections, including labels, code matches, changed files and search results. These items can be relevant even when there are many. Let the caller choose the upstream page size and follow pagination. Do not silently shrink a requested page or discard labels to reduce response size.

### String limits

String limits should be **liberal**. The purpose is to prevent runaway responses (multi-megabyte issue bodies, enormous diffs), not to aggressively compress content. Cutting a body at 300 chars is too short — the agent may not have enough context and will make a second API call anyway, costing more tokens overall.

Every string without a limit is capped by the global `default_string_limit` (2000 chars). A tool rule exists to raise that where content is longer, or to set a deliberate value; don't add a rule that only tightens short fields.

Rough guidance:
- **Content of one item in a list** (issue body, comment, commit message): no rule; the default applies, and the agent opens the item when it needs the rest
- **A single item or a whole document** (issue or PR body, Jira description, Confluence page body, a page of Slack messages returned as one string): 25000 chars. The agent nearly always needs all of it, so a cut only adds a `read`. Above ~25K, clients cut the result themselves; see [Large MCP responses](large-mcp-responses.md)
- **Code diffs (patch)**: 5000 chars per file

These are starting points, not rules. If a field is important and the limit is causing second API calls, raise it.

### `include_only` vs `exclude`

**`include_only`** keeps only the listed top-level fields. Use it on a list operation only when you can name everything an agent needs; a missed field is silently gone.

**`exclude`** uses exact dotted object paths relative to the response document root. `user.avatar_url` removes that field while retaining the user object and its other fields; `description` removes only a root-level description. It does not match repository descriptions nested under a pull request. Never exclude content fields (`body`, `description`, `message`); cap them with `string_limits`.

See [Exclude paths](#exclude-paths) for stepping into array items with `[]`. Keep an array when its items contain useful context; do not remove it just to discard metadata on each item.

### `depth_limit`

`depth_limit` stops deeply-nested objects from being included in full. It is a blunt instrument — use it only on named per-tool configs where you've seen the real response shape and know that deep nesting is noise. Do not set it in the `"*"` wildcard; an unlisted tool will silently lose nested data you haven't inspected.

### HTML/Markdown stripping

Stripping is opt-in. There are two mechanisms:

- `strip_markup: true` on a `ProjectionConfig` — strips HTML and Markdown from all content fields in that tool's response.
- `auto_strip_threshold` in the global `~/.mini/config.yaml` — strips content fields globally when their value exceeds the threshold. Disabled by default (0). Enable only if your upstreams consistently embed large blocks of rendered HTML that add no value for agents.

Do not set `auto_strip_threshold` inside a projection YAML — `ProjectionConfig` does not have that field. It belongs in global config only.

### Response shape matters

Some MCPs return flat arrays for list operations; others (particularly GraphQL-based ones like the GitHub MCP's issue tools) return wrapped objects like `{issues: [], totalCount, pageInfo}`. `include_only` operates at the **top level** of the response. A list like `[number, title, state]` on a `{issues: []}` response would drop the `issues` array entirely.

Before writing a projection config:
1. Fetch a live raw response (`mini call -r <server> <tool>`) to see the actual shape.
2. Match `include_only` to top-level keys if you use it.
3. For wrapped responses, either include the wrapper key (`issues`, `items`, `nodes`) or don't use `include_only`.

### Exclude paths

`exclude` paths are exact object fields from the response root, separated by dots. `user.avatar_url` removes that one field and keeps the rest of `user`; `description` removes only a root-level description. A path never crosses an array on its own: `[]` steps into every item. Use `[].author.avatar_url` for a flat array of commits, `items[].author.avatar_url` for one wrapped in `items`, and `groups[].members[].avatar_url` for nested arrays. Items where the path doesn't fit, such as a null author, are left alone. A path must end in a field name; to drop a whole array, name its property (`items`). Array indexes, wildcards and jq expressions are rejected.

Quote any path containing `[]` in YAML. Unquoted, `- [].author.avatar_url` and `exclude: [items[].x]` are YAML syntax errors, and the whole server file fails to load:

```yaml
list_commits:
  exclude:
    - "[].author.avatar_url"
    - "[].committer.avatar_url"
```

### Write operation responses

When an agent creates a PR, posts a comment, or pushes files, the response typically returns the created resource in the same shape as the corresponding read operation. Project write operation responses the same way as get operations — the agent needs the full confirmation, including the identifier (number, sha, html_url) to reference what it just created.

### Building a config for a new server

1. Fetch live raw responses for each tool: `mini call -r <server> <tool>`
2. For each proposed exclusion, identify the tool-specific workflow reason it is almost never useful during a typical month of development; do not infer that from a field name or a null sample.
3. Use `include_only` only for list operations, and only when you can name everything an agent needs
4. Set string limits generously, using the guidance above
5. For each tool, justify API URL, URL template or node ID exclusions against the workflow and the information retained
6. Add `depth_limit: 2-3` only on specific tools where you've confirmed deep nesting is noise
7. Use `strip_markup: true` on a specific tool only if the response embeds raw HTML that adds no value

### Validating configs

Inspect live payloads to confirm actual response shapes and add regression checks for the projection behavior that matters. Payload samples do not establish that a field is never useful, especially when values are null or collections are empty. Validate workflow outcomes separately with agent-flow evals; token savings are not proof of task success.

---

## Overriding defaults

Use the MCP `config` tool with `action: get_projection` to read a tool's current rule, then `action: set_projection` to replace it mid-session, or edit `projections:` in `~/.mini/servers/<server>.yaml` directly.

Bundled projections are only applied when `mini add` first installs the server and the user has not set projections. They are never re-applied on subsequent runs.
