# Phase 1 — Foundation — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md` in repo root. Before starting, read `docs/adr-phase-1-foundation.md` (architect's design for this phase) and `phase-1.log` (architect's and builder's entries, covering what was decided and what was actually implemented, including any noted deviations).

## Your task this phase
Verify the storage layer and model-client interface builder produced actually do what the spec (§3, §4) and architect's design require. This phase has no tools and no turn loop, so your scope is narrower than later phases will be — focus entirely on correctness of the foundation, since every later phase depends on it being right.

## What to check

1. **Schema correctness**
   - All three tables (`sessions`, `files`, `events`) exist with the exact columns specified in §4
   - `PRAGMA journal_mode=WAL` is actually active after connection open (query `PRAGMA journal_mode` and confirm the returned value, don't just check the code calls it)
   - Schema creation is idempotent — running it twice against an existing DB file doesn't error or duplicate anything
   - Foreign key relationships (`events.session_id → sessions.id`, `events.file_id → files.id`, `files.session_id → sessions.id`) behave correctly on insert

2. **Storage helper correctness**
   - Insert/query helpers round-trip data correctly (write a row, read it back, fields match)
   - The `write_count`-style query helper architect/builder built for future loop-detection use actually returns correct counts against seeded test data — even though nothing calls it in production yet, it needs to be right now since Phase 3 will trust it without re-verifying

3. **`LLM` interface correctness**
   - The mock implementation satisfies the interface and returns deterministic, controllable responses for use in later phases' tests
   - The real OpenAI-compatible implementation compiles and, if you have a way to test it against Metis or local Qwen without real tool-calling infrastructure yet, confirm a basic non-tool-call round-trip works (a plain text-in, text-out request)
   - Error surfacing: deliberately trigger a timeout and a bad-auth-key case against the real client and confirm the returned error carries enough distinguishing information for later retry logic (§8) to act on — don't just confirm "it errors," confirm the error is inspectable in the way §8 will need

## Known pitfalls to watch for
- SQLite driver + WAL mode interaction: some pure-Go SQLite drivers have had historical bugs around WAL mode not actually engaging silently — verify, don't assume, per the check above.
- Don't let this phase's tests quietly depend on tool-calling behavior that doesn't exist yet — if you find yourself wanting to test a tool call, that's Phase 2, not this one.

## Explicitly out of scope
No tool execution tests, no turn-loop tests, no config/bootstrap tests, no git integration tests.

## Deliverable
Test suite covering the above, pass/fail report. If failures point to a design gap rather than an implementation bug, say so explicitly and identify whether it's builder's implementation or architect's design that needs revisiting — per the spec's harness-level bias toward explicit, checkable outcomes rather than ambiguous judgment calls (§14). Append your pass/fail report to `phase-1.log` via `write_log`, per §15.
