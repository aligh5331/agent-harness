# Phase 2 — Tool Execution & Safety — Fix Cycle 2 — Builder Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read the "Fix Cycle 2" section of `docs/adr-phase-2-tools-safety.md` and architect's latest `phase-2.log` entry — implement exactly what's specified there.

## Your task this cycle
Apply architect's fix to `checkPrefix` in `internal/tools/resolve.go`: resolve `root` via `filepath.EvalSymlinks` with the specified fallback behavior, so both sides of the prefix comparison go through the same resolution.

## Constraints
- Existing tests in `internal/tools/tools_test.go` and `e2e/phase2_test.go` should still pass unless architect's change explicitly requires updating a specific test's expectations — if something previously-passing now needs to change, say so explicitly in your log entry.
- Don't touch anything outside `internal/tools/resolve.go` unless the fix genuinely requires it.
- If architect's ADR leaves any implementation detail ambiguous, flag it back rather than guessing on something that affects path-scoping correctness.

## Deliverable
Working code implementing the fix, compiling cleanly, existing test suite still green (or explicitly noted deviations). Append a `phase-2.log` entry describing what changed.
