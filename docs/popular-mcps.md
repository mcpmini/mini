# Popular MCPs for Developer Workflows

Research sources used (April 2026):
- **PulseMCP** — estimated weekly GitHub repo visitors (best signal for local/self-hosted MCPs)
- **Smithery** — active connections on their hosted platform (best signal for remote MCPs)
- **npm weekly downloads** — from Glama's "State of MCP 2025" report

These sources measure different things. Playwright is #1 by all measures because it's installed locally by millions. Exa tops Smithery because it's a hosted API. Neither tells the full story alone. Combined, they give a reasonable picture.

## Top 30 for Developer Workflows

### Tier 1 — Universal (every dev team)

| # | Server | Est weekly visitors | Config status |
|---|---|---|---|
| 1 | GitHub | 115K (Pulse) | ✅ projections + permissions |
| 2 | Atlassian (Jira + Confluence) | 292K (Pulse) | ✅ projections + permissions |
| 3 | Slack | 10K (Smithery) | ✅ projections + permissions |
| 4 | Notion | ~2K (Smithery) | ✅ projections + permissions |
| 5 | Linear | ~1K (Smithery) | ✅ projections + permissions |

### Tier 2 — Widely used, high value for mini

| # | Server | Est weekly visitors | Config status |
|---|---|---|---|
| 6 | Figma | 104K (Pulse) | ✅ projections + permissions |
| 7 | GitLab | — | ✅ projections + permissions |
| 8 | Sentry | 600 (Smithery) | ✅ projections + permissions |
| 9 | Exa Search | 57K (Smithery) | ❌ needs config |
| 10 | Tavily Search | 109K (Pulse) | ❌ needs config |
| 11 | Brave Search | 12K (Smithery) | ✅ projections + permissions |

### Tier 3 — Growing fast, high signal

| # | Server | Est weekly visitors | Config status |
|---|---|---|---|
| 12 | PostHog | 166K (Pulse) | ❌ needs config |
| 13 | Datadog | major enterprise, not well indexed publicly | ❌ needs config |
| 14 | PagerDuty | not indexed, widely used in oncall | ❌ needs config |
| 15 | n8n | 77K (Pulse) | ❌ needs config |
| 16 | Supabase | 7K (Smithery) | ❌ needs config |
| 17 | Stripe | widely used, not indexed | ❌ needs config |

### Tier 4 — Testing & DevX

| # | Server | Est weekly visitors | Config status |
|---|---|---|---|
| 18 | Storybook | 604K (Pulse) | ❌ (small responses, low value) |
| 19 | Cypress Cloud | 179K (Pulse) | ❌ needs config |
| 20 | Playwright | 1.5M (Pulse) | no config needed — simple responses |
| 21 | Chrome DevTools | 1.4M (Pulse) | no config needed |

### Tier 5 — Data & Cloud

| # | Server | Est weekly visitors | Config status |
|---|---|---|---|
| 22 | DuckDB | 899K (Pulse) | ❌ needs config (SQL results can be huge) |
| 23 | Google Sheets | 51K (Smithery) | ❌ needs config |
| 24 | Gmail | 37K (Smithery) | ❌ needs config |
| 25 | Google Drive | 8K (Smithery) | ❌ needs config |
| 26 | AWS Documentation | 352K (Pulse) | no config needed — already curated docs |
| 27 | Vercel | not indexed | ❌ needs config |

### Tier 6 — Worth tracking

| # | Server | Notes |
|---|---|---|
| 28 | Cloudflare | Workers, Pages, D1 — growing enterprise use |
| 29 | Google Cloud Monitoring | 185K Pulse — ops/SRE |
| 30 | Context7 | 572K Pulse, already connected — no config needed |

## Key findings from research

**What Smithery undercounts:** GitHub, Slack, Jira, Linear are used locally via Claude Desktop / Claude Code config, not via Smithery's hosted platform. Their real usage is much higher than Smithery suggests.

**What PulseMCP shows:** Most-visited repos skew toward Microsoft/Google official servers (Playwright, Chrome DevTools, GKE) which are installed locally by huge numbers of devs.

**Where mini adds the most value:** Tools with large, noisy JSON responses benefit most from projection configs. The highest-value targets are:
1. Atlassian — Jira issue metadata is extremely noisy (renderedFields, changelogs, etc.)
2. GitHub — PR/issue lists include dozens of URL fields per item
3. Slack — conversation history includes block payloads, reactions, bot profiles
4. Notion — block trees are deeply nested and verbose

**No-config-needed MCPs:** Playwright, Filesystem, Git, Context7, Sequential Thinking — these return focused, already-small responses. Adding projection configs would be premature.

## Next wave to build (in priority order)

1. **Exa Search** — highest Smithery connections after consumer tools, used heavily in research agents
2. **Tavily Search** — 109K Pulse, official, widely recommended
3. **PostHog** — 166K Pulse, official, product analytics in many dev stacks
4. **Datadog** — major enterprise monitoring, official MCP exists (not well indexed)
5. **DuckDB** — 899K Pulse, SQL result sets can be enormous, high projection value
6. **Google Sheets** — 51K Smithery, large spreadsheet data benefits from projection
7. **Gmail** — 37K Smithery, email threads need trimming
8. **Stripe** — payments data common in dev workflows
9. **Supabase** — growing fast, combines DB + auth + storage
10. **Vercel** — deployment logs and preview URLs are common in dev CI/CD
