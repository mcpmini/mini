# Test Scenarios

Critical flows in descending importance. Each scenario is a named unit or integration test that should exist and pass.

---

## 1. Proxy mode — tool routing

**Why critical:** The primary production path for Claude Code.

| Scenario | Status |
|---|---|
| Upstream tool exposed as `server__tool` in tools/list | ✅ `TestProxy_ToolsList_ContainsMiniTools` |
| Tool call routes to correct upstream server | ✅ `TestProxy_Call_NoProjection_PassesRawJSON` |
| Unknown tool returns error | ✅ `TestProxy_UnknownTool_ReturnsError` |
| Malformed `server__tool` name returns error | ✅ `TestProxy_NoDoubleUnderscore_ReturnsError` |
| `config` and `read` tools available in proxy mode | ✅ `TestProxy_ToolsList_ContainsMiniTools` |
| Proxy instructions don't mention `perm_call` | ✅ `TestProxy_Initialize_Instructions` |

---

## 2. Per-session proxy mode (daemon path)

**Why critical:** Production path when `mini proxy` connects to daemon. Different from server-level `WithProxyMode()`.

| Scenario | Status |
|---|---|
| `_mini_proxy_mode` in initialize switches session to proxy | ✅ `TestProxy_PerSession_ProxyAndStandardCoexist` |
| Proxy and standard sessions coexist on same server | ✅ `TestProxy_PerSession_ProxyAndStandardCoexist` |
| Proxy session gets proxy instructions | ✅ `TestProxy_Initialize_PerSessionInstructions` |
| Standard session gets standard instructions | ✅ `TestProxy_Initialize_PerSessionInstructions` |
| Standalone `Serve()` inherits server-level proxyMode | ✅ `TestProxy_StandaloneServe_InheritsProxyMode` |
| `_mini_proxy_mode` injected into initialize by proxy client | ✅ `TestInjectProxyMode_initialize_addsFlag` |
| Non-initialize messages not modified by proxy client | ✅ `TestInjectProxyMode_nonInitialize_unchanged` |

---

## 3. Per-session projection — configuration and persistence

**Why critical:** Session projections are the primary way agents customize mini at runtime. Bug here means projections silently don't apply.

| Scenario | Status |
|---|---|
| `set_projection session_only:true` applies format:mini | ✅ `TestProxy_Call_MiniFormat_RendersLines` |
| `set_projection session_only:true` via per-session daemon path | ✅ `TestProxy_Call_MiniFormat_PerSessionProxyMode` |
| Session projection field exclusions applied across multiple calls | ❌ missing |
| Session projection does not affect other sessions | ❌ missing |
| `set_projection session_only:false` persists to disk | ✅ `TestSetProjection_*` (configure tests) |
| `reload` does not wipe session-only projections | ❌ missing |
| Session projection overrides server-level projection | ❌ missing |
| `set_projection` with `nil` projection clears it | ❌ missing |

---

## 4. Field projection — trimming correctness

**Why critical:** The core value of mini. Wrong trimming means agents get bad data.

| Scenario | Status |
|---|---|
| `exclude_always` removes fields from response | ✅ (projection engine tests) |
| `include` list keeps only specified fields | ✅ (projection engine tests) |
| `string_limits` truncates long strings | ✅ (projection engine tests) |
| `array_limits` caps array length | ✅ (projection engine tests) |
| Plain array (not wrapped in object) trimmed correctly | ❌ `projectArray` 0% coverage |
| Wildcard `*` projection applies to all tools on a server | ✅ (server tests) |
| Elided field names reported in envelope | ✅ (envelope tests) |
| Projection on nested objects respects depth limit | ✅ (projection engine tests) |

---

## 5. Response format — proxy mode output tiers

**Why critical:** Four different output paths in `formatProxyEnvelope`; each one changes what agents see.

| Scenario | Status |
|---|---|
| No projection → raw JSON passthrough | ✅ `TestProxy_Call_NoProjection_PassesRawJSON` |
| Projection + small response → `[Projected — ...]\n{json}` | ✅ `TestProxy_Call_WithProjection_Small_BracketNote` |
| Projection + large response → `[Projected — ...]\nFile: /path` | ✅ `TestProxy_Call_WithProjection_Large_FilePath` |
| Projection + large + nothing elided → bare `File: /path` | ✅ `TestProxy_Call_Large_WithProjection_NoNote_FilePathOnly` |
| Truncation appears in bracket note | ✅ `TestProxy_Call_WithTruncation_ProjectionNote` |
| `format:mini` per-tool projection → RenderLines output | ✅ `TestProxy_Call_MiniFormat_RendersLines` |
| `format:mini` via global `ResponseFormat` config | ✅ `TestProxy_Call_GlobalMiniFormat_Respected` |
| `format:mini` via per-session daemon path | ✅ `TestProxy_Call_MiniFormat_PerSessionProxyMode` |
| `mini_read` reads file path from large response | ✅ `TestProxy_MiniRead_ReadsFile` |
| `mini_read` rejects path traversal | ✅ `TestProxy_MiniRead_RejectsPathTraversal` |

---

## 6. Standard mode — 4-tool interface

**Why critical:** Default mode for non-Claude Code clients.

| Scenario | Status |
|---|---|
| `list` returns all tools across connected servers | ✅ (server tests) |
| `list` with query filters results | ✅ (server tests) |
| `call` routes to upstream and returns projected response | ✅ `TestExecuteRoutesToUpstream` |
| `call` on protected tool returns error directing to `perm_call` | ✅ (server tests) |
| `call` on tool with no projection coverage returns error | ✅ (server tests) |
| `perm_call` on open tool with projection returns error | ✅ (server tests) |
| `perm_call` on open tool without projection succeeds | ✅ (server tests) |
| Upstream tool error returned as tool error envelope | ✅ (server tests) |

---

## 7. File response store

**Why critical:** Large responses silently failing to write means agents get truncated/missing data.

| Scenario | Status |
|---|---|
| Response over inline_threshold written to file | ✅ (response store tests) |
| Slim file and raw file both created | ❌ `writeSlimFile`/`writeRawFile` undertested (40-45%) |
| Disk budget enforced via eviction | ❌ `evictOvershoot` only 25% covered |
| TTL cleanup removes expired files | ✅ `evictExpired` covered |
| Concurrent writes don't corrupt files | ❌ missing concurrent write test |
| File paths stay within store directory (symlink safety) | ✅ `TestProxy_MiniRead_RejectsPathTraversal` |

---

## 8. add_server / remove_server

**Why critical:** Runtime server management. Bugs here affect multi-agent setups.

| Scenario | Status |
|---|---|
| `add_server` HTTP transport adds tools to registry | ✅ (configure tests) |
| `add_server` SSRF blocks private IPs | ✅ `TestSSRF_*` |
| `add_server` strips auth/headers from agent-provided config | ✅ (configure tests) |
| `remove_server` removes tools from registry | ✅ (configure tests) |
| Concurrent `add_server`/`remove_server` race (generation counter) | ✅ `TestServerOpMu_*` |
| `add_server` then immediate `remove_server` → server stays removed | ❌ missing (TOCTOU regression test) |
| `tools/list_changed` notification sent after add/remove in proxy mode | ✅ `TestProxy_NotifyAll_OnRemoveServer` |

---

## 9. Reconnect and reliability

**Why critical:** Long-running agent sessions must survive upstream hiccups.

| Scenario | Status |
|---|---|
| Transport error triggers reconnect loop | ✅ (reconnect tests) |
| RPC error does not trigger reconnect | ✅ (reconnect tests) |
| Context cancellation does not trigger reconnect | ✅ (reconnect tests) |
| Reconnect succeeds and tools remain accessible | ✅ (reconnect tests) |
| `MaxPendingRequests` semaphore blocks excess concurrent calls | ✅ (upstream tests) |

---

## 10. Security boundaries

**Why critical:** These protect the local machine.

| Scenario | Status |
|---|---|
| SSRF: private IP rejected in `add_server` | ✅ (configure tests) |
| SSRF: loopback rejected | ✅ (configure tests) |
| SSRF: `.local`/`.internal` hostnames rejected | ✅ (ssrf tests) |
| DNS rebinding: cross-origin POST returns 403 | ✅ `TestHTTP_CrossOrigin_Rejected` |
| Server name validated at all input boundaries | ✅ (config tests) |
| `dangerous_allow_runtime_stdio` required for stdio add_server | ✅ (configure tests) |
| Path traversal in `mini_read` rejected | ✅ `TestProxy_MiniRead_RejectsPathTraversal` |
| `read` symlink escape rejected | ✅ (path tests) |

---

## Known coverage gaps to address

Priority order:

1. **Per-session projection exclusions across calls** — verify field exclusions in session projection survive multiple HTTP requests (not just format:mini)
2. **Session isolation** — two sessions with conflicting projections don't interfere
3. **`reload` doesn't wipe session projections** — currently untested
4. **`projectArray` (0%)** — plain array (not wrapped) trimming path dead in unit tests
5. **File store write paths** (`writeSlimFile` 45%, `writeRawFile` 40%, `writeExclusive` 54%) — needs more write-path edge case tests
6. **`evictOvershoot` (25%)** — disk budget enforcement barely covered
7. **Concurrent file writes** — no test for two simultaneous large responses
8. **TOCTOU regression** — add a test that `remove_server` wins when racing with `add_server` (generation counter)
