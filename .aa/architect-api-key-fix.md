# Cross-Phase Fix — Missing API Key Mechanism — Architect Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. This is a fix to `internal/llm` (Phase 1) and `internal/config` (Phase 4), discovered during the first real dogfooding run (§16) against a live endpoint — every test in this project so far used the Fake LLM or explicitly tested the 401-failure path as its expected outcome, so this gap was never exercised until now.

## Context
Two related bugs in `internal/llm/openai.go`'s `Call`:
1. `Request` (in `internal/llm/llm.go`) has no `APIKey` field at all — nowhere in the type chain from YAML config through to the HTTP call is there a place for a credential to live.
2. `goopenai.DefaultConfig(req.Model)` passes the model name as the API key argument (a straightforward mistake — `DefaultConfig(authToken string)` expects a credential, not a model name), meaning even manually patching in a key elsewhere wouldn't get used by this line as written.

Neither `AgentConfigFile` (YAML frontmatter schema, `internal/config/parser.go`) nor any existing agent `.md` file has an `api_key` field — this needs to be a first-class part of the fix, not just the `internal/llm` layer.

## Your task this cycle
1. **Decide the credential source.** Given this project's established security posture (`.env` contents explicitly treated as sensitive material never committed to git, per §15), specify environment-variable-based credential loading rather than a YAML field in a git-tracked config file. Specify the exact mechanism: does each agent config specify an env var *name* to read (e.g. `api_key_env: METIS_API_KEY` in frontmatter, so different agents/providers can point at different env vars), or is there a single fixed env var the harness always reads? Given this project's own multi-provider-per-agent design (some agents on Metis, some on local llama.cpp which may need no key at all or a dummy one), the per-agent env-var-name approach is likely more correct — confirm or propose an alternative with reasoning.
2. **Specify the type changes**: add `APIKey` to `Request` (internal/llm/llm.go), add whatever field `AgentConfigFile`/`loop.AgentConfig` needs to carry the env var name (not the key value itself) through to where `main.go` resolves it via `os.Getenv` right before constructing the request.
3. **Fix the `DefaultConfig` bug directly**: specify that `goopenai.DefaultConfig(req.APIKey)` is the corrected line.
4. **Missing-key behavior**: specify what happens if the resolved env var is empty/unset — should this be a hard `log.Fatalf` at startup (fail loud, matching the harness's overall philosophy) rather than silently attempting a call that will predictably 401? This is preferable to what just happened to the user — a confusing runtime 401 instead of a clear "the FOO_API_KEY environment variable is not set" error.
5. **Testing implication — close the blind spot, not just the bug.** Specify that fix-cycle tests must verify the actual `Authorization` header value sent in a real HTTP request (via `httptest`, capturing and asserting on the header content, not just the response status code) — every existing auth-related test in this project checks response *handling*, none verify what credential was actually *sent*. This is what let the bug hide for 7 phases; the test suite needs this class of check going forward.

## Deliverable
Write a short ADR (`docs/adr-fix-api-key-mechanism.md`) covering all five items with enough specificity for builder to implement without guessing. Append findings to a log (use your judgment on which phase log this belongs to, or create a dedicated one, given this spans multiple phases' original work).
