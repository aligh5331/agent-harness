# Phase 6 — Phase 0 Generator — Builder Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-6-prompt-generator.md` and `phase-6.log`. Implement against that ADR, including its worked example — your implementation's output on that same test spec should match what architect showed, or you should flag exactly where and why it diverges.

## Your task this phase
1. The `prompt-generator` agent config (`agents/prompt-generator.md`, both in `internal/config/embedded/agents/` and as the pattern for `.aa/agents/`) — a real system prompt, not a placeholder, written to reliably produce kickoff briefings matching the structural shape architect specified.
2. Whatever code/wiring is needed to invoke this agent against a spec file input and capture its output (kickoff briefings, loop templates, stage-selection manifest) at the correct paths.
3. The stage-selection manifest format and writer, per architect's design.

## Constraints
- Don't modify any of the five existing internal packages beyond what's strictly needed to wire this agent's invocation (e.g. if `cmd/harness` needs a new flag or mode, that's fine — restructuring `internal/loop` is not, without flagging back first).
- Test against architect's worked example concretely: run this phase's implementation with the same test spec architect used, and confirm the generated output actually matches (or note precisely where it diverges and why, e.g. non-determinism in model output — if using a real LLM call for generation, use the Fake LLM for this phase's own automated tests, matching every prior phase's pattern, and note separately if a manual real-model smoke test was also done).

## Deliverable
Working code for the prompt-generator agent config, invocation wiring, and manifest writer. Append a `phase-6.log` entry via `write_log` summarizing what was implemented, confirming the worked-example comparison, and noting any deviations from the ADR.
