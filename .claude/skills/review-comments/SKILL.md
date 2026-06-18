---
name: review-comments
description: Adversarial audit of every net-new comment in the diff. Default verdict is DELETE — a comment survives only if no code change could replace it. Run before committing or pushing any change that added comments.
argument-hint: [optional: path or commit range, default is uncommitted + unpushed diff]
allowed-tools: Read, Grep, Bash, Edit
---

Audit net-new comments in $ARGUMENTS (default: `git diff` of uncommitted changes plus any
commits not yet on the upstream branch).

**Mindset:** LLMs over-comment by default. Comments rot — they don't compile, they go stale
silently, and they add ongoing maintenance risk. The default verdict is DELETE. The burden
of proof is on keeping a comment, not removing it. Work through every alternative before
deciding a comment is load-bearing.

## Step 1 — Collect candidates

```bash
git diff HEAD --unified=0 -- '*.go'                     # uncommitted
git diff @{upstream}..HEAD --unified=0 -- '*.go'        # committed but unpushed (if upstream exists)
```

Extract every added line (`+`) that is a comment or doc comment block. Skip comments that
existed before this diff and were only moved or reflowed.

## Step 2 — For each candidate, exhaust every alternative to keeping it

Work through each alternative in order. If any applies, apply it and the comment is gone.

1. **Rename** — would a better function, variable, or type name make this comment
   unnecessary? If yes: rename and delete. Good names are free; comments have a cost.

2. **Extract a function** — is the comment marking a block of logic ("// validate the
   token", "// build response")? If yes: extract that block into a named function and
   delete the comment. The function name does the work.

3. **Write a test** — is the comment explaining expected behavior, an edge case, or a
   subtle invariant? If yes: write (or rename an existing) test that documents the
   behavior by exercising it. Tests are compiled and kept honest; comments aren't.

4. **Delete outright** — delete if any of the following:
   - Restates what the code does in English
   - Acts as a section divider or structural marker
   - Repeats information already in the function name, type, or signature
   - States a fact a reader gets for free by navigating the code (e.g. "shared by callers
     X and Y" — one find-references away). Don't manufacture a why-framing just to save it.

5. **Trim and lead with the why** — cut everything that describes what the code does
   (already visible) and lead with the deep why. A comment that survives should open with
   the external constraint, not the implementation detail:
   `// old clients can send "foo":"bar", validate for legacy compat` not
   `// validate x because old clients send "foo":"bar"`. One clause is almost always
   enough — if a kept comment runs past one line, trim harder.

6. **Keep** — a comment reaches Keep only after all five alternatives above are genuinely
   exhausted. Before marking Keep, apply three additional checks:

   - **Deep why, not shallow why.** A comment that references internal implementation
     ("have to do this because `internalServeFunc` passes this structure down") is a
     shallow why — it will rot as the code evolves and teaches nothing about the world.
     A comment that explains an external constraint — client or consumer behavior,
     protocol requirements, backward compatibility, data invariants that originate
     outside this codebase — is a deep why. Deep why: keep. Shallow why: push the author
     to articulate the underlying external constraint, or delete.

   - **Verify claims.** If the comment asserts "this prevents X" or "this blocks Y",
     check that claim against the actual code. Don't take it on faith. A comment that
     makes a false or misleading claim is worse than no comment.

   - **Drift risk.** Will this comment go stale silently if the adjacent code changes?
     High-drift comments attached to volatile implementation details should be deleted
     even if they feel useful today. Low-drift comments about stable external constraints
     are safer to keep.

   Expect to remove 80–90% of comments on a first pass through a diff.

   The one standing exception: `// Foo does X` doc comments on *exported* identifiers
   are Go convention. Keep them even when they read as "what."

## Step 3 — Apply changes

Apply every deletion, rename, extraction, and trim directly with `Edit`. After renames or
extractions, run the relevant tests to confirm nothing broke.

## Step 4 — Report

One line per comment touched: `file:line — verdict — one-sentence reason`. For any comment
kept, state explicitly which alternatives it survived and what external constraint it
documents. If the diff had no net-new comments, say so in one line.
