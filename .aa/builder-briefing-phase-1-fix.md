# Phase 1 — Foundation — Fix Cycle — Builder Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read the "Fix Cycle 1" section of `docs/adr-phase-1-foundation.md` and the architect's latest `phase-1.log` entry — implement exactly what's specified there, not your own interpretation of the original gap report.

## Your task this cycle
Implement architect's Fix Cycle 1 decisions in `internal/llm/openai.go` (and `internal/llm/llm.go` if the `ErrorCategory` enum or `LLMError` struct changes):

1. Wire up real `Retry-After` header capture per architect's chosen approach, replacing the `parseRetryAfter` stub.
2. Implement the error category change (split or otherwise) exactly as architect specified, updating `classifyError`'s switch logic to route to the new categories correctly.

## Constraints
- Existing tests in `internal/llm/llm_test.go` should still pass unless architect's change explicitly requires updating a specific test's expectations — if a previously-passing test now needs to change, say so explicitly in your log entry rather than silently editing it.
- Don't touch `internal/store/` — this fix cycle is scoped to `internal/llm/` only.
- If architect's ADR leaves any implementation detail genuinely ambiguous (not just requiring your judgment on trivial things like variable naming), flag it back rather than guessing on something that affects retry behavior correctness.

## Deliverable
Working code implementing both fixes, compiling cleanly, existing test suite still green (or explicitly noted deviations). Append a `phase-1.log` entry via `write_log` describing exactly what changed and confirming test status.
