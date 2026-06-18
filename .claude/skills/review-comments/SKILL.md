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

4. **Delete outright** — does it restate what the code does, act as a section divider,
   repeat the signature, or describe something a reader gets for free by reading the
   next line? Delete it.

5. **Trim** — if a multi-sentence comment has a load-bearing clause mixed with
   restatement, cut everything except the load-bearing clause. Never keep a sentence
   that describes *what*; only sentences that explain *why* a non-obvious constraint exists.

6. **Keep** — a comment survives only if all five alternatives above have been genuinely
   exhausted: no rename would capture it, no function name would capture it, no test
   would be better, it isn't restatement, and trimming has already been applied. This
   should be rare. If you find yourself keeping more than one or two comments in a diff,
   reconsider — you are probably being too lenient.

   The one standing exception: `// Foo does X` doc comments on *exported* identifiers
   are Go convention. Keep them even when they read as "what."

## Step 3 — Apply changes

Apply every deletion, rename, extraction, and trim directly with `Edit`. After renames or
extractions, run the relevant tests to confirm nothing broke.

## Step 4 — Report

One line per comment touched: `file:line — verdict — one-sentence reason`. For any comment
kept, state explicitly which of the six paths above it survived and why. If the diff had
no net-new comments, say so in one line.
