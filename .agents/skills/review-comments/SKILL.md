---
name: review-comments
description: Adversarial audit of every net-new comment in the diff. Default verdict is DELETE — a comment survives only if no code change could replace it. Run before committing or pushing any change that added comments.
argument-hint: [optional: path or commit range, default is uncommitted + unpushed diff]
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

For each candidate, open the file and read the full containing function or type — plus the
containing struct's doc comment for field comments. Never judge a comment from the diff
hunk alone; redundancy and false claims are only visible in the surrounding code.

## Step 2 — For each candidate, exhaust every alternative to keeping it

Work through each alternative in order. If any applies, apply it and the comment is gone.

**Keep a worksheet.** For every candidate, write one line as you go recording each
alternative attempted and why it did not apply, e.g.
`engine.go:42 — rename: no identifier carries it / extract: single stmt / test: exists / delete: makes a why-claim → verify claim → KEEP`.
A Keep verdict without a recorded attempt at every prior alternative is invalid — go back
and attempt them. The worksheet is also your Step 4 report.

1. **Rename** — would a better function, variable, field, or type name make this comment
   unnecessary? If yes: rename and delete. Good names are free; comments have a cost.
   Scan every identifier the comment mentions — not just the function or type it sits above.
   A struct-level comment that describes one field is really a field-naming problem.
   For test functions: the name *is* the comment. Make it long enough to be self-documenting
   (`TestLogin_ExpiredToken_ReturnsUnauthorized` not `TestTokenExpiry`). Name tests from
   the caller's perspective — what does the caller observe? — not from the internal
   mechanism being exercised.

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
   - Redundant with the containing type's doc comment — before keeping a field comment,
     read the struct or interface doc. If it already covers when/how/why the field is set,
     the field comment is dead weight.
   - States a fact a reader gets for free by navigating the code (e.g. "shared by callers
     X and Y" — one find-references away). Don't manufacture a why-framing just to save it.

5. **Trim and lead with the why** — cut everything that describes what the code does
   (already visible) and lead with the deep why. A comment that survives should open with
   the external constraint, not the implementation detail:
   `// old clients can send "foo":"bar", validate for legacy compat` not
   `// validate x because old clients send "foo":"bar"`. One clause is almost always
   enough — if a kept comment runs past one line, trim harder.

6. **Relocate to the line it explains** — a surviving comment belongs *inside* the function,
   on the specific statement it explains, not floating above the function signature. A
   header comment claims to describe the whole function but usually explains just one line,
   and it silently drifts the moment that line moves or the function grows. Make the function
   name carry the "what"; put the comment next to the exact bit of code it justifies, as
   locally as possible. Above-the-signature comments are reserved for genuine doc comments on
   *exported* identifiers (Go convention) and for a true one-line whole-function summary the
   name itself cannot carry — not for explaining one statement in the body.
   **After relocating, re-apply step 1.** Moving a comment from a struct to a field often
   unlocks a rename that wasn't visible at the struct level. A comment "uses ISO 8601
   format" floating above the struct becomes a rename opportunity once it lands on the
   `Date` field — rename to `ISO8601Date` and delete.

7. **Keep** — a comment reaches Keep only after all six alternatives above are genuinely
   exhausted. Before marking Keep, apply three additional checks:

   - **Deep why, not shallow why.** The deep why is the constraint a developer needs to
     understand to safely modify the code — not the mechanism by which it works.
     `// retry because the server returns 503 during rolling deploys` (developer constraint:
     don't remove this retry or deploys break) is deep.
     `// retry by sleeping 100ms then re-dialing the connection` (mechanism) is shallow —
     a developer can read that from the next two lines. External constraints — client
     behavior, protocol requirements, backward compatibility, data invariants from outside
     this codebase — are almost always deep. Internal implementation references are almost
     always shallow. Deep why: keep. Shallow why: push the author to articulate the
     underlying constraint, or delete.

   - **Verify claims.** Check every factual claim in the comment against the actual code.
     Not just assertions like "this prevents X" — also causal claims ("passed by value so
     each call gets its own copy"), behavioral claims ("always set", "never nil"), and
     format claims ("uses dot-notation"). Read the code and confirm the mechanism is what
     the comment says it is. Comments that describe assertions in tests: verify the
     assertion itself is still correct, not just whether the comment is redundant. A comment
     that makes a false or stale claim is actively harmful.

   - **Drift risk.** Will this comment go stale silently if the adjacent code changes?
     High-drift comments attached to volatile implementation details should be deleted
     even if they feel useful today. Low-drift comments about stable external constraints
     are safer to keep.

   Expect to remove 80–90% of comments on a first pass through a diff. **Calibration gate:**
   if more than 1 in 5 candidates earned Keep, your bar is too low — re-audit every Keep
   assuming the verdict is wrong, and for each state which specific external constraint it
   documents that no rename, extraction, or test could capture.

   The one standing exception: `// Foo does X` doc comments on *exported* identifiers
   are Go convention. Keep them even when they read as "what." But do not over-apply this:
   a comment sitting above a struct that actually describes a specific field is not a
   struct doc comment — it's a misplaced field comment. Relocate it (step 6) and re-evaluate.

## Step 2.5 — Structural smell check

If you removed 5+ comments from a single function or file, that density is a signal: the
code probably needs explaining because the abstractions are wrong, not because the logic is
subtle. Note this in the report and recommend running `structure-review` on the package.
Don't attempt the structural analysis yourself — this skill is scoped to comments.

## Step 3 — Apply changes

Apply every deletion, rename, extraction, and trim directly with `Edit`. After renames or
extractions, run the relevant tests to confirm nothing broke.

## Step 4 — Report

One line per comment touched: `file:line — verdict — one-sentence reason`. For any comment
kept, state explicitly which alternatives it survived and what external constraint it
documents. If the diff had no net-new comments, say so in one line.
