# Phase 3 — Turn Loop & Loop Detection — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-3-turn-loop.md`, librarian's `.feature` files, and `phase-3.log` (architect's, librarian's, and builder's entries).

## Your task this phase
Verify the turn loop, halt detection, retry/backoff, and session-reuse logic all behave correctly — this phase has the most interacting edge cases so far, so prioritize the interaction scenarios over restating basic tool correctness (already covered in Phase 1/2).

## What to check

1. **Carried-forward fix verification**: confirm `ToolConfig.ProjectRoot` is genuinely symlink-resolved when constructed by the turn loop — construct a symlinked-root test scenario at this integration level (not just the unit-level test builder added), confirming `AllowPath` behaves correctly end-to-end through the actual loop, not just in isolation.

2. **Halt detection — all of librarian's interaction scenarios**, especially: two halt conditions true in the same turn (confirm exactly one halt event logged); delta/semantic halt firing independently of hardcoded thresholds; halt happening before an unnecessary extra model call (verify via event log — no `model_call` event should follow a halt).

3. **Token-budget vs. max-turns precedence**: construct a scenario where cumulative tokens would cross 85% before max_turns, and confirm the token-based stop fires first; separately confirm max_turns still works as a backstop if you can construct a scenario where token growth is unusually slow.

4. **Retry/backoff — one test per error category** (all six: Timeout, RateLimit-with-header, RateLimit-without-header, Quota, Malformed, Auth, Unknown — that's seven scenarios since RateLimit splits in two), confirming each produces exactly the specified behavior. Pay particular attention to Auth and Unknown since these are the newest categories (from Phase 1's Fix Cycle 1) and haven't been tested in a real retry-loop context yet.

5. **Session-reuse correctness**:
   - `resume_count` increments correctly across multiple halts on the same logical session
   - The templated summary genuinely reflects real prior data — seed a session with specific known file writes and a specific failure, then confirm the resumed session's initial context actually contains those specifics, not a generic placeholder
   - Confirm message history on resume is NOT a full replay — check the actual message count/size sent to the model on the resumed call

6. **Malformed-retry vs. halt-counter interaction**: confirm a malformed-response retry that succeeds on its second attempt does not increment any halt-detection counters (write-count, etc.), since no tool call occurred during the failed attempt.

## Regression check
Re-run the full existing suite (Phase 1 store/llm + Phase 2 tools, 136 tests as of Phase 2's last count) to confirm nothing regressed from wiring everything together.

## Deliverable
Test suite and pass/fail report covering all of the above, with explicit attention to whether any interaction scenario (item 2, 4, 5, 6) revealed unspecified or contradictory behavior between the ADR and the `.feature` files — if so, name which agent's output needs revisiting. Append to `phase-3.log` via `write_log`. If this passes cleanly, Phase 3 is ready to merge per §15.
