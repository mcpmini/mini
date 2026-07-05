# Forge — what it is and where it's going

## What Forge is

Forge is a local code runner built for coding agents. Agents are often slow and token-hungry at
multi-step work because every tool call and every intermediate result has to pass back through
the model. Forge lets the agent write a small TypeScript program and run it locally instead: the
program composes tools, libraries, and APIs, keeps the in-between values inside the sandbox, and
returns only the useful result.

The hypothesis, kept simple: for multi-step agent work, model-written TypeScript running locally
uses fewer turns, less context, and less wall-clock time than a series of direct tool calls. Each
stage is built and dogfooded to test whether that's true in practice.

Forge is an open-source developer tool that runs on the developer's own machine. It is not a
hosted service, a team product, or a workflow marketplace.

## Forge and mini are separate tools

- **mini** is an MCP proxy focused on reducing the context and token cost of MCP tools and their
  responses.
- **Forge** is a sandboxed code runner for agents, usable over MCP or a CLI, with security as a
  first-class concern.

They compose rather than depend on each other. Forge is itself an MCP server, so mini can add it
as an upstream like any other tool source. For now Forge lives inside the mini codebase because
that's a convenient place to prototype — mini already has a working MCP server. When and how Forge
becomes its own binary/repo, and whether the two ever share a small common library, are questions
to answer once there are two real consumers, not to design up front. Meanwhile the Forge code is
kept from assuming mini specifics: paths, config location, and tool access are passed in by the
host.

## The runtime

Each program runs in a fresh Deno subprocess. Deno fits well: agents write TypeScript reliably,
it runs without a build step, npm and JSR packages work, and filesystem/network/env/subprocess
access are all denied by default. A fresh subprocess per run means no state leaks between runs.
Deno's sandbox is a practical safety boundary for a local developer tool — not a hardened
multi-tenant VM boundary, and we don't pretend otherwise.

## The capability ladder

Capability is added one deliberate step at a time; each step keeps every earlier restriction. See
the per-stage docs for detail. Stages 1–5 are built (stage 4 is being reshaped); the next stage is
not started.

1. Run code · 2. Use libraries · 3. Reach the internet and use secrets · 4. Call your connected
tools · 5. Work with your files.

**Next: reuse and composition (not being built yet).** The larger opportunity is procedural
memory for agents: saving successful programs, naming and discovering them, sharing helper code,
and composing one Forge program from another — so agents stop rewriting the same logic every
session. This is only worth building once the earlier stages prove useful and the approval story
below exists, because saved, reusable programs that carry authority are exactly what needs
careful human sign-off. Treat it as its own design pass when the time comes.

## Where the safety story is going: a policy framework

Today, the only thing that grants authority is the owner's config (the net/env/file allowlists),
and the agent holds none of it — that principle stays. But config allowlists are global and
coarse: they say what *any* program may do, not what *this* program is doing right now. As Forge
grows, the intended direction is a **policy framework** with a human in the loop:

- Each run carries a declared policy — the folders, hosts, env vars, and tools it intends to use.
- Forge evaluates and enforces that policy.
- Unless the owner has opted into a deliberately permissive mode, a human sees the program *and*
  its declared policy in plain language and approves, denies, or narrows it — and that approval
  (or a matching owner-defined global policy) is what confers the authority. The agent only ever
  *proposes*; approval grants.
- Owners could define global policies for routine, pre-approved work, and there's room for the
  agent to check "would this be allowed?" before it even writes the program.

This is the honest answer to the gaps the per-stage docs flag — an allowed host that can carry
data outward, sandbox code driving tools unattended, a saved program keeping authority a newer
version shouldn't. It's a direction, not a built feature, and it needs its own design pass with
real user stories. Until it exists, the config allowlists remain the whole authority model, and
the docs say so plainly rather than pretending a single control is a full boundary.

Guiding posture throughout: deny ambient access by default; add capability deliberately; keep
credentials out of generated code where practical; make new authority visible and explain it in
plain language, not jargon; and treat the agent's own explanations as untrusted — enforcement
lives in Forge and the systems underneath, never in the model's say-so.
