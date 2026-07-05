# Stage 4 — call your connected tools

Branch: `forge-04-mcp-tools` (was `forge-stage4`). Capability: the agent's code can call the MCP
tools the host already has connected, from inside the sandbox.

## What the user gets

Scripts can invoke the tools mini already connects to — GitHub, Linear, Slack, and so on — from
inside the locked box. The big win: fetch 200 pull requests, crunch them locally, and return a
10-line summary, without those 200 huge responses ever passing through the model's context. The
code uses the host's existing connections, so no credentials live in the code.

## Behavior (shipped, being reshaped)

Today the sandbox gets a `mini` global with `mini.list()` and `mini.call(server, tool, params)`,
which reach the host's tools over a small, per-run, token-authenticated local channel (the
"bridge"). The bridge starts fresh for each run and shuts down after. Tool responses come back
raw (unshaped) because they stay inside the sandbox and never reach the model.

**This is being reworked — see requirements below.** The bridge mechanism stays; the guest-facing
API changes.

## Requirements — next build

1. **Replace the `mini` global with an `mcp` global** shaped like the tools themselves:
   `await mcp.github.list_issues({...})` instead of `mini.call("github", "list_issues", {...})`.
   The names match exactly what the agent already knows the tools as, so there's nothing to
   translate. Generated fresh per run from the host's tool list. (This matches the original
   prototype plan, which called for an injected `tools`/`mcp` abstraction; the `mini.list/call`
   shape was a prototype shortcut.)
2. **Hidden tools are absent; permission tiers are ignored.** A hidden tool doesn't appear on the
   `mcp` object and can't be called. The `open`/`protected` distinction is *not* enforced at the
   bridge — that tiering is a mini compact-mode concept that was wired in here without being a
   real decision, and it gave false comfort. Until the policy framework exists (vision doc), the
   honest statement is: any non-hidden tool is callable from sandbox code. Real per-call authority
   is a policy-framework job, not a permission-field job.
3. **Tool metadata by reflection, plus one search helper.** Each callable carries its
   `.description` / `.inputSchema` / `.outputSchema` as properties, so an agent can inspect a
   tool it knows by name with plain property access — no separate "describe" call. For *finding*
   a tool, add `mcp.search("github pull requests")` — a keyword search over the non-hidden tools,
   available only inside the sandbox (never a model-facing tool). Its description should teach
   keyword queries, not natural-language questions.
   - Prerequisite: the host's tool search is currently a whole-phrase substring match, so
     multi-word queries return nothing. It needs to match query words independently and rank by
     how many hit (tool-name matches above description matches) before it can back `mcp.search`.
4. **A wrong call explains itself.** Before dispatching, check the params against the tool's
   input schema; a missing required field (or an unknown field where the schema forbids extras)
   fails fast with the expected-parameter list, so code can correct itself in one step. (This
   already exists as `checkParamsAgainstSchema` — keep it under the new callables.)
5. **Keep the scratch-dir handle off the `mcp` object.** It's a runtime facility, not a tool —
   put it on its own small global (e.g. `forge.tmpDir`). See stage 5.
6. **Teach the idiom in the tool description.** Rewrite the `execute_code` description: call tools
   you know directly via `mcp.<server>.<tool>(...)`; use `mcp.search` to find one; a wrong call
   tells you the right parameters. Drop any "check the schema first" dance.

## Known gaps (accepted / deferred)

- **Sandbox code can drive tools unattended.** Any code in the sandbox — including a dependency
  that turns out to be buggy or tampered with — can call non-hidden tools in a loop, with no
  per-call approval, and the host client never sees those calls to prompt on. This is the central
  reason the policy framework exists. Accepted for now; the framework is the answer, not a
  per-tool config knob.

## Decisions log

Executing agents: append entries here.
