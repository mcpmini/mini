---
name: test-writer
description: Design, add, or revise tests for behavior changes in mini when choosing coverage, setup, assertions, or failure simulation needs thought. For PR review, use review-pr.
---

# Write tests for mini

Read [the testing guide](../../../docs/testing.md) before choosing coverage or adding helpers. Follow the user's scope; a refactor or documentation change does not automatically need a new test.

1. Trace the changed behavior from a production entry point. Find existing tests, fakes, and setup helpers. State the contract and a plausible regression the tests should catch.
2. Choose the smallest level that can expose that regression clearly. Add a wider case only for wiring, lifecycle, process behavior, or a critical journey. Put independent permutations at the component level.
3. Write a readable setup, visible action, and decisive assertion. Reuse or extract domain-named helpers for repeated setup; avoid hiding the action or assertion. Exercise the relevant error, state, timing, or cleanup boundary when it changes the outcome.
4. Run the focused tests with the right tags and confirm they were selected, then run the relevant repository gate. If assertion strength is uncertain, temporarily perturb the behavior and check that the test fails for the expected reason. Restore the code. This check is optional; test-first sequencing and mutation reports are not required.
5. Report which behavior is protected, commands and results, and any material gap. Distinguish setup or sandbox failures from product failures.

Use existing `AGENTS.md` commands and test conventions. Keep this skill about authoring; `review-pr` owns review findings and verdicts.
