# Phase 3 — Turn Loop & Loop Detection — Builder Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-3-turn-loop.md` and the `.feature` files librarian produced this phase, plus `phase-3.log`. Implement from the `.feature` specs primarily — they're the concrete behavioral contract; the ADR is the design rationale behind them.

## Your task this phase
Implement the turn loop, halt detection, retry/backoff, and session-reuse logic exactly as specified.

## Critical carried-forward fix — do this first
Before anything else, apply the `ToolConfig.ProjectRoot` symlink-resolution fix architect specified (constructing `ToolConfig` with an `EvalSymlinks`-resolved root, not a raw path). This closes the gap Phase 2 explicitly deferred to this phase. Write a quick regression test confirming `AllowPath` behaves correctly with a symlinked root before moving on to the rest of the loop — don't let this get lost among the larger turn-loop work.

## Implementation scope
1. Turn loop wiring `internal/llm` (Phase 1) and `internal/tools` (Phase 2) together
2. Halt detection (hardcoded write-count/content-hash + delta/semantic query mode) reading from `internal/store` (Phase 1)
3. Cumulative-token-based stop condition as primary control, `max_turns` as backstop
4. Retry/backoff per the full `ErrorCategory` set (all 6 categories, including Fix Cycle 1's Auth/Unknown split)
5. Session-reuse: same `session_id`, `resume_count` increment, templated-summary fresh context (not replay)

## Constraints
- Don't modify `internal/llm`, `internal/store`, or `internal/tools` beyond the `ToolConfig.ProjectRoot` fix above — those packages are merged and fixed.
- If a librarian `.feature` scenario and the ADR seem to disagree on a specific behavior, flag it back rather than picking one silently — this is exactly the kind of ambiguity the librarian step exists to catch, so if it slipped through, it's worth surfacing.
- No config file parsing, no git integration in this phase — hand-construct whatever per-agent settings the loop needs for its own tests.

## Deliverable
Working Go code implementing the turn loop and all four supporting mechanisms, compiling cleanly, passing librarian's `.feature`-derived test scenarios. Append a `phase-3.log` entry via `write_log` summarizing what was built, confirming the carried-forward `ProjectRoot` fix was applied, and noting any deviations from the ADR or `.feature` files.
