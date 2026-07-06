# Phase 3 — Turn Loop & Loop Detection — Fix Cycle 2 — Builder Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read the "Fix Cycle 2" section of `docs/adr-phase-3-turn-loop.md` and architect's latest `phase-3.log` entry — implement exactly what's specified there.

## Your task this cycle
Apply architect's fix to `tools.NewDefaultRegistry`'s signature (returning the resolved root), and update every call site architect enumerated — test files and any production wiring — to source `resolvedRoot` from that single return value rather than computing or passing it separately.

## Constraints
- Update `TestTester_SymlinkEndToEndThroughLoop` specifically: it currently passes raw `symDir` to both `NewDefaultRegistry` and `loop.New` separately — after this fix, it should get the resolved root from `NewDefaultRegistry`'s return value and pass that same value to `loop.New`, eliminating the possibility of the mismatch by construction.
- Existing tests should still pass unless architect's signature change explicitly requires updating a specific test's call site — if so, update it and note it in your log entry.
- Don't touch anything outside what architect's ADR addendum specifies.

## Deliverable
Working code implementing the signature change and all call-site updates, compiling cleanly, existing test suite green. Append a `phase-3.log` entry via `write_log` listing every call site you updated, so tester can confirm none were missed.
