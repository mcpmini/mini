---
name: structure-review
description: Flow-first structural review — maps execution flows top-down, extracts the domain model, and compares it to how the code is actually organized. Catches abstraction mismatches that bottom-up function-level reading misses.
argument-hint: [package paths, or blank for packages changed in the current branch diff]
---

Structural review of $ARGUMENTS (default: packages changed in the current branch diff).

**This review works top-down, not bottom-up.** Do not start by reading functions and evaluating them in isolation — that is exactly how structural problems stay invisible. Every function can look fine on its own while the decomposition is wrong for the domain. Start by mapping what the system *does*, then compare that to how the code is *organized*.

---

## Phase 0 — Smell test

Before doing the full flow analysis, read through the target packages and watch for signals that structural problems are likely. These are symptoms, not findings — they tell you where to focus, not what's wrong.

**Signals to watch for:**
- **Too many parameters** — functions with 4+ params, especially bare booleans or strings. A domain concept is hiding in those parameter lists, waiting to become a type.
- **Bare callbacks** — `func()` parameters or closure captures where the flows show a named domain action. The code is doing something meaningful but refuses to give it a name.
- **High comment density** — if the code needs lots of comments to explain itself, the abstractions are probably wrong. Code that fits its domain is self-describing.
- **Painful tests** — heavy setup boilerplate, fragile assertions, or tests that break when unrelated code changes. If testing one concept requires building up lots of unrelated state, the boundaries are in the wrong place.
- **Large files** — a file over 300 lines often means mixed concerns. Two domain concepts sharing a file because they were written at the same time, not because they belong together.

If you see clusters of these signals in the same package, that package is the priority for flow analysis. If nothing lights up, proceed with the full analysis anyway — some structural problems are quiet.

---

## Phase 1 — Scope and entry points

Identify the packages to review. For each package:

1. Read every exported function and method — these are the entry points.
2. Read every struct type — these are the nouns the code claims matter.
3. Skim test files to understand what behaviors the author considers important.

Do not evaluate anything yet. You are building a map, not judging code.

---

## Phase 2 — Map execution flows and extract verbs

This is the most important phase. The flow mapping is not a preparatory step — it is the exercise that reveals what the abstractions should be. By the time you finish tracing flows and naming the verbs, the natural types and boundaries will be obvious. Everything after this phase is confirming what the flows already told you.

For each entry point, trace the complete execution flow end-to-end. Follow through every function call, not just the entry point. For each flow, record:

- **Trigger:** what initiates it (caller, event, timer, error condition)
- **Chain:** the sequence of actions, in order. Name each action as a verb phrase.
- **State:** what is read, what is written, what is passed between actions
- **Failure modes:** each error branch is its own flow variant — trace those too
- **Termination:** where and how the flow ends

**Naming is the exercise.** Name each flow and each action within it using domain language — what it accomplishes from the caller's perspective, not what the code does internally. "Recover daemon connection after auth failure" — not "handleReconnect calls reresolve then reinitializes." If you can't name a flow or action without referencing implementation details, that's a structural signal: the code has no abstraction for this concept.

**Be exhaustive.** List every distinct flow:
- Happy paths for each entry point
- Error and recovery paths
- Concurrent interactions (two flows that can run simultaneously)
- Lifecycle flows (startup, shutdown, reconnection, reinitialization)
- Edge cases (empty state, first-run, degraded mode)

**What the verbs reveal.** As you name actions across flows, patterns emerge:
- The same verb appears in multiple flows → that's a reusable operation, likely a method
- Several verbs share the same state → they belong on the same type
- A chain of verbs always runs in sequence → that sequence is a higher-level operation
- Two verbs never share state → they belong on different types
- A verb has no name in the existing code (it's inline logic or a bare callback) → that's a missing abstraction

Write out the numbered flow list with the verb chain for each flow. This is the primary output of the review — the types, methods, and boundaries fall out of it directly.

---

## Phase 3 — Derive the domain model from the flows

Forget the existing code. Given the flows and verbs you just mapped, **if no code existed and you were writing this from scratch, what types would you create?**

This is not a rhetorical exercise. Actually write out the types, their methods, and which flows they serve. The gap between this blank-slate design and the actual code *is* the structural finding.

### Nouns → types
Group the verbs by the state they share. Each group is a type. Name it after the domain entity it represents — "DaemonResolver", "Session", "Forwarder" — not after implementation mechanics.
- Things with a lifecycle (created, used, destroyed) are types
- Things that hold state multiple flows read or write are types
- Things that multiple flows reference by name are types

### Verbs → methods
Each verb from the flow chains becomes a method on its type. Verify the grouping:
- Verbs that share state → methods on the same type
- Verbs that are independent (different state entirely) → methods on different types
- Verbs with sequence dependency → the caller orchestrates the sequence, or a higher-level method encapsulates it

### Boundaries → separation
Each type should have one reason to change — not one method, but one domain concern. If modifying how "daemon resolution" works forces you to also touch "request forwarding" code, those concerns are coupled in a type that has two reasons to change.
- Groups of verbs that share state vs. groups that don't → type boundary
- Independent lifecycles → type boundary
- Independent failure modes → type boundary

Write out the domain model: types, their methods, the boundaries. Then compare to the actual code.

---

## Phase 4 — Compare to actual structure

Now read the actual code structure. For each type, file, and function, answer:
1. Which domain noun does this type correspond to?
2. Which domain verbs do this type's methods implement?
3. Does this file contain code for one domain concept or several?

Flag every mismatch:

### Mixed concerns
A type or file handles verbs from multiple unrelated domain concepts. The test: if you changed how concept A works, would you touch code that implements concept B?

**Proof:** name the two+ domain concepts, list which methods belong to each, show they share a type or file. State why they are independent (different state, different lifecycle, different failure modes).

### Missing abstractions
A domain concept exists in the flows but has no corresponding named type. It appears as:
- A bare `func()` callback or closure where the flows show a named domain action
- Inline logic in a larger function where the flows show a distinct step
- Parameters always passed together where the flows show a single entity
- A pattern repeated across flows with no shared implementation

**Proof:** name the domain concept, show where it appears in the flow list, show that no type or named function represents it.

### Cryptic naming
A name you cannot predict the behavior of without reading the implementation. The test: could a reader who understands the domain (but hasn't read this code) guess what this does from its name alone?

**Proof:** state what the name suggests vs. what the function actually does in domain terms. Propose a name derived from the flow list.

### Wrong boundaries
Type or file boundaries that don't align with domain concept boundaries.
- Two types always modified together → should be one
- One type used in two independent contexts → should be two
- A function that crosses a domain boundary (starts in concept A, ends in concept B)

**Proof:** name the boundary as drawn vs. as the domain model says it should be. Show a concrete scenario where the wrong boundary causes friction.

### Over-abstraction
Indirection that doesn't correspond to any domain concept.
- An interface with exactly one implementation and no test fake
- A wrapper type that adds no behavior
- An indirection layer between things the domain model shows are directly connected

**Proof:** show that removing the abstraction makes the flow clearer without losing real flexibility.

### Responsibility diffusion
A single domain concept scattered across multiple types with no clear owner. Understanding the concept requires reading all of them.

**Proof:** name the concept, list every type that holds part of it, show that no single type owns it. Propose which type should.

---

## Phase 5 — Severity

For each finding, assess:

1. **Blast radius:** how many files must change to modify the mismatched concept? More = higher.
2. **Bug surface:** does the mismatch create opportunities for bugs? Mixed concerns → accidental state corruption. Missing abstractions → inconsistent handling across flows. Wrong boundaries → broken invariants.
3. **Test difficulty:** does the mismatch make the code harder to test in isolation?

**Severity:**
- **HIGH** — mismatch makes a critical flow untestable, or modifying one domain concept requires changes across 3+ unrelated files, or the mismatch actively masks bugs
- **MEDIUM** — materially harder to understand or modify; 2+ files change together; cryptic names on important flows
- **LOW** — real mismatch but the code is small or stable enough that the cost is low

---

## Report

```markdown
# Structure Review — [scope]
**Date:** YYYY-MM-DD

## Execution flows
1. [verb-phrase description of flow]
2. ...

## Domain model
**[Noun]** — [one-line description]
  Verbs: [list of actions]
  State: [what it holds]

**[Noun]** — ...

Boundaries: [where one concept ends and another begins, and why]

## Findings

### SEVERITY — Category: [title]
**Location:** file(s) and type(s)
**Domain concept:** what concept is mismatched
**Current structure:** how the code is organized
**Natural structure:** how the domain model says it should be organized
**Impact:** what goes wrong (hard to test, hard to modify, bug risk, masks behavior)

## Non-issues
[Structural choices investigated and confirmed correct — state why the current
structure matches the domain. Prevents re-flagging in future reviews.]
```

Focus findings on structural mismatches the flows reveal. Don't flag code that is well-structured for its domain — confirm it in non-issues.
