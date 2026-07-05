# Stage 3 — reach the internet and use secrets

Branch: `forge-03-network` (was `forge-stage3`). Capability: the agent's code can call the
internet and use secrets, but only the ones the owner allows.

## What the user gets

The owner can say, in config, "code may call `api.github.com`" and "code may use my
`GITHUB_TOKEN`." Then the agent's scripts can talk to real APIs and SDKs themselves — fetch
from a service, authenticate, post results — but only to the hosts and with the secrets the
owner listed. Everything else stays blocked. The agent never gets to choose what's allowed;
only the owner's config does.

## Behavior (shipped)

- `code_mode.url_allow_list` — hosts (and optional ports, and `*.` subdomain wildcards) the
  code may reach. A bare `*` is refused; unrestricted egress needs the explicit
  `dangerous_allow_any_url: true`.
- `code_mode.env_var_allow_list` — env var names the code may read. Values flow from the
  daemon's own environment into the run; a name that's granted but unset simply reads as
  undefined without failing the run. Values never pass through model context.
- Config entries are never auto-corrected — an invalid entry fails loudly so the owner fixes it.

## Requirements — next build

1. **`--ignore-env` for library compatibility.** Mainstream npm libraries (axios, chalk, pino)
   read environment variables on startup to check for proxy/color/CI settings. Today, reading a
   variable that wasn't granted fails the whole run — even when the variable isn't set — so these
   common libraries crash with a confusing error about a variable no one has ever set. Fix: always
   pass Deno's `--ignore-env` flag, which makes an ungranted variable read as "not set" instead of
   crashing. Granted variables (from `env_var_allow_list`) keep returning their real values.
   Nothing leaks — an ignored read never returns a value. This sets the minimum supported Deno
   version to 2.6.0. Once it lands, drop the "prefer fetch-native libraries" workaround guidance.

## Known gaps (accepted / deferred)

- **An allowed host can carry data outward.** Allowing a host says nothing about intent — if
  `api.github.com` is allowed and a token is granted, code can send data *out* through a
  legitimate destination (open an issue, push a gist) using the owner's own credential. The
  allowlist authorizes the host, not the purpose. There's no clean fix without the policy
  framework (see the vision doc); narrower tokens and the eventual human-approval flow are the
  real answers.
- **Internal network addresses.** Code that can reach the network could also reach
  cloud-metadata addresses (`169.254.169.254`) and private ranges. Deferred deliberately: reaching
  those in a harmful way needs both a `dangerous_*` egress setting *and* a cloud deployment, and
  this is a local dev tool. Revisit at release/distribution time — the fix is one deny flag if
  hosted use ever becomes supported. See the open-questions reference (`~/proj/forge-open-questions-reference.md`).

## Decisions log

Executing agents: append entries here.
