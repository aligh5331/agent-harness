# Phase 7 — Spec Audit — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md` §14. Before starting, read `docs/adr-phase-7-spec-audit.md` and `phase-7.log`.

## Your task this phase
1. **Layer 1 correctness**: run the per-phase audit mode against one of this project's own already-completed phases (e.g. Phase 6, which has a known real gap-then-fix history) and confirm it produces sensible, accurate output — this is a strong test case since you know the real history to check against.
2. **Layer 2 correctness**: run the full audit mode against the project's current full state and confirm it produces a genuinely useful violations-style report, not just a restatement of the spec.
3. **Scope boundary check**: confirm the per-phase audit does NOT reach into concerns outside that phase's own briefings (per spec §14's explicit prohibition on scope creep), and confirm the full audit doesn't just repeat each phase's per-phase check redundantly — it should be looking for cross-phase issues specifically.
4. **Output format usability**: confirm both audit outputs are actually structured/parseable per builder's chosen format, not just prose that happens to look organized.

## Deliverable
Test suite and pass/fail report. Append a `phase-7.log` entry.
