# Phase 3 — Turn Loop & Loop Detection — Librarian Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-3-turn-loop.md` (architect's design for this phase) and `phase-3.log`.

## Why this phase has a librarian step
The turn loop, halt detection, retry/backoff, and session-reuse logic have more interacting edge cases than prior phases' straightforward tool implementations — e.g. what happens when a halt condition and a retry-triggering error occur in the same turn, or when session-reuse needs to happen mid-halt-check. Translating architect's design into concrete BDD scenarios up front gives builder unambiguous behavioral specs to implement against, rather than builder inferring edge-case behavior from prose.

## Your task this phase
Write Gherkin `.feature` files covering the behaviors architect specified. Focus on the interaction cases and edge cases specifically — not exhaustive restatement of every tool from Phase 2, which is already tested.

## What to cover

1. **Turn loop basics**: a session with no tool calls terminates after one model response; a session with tool calls loops until the model stops calling tools; tool results get correctly appended to message history before the next model call.

2. **Halt detection interactions**:
   - A file hits the write-count threshold — verify the halt happens immediately after that tool result, before the next model call (no wasted API call)
   - A file is rewritten with identical content (hash match) — verify this triggers halt even if write-count alone wouldn't have
   - The delta/semantic check returns "yes, this looks like a loop" — verify the session halts based on that signal alone, even if hardcoded thresholds haven't been hit
   - Two halt conditions become true in the same turn (e.g. both write-count AND content-hash) — verify only one halt event is logged, not both, and the session cleanly stops

3. **Max-turns / token-budget interaction**:
   - Cumulative tokens cross the 85% threshold before max_turns is reached — verify the token-based stop triggers first
   - max_turns is reached before the token threshold — verify the backstop triggers (this should be rare in practice per the design, but must still work)

4. **Retry/backoff per error category** — one scenario per category from architect's ADR (Timeout, RateLimit-with-header, RateLimit-without-header, Quota, Malformed, Auth, Unknown), verifying each produces the specified behavior (retry with correct backoff/delay, or halt-for-user, or bounded retry then halt).

5. **Session-reuse**:
   - A timeout triggers session-reuse — verify `resume_count` increments, `session_id` stays the same, and the new message history is the templated summary, not a replay of prior messages
   - Verify the templated summary actually reflects real prior session data (files touched, last failure) rather than being empty/generic

6. **Interaction between retry and halt**: a malformed-response retry succeeds on the second attempt — verify this does NOT count against any halt-detection write/loop counters, since no tool call happened yet (matches spec's "model errors don't consume retries against the halt logic" intent — confirm this is architect's actual intent in the ADR, flag if ambiguous).

## Deliverable
`.feature` files under an appropriate test directory (match whatever convention Phase 1/2 established for test organization), covering the scenarios above in Gherkin syntax. Append a `phase-3.log` entry via `write_log` noting what's covered and flagging anything in architect's ADR that was ambiguous enough to require an assumption on your part.
