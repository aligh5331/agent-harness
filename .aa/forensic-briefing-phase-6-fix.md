# Phase 6 — Phase 0 Generator — Fix Cycle 1 — Forensic Re-Audit

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md` §14 layer 1. Before starting, read the "Fix Cycle 1" section of `docs/adr-phase-6-prompt-generator.md` and every Fix Cycle 1 `phase-6.log` entry (architect, builder, tester).

## Context
Your original Phase 6 audit stated the prompt-generator's tool config "properly grants required tools and restricts scope" — this did not hold up under independent review: `create_file` had no `paths:` restriction at all, contradicting the ADR's own stated constraint and this project's established per-agent scoping pattern (§6.2). This re-audit exists specifically to confirm the fix closes that gap, and to re-examine whether anything else in Phase 6's original scope check was taken on trust rather than verified directly against the code.

## Your task this cycle
1. Directly inspect `internal/config/embedded/agents/prompt-generator.md` (and the `.aa/` extracted copy) yourself — confirm the `paths:` restriction is present and correctly scoped, don't rely on builder's or tester's log summaries alone.
2. Re-examine your own original four checks from the first Phase 6 audit with the same "verify directly, don't trust the claim" standard this gap should have received the first time:
   - Manifest correctness
   - Briefing structure
   - Tool/agent config (this is the one that was wrong — confirm it's actually right now)
   - Deviation check
3. Specifically check whether `edit_file` being absent from this agent's tool grant (rather than present-but-restricted) is correct and intentional, per architect's fix-cycle clarification.

## Deliverable
An updated audit verdict per item, explicitly noting that the tool/agent config item was previously incorrect and is now confirmed fixed (or still not fixed, if your re-check finds otherwise). Append a `phase-6.log` entry via `write_log`. If everything genuinely checks out this time, Phase 6 is ready.
