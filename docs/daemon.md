# The mini daemon

mini can run per-process (agent ⇄ `mini serve` over stdio), but a shared daemon exists for two
reasons:

- **Shared upstream connections and warm state** across chats — no re-dialing upstreams or
  re-running the MCP handshake for every chat.
- **One process holds credentials.** The daemon injects each upstream's OAuth tokens / API keys
  at startup and spends them for connected clients, so individual chats never touch them.

## Architecture

```
 chat A ── mini serve (proxy, stdio) ─┐
 chat B ── mini serve (proxy, stdio) ─┼─▶  mini daemon  ──▶ upstream MCP servers
 chat C ── mini serve (proxy, stdio) ─┘   (127.0.0.1 HTTP)   (stdio / HTTP)
```

- Each chat runs a **proxy** (`mini serve`): MCP over stdio to the agent, forwarding each
  JSON-RPC request to the daemon over loopback HTTP (`POST /mcp`).
- One **daemon** (`mini daemon`) on `127.0.0.1:<daemon_port>` owns the upstream connections,
  projections, and sessions. Many proxies, one daemon.
- Code: `internal/proxy` (proxy), `internal/server` (`ServeHTTP`), `cmd/mini/daemon.go`
  (lifecycle), `internal/daemon` (rendezvous + spawn lock).

## Connecting

A proxy finds or starts the daemon via `resolveDaemonPort`:

1. `RunningPort(configDir)` reads `daemon.port` and probes `GET /healthz`, returning the port
   only if the daemon answers `200`. The port file is a rendezvous hint; liveness comes from the
   probe, never from the file's existence or a PID.
2. No answer → start one (`daemon.Start`), under a spawn lock so a crowd of reconnecting proxies
   yields a single daemon.
3. Read `daemon.token`, forward with it.

`GET /healthz` reports `{"ok":true,"sessions":N}`.

## Auth

`/mcp` is authenticated; `/healthz` is open (no secret, polled for liveness).

- **Bearer token.** `mini daemon` mints a 32-byte `crypto/rand` token and stores it `0600`
  (via `O_EXCL`) in `daemon.token`. `/mcp` requires `Authorization: Bearer <token>`, compared
  with `crypto/subtle`. The proxy reads it from the file, so the file permissions are the real
  boundary.
- **Stable across restarts (`EnsureToken`).** A respawned daemon reuses the token instead of
  rotating it, so connected proxies survive a respawn rather than getting `401`. It's reused
  only if the file is `0600`; a looser file is treated as compromised and re-minted.
- **DNS-rebinding defense.** `/mcp` rejects any non-loopback `Host` (`127.0.0.1`, `::1`,
  `localhost`), so a page rebinding `evil.com → 127.0.0.1` is refused even though the connection
  lands on loopback. Skipped only for an explicit `--dangerous-nonloopback-http` bind.

## Recovery

When the daemon dies, open chats recover without a restart. The proxy's loop (`internal/proxy`):

1. **Classify the failure** (`outcome.go`): dial / `ECONNREFUSED` → `transportDown`; `401` →
   stale token; `not initialized` → session lost; anything else (e.g. a reset after the bytes
   were sent) → `outcomeOther`, which is never retried.
2. **Respawn, single-winner.** `Reresolve` → `resolveDaemonPort` → `daemon.Start`. A `flock`
   spawn lock (`spawnlock_*.go`) lets one proxy spawn while the rest block, then find the daemon
   up and skip spawning. flock releases on process death, so a spawner crashing mid-start can't
   deadlock the others. The OS socket bind sits underneath: only one process can bind
   `daemon_port`, so two daemons are impossible even without the lock — the lock only removes the
   wasted-spawn herd at scale.
3. **Reconnect.** Re-read the token, re-`initialize`, retry. Bounded attempts with jittered
   backoff.
4. **One recovery per proxy.** Concurrent forwards share a generation-counted, mutex-guarded
   `daemonLink`, so N in-flight requests trigger one respawn.

### Idempotency

Retry only when the request provably never executed: a dial failure (never reached the daemon),
`401` (rejected before dispatch), or `not initialized` (gated before dispatch). A failure
*after* the bytes were sent is `outcomeOther` and goes back to the agent unretried, because the
daemon may already have run a non-idempotent write like `create_issue`. Uncertain cases fail
safe. A replayed write is worse than a surfaced error.

## Port lifecycle

The port is freed the instant the daemon dies, on any signal. The kernel closes a dead
process's descriptors — including the listening socket — as it exits, and a listening socket has
no `TIME_WAIT` (that applies to ESTABLISHED connections, on the side that closed first), so
`daemon_port` is available for the respawn immediately, even after `SIGKILL`. Go's `net.Listen`
sets `SO_REUSEADDR`, so the rebind succeeds even if old client connections linger in `TIME_WAIT`.

`SIGTERM` removes `daemon.port`; `SIGKILL` leaves it stale. That's harmless: `RunningPort`
validates with `/healthz`, so a stale file pointing at a dead or recycled port fails the probe
and reads as "no daemon." This is why liveness is a health probe, not a PID or file-existence
check.

**Fixed vs ephemeral port.** A fixed `daemon_port` gives a stable rendezvous — a respawn returns
on the same port, the proxy's cached port stays valid, recovery is fast. `daemon_port: 0`
(OS-assigned) also works (the respawn lands on a new port, the proxy re-reads the file) but
loses that, and single-winner contention only bites with a fixed port — two `:0` racers bind two
different ports. The suite covers both.

## Single-instance: what we use, what we rejected

Databases stress this hardest — a stale lock or a double-start corrupts data — so their failure
modes are the most instructive.

| Approach | Verdict |
|---|---|
| OS socket bind (one binder, rest get `EADDRINUSE`) | Primary. The kernel is the lock, released on death. tmux and git-credential-cache rely on it. |
| `flock` spawn lock | Added, to collapse the spawn herd at scale. Advisory, auto-released on death. |
| `/healthz` liveness probe | Used for rendezvous. Beats `kill(pid,0)` — it confirms the service answers, defeating PID reuse. |
| PID file (`postmaster.pid`, `mongod.lock`) | Rejected. The classic stale-lock / PID-reuse failure (manual `rm` after a crash, broken on Windows). If used at all, debug metadata behind a real lock — never the lock. |
| OS supervision (launchd / systemd / service) | Right for managed/server deployments where the OS owns the lifecycle. Not the zero-config default. |
| Unix socket | Future: removes the port (and port-squatting), makes the socket path the lock, and replaces the token with file permissions + peer credentials. |

References: [tmux(1)](https://man7.org/linux/man-pages/man1/tmux.1.html),
[git-credential-cache--daemon](https://git-scm.com/docs/git-credential-cache--daemon),
[flock(2)](https://man7.org/linux/man-pages/man2/flock.2.html),
[PostgreSQL postmaster.pid](https://www.crunchydata.com/blog/postgres-postmaster-file-explained),
[RFC 8252](https://www.rfc-editor.org/rfc/rfc8252.html),
[localhost CORS & DNS rebinding](https://github.blog/security/application-security/localhost-dangers-cors-and-dns-rebinding/).

## Threat model

We don't aim to be more secure than the agent running mini — we aim to add no new attack
surface. An MCP client already runs with the user's privileges and can read the user's files, so
defending against threats that already own the session is theater. The job is to not open a door
that wasn't there before.

**Out of scope** (already game over):

- **Same-user code.** A process running as the user can read `~/.mini`, take the tokens, drive
  the agent, and exfiltrate. The `0600` token can't stop that and doesn't try — ssh-agent and
  Docker make the same assumption.
- **Root / full system compromise.** Same.

**In scope** — the surface mini adds by running a credential-holding listener and an outbound
fetcher:

- **Browser DNS rebinding.** A visited web page is untrusted code on the loopback interface;
  without defense it could `POST` to `127.0.0.1/mcp` and spend the user's upstream credentials.
  Stopped by the bearer token (a page can't read the `0600` file) and the `Host` check (a rebound
  request carries the attacker's `Host`).
- **Other local users on a shared host.** Loopback is shared by everyone on the box. The `0600`
  token, lock, and port files gate it; a Unix socket would make this filesystem-enforced.
- **SSRF.** mini fetches outbound, so a crafted or attacker-controlled upstream URL could try to
  reach internal services or rebind to a private IP. Stopped by `ValidateURL` plus an SSRF-safe
  dialer that re-validates resolved IPs at connect time (private / loopback / link-local / NAT64
  / etc.) and a no-redirect client, so a trusted host can't 3xx a session token to an internal
  one.

**Residual, documented.** On loopback TCP another local user could squat `daemon_port` in the
gap after the daemon dies and harvest the token a reconnecting proxy sends. The `0600` files
limit who can; a Unix socket removes port-squatting entirely, which is the main argument for
moving to one.
