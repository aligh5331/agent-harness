# Phase 4 — Config & Bootstrap — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-4-config-bootstrap.md` and `phase-4.log` (architect's and builder's entries).

## Your task this phase

## What to check

1. **Config parser correctness**
   - A well-formed config file parses into the correct `AgentConfig`/`AgentToolConfig` values — check every field, not just a few
   - The `bash_exec: null` vs. key-absent edge case behaves exactly as architect specified — construct both variants and confirm the resulting `AgentToolConfig` matches architect's documented intent
   - A malformed frontmatter (missing closing `---`, invalid YAML) fails loud with a clear error, not a silent partial parse
   - The system prompt body is captured correctly, including cases with multiple paragraphs, code blocks, or content that itself contains `---` (shouldn't be mistaken for a frontmatter delimiter if it's not at the very start)

2. **Bootstrap correctness**
   - First run (no `.aa/`) correctly extracts all embedded agent configs and skills to disk
   - Second run (`.aa/` already exists) does NOT overwrite user edits — modify an extracted file, re-run, confirm the modification persists
   - The missing-file fallback behavior (architect's decision) works as specified — delete one agent's config from an existing `.aa/`, confirm the documented behavior (error vs. fallback to embedded) actually happens

3. **Skills discovery**
   - `list_dir(skills/)` + manifest-line extraction produces the expected lightweight context injection — confirm full skill content is NOT read unless explicitly requested, by checking what's actually in the initial session context vs. what's available on disk

4. **`cmd/harness` end-to-end**
   - Running the CLI against a real (test-fixture) project directory with a real config file successfully constructs and runs a `TurnLoop` against a Fake LLM — this is the first true end-to-end test of the whole stack (Phase 1 storage + Phase 2 tools + Phase 3 loop + Phase 4 config), so treat it as an integration smoke test, not just a unit test of Phase 4's own code

5. **Carried-forward Phase 3 decisions**
   - Confirm whatever architect decided for session-ID exposure is actually implemented and testable — if `HaltReason` gained a field, confirm it's populated correctly
   - Confirm the delta-call-count decision (dedicated event type, or documented behavior) matches what architect specified

## Regression check
Full existing suite (Phase 1 store/llm + Phase 2 tools + Phase 3 loop, 157 tests as of Phase 3's last count) still passes.

## Deliverable
Test suite and pass/fail report covering all of the above. Append to `phase-4.log` via `write_log`. If this passes cleanly, Phase 4 is ready to merge per §15.
