# Phase 3 — Turn Loop & Loop Detection — Fix Cycle 2 — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read the "Fix Cycle 2" section of `docs/adr-phase-3-turn-loop.md` and architect's/builder's latest `phase-3.log` entries.

## Your task this cycle

## The test that was missing
The original symlink-through-the-loop test didn't actually expose the root-mismatch bug because it used an unrestricted agent (no `AllowedPaths`), so `AllowPath` never computed `filepath.Rel` against the potentially-mismatched root. Write the combination that does exercise it:

1. Construct a symlinked project root (real dir + symlink pointing to it, same pattern as the existing test).
2. Configure an agent with an **active `AllowedPaths` glob restriction** (e.g. `edit_file: {paths: ["docs/*.md"]}`), through the symlinked root.
3. Confirm a path matching the glob, accessed through the symlink, is correctly allowed.
4. Confirm a path NOT matching the glob is correctly rejected — through the symlink, not just on an unresolved raw path.
5. This is the actual proof that `NewDefaultRegistry`'s returned resolved root and `loop.New`'s stored `resolvedRoot` now structurally cannot diverge — before the fix, this specific combination (symlink + active glob restriction) is what would have surfaced the bug; confirm it did NOT surface it before you move on (i.e., verify this test would have failed against the pre-fix code, either by checking it against the prior commit or by reasoning through why the mismatch would have broken it).

## Regression check
Confirm every call site builder updated (per their `phase-3.log` list) compiles and behaves correctly — spot-check at least one test file update directly rather than trusting the log's claim alone. Re-run the full existing suite.

## Deliverable
Pass/fail report covering the new combined symlink+glob test and full regression status. Append to `phase-3.log` via `write_log`. If this passes cleanly, Phase 3 is ready to merge per §15.
