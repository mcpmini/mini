# MCP connection lifecycle, and where mini sits in it

How an MCP client connects to a server, what the spec mandates, how agents behave at a high
level, and how mini — which is both a server (to the agent) and a client (to upstreams) — fits
between them.

## The spec lifecycle

Source of truth: `~/proj/modelcontextprotocol`, `docs/specification/<version>/basic/lifecycle.mdx`
and `schema/<version>/schema.ts`. Behavior below is stable across 2024-11-05, 2025-03-26,
2025-06-18, 2025-11-25, and draft unless noted.

The lifecycle has three phases. Initialization is the one that matters here.

1. **Initialization** — *"MUST be the first interaction between client and server."* The client
   **MUST** initiate by sending an `initialize` **request** (a JSON-RPC request, method
   `initialize` — not a tool call). The server replies with its capabilities, and the client
   confirms with a `notifications/initialized` notification.
2. **Operation** — `tools/list`, `tools/call`, `resources/*`, etc.
3. **Shutdown** — transport close.

The opening exchange, in order:

```
1. client → server   initialize (request)          ← the very first interaction
2. server → client   InitializeResult
3. client → server   notifications/initialized      ← notification, no response
4. client → server   tools/list                     ← discovery; operation phase begins
   ...
N. client → server   tools/call                     ← only when the model runs a tool
```

`tools/call` is unrelated to `initialize`. The first message on the wire is always the
`initialize` request; tool *calls* come much later, if at all.

### What InitializeResult can carry

```ts
interface InitializeResult extends Result {
  protocolVersion: string;
  capabilities: ServerCapabilities;
  serverInfo: Implementation;
  instructions?: string;   // the only model-facing free text
  _meta?: { [key: string]: unknown };
}
```

- `instructions` is the only channel the spec routes *to the model* — *"thought of like a
  'hint' to the model… MAY be added to the system prompt."* Sent once, at handshake.
- There is **no dedicated warnings/issues field**. A JSON-RPC `error` on `initialize` is for
  hard failure only (protocol-version mismatch, failed capability negotiation, timeout) and
  **fails the whole connection** — not a soft "one thing is degraded" signal.
- `_meta` is free-form but clients ignore unknown keys; not model-facing.

### What the InitializeRequest carries (and does not)

```ts
interface InitializeRequestParams { protocolVersion; capabilities; clientInfo; }
```

No timeout/deadline field, in any version. **The client's connect timeout is private and
invisible to the server.** A server cannot read it and must not depend on its value.

### Timeouts and ping (spec guidance)

- §Timeouts: implementations **SHOULD** establish timeouts on all sent requests and **SHOULD**
  always enforce a maximum, to prevent hung connections and resource exhaustion.
- §Ping: an **optional** `ping` request/response (either party may send it). The spec says
  implementations **SHOULD** periodically ping to detect connection health, frequency
  configurable. It is all SHOULD/MAY — nothing is MUST.

## How agents connect (high level)

Coding agents that consume MCP — Codex (open source) and Claude Code among them — follow the
same broad shape at startup, and it informs how mini should behave:

- **Each server is connected independently and in parallel** (with some bound on how many at
  once), each running its own `initialize` → `tools/list` handshake. One slow or dead server
  does not block the others.
- **Each connection is time-boxed** with a per-server connect timeout on the order of tens of
  seconds, and a failed or timed-out server is **isolated**: it's marked unavailable and
  startup continues. A single bad server does not sink the session.
- **Neither pings to check liveness.** A dead server is detected *reactively* — when its
  transport drops or a later call fails — not by a periodic health check.
- **Failures surface to the user, not the model.** Dead-server state shows up in the client's
  UI; the agent/model just sees the affected tools quietly disappear, or gets an error when it
  calls a tool on a server that has gone away.

Codex is open source; its specifics (the per-server startup timeout, parallel task fan-out,
and how a required vs. optional server is treated) are documented with permalinks in
[codex-mcp-loading.md](codex-mcp-loading.md). For other agents, treat the four behaviors above
as the contract mini should be compatible with.

The two takeaways that drive mini's design:

1. Agents treat each upstream connection as **independent, time-boxed, and non-fatal**.
2. Agents do **not** tell the model when a server dies — tools just vanish or fail on call.

## Where mini fits: two layers, two `initialize`s

mini is a proxy, so it sits in the middle of **two** independent lifecycles:

- **Layer 1 — agent ↔ mini.** The agent is the client; mini is the server. The agent's first
  interaction is a Layer-1 `initialize` request to mini.
- **Layer 2 — mini ↔ upstreams.** mini is the client; github/sentry/etc. are the servers. mini
  sends its own Layer-2 `initialize` + `tools/list` to each upstream.

Each layer is a full, separate handshake. They are easy to conflate but are different messages
on different connections.

### Connection modes

- **Standalone** (`mini serve --standalone`, or `--http`): the agent spawns mini directly; mini
  serves Layer 1 over stdio (and optionally HTTP).
- **Daemon** (default): the agent spawns a thin `mini` **proxy**, which connects to a shared,
  long-lived **daemon** over HTTP. The proxy bridges agent stdio ↔ daemon HTTP. The Layer-1
  `initialize` is *forwarded* by the proxy to the daemon, not answered locally.

### Layer-2 upstream connect is eager, at boot

In both modes mini connects every upstream **up front**, before serving any Layer-1 request —
not lazily on first tool call. `buildAndConnectServer` dials each upstream and runs its Layer-2
`initialize` + `tools/list`, and only after that does mini start serving.

### Cold-start timeline (daemon mode)

```
agent launches
 └─ spawns `mini` proxy
     └─ daemon.Start(..., 3s): no daemon → spawn daemon, poll /healthz for ≤3s
          DAEMON BOOT (runDaemon):
            buildAndConnectServer:
              ├─ dial github   → Layer-2 initialize → tools/list   ┐ eager, at boot,
              ├─ dial sentry   → Layer-2 initialize → tools/list   │ no client involved,
              └─ dial atlassian→ Layer-2 initialize → tools/list   ┘ currently SERIAL
            ── only now ── start HTTP server → /healthz live
     └─ proxy reaches healthy daemon within 3s → connects
 └─ agent sends Layer-1 initialize → proxy → daemon → reply (from boot-loaded state)
    then tools/list → tools collected at boot
```

- **Warm daemon** (steady state): `/healthz` is already up; the agent's Layer-1 `initialize`
  round-trips in milliseconds. No issue.
- **Cold start** (first connect, daemon death, config reload): the daemon serves `/healthz`
  only *after* every Layer-2 handshake completes, and the proxy waits just **3 seconds**. The
  binding budget on cold start is this 3s, not the agent's connect timeout.

### The failure mode

Today the Layer-2 connect is **serial and unbounded for stdio** (no handshake timeout) and runs
*before* mini serves Layer 1. So one slow or hung upstream blocks mini from answering the
agent's first interaction:

- Hung stdio upstream → `buildAndConnectServer` never returns → daemon never serves `/healthz`
  → proxy gives up at 3s → falls back to standalone, which re-runs the same eager connect and
  hangs again → the agent's Layer-1 `initialize` is never answered → the agent hits its own
  connect timeout and concludes **mini itself is dead**, losing every tool from every upstream.

A single bad upstream takes down the whole proxy — the opposite of the per-upstream isolation
agents already expect. This also violates the spec's §Timeouts SHOULD. Tracking and fix:
[issue #33](https://github.com/mcpmini/mini/issues/33).

### How mini should fit (target shape)

mini should give the agent the isolation it assumes, and — because it sits in the middle —
surface upstream health to the *model*, which the agents do not:

- **Decouple serve from connect.** Start the HTTP server / `/healthz` (daemon) or `Serve`
  (standalone) *before* connecting upstreams, so Layer 1 is answered immediately with zero
  upstreams required. Connect Layer 2 in the background, in parallel, each time-boxed; announce
  tools as they arrive via `notifications/tools/list_changed` (capability already advertised).
- **Per-upstream connect timeout** then bounds only "when do we mark a server degraded," not a
  race against any client deadline.
- **Surface degraded upstreams to the model**: fold them into the Layer-1 handshake
  `instructions` (the one model-facing channel that works in proxy mode) and into
  `config{action:"status"}`, with re-auth hints (`mini auth <server>`).
