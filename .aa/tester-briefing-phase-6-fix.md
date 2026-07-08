# Phase 6 — Phase 0 Generator — Fix Cycle 1 — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read the "Fix Cycle 1" section of `docs/adr-phase-6-prompt-generator.md` and architect's/builder's latest `phase-6.log` entries.

## Your task this cycle
1. Confirm the `paths:` restriction is present in both the embedded config and any extracted `.aa/` copy, and that they match.
2. Confirm builder's new "attempt to write outside scope → rejected" test actually exercises the restriction correctly (not a no-op that would pass even without the fix — check it fails against the pre-fix config, matching the rigor Phase 2/3's symlink fix tests used).
3. Confirm the legitimate case still works: the generator can still successfully write kickoff briefings to wherever they actually belong, unaffected by the new restriction.
4. Regression: full existing suite still passes.

## Deliverable
Pass/fail report. Append to `phase-6.log` via `write_log`.
