# Cross-Phase Fix — Missing API Key Mechanism — Builder Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-fix-api-key-mechanism.md` in full.

## Your task this cycle
Implement exactly what architect specified:
1. `APIKey` field on `internal/llm.Request`.
2. Whatever env-var-name field architect specified on the YAML config schema and `loop.AgentConfig`.
3. Fix `goopenai.DefaultConfig(req.Model)` → use the real API key per architect's design.
4. Fail-loud behavior in `cmd/harness/main.go` when the resolved env var is empty/unset, with a clear error message naming which env var is missing.
5. Update every existing agent `.md` config file (embedded AND `.aa/` extracted copies — don't repeat Phase 6's mistake of only fixing one location) to include the new env-var-name field, using real values matching this project's actual provider setup (Metis for deepseek-backed agents).

## Constraints
- Never write an actual API key value into any file that could be committed — only the env var *name* belongs in config.
- Add the header-verification test architect specified: an `httptest` server that captures the incoming `Authorization` header and asserts its exact value, proving a real key is actually transmitted, not just that a request was attempted.
- Existing tests should still pass; if any auth-related test needs updating because it previously didn't care about header content, update it and note why.

## Deliverable
Working code, `go build`/`go vet` clean, new header-verification test passing, all agent config files (embedded + `.aa/`) updated consistently. Document what was changed and why.
