# Phase 1 — Foundation — Fix Cycle — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read the "Fix Cycle 1" section of `docs/adr-phase-1-foundation.md` and the architect's and builder's latest `phase-1.log` entries.

## Your task this cycle
Verify both fixes actually work, not just that the code compiles:

1. **Retry-After capture**: construct a test (likely `httptest`-based, matching the existing pattern in `llm_test.go`/`openai.go`'s test suite) where the mock server returns a 429 with an actual `Retry-After` header set, and confirm the resulting `LLMError.RetryAfter` reflects the header's value — not zero, not a hardcoded default. Also test the case where a 429 arrives *without* a `Retry-After` header, to confirm the fallback behavior (whatever architect specified for that case) still works correctly.

2. **Error category correctness**: for whatever new category/categories architect specified, construct the corresponding test cases (e.g. a 401 with an invalid-key-style body) and confirm they land in the new category, not `ErrCategoryOther`. Also confirm that genuine uncategorized/unknown errors (network failure, unexpected status code) still land somewhere sensible per architect's spec — don't just test the new category in isolation, confirm the old catch-all cases didn't get misrouted by the change.

## Regression check
Re-run the full existing Phase 1 test suite (store + llm + e2e) to confirm nothing that previously passed now fails. If anything regresses, identify whether it's an expected consequence of architect's design change (and therefore the test needs updating) or an actual bug in builder's implementation.

## Deliverable
Pass/fail report covering both fixes plus regression status. Append to `phase-1.log` via `write_log`. If this passes cleanly, Phase 1 is done and ready to merge per §15's branch/audit convention.
