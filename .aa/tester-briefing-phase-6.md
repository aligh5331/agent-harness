# Phase 6 — Phase 0 Generator — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-6-prompt-generator.md` (including its worked example) and `phase-6.log`.

## Your task this phase

## What to check
1. **Stage-selection manifest correctness** — given a test spec describing a small multi-phase project, confirm the generated manifest correctly reflects the agents each phase should need. Construct at least one test spec that clearly should NOT need every agent in some phase (e.g. a pure-bugfix phase), and confirm the generator actually omits agents rather than defaulting to "all five every time."
2. **Kickoff briefing structural correctness** — confirm generated briefings actually contain: project context, explicit read-first paths pointing at real prior-phase artifact locations (not placeholder text), a task breakdown, an out-of-scope section, and a deliverable section with a concrete output path. Compare structurally against this repo's own real `.aa/*-briefing-phase-*.md` files as the reference shape.
3. **Handoff convention compliance** — confirm generated builder/tester briefings for a given phase correctly reference that phase's architect output path (`docs/adr-phase-N-<name>.md`) and `phase-N.log`, per spec §12.1 — not embedded content, actual path references the agent would `read_file` itself.
4. **Loop-template output** (if architect scoped Phase 0 to generate these fresh) — confirm placeholder syntax is consistent and the template is genuinely fillable via `text/template` against realistic `events`/`files` data, not just plausible-looking prose.
5. **Worked-example parity** — run the generator against architect's own worked-example spec from the ADR and confirm the output matches what the ADR shows (or matches builder's documented, justified divergence).

## Deliverable
Test suite and pass/fail report covering the above. Append to `phase-6.log` via `write_log`. If this passes cleanly, Phase 6 is ready.
