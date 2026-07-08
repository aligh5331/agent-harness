# Phase 6 — Phase 0 Generator — Fix Cycle 1 — Architect Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md` §6.2. Before starting, read `docs/adr-phase-6-prompt-generator.md` and `phase-6.log` (including forensic's audit entry).

## Context (found during independent review of forensic's audit, not caught by forensic itself)
`internal/config/embedded/agents/prompt-generator.md` grants `create_file: {}` with no `paths:` restriction — unscoped write access to anywhere in the project root. This directly contradicts the ADR's own constraint #1 ("Strictly read-only for project code") and every other agent's established pattern in this project: architect is glob-scoped to `docs/adr-*.md` (§6.2's own reference example), forensic gets zero write tools at all — specifically *because* the harness's design philosophy is "never trust the model to stay in bounds, enforce it structurally." `WriteManifest` (the manifest writer) is a harness-side Go function unaffected by this, but the kickoff-briefing files themselves are created via the agent's own `create_file` tool call, currently held in bounds only by prompt wording ("follow naming conventions"), not by enforcement.

## Your task this cycle
Specify the correct `paths:` glob restriction for `prompt-generator`'s `create_file` grant. This agent legitimately needs to write:
- Kickoff briefing files (e.g. `.aa/agents/architect-briefing-phase-N.md`, matching wherever this project's own `.aa/agents/` briefings actually live — confirm the exact directory against what builder's implementation actually uses, don't assume)
- Possibly loop-template files, if architect's original Phase 6 design has this agent generating those (confirm against your own Phase 6 ADR item 3 — if generic templates are embedded defaults rather than agent-generated, this agent may not need broad create_file access for templates at all)

Specify the exact glob pattern(s), confirm they're narrow enough to prevent writing to `docs/`, `internal/`, or anywhere else project code lives, and confirm whether `edit_file` needs the same restriction (currently not granted to this agent at all per the config — confirm that omission is intentional and correct, i.e. this agent only ever creates new briefing files, never edits existing ones).

## Deliverable
Amend `docs/adr-phase-6-prompt-generator.md` with a "Fix Cycle 1" section specifying the exact `paths:` restriction to add. Append a `phase-6.log` entry.
