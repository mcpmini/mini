---
name: structure-review
description: Flow-first structural review — maps execution flows top-down, extracts the domain model, and compares it to how the code is actually organized. Catches abstraction mismatches that bottom-up function-level reading misses.
argument-hint: [package paths, or blank for packages changed in the current branch diff]
---

Structural review of $ARGUMENTS (default: packages changed in the current branch diff).

Read [Go engineering in mini](../../../docs/go-guidelines.md). Structural signals guide investigation; justify a finding with concrete maintenance cost, a testing obstacle, or a threatened invariant. Ordinary callbacks, one-implementation interfaces, and coordinated edits across files can be appropriate.

**This review works top-down, not bottom-up.** Do not start by reading functions and evaluating them in isolation — that is exactly how structural problems stay invisible. Every function can look fine on its own while the decomposition is wrong for the domain. Start by mapping what the system *does*, then compare that to how the code is *organized*.

---

## Phase 0 — Smell test

Before doing the full flow analysis, read through the target packages and watch for signals that structural problems are likely. These are symptoms, not findings — they tell you where to focus, not what's wrong.

**Signals to watch for:**
- **Too many parameters** — functions with 4+ params, especially bare booleans or strings. A domain concept is hiding in those parameter lists, waiting to become a type.
- **Bare callbacks** — check whether closures hide ownership or policy that needs a clearer contract. Ordinary callback APIs can be appropriate.
- **High comment density** — check for unclear naming or organization, while preserving useful contracts and non-obvious invariants.
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
- Two verbs never share state → check whether separate functions or owners would clarify their contracts
- A verb has no name in the existing code → check whether naming it would expose a meaningful contract rather than fragment a readable operation

Write out the numbered flow list with the verb chain for each flow. This is the primary output of the review — the types, methods, and boundaries fall out of it directly.

Format each flow entry like this (imitate the structure, not the content):

```
3. Recover daemon connection after auth failure
   Trigger: request fails with 401 from the daemon
   Chain: detect stale token → re-resolve daemon address → re-run handshake → replay original request
   State: reads port file + token file; writes new session token
   Failure variants: daemon gone (→ flow 4, spawn daemon); token refresh fails (→ surface auth error)
   Termination: original caller receives the response or the auth error
```

---

## Phase 3 — Derive the domain model from the flows

Forget the existing code. Given the flows and verbs you just mapped, **if no code existed and you were writing this from scratch, what types would you create?**

Write a candidate model and compare it with the existing design. A difference is an investigation prompt, not a finding. Show the concrete cost, testing obstacle, or invariant violation before recommending a new abstraction.

### Nouns → types
Group operations by state, invariants, and lifecycle. Create a type when it makes ownership or a contract clearer; pure operations may remain functions. Name types after the domain entity they represent — "DaemonResolver", "Session", "Forwarder" — not after implementation mechanics.
- Things with a lifecycle (created, used, destroyed) are types
- Things that hold state multiple flows read or write are types
- Things that multiple flows reference by name are types

### Verbs → methods
Check which operations belong as methods or functions. Verify the grouping:
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

Investigate mismatches that create concrete friction or risk:

### Mixed concerns
A type or file handles verbs from multiple unrelated domain concepts. The test: if you changed how concept A works, would you touch code that implements concept B?

**Proof:** name the two+ domain concepts, list which methods belong to each, show they share a type or file. State why they are independent (different state, different lifecycle, different failure modes).

### Missing abstractions
A recurring invariant or ownership boundary is hard to express or enforce in the current structure. Candidate signals include:
- A bare `func()` callback or closure where the flows show a named domain action
- Inline logic in a larger function where the flows show a distinct step
- Parameters always passed together where the flows show a single entity
- A pattern repeated across flows with no shared implementation

**Proof:** name the concept and mapped flows, show the concrete cost or unenforced invariant, and explain how a named operation or type improves it. Absence of a type or helper alone is insufficient.

### Cryptic naming
A name you cannot predict the behavior of without reading the implementation. The test: could a reader who understands the domain (but hasn't read this code) guess what this does from its name alone?

**Proof:** state what the name suggests vs. what the function actually does in domain terms. Propose a name derived from the flow list.

### Wrong boundaries
Type or file boundaries that don't align with domain concept boundaries.
- Two types frequently modified together → check for misplaced responsibility or a legitimate shared contract
- One type used in two independent contexts → check whether their invariants or lifecycles genuinely conflict
- A function that crosses a domain boundary (starts in concept A, ends in concept B)

**Proof:** name the boundary as drawn vs. as the domain model says it should be. Show a concrete scenario where the wrong boundary causes friction.

### Over-abstraction
Indirection that doesn't correspond to any domain concept.
- An interface whose contract adds no useful boundary, substitution, or independent testing; implementation count alone is insufficient
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
- **HIGH** — a structural mismatch demonstrably threatens a critical invariant or conceals a consequential bug
- **MEDIUM** — concrete, material maintenance or testing cost, or reachable product risk
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

**Gate before writing the report:** every finding must cite the flow number(s) from Phase 2 that expose the mismatch. A finding you cannot tie to a mapped flow is bottom-up opinion — drop it or go back and map the flow that motivates it.
