# Cross-Phase Fix — Missing API Key Mechanism — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-fix-api-key-mechanism.md` and builder's summary of changes.

## Your task this cycle
1. **Header verification**: confirm the new `httptest`-based test actually captures and asserts the real `Authorization` header value — deliberately break it (comment out the fix) and confirm the test fails, proving it's not a decoy pass.
2. **Missing-key fail-loud**: run the harness with the relevant env var deliberately unset and confirm a clear, specific error naming the missing variable — not a generic 401 from a live call.
3. **Real end-to-end**: with a real key actually set (coordinate with the user for a real Metis key in the shell environment, not committed anywhere), run an actual `--agent prompt-generator --spec` or similar call against the live endpoint and confirm a genuine non-401 response comes back — this is the first real confirmation this harness has ever successfully talked to a live model.
4. **Config consistency**: confirm every agent `.md` file, both embedded and `.aa/` extracted, has the new field, and that no file was missed the way Phase 6's first pass missed one location.
5. Full regression suite still passes.

## Deliverable
Pass/fail report, including confirmation that at least one real, live, successfully-authenticated call was made and worked.
