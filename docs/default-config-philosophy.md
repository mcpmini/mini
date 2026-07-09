# Default Configuration Philosophy

This document explains the principles behind mini's bundled default visibility and projection configs. Anyone adding or updating a bundled default should read this first.

---

## Tool visibility defaults

### The core rule

**If a developer could plausibly use a tool once a week, it should be included.**

That's the bar. Not "could it ever be useful" — lots of things could ever be useful. The question is whether it belongs in a typical weekly agent workflow.

### Visibility tiers

**Visible (open):** Tools a developer might use any week. The visible list should feel like a natural API surface.

**Protected:** Write operations that could reasonably happen weekly. This only changes enforcement in compact mode and the direct CLI, where `call` refuses protected tools and `perm_call` can run them. In proxy mode, client approval settings are the gate.

**Hidden:** Very rarely used tools. They do not appear in normal discovery and do not consume schema tokens. Default stance: if used less than once a month, hide it.

### Applying the rules

1. **Read?** Visible, unless genuinely niche (monthly or less).
2. **Write used weekly?** Protected for compact/CLI users; configure client approvals separately for proxy mode.
3. **Write used rarely** (repo creation, forks, org management)? Hidden.
4. **Platform-specific or admin-level?** Hidden.

### Why this matters

Schema tokens are paid on every conversation. A server with 44 tools where 20 are irrelevant wastes tokens on every single agent turn. Good defaults mean agents start with a clean, focused surface.

---

## Projection defaults

Projection configs reduce the token cost of tool responses. The goal is not to hide information — it's to trim provably-useless noise while keeping everything an agent might legitimately need. **When in doubt, keep the field.**

### The bar for exclusion is very high

Every excluded field is a potential loss. Agents use responses for context, and context that seems irrelevant often isn't. Even things like emoji reactions on a comment can carry signal. A field should only be excluded if you can make a strong, specific case that **it is never useful in any realistic agent workflow** — not just that it's rarely useful, or that you can't think of a use.

The two categories of fields that clear this bar:

1. **URL template strings** — Fields like `archive_url: "https://api.github.com/repos/{owner}/{repo}/{archive_format}{/ref}"` are parameterised REST endpoint templates for API clients to construct URLs. They are not real URLs and have no agent use case. Exclude these.
2. **Deprecated or empty fields** — Fields that are always empty or were removed by the upstream API (e.g. `gravatar_id` on GitHub, which has been empty for years).

Everything else needs a genuine justification.

### List vs get vs write operations have different needs

**List operations** (returning many items): use an explicit `include` list. These responses can contain dozens of objects, so field selection matters most here. Only include what an agent needs to make a decision without fetching individual items.

**Get/detail operations** (single resource): minimal projection. Use `depth_limit` and generous `string_limits`. Do not use an `include` filter — it's too easy to accidentally exclude fields the agent needs. Let the response come through mostly intact.

**Write/mutate operations** (create, update, delete): the response typically confirms what was done, including the created resource. Projection needs are minimal — the response is usually already compact, and the agent needs the identifier (id, number, sha, html_url) to reference what it just created. Apply the same rules as get operations.

### String limits

String limits should be **liberal**. The purpose is to prevent runaway responses (multi-megabyte issue bodies, enormous diffs), not to aggressively compress content. Cutting a body at 300 chars is too short — the agent may not have enough context and will make a second API call anyway, costing more tokens overall.

Rough guidance:
- **List context body/description**: 1000–2000 chars
- **Get/detail context body**: 5000–10000 chars (enough for almost any real issue or PR description)
- **Code diffs (patch)**: 3000–5000 chars per file
- **Commit messages**: 500 chars in lists, 2000 chars in full commit views
- **Short descriptive fields** (repo description, search fragments): 300–500 chars

These are starting points, not rules. If a field is important and the limit is causing second API calls, raise it.

### Include lists vs exclude_always

**Use `include` lists on list operations.** They define a whitelist of fields to pass through — everything else is dropped. This is the right tool when a response contains many objects and you can clearly enumerate what's needed.

**Use `exclude_always` sparingly, only for field-level exclusion that applies regardless of context.** The only appropriate use is for URL template strings and deprecated fields. Never use `exclude_always` on content fields (body, description, message, etc.) — use `string_limits` instead.

**Never use a wildcard `exclude_always` to broadly suppress categories of fields** — this is too blunt and will silently drop useful data. If a field is noisy in one tool but useful in another, handle it per-tool.

### `depth_limit`

`depth_limit` stops deeply-nested objects from being included in full. It is a blunt instrument — use it only on named per-tool configs where you've seen the real response shape and know that deep nesting is noise. Do not set it in the `"*"` wildcard; an unlisted tool will silently lose nested data you haven't inspected.

### HTML/Markdown stripping

Stripping is opt-in. There are two mechanisms:

- `strip_markup: true` on a `ProjectionConfig` — strips HTML and Markdown from all content fields in that tool's response.
- `auto_strip_threshold` in the global `~/.mini/config.yaml` — strips content fields globally when their value exceeds the threshold. Disabled by default (0). Enable only if your upstreams consistently embed large blocks of rendered HTML that add no value for agents.

Do not set `auto_strip_threshold` inside a projection YAML — `ProjectionConfig` does not have that field. It belongs in global config only.

### Response shape matters

Some MCPs return flat arrays for list operations; others (particularly GraphQL-based ones like the GitHub MCP's issue tools) return wrapped objects like `{issues: [], totalCount, pageInfo}`. The projection `include` list operates at the **top level** of the response. An include list like `[number, title, state]` on a `{issues: []}` response would drop the `issues` array entirely.

Before writing a projection config:
1. Fetch a live raw response (`mini call -r <server> <tool>`) to see the actual shape.
2. Match the `include` list to top-level keys if you use one.
3. For wrapped responses, either include the wrapper key (`issues`, `items`, `nodes`) or use no include list and rely on depth_limit.

### Write operation responses

When an agent creates a PR, posts a comment, or pushes files, the response typically returns the created resource in the same shape as the corresponding read operation. Project write operation responses the same way as get operations — the agent needs the full confirmation, including the identifier (number, sha, html_url) to reference what it just created.

### Building a config for a new server

1. Fetch live raw responses for each tool: `mini call -r <server> <tool>`
2. Look at every field and ask: "Can I make a strong case that no agent would ever want this?"
3. Use `include` lists only for list operations
4. Set string limits generously — 1–5K depending on context
5. Exclude URL template strings (fields with `{placeholder}` syntax) via per-tool `exclude_always`
6. Add `depth_limit: 2-3` only on specific tools where you've confirmed deep nesting is noise
7. Use `strip_markup: true` on a specific tool only if the response embeds raw HTML that adds no value

### Validating configs

Check your config against live fixture data, not just schema files. The actual fields returned by a live MCP can differ significantly from what the API documentation implies — especially for MCPs that use GraphQL internally.

---

## Overriding defaults

Users can always override bundled defaults in their server YAML:

```yaml
permissions:
  protected: []          # remove compact/CLI protected gates
  hidden:
    - delete_file        # add individual tools to hidden
```

For projections, use `mini config action=set_projection` to tune per-tool settings mid-session, or edit `~/.mini/servers/<server>.proj.yaml` directly.

Bundled defaults are only applied when `mini add` first installs the server and the user has not set explicit visibility settings or projections. They are never re-applied on subsequent runs.
