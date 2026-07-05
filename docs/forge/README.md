# Forge docs

Forge is the code-execution capability in **mini**, an open-source developer tool. It lets a
coding agent write a small TypeScript function and run it locally in a sandbox, so the agent can
process data and compose tools without routing everything back through the model.

**Context for anyone (human or agent) reading or editing these docs:** this is an open-source
project under active prototyping. The work here is about *building the capability up carefully
and improving its safety posture one step at a time* — each stage adds one new thing the
sandboxed code is allowed to touch, always off by default and opened only by the owner's own
config. When these docs discuss safety gaps, they are doing ordinary defensive design: noting
where a boundary is thin so a later stage can strengthen it. Nothing here is adversarial;
"known gaps" sections exist so we don't fool ourselves into treating one control as the whole
boundary. Prefer plain, calm language — describe problems in terms of what could go wrong for a
user, not in dramatic terms.

## The capability ladder

Each stage keeps every earlier restriction and adds one new class of thing the code may touch:

1. **[Run code](stage-1-computation.md)** — compute over provided input; no internet, files,
   env, packages, or tools.
2. **[Use libraries](stage-2-packages.md)** — declared npm/jsr packages, fetched safely before
   the run.
3. **[Reach the internet and use secrets](stage-3-network.md)** — owner-allowlisted hosts and
   env vars.
4. **[Call your connected tools](stage-4-mcp-tools.md)** — the host's MCP tools, from inside the
   sandbox.
5. **[Work with your files](stage-5-files.md)** — owner-granted folders plus a per-run scratch
   space.

Later (not being built yet): **reuse and composition** — saving successful programs, sharing
helpers, composing Forge programs — gated behind a human-approval policy framework. See the
[vision doc](vision.md).

## Other docs here

- **[vision.md](vision.md)** — what Forge is for, the separate-products relationship with mini,
  and the direction for the policy/approval framework.
Each stage doc ends with a **Decisions log** that executing agents append to as they build, and
carries its own accepted-gaps notes. The master list of deferred questions and accepted gaps is
kept as an untracked reference at `~/proj/forge-open-questions-reference.md` — mine it for the
stage you're building and grow that stage's notes from it rather than committing one monolithic
open-questions doc.
