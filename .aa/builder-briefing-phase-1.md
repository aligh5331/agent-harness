# Phase 1 — Foundation — Builder Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md` in repo root. Before doing anything else, read `docs/adr-phase-1-foundation.md` — architect's design output for this phase — and the current `phase-1.log` for context on what's already been decided. Implement against that document, not against your own reading of the spec, if the two ever seem to disagree; flag the disagreement instead of silently picking one.

## Your task this phase
Implement the foundation layer exactly as architect specified:

1. **Storage** — the three-table SQLite schema (§4 of spec), `PRAGMA journal_mode=WAL` set on connection open, idempotent schema creation on startup, and the Go struct types + basic CRUD-style helper functions (insert session, insert event, insert/update file row, query helpers the way §7's loop-detection logic will eventually need — e.g. a function that returns `write_count` for a given session+path pair, even though nothing calls it yet).

2. **Model client** — the `LLM` interface implementation against an OpenAI-compatible endpoint, using whichever client library architect specified. Must compile and satisfy the interface; does not need to be battle-tested against every provider quirk yet, since tool-calling round-trips don't exist until Phase 2.

3. **A fake/mock `LLM` implementation** satisfying the same interface, for use in this phase's own tests and in later phases before wiring up real provider calls is necessary.

## Constraints
- Follow the package layout architect defined — don't introduce new top-level packages without flagging it back.
- Keep it simple: this phase has no tools, no loop, no config. Resist the urge to build scaffolding for those now, even if it seems convenient — build only what Phase 1 needs, per the spec's non-goals and phasing (§13.1).
- Pure-Go dependencies preferred (no cgo) unless architect explicitly specified otherwise, given the devbox environment.

## Explicitly out of scope
Same as architect's briefing: no tools, no turn loop, no config/bootstrap, no git integration.

## Deliverable
Working Go code implementing the storage layer and model client interface exactly per architect's design, compiling cleanly, with the mock `LLM` implementation included. Append a brief entry to `phase-1.log` via `write_log` summarizing what you implemented and any deviations from the ADR you had to make, per §15. Hand off to tester once it builds.
