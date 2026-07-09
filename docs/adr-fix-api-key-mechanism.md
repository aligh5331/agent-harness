# ADR — Fix: Missing API Key Mechanism

**Status:** Approved (Architect)
**Type:** Cross-phase fix (Phase 1 + Phase 4)
**Date:** 2026-07-09
**Trigger:** First dogfooding run (§16) against live Metis endpoint — every prior test used Fake LLM or explicitly tested the 401-failure path as the expected outcome, so the gap was never exercised.

---

## 1. Problem Summary

Two bugs in `internal/llm/openai.go`'s `Call` method, discovered during live-endpoint testing:

1. **Missing field:** `llm.Request` has no `APIKey` field. Nowhere in the type chain from YAML config → `AgentConfig` → `Request` → HTTP call is there a place for a credential to live.

2. **Bug in DefaultConfig call:** Line 107 of `openai.go` reads:
   ```go
   config := goopenai.DefaultConfig(req.Model)
   ```
   `goopenai.DefaultConfig(authToken string)` expects an API key/token as its argument, but receives `req.Model` (e.g. `"deepseek-v4-flash"`). This means the model name is being set as the `Authorization` header value — the request authenticates with the literal string `"Bearer deepseek-v4-flash"` instead of an actual credential, guaranteeing a 401 from any real endpoint.

Neither `AgentConfigFile` (YAML frontmatter schema, `internal/config/parser.go`) nor any existing agent `.md` file has an `api_key` field — this needs a first-class addition to the config layer, not just the LLM layer.

---

## 2. Credential Source Decision

### Decision: Per-agent env-var name in YAML frontmatter

Each agent config file carries an `api_key_env` field in its YAML frontmatter (not the key value itself). At runtime, `cmd/harness/main.go` resolves the value via `os.Getenv` after parsing the config, then injects it into the `llm.Request` before calling the LLM.

**Rationale:**
- `.env` contents are explicitly treated as sensitive material never committed to git, per §15 of the spec ("SQLite database is never committed... may contain sensitive material surfaced during a run — `.env` contents..."). An env-var name in a git-tracked config file is safe; the key value itself is never stored in git.
- The project uses multiple providers per agent (some agents on Metis, some on local llama.cpp). A single fixed env var (e.g. `OPENAI_API_KEY`) would force every agent to share one credential, even when one agent targets a local endpoint that needs no key (or a dummy one) and another targets a paid provider. Per-agent env-var-name solves this cleanly.
- The pattern matches 12-factor app conventions (config via environment variables) already established in the project's design philosophy.

**Rejected alternatives:**
| Option | Reason for rejection |
|--------|---------------------|
| Single fixed env var (`OPENAI_API_KEY`) | Cannot support mixed providers (local keyless + remote keyed) per the project's own multi-provider design |
| YAML field with literal key value (`api_key: sk-...`) | Would commit credentials to git — violates §15 security posture |
| YAML field with key value, gitignored config file | Adds complexity (separate sensitive config file) without benefit over env vars; env vars are the standard for 12-factor apps |
| System keyring / secret store | Over-engineered for a single-user CLI tool; defer to v2 if needed |

---

## 3. Type Changes

### 3.1 `llm.Request` — add `APIKey` field

File: `internal/llm/llm.go`

```go
type Request struct {
    Model     string
    BaseURL   string
    APIKey    string          // <-- NEW: auth token for the request
    Messages  []Message
    Tools     []ToolDef
    MaxTokens int
}
```

This is the transport-level field — it carries the resolved credential value (not an env var name). The `OpenAIClient.Call` method reads it directly.

### 3.2 `AgentConfigFile` — add `APIKeyEnv` field

File: `internal/config/parser.go`

```go
type AgentConfigFile struct {
    Name             string                `yaml:"name"`
    Model            string                `yaml:"model"`
    BaseURL          string                `yaml:"base_url"`
    APIKeyEnv         string                `yaml:"api_key_env"`       // <-- NEW: name of env var to read
    ContextMaxTokens int                   `yaml:"context_max_tokens"`
    Temperature      float64               `yaml:"temperature"`
    MaxFileWrites    int                   `yaml:"max_file_writes,omitempty"`
    Tools            map[string]*ToolEntry `yaml:"tools"`
}
```

This is the config-file-level field — it carries the *name* of the environment variable (e.g. `"METIS_API_KEY"`), not the key value itself.

### 3.3 `loop.AgentConfig` — add `APIKeyEnv` field

File: `internal/loop/loop.go`

```go
type AgentConfig struct {
    Name             string
    ModelName        string
    BaseURL          string
    APIKeyEnv        string              // <-- NEW: env var name for API key
    ContextMaxTokens int
    Temperature      float64
    SystemPrompt     string
    UserPrompt       string
    Tools            tools.AgentToolConfig
    MaxFileWrites    int
}
```

### 3.4 `ToAgentConfig` — propagate the field

File: `internal/config/parser.go`, in the `ToAgentConfig` method:

```go
cfg := loop.AgentConfig{
    Name:             f.Name,
    ModelName:        f.Model,
    BaseURL:          f.BaseURL,
    APIKeyEnv:        f.APIKeyEnv,       // <-- NEW: propagate env var name
    ContextMaxTokens: f.ContextMaxTokens,
    Temperature:      f.Temperature,
    SystemPrompt:     systemBody,
    MaxFileWrites:    f.MaxFileWrites,
    Tools:            make(tools.AgentToolConfig),
}
```

### 3.5 `cmd/harness/main.go` — resolve env var before building the request

After parsing config (line ~135) and before constructing the request sent to `loop.New`, resolve the API key:

```go
// Resolve API key from environment variable (if configured).
apiKey := ""
if cfg.APIKeyEnv != "" {
    apiKey = os.Getenv(cfg.APIKeyEnv)
    if apiKey == "" {
        log.Fatalf(
            "environment variable %s is required for agent %q but is not set",
            cfg.APIKeyEnv, cfg.Name,
        )
    }
}
```

Then pass it into the `TurnLoop` — either via `AgentConfig` (add an `APIKey` field there too) or via a separate parameter. The cleanest approach is to resolve it in `main.go` and pass it through to `llm.Request` at the call site in `loop.go`. This means `loop.AgentConfig` needs a resolved `APIKey` field (not just the env var name), because the turn loop constructs the `llm.Request` internally.

**Revised plan:** Add both `APIKeyEnv` (the env var name, for traceability) and `APIKey` (the resolved value) to `loop.AgentConfig`:

```go
type AgentConfig struct {
    Name             string
    ModelName        string
    BaseURL          string
    APIKeyEnv        string    // env var name from YAML frontmatter
    APIKey           string    // resolved value (set by main.go, not from YAML)
    // ... rest unchanged
}
```

In `main.go`:

```go
cfg.APIKey = apiKey  // set the resolved value
```

In `loop.go`'s `callWithRetry` / `Run` method, when building `llm.Request`:

```go
resp, err := l.callWithRetry(ctx, llm.Request{
    Model:     l.agentCfg.ModelName,
    BaseURL:   l.agentCfg.BaseURL,
    APIKey:    l.agentCfg.APIKey,    // <-- NEW: pass resolved key
    Messages:  messages,
    Tools:     l.registry.Definitions(),
    MaxTokens: l.agentCfg.ContextMaxTokens / 2,
}, &retryState)
```

**Validation rule in `parseAndTranslate`:** `api_key_env` is NOT required for all agents. It is optional. Agents targeting local endpoints (e.g. `http://localhost:7890/v1`) that expect no auth can omit it entirely. The empty-string default causes `main.go` to skip the env var lookup (and skip the `log.Fatalf` guard).

---

## 4. Fix the `DefaultConfig` Bug

File: `internal/llm/openai.go`, line 107.

**Before:**
```go
config := goopenai.DefaultConfig(req.Model)
```

**After:**
```go
config := goopenai.DefaultConfig(req.APIKey)
```

No other changes to the `Call` method are needed — `goopenai.DefaultConfig` sets the `authToken` field in `ClientConfig`, which `goopenai.NewClientWithConfig` uses to populate the `Authorization: Bearer <token>` header on every outgoing request.

---

## 5. Missing-Key Behavior: Fail Loud at Startup

**Decision: Hard `log.Fatalf` if the env var is configured but empty/unset.**

The resolved key is checked in `main.go` immediately after `config.ParseAgentConfig`, before any store or LLM client is created:

```go
if cfg.APIKeyEnv != "" {
    apiKey := os.Getenv(cfg.APIKeyEnv)
    if apiKey == "" {
        log.Fatalf(
            "agent %q requires env var %s (from api_key_env in %s) but it is not set",
            cfg.Name, cfg.APIKeyEnv, agentConfigPath,
        )
    }
    cfg.APIKey = apiKey
}
```

This matches the harness's overall philosophy of failing loud with a clear message rather than silently attempting a call that will predictably 401. The error message includes the agent name, the env var name, and the config file path — everything the user needs to fix the problem.

**Edge case — `api_key_env` is set but the env var resolves to a value that happens to be a valid credential for a different provider:** This is not a problem the harness can or should solve. The env var name is chosen by the user when authoring the agent config file. If they point `METIS_API_KEY` at an env var that holds a llama.cpp dummy token, that's a config error, not a harness bug.

---

## 6. Testing Implication — Close the Blind Spot

**Problem:** Every existing auth-related test in this project checks response *handling* (e.g. "does the LLMError have category ErrCategoryAuth when the server returns 401?"). None verify what credential was actually *sent* in the `Authorization` header. This is how the `DefaultConfig(req.Model)` bug survived for 7 phases.

**Required fix-cycle tests:**

### 6.1 Authorization header assertion via httptest

A new test in `internal/llm/llm_test.go` (or a new test file `internal/llm/openai_test.go`) that:

1. Starts an `httptest.NewServer` with a handler that **captures the `Authorization` header** from the incoming request and asserts its value before responding with a fake 200 OK.
2. Creates an `OpenAIClient` pointed at the test server's URL.
3. Calls `Call` with a `Request` that has a known `APIKey` value.
4. Asserts the server received `Authorization: Bearer <known-key>`.

```go
func TestOpenAIClient_SendsAuthorizationHeader(t *testing.T) {
    var gotAuth string
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        gotAuth = r.Header.Get("Authorization")
        w.WriteHeader(200)
        w.Write([]byte(`{"id":"test","object":"chat.completion","created":123,"model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`))
    }))
    defer srv.Close()

    client := NewOpenAIClient()
    resp, err := client.Call(context.Background(), Request{
        Model:   "test-model",
        BaseURL: srv.URL + "/v1",
        APIKey:  "sk-test-key-12345",
        Messages: []Message{
            {Role: "user", Content: "hi"},
        },
    })
    if err != nil {
        t.Fatalf("Call: %v", err)
    }
    if resp.Text != "ok" {
        t.Errorf("Text = %q, want %q", resp.Text, "ok")
    }
    expectedAuth := "Bearer sk-test-key-12345"
    if gotAuth != expectedAuth {
        t.Errorf("Authorization header = %q, want %q", gotAuth, expectedAuth)
    }
}
```

### 6.2 Config-to-request integration test

A test in `internal/config/config_test.go` that:

1. Parses a config with `api_key_env: TEST_API_KEY`.
2. Sets `os.Setenv("TEST_API_KEY", "sk-test-from-env")` (and defers cleanup).
3. Verifies `cfg.APIKeyEnv == "TEST_API_KEY"`.
4. Verifies that after the main.go resolution logic (or a test helper), `cfg.APIKey == "sk-test-from-env"`.

### 6.3 Missing env var test

A test in `internal/config/config_test.go` that:

1. Parses a config with `api_key_env: TEST_API_KEY`.
2. Does NOT set the env var.
3. Verifies that the resolution helper (or `main.go`'s logic extracted to a function) returns an error / calls `log.Fatalf` equivalent.

### 6.4 Existing test coverage preserved

All existing tests must continue to pass. The `Fake` LLM does not use `APIKey` — it ignores the field entirely, so existing `Fake`-based tests need no changes.

---

## 7. Changes to Existing Agent Config Files

All six embedded agent config files in `internal/config/embedded/agents/*.md` need the `api_key_env` field added with the appropriate value. Given the current setup (all agents use `https://api.metisai.ir/v1` as their `base_url`), the natural choice is `api_key_env: METIS_API_KEY`.

However, this is a **config authoring decision**, not an architecture decision. The ADR specifies that the field exists and is optional. The builder will add it to the embedded defaults with a placeholder value that the user can customize.

---

## 8. Summary of Changes

| File | Change |
|------|--------|
| `internal/llm/llm.go` | Add `APIKey string` to `Request` struct |
| `internal/llm/openai.go` | Line 107: `req.Model` → `req.APIKey` |
| `internal/config/parser.go` | Add `APIKeyEnv string` to `AgentConfigFile`; propagate in `ToAgentConfig` |
| `internal/loop/loop.go` | Add `APIKeyEnv string` and `APIKey string` to `AgentConfig`; pass `APIKey` in `llm.Request` construction |
| `cmd/harness/main.go` | Resolve env var after config parse; `log.Fatalf` if set but empty |
| `internal/llm/llm_test.go` (or `openai_test.go`) | New test: `TestOpenAIClient_SendsAuthorizationHeader` with httptest |
| `internal/config/config_test.go` | New tests: env var resolution, missing env var error |
| `internal/config/embedded/agents/*.md` | Add `api_key_env: METIS_API_KEY` to each agent frontmatter |

---

## 9. Constraints and Risks

1. **Backward compatibility:** The `api_key_env` field is optional. Existing configs without it continue to work (the resolved key will be empty string, which `goopenai.DefaultConfig("")` will accept — the `Authorization` header will be `Bearer ` with an empty token, which will predictably 401, but that matches the current behavior and is a configuration issue, not a regression).

2. **Env var checked at startup, not per-call:** The env var is read once at startup. If the user changes the env var value while the harness is running, it won't be picked up. This matches the 12-factor app convention and avoids the complexity of hot-reload. If hot-reload is needed later, it's a separate feature.

3. **No validation of key format:** The harness does not validate that the resolved key looks like a valid API key (e.g. starts with `sk-` for OpenAI-compatible providers). Different providers use different key formats; validation is the provider's job, not the harness's.

4. **Fake LLM unaffected:** The `Fake` implementation ignores `APIKey` entirely. All existing tests that use `Fake` continue to pass unchanged.

5. **Test isolation for env vars:** Tests that call `os.Setenv` must clean up with `os.Unsetenv` (or `t.Setenv`, which auto-clears at test end). The builder should use `t.Setenv` for test isolation.