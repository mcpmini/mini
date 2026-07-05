# Stage 5 — work with your files

Branch: `forge-05-files` (was `forge-stage5`). Capability: the agent's code can read and write
folders the owner grants, and always gets a private scratch space that's wiped after the run.

## What the user gets

The owner can grant folders — "code may read `~/proj/data`, may write `~/exports`" — and the
agent's scripts can then process real files on disk or leave output files behind. Every run also
gets its own private scratch folder that's created fresh and deleted afterward, so code has
somewhere to stage temporary work without the owner granting anything.

## Behavior (shipped)

- `code_mode.file_read_allow_list` / `file_write_allow_list` — folders the code may read/write.
  Entries must be absolute and already tidy (no `..`, no trailing cleanup needed), and must sit
  inside the owner's home directory or the system temp directory; a system path like `/etc` is
  refused. Granting a whole folder (a project root, or even the home directory) is a supported,
  first-class use.
- A per-run scratch folder is created by the host, granted to that run, and removed when it ends.
  The sandbox sees it as its temp directory.
- Executing files is never allowed under any config — the flags that would let code run other
  programs or load native libraries are never emitted (enforced by test).
- Already-closed sharp edges: a comma in a grant path (which Deno would have split into extra
  grants) is refused; a grant entry that is itself a symlink pointing outside the allowed area
  is refused; the scratch folder is cleaned up on every normal exit, and leftovers from a hard
  crash are swept at the next startup.

## Requirements — next build

Security-by-default tightening, plus the folder relocation. All are validation-time refusals in
the existing style (fail loudly, name the entry and the reason, never silently rewrite):

1. **Refuse the whole home folder as a single grant.** Granting `~` sweeps in every credential
   folder (`~/.ssh`, `~/.aws`, and the app's own config folder). Instead, the owner grants
   specific folders; an explicit `~/.ssh` entry is fine because it's a deliberate, informed
   choice. No escape-hatch flag for the whole-home case until someone actually needs it.
2. **Refuse the system temp folder as a single grant** — it covers other runs' scratch, the
   package caches, and every other program's temp files. Specific temp subfolders are still fine.
3. **Never let a write grant cover the app's config folder.** Config is re-read when the daemon
   restarts, so code writing there could change what future runs are allowed to do — a boundary
   must not be able to edit its own definition. This one has no opt-in. Reading the config folder
   stays allowed (it's present data and necessarily explicit). The config folder path is passed
   in by the host, never hardcoded, so this survives a custom `--config` location and a future
   standalone Forge.
4. All three checks must also catch the resolved form: an entry that is itself a link pointing at
   one of these targets is refused the same way.

**Folder relocation** (coordinate with stage 2's cache move):

5. Move the per-run scratch folders out of the system temp directory into the app's own state
   directory — `<config>/internal/forge/scratch/`. Together with the stage-2 cache move
   (`<config>/internal/forge/cache/`), this means the startup sweeper scans only the app's own
   folder instead of scanning the shared system temp directory and matching on name prefixes.
   The sandbox contract is unchanged (its temp directory still points at the per-run scratch).
   **The owner accepted this location provisionally, without strong feeling — revisit if it
   causes friction.**

## Known gaps (accepted)

- An explicitly granted folder can still contain secrets the owner forgot were there — their
  call, since the grant was deliberate.
- Deno follows a pre-existing symlink *inside* a granted folder to wherever it points. Code in
  the sandbox can't create such links (checked), so this only matters if a granted folder already
  contains one pointing outside — a granted project tree is trusted not to. This is Deno's
  boundary to own; no flag changes it. See the open-questions reference (`~/proj/forge-open-questions-reference.md`).
- A granted write folder has no size limit; the only ceiling on disk use is the run timeout.
  Consistent with the accepted "no resource ceilings" stance. See the open-questions reference (`~/proj/forge-open-questions-reference.md`).

## Decisions log

Executing agents: append entries here.
