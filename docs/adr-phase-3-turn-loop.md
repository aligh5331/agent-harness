# ADR-Phase-3 — Turn Loop & Loop Detection

**Status:** Approved (Architect)
**Phase:** 3 of 7
**Date:** 2026-07-06
**Agents:** architect → librarian → builder → tester
**Depends on:** Phase 1 (internal/store, internal/llm), Phase 2 (internal/tools) — both fixed, merged dependencies

---

## Table of Contents

1. [Package Layout](#1-package-layout)
2. [AgentConfig — Hand-Constructed Per-Agent Configuration](#2-agentconfig--hand-constructed-per-agent-configuration)
3. [TurnLoop — Core Loop Structure](#3-turnloop--core-loop-structure)
4. [Tool Dispatch and Result Formatting](#4-tool-dispatch-and-result-formatting)
5. [Halt Detection — Hardcoded](#5-halt-detection--hardcoded)
6. [Halt Detection — Delta/Semantic](#6-halt-detection--deltasemantic)
7. [Max Turns — Cumulative Token Stop](#7-max-turns--cumulative-token-stop)
8. [Retry/Backoff — All Six Error Categories](#8-retrybackoff--all-six-error-categories)
9. [Session-Reuse Sequence (§9)](#9-session-reuse-sequence-9)
10. [Carried-Forward Fix: ToolConfig.ProjectRoot Symlink Resolution](#10-carried-forward-fix-toolconfigprojectroot-symlink-resolution)
11. [Carried-Forward Fix: Content Hash in Tool Results](#11-carried-forward-fix-content-hash-in-tool-results)
12. [Open Decisions for Builder](#12-open-decisions-for-builder)
13. [Risk Register](#13-risk-register)

---

## 1. Package Layout

```
agent-harness/
├── internal/
│   ├── loop/                   # NEW — turn loop, halt detection, retry, session-reuse
│   │   ├── loop.go             #   TurnLoop struct, Run() method
│   │   ├── halt.go             #   Hardcoded + delta/semantic halt detection
│   │   ├── backoff.go          #   Retry/backoff logic for all 6 error categories
│   │   ├── resume.go           #   Session-reuse templated summary builder
│   │   └── loop_test.go        #   Tests using Fake LLM + in-memory Store
│   ├── store/                  # Phase 1 — unchanged
│   ├── llm/                    # Phase 1 — unchanged
│   └── tools/                  # Phase 2 — one addition: content_hash in tool results
├── docs/
│   └── adr-phase-3-turn-loop.md  # ← this file
```

### Justification

| Package | Rationale |
|---------|-----------|
| `internal/loop/` | The turn loop is a single-threaded orchestrator that imports `internal/llm`, `internal/tools`, and `internal/store`. It has no consumers outside itself — it is the top-level orchestration that `cmd/harness` calls into. One package with no sub-packages keeps the call chain flat. |
| `loop.go` | Exports `TurnLoop` struct and `Run() method`. Contains the main loop body and the `AgentConfig` type. |
| `halt.go` | Exports `HaltReason` type and two check functions: `CheckHardcoded` and `CheckDelta`. Not methods on TurnLoop — standalone functions that accept a `*store.Store` and `sessionID` so they can be unit-tested without a full loop. |
| `backoff.go` | Exports `RetryPolicy` struct and `NextBackoff` method. Contains the exponential-backoff-with-jitter calculation and the per-category policy table. |
| `resume.go` | Exports `BuildResumeSummary` function that queries `events`/`files` and renders a `text/template` summary. |

### What does NOT live in `internal/loop/`

- `internal/llm/` and `internal/tools/` remain independent — the loop imports them, never the reverse.
- `Tool`, `Registry`, `ToolConfig`, `AgentToolConfig` remain in `internal/tools/` — the loop uses them, does not redefine them.

---

## 2. AgentConfig — Hand-Constructed Per-Agent Configuration

Since real YAML-frontmatter config parsing is Phase 4, Phase 3 uses hand-constructed Go structs. These live in `internal/loop/loop.go` alongside the `TurnLoop` struct.

```go
// AgentConfig defines the configuration for a single agent session.
// In Phase 3 this is hand-constructed; Phase 4 will parse it from YAML.
type AgentConfig struct {
    // Name identifies the agent type: architect|builder|tester|forensic|librarian.
    // Stored in the session DB row for audit.
    Name string

    // ModelName is the OpenAI-compatible model identifier (e.g. "deepseek-v4-flash").
    ModelName string

    // BaseURL is the OpenAI-compatible API endpoint URL.
    BaseURL string

    // ContextMaxTokens is the model's context window limit.
    // Used to compute the cumulative-token stop threshold (§7).
    ContextMaxTokens int

    // Temperature is the model sampling temperature.
    Temperature float64

    // SystemPrompt is the agent's fixed system prompt.
    SystemPrompt string

    // UserPrompt is the initial user message (kickoff prompt).
    UserPrompt string

    // Tools is the per-agent per-tool access control map.
    // A tool name present in this map is granted to the agent.
    // A tool name absent is not available.
    Tools tools.AgentToolConfig
}
```

### Hand-constructed usage pattern for tests

```go
agentCfg := loop.AgentConfig{
    Name:             "builder",
    ModelName:        "deepseek-v4-flash",
    BaseURL:          "http://localhost:7890/v1",
    ContextMaxTokens: 32768,
    Temperature:      0.2,
    SystemPrompt:     "You are a Go builder agent.",
    UserPrompt:       "Implement feature X from the spec.",
    Tools: tools.AgentToolConfig{
        "read_file":   {},
        "edit_file":   {},
        "create_file": {},
        "list_dir":    {},
        "bash_exec":   {},
        "write_log":   {},
    },
}
```

---

## 3. TurnLoop — Core Loop Structure

### 3.1 Constructor and Config

```go
// TurnLoop is the core orchestration: call LLM, dispatch tools, detect loops.
// One instance per agent session. Not safe for concurrent use (serial turn loop).
type TurnLoop struct {
    llm         llm.LLM
    store       *store.Store
    registry    tools.Registry       // already filtered for this agent
    agentCfg    AgentConfig
    logPath     string               // path to phase-N.log for write_log tool
}

// New creates a TurnLoop. The registry should already be filtered via
// FilterByAgentConfig — the loop does not filter further.
func New(
    llmClient llm.LLM,
    st *store.Store,
    filteredRegistry tools.Registry,
    cfg AgentConfig,
    logPath string,
) *TurnLoop {
    return &TurnLoop{
        llm:      llmClient,
        store:    st,
        registry: filteredRegistry,
        agentCfg: cfg,
        logPath:  logPath,
    }
}
```

### 3.2 The Run() Method — Full Flow

```go
// HaltReason describes why the loop stopped.
type HaltReason struct {
    Code        HaltCode // halt_completed|halt_hardcoded|halt_delta|halt_token_limit|halt_max_turns|halt_error
    Message     string   // human-readable explanation
    ResumeCount int      // incremented on session-reuse
}

type HaltCode int

const (
    HaltCompleted   HaltCode = iota // model responded with text, no more tool calls
    HaltHardcoded                   // write_count or content_hash triggered
    HaltDelta                       // delta/semantic model check triggered
    HaltTokenLimit                  // cumulative_tokens > context_max_tokens * 0.85
    HaltMaxTurns                    // backstop max_turns reached
    HaltError                       // unrecoverable error
    HaltQuota                       // ErrCategoryQuota — needs user intervention
    HaltAuth                        // ErrCategoryAuth — needs user config fix
    HaltMalformed                   // repeated ErrCategoryMalformed
)
```

The `Run` method implements the full loop:

```go
func (l *TurnLoop) Run(ctx context.Context) (HaltReason, error) {
    // Step 1: Create session in DB.
    sessionID, err := l.createSession(ctx)
    if err != nil {
        return HaltReason{Code: HaltError}, fmt.Errorf("create session: %w", err)
    }

    // Step 2: Build initial message history.
    messages := []llm.Message{
        {Role: "system", Content: l.agentCfg.SystemPrompt},
        {Role: "user", Content: l.agentCfg.UserPrompt},
    }

    // Step 3: Track cumulative state.
    turnIndex := 0
    cumulativeTokens := 0
    maxTurns := 50  // generous backstop — see §7
    haltOnResume := false  // set true to trigger session-reuse sequence
    finalHalt := HaltReason{Code: HaltCompleted}

    // Step 4: Retry state (reset each LLM call attempt).
    var retryState RetryState

    // Step 5: Main loop.
    for turn := 0; turn < maxTurns; turn++ {
        turnIndex = turn

        // --- 5a. Check cumulative token limit before calling LLM. ---
        if cumulativeTokens >= int(float64(l.agentCfg.ContextMaxTokens)*0.85) {
            finalHalt = HaltReason{
                Code:    HaltTokenLimit,
                Message: fmt.Sprintf("cumulative tokens %d exceeds %.0f%% of context limit %d",
                    cumulativeTokens, 85.0, l.agentCfg.ContextMaxTokens),
            }
            break
        }

        // --- 5b. Call LLM with exponential backoff on retryable errors. ---
        resp, err := l.callWithRetry(ctx, llm.Request{
            Model:     l.agentCfg.ModelName,
            BaseURL:   l.agentCfg.BaseURL,
            Messages:  messages,
            Tools:     l.registry.Definitions(),
            MaxTokens: l.agentCfg.ContextMaxTokens / 2, // generous per-response limit
        }, &retryState)

        if err != nil {
            // callWithRetry handles retries internally. If it returns an error,
            // it's an unrecoverable error or a halted-on-retry-exhaustion signal.
            halt, haltErr := l.handleLLMFatalError(ctx, sessionID, turnIndex, err, &retryState)
            if haltErr != nil {
                return HaltReason{}, haltErr
            }
            finalHalt = halt
            haltOnResume = (halt.Code == HaltTimeout || halt.Code == HaltMalformed || halt.Code == HaltUnknown)
            break
        }

        // --- 5c. Log model_call event. ---
        tokensUsed := resp.Usage.TotalTokens
        cumulativeTokens += tokensUsed
        l.logModelCall(ctx, sessionID, turnIndex, tokensUsed, resp)

        // --- 5d. Check if model wants to respond with text (no tool calls) ---
        if len(resp.ToolCalls) == 0 {
            // Model replied with text — session completed normally.
            finalHalt = HaltReason{
                Code:    HaltCompleted,
                Message: truncate(resp.Text, 200),
            }
            break
        }

        // --- 5e. Process each tool call (serial — parallel deferred to v2). ---
        for _, tc := range resp.ToolCalls {
            // 5e-i. Dispatch tool call.
            toolResult, toolErr := l.dispatchTool(ctx, sessionID, turnIndex, tc)

            // 5e-ii. Format tool result as a Message for the model.
            toolMsg := l.formatToolResult(tc, toolResult, toolErr)
            messages = append(messages, toolMsg)

            // 5e-iii. Hardcoded halt check after every tool result.
            if halt, triggered := l.checkHardcoded(ctx, sessionID, tc, toolResult, toolErr); triggered {
                finalHalt = halt
                haltOnResume = false // hardcoded halt is final — no resume
                break
            }
        }
        if finalHalt.Code != HaltCompleted && finalHalt.Code != 0 {
            break // halted during tool processing
        }

        // --- 5f. Periodic delta/semantic check (every N turns). ---
        if turn > 0 && turn%5 == 0 {
            if halt, triggered := l.checkDelta(ctx, sessionID, messages); triggered {
                finalHalt = halt
                haltOnResume = false // delta halt is final — model confirmed loop
                break
            }
        }

        // Reset retry state after a successful turn.
        retryState = RetryState{}
    }

    // Step 6: Finalize session.
    l.finalizeSession(ctx, sessionID, finalHalt, cumulativeTokens)

    return finalHalt, nil
}
```

### 3.3 Model-Call Event Logging

```go
func (l *TurnLoop) logModelCall(ctx context.Context, sessionID int64, turnIndex int, tokens int, resp llm.Response) {
    argsJSON := fmt.Sprintf(`{"model":"%s","turn":%d}`, l.agentCfg.ModelName, turnIndex)
    resultJSON := fmt.Sprintf(`{"text_len":%d,"tool_calls":%d}`, len(resp.Text), len(resp.ToolCalls))
    tokensUsed := tokens

    l.store.InsertEvent(ctx, store.Event{
        SessionID:  sessionID,
        TurnIndex:  &turnIndex,
        EventType:  "model_call",
        ArgsJSON:   &argsJSON,
        ResultJSON: &resultJSON,
        TokensUsed: &tokensUsed,
        CreatedAt:  store.NowUTC(),
    })
}
```

### 3.4 Tool Event Logging

```go
func (l *TurnLoop) logToolCall(ctx context.Context, sessionID int64, turnIndex int, tc llm.ToolCall, argsRaw string) int64 {
    evtID, _ := l.store.InsertEvent(ctx, store.Event{
        SessionID: sessionID,
        TurnIndex: &turnIndex,
        EventType: "tool_call",
        ToolName:  &tc.Function.Name,
        ArgsJSON:  &argsRaw,
        CreatedAt: store.NowUTC(),
    })
    return evtID
}

func (l *TurnLoop) logToolResult(ctx context.Context, sessionID int64, turnIndex int, toolName string, resultJSON string, fileID *int64) int64 {
    evtID, _ := l.store.InsertEvent(ctx, store.Event{
        SessionID:  sessionID,
        TurnIndex:  &turnIndex,
        EventType:  "tool_result",
        ToolName:   &toolName,
        FileID:     fileID,
        ResultJSON: &resultJSON,
        CreatedAt:  store.NowUTC(),
    })
    return evtID
}
```

---

## 4. Tool Dispatch and Result Formatting

### 4.1 Dispatch Tool Call

```go
func (l *TurnLoop) dispatchTool(ctx context.Context, sessionID int64, turnIndex int, tc llm.ToolCall) (tools.Result, error) {
    tool, ok := l.registry[tc.Function.Name]
    if !ok {
        // Tool not in filtered registry — this should not happen if the model
        // was only given definitions from the filtered registry, but guard anyway.
        return tools.Result{}, fmt.Errorf("tool %q not found in agent's registry", tc.Function.Name)
    }

    // Build ToolConfig with symlink-resolved ProjectRoot (§10) and per-tool restrictions.
    agentRestrictions := l.agentCfg.Tools[tc.Function.Name]
    toolCfg := tools.ToolConfig{
        ProjectRoot:  l.resolvedRoot,
        AllowedPaths: agentRestrictions.PathGlobs,
    }

    // Log the tool call event first (capture arguments).
    argsRaw := tc.Function.Arguments
    evtID := l.logToolCall(ctx, sessionID, turnIndex, tc, argsRaw)

    // Execute.
    result, err := tool.Execute(ctx, json.RawMessage(argsRaw), toolCfg)

    // Compute result JSON for logging.
    var resultJSON string
    if err != nil {
        resultJSON = fmt.Sprintf(`{"error":"%s"}`, escapeJSON(err.Error()))
    } else {
        dataJSON, _ := json.Marshal(result.Data)
        resultJSON = string(dataJSON)
    }

    // Update file tracking for file-touching tools.
    var fileID *int64
    if resultFileID := l.trackFile(ctx, sessionID, tc, result, err, evtID); resultFileID != nil {
        fileID = resultFileID
    }

    // Log the tool result event.
    l.logToolResult(ctx, sessionID, turnIndex, tc.Function.Name, resultJSON, fileID)

    return result, err
}
```

### 4.2 File Tracking (UpsertFile)

After every edit_file or create_file, the loop upserts the file row:

```go
func (l *TurnLoop) trackFile(ctx context.Context, sessionID int64, tc llm.ToolCall, result tools.Result, err error, eventID int64) *int64 {
    if tc.Function.Name != "edit_file" && tc.Function.Name != "create_file" {
        return nil
    }

    // Extract the path from the tool's arguments (we need it even on error,
    // since the file row tracks attempted writes too).
    var args struct {
        Path string `json:"path"`
    }
    if unmarshalErr := json.Unmarshal([]byte(tc.Function.Arguments), &args); unmarshalErr != nil {
        return nil
    }
    if args.Path == "" {
        return nil
    }

    var contentHash *string
    var writeCount int

    if err == nil {
        // Successful write — extract content_hash from tool result.
        if tc.Function.Name == "edit_file" {
            if ef, ok := result.Data.(EditFileResult); ok {
                if ef.ContentHash != "" {
                    contentHash = &ef.ContentHash
                }
                writeCount = 1 // upsert increments it
            }
        } else if tc.Function.Name == "create_file" {
            if cf, ok := result.Data.(CreateFileResult); ok {
                if cf.ContentHash != "" {
                    contentHash = &cf.ContentHash
                }
                writeCount = 1
            }
        }
    }

    fileID, _ := l.store.UpsertFile(ctx, store.File{
        SessionID:   sessionID,
        Path:        args.Path,
        ContentHash: contentHash,
        LastEventID: &eventID,
        WriteCount:  writeCount, // upsert always increments write_count
    })

    return &fileID
}
```

### 4.3 Formatting Tool Results for the Model

The model sees `tool`-role messages. Errors from tools (especially `ErrNoMatch`, `ErrAmbiguousMatch`) must be surfaced clearly so the model can correct its input.

```go
func (l *TurnLoop) formatToolResult(tc llm.ToolCall, result tools.Result, err error) llm.Message {
    var content string

    if err != nil {
        // Distinguishable errors get clear messages for the model.
        switch e := err.(type) {
        case *tools.ErrNoMatch:
            content = fmt.Sprintf("ERROR: edit_file: zero matches for old_str in %q. The string you searched for was not found. Provide more surrounding context to make the match unique.", e.Path)
        case *tools.ErrAmbiguousMatch:
            content = fmt.Sprintf("ERROR: edit_file: old_str matches %d times in %q. Provide more surrounding context to disambiguate which occurrence to replace.", e.MatchesFound, e.Path)
        case *tools.PathEscapeError:
            content = fmt.Sprintf("ERROR: path %q escapes the project root. Use a path within the project.", e.Path)
        case *tools.DisallowedPathError:
            content = fmt.Sprintf("ERROR: path %q is not allowed by your tool restrictions. You may only access paths matching: %v", e.Path, e.Globs)
        default:
            // Generic tool error.
            content = fmt.Sprintf("ERROR: %s", err.Error())
        }
    } else {
        // Success — serialize result.Data to readable text.
        content = l.formatResultData(tc.Function.Name, result.Data)
    }

    return llm.Message{
        Role:       "tool",
        ToolCallID: tc.ID,
        Content:    content,
    }
}
```

**`formatResultData`** converts each tool's result struct into a model-readable string:

- `read_file`: `"<path> (<line_count> lines)\n<content>"` — the content already includes line numbers.
- `edit_file`: `"Success: replaced 1 match in <path>"` — concise, the model can read the file to verify.
- `create_file`: `"Success: created <path>"`.
- `list_dir`: `"<path> entries: [<entry1>, <entry2>, ...]"`.
- `bash_exec`: `"Exit code: <code>\nstdout:\n<output>\nstderr:\n<error>"` — trimmed to 4000 chars to keep the message history bounded.
- `write_log`: `"Logged <N> bytes to phase log."`.

### 4.4 Model Response When No Tool Calls

When the model returns a text response with no tool calls, the loop considers the session complete (`HaltCompleted`). The text is logged as a `text` event and optionally included in `phase-N.log`.

---

## 5. Halt Detection — Hardcoded

Located in `internal/loop/halt.go`. Runs after every tool result, before the next model call. Costs zero additional model calls.

### 5.1 File Write Count Threshold

```go
const DefaultMaxFileWrites = 5

// HaltHardcodedResult is returned by CheckHardcoded.
type HaltHardcodedResult struct {
    Triggered bool
    Reason    string          // human-readable explanation
    Code      HaltCode        // only HaltHardcoded when triggered
}

func (l *TurnLoop) checkHardcoded(
    ctx context.Context,
    sessionID int64,
    tc llm.ToolCall,
    result tools.Result,
    err error,
) (HaltReason, bool) {

    // Only check for file-writing tools.
    if tc.Function.Name != "edit_file" && tc.Function.Name != "create_file" {
        return HaltReason{}, false
    }

    // Extract path from arguments.
    path, ok := extractPathArg(tc.Function.Arguments)
    if !ok {
        return HaltReason{}, false
    }

    // Get current write count from DB.
    count, _ := l.store.FileWriteCount(ctx, sessionID, path)

    // Check content hash for unchanged-content rewrite.
    if err == nil && tc.Function.Name == "edit_file" {
        // Extract the content hash from the result (which needs to be added — see §11).
        // If the file row already has this hash, it means the content didn't change
        // between writes — a stronger loop signal than write count alone.
        currentHash, hashErr := l.getCurrentHash(result)
        if hashErr == nil && currentHash != "" {
            fileRow, _ := l.store.FileByPath(ctx, sessionID, path)
            if fileRow != nil && fileRow.ContentHash != nil && *fileRow.ContentHash == currentHash {
                // Content hash unchanged — strong loop signal.
                return HaltReason{
                    Code:    HaltHardcoded,
                    Message: fmt.Sprintf("hardcoded halt: %q content unchanged after edit (hash: %s)", path, currentHash),
                }, true
            }
        }
    }

    // Check write count threshold.
    if count >= DefaultMaxFileWrites {
        return HaltReason{
            Code:    HaltHardcoded,
            Message: fmt.Sprintf("hardcoded halt: %q edited %d times (max %d)", path, count, DefaultMaxFileWrites),
        }, true
    }

    return HaltReason{}, false
}
```

### 5.2 Content Hash Detection Flow

The content hash comparison works as follows:

1. **After successful edit_file/create_file**: the tool result includes the new `ContentHash` (§11). The loop passes this to `store.UpsertFile`, which updates the `content_hash` column and increments `write_count`.

2. **On the next write to the same file**: the loop reads the current hash from `result.Data`, then queries the existing file row via `store.FileByPath`. If the previous hash equals the new hash, the content did not change — halt.

3. **Edge case — first write**: `content_hash` is `nil` in the DB. The comparison is skipped. The first edit always creates a new hash.

4. **Edge case — edit_file with error (ErrNoMatch/ErrAmbiguousMatch)**: no write occurs, so no hash comparison is needed. The file's `write_count` is not incremented (the tool returned an error, no file was modified). However, the loop still increments `write_count` via `UpsertFile` because the DB tracks "attempted writes" not just "successful writes". Wait — let me reconsider.

Actually, looking at the current `store.UpsertFile` implementation:
```go
ON CONFLICT(session_id, path) DO UPDATE SET
    content_hash = COALESCE(EXCLUDED.content_hash, content_hash),
    last_event_id = COALESCE(EXCLUDED.last_event_id, last_event_id),
    write_count = write_count + 1
```

The `write_count` is always incremented on conflict. This means even on tool errors, the write count goes up. This is actually the correct behavior for loop detection: repeated failed edit attempts on the same file are still a loop signal.

But we need to be careful: `content_hash` is only set when there's a successful write. On error, the ContentHash is nil, so `COALESCE(EXCLUDED.content_hash, content_hash)` preserves the previous value. That's fine.

### 5.3 Threshold Values (Configurable via Constants)

Hardcoded thresholds for Phase 3 — these are package-level `const` values in `halt.go`:

```go
const (
    // DefaultMaxFileWritesPerPath is the maximum number of times a single file
    // may be written (via edit_file or create_file) before the loop halts.
    DefaultMaxFileWrites = 5

    // DefaultDeltaCheckInterval is how many turns between delta/semantic checks.
    DefaultDeltaCheckInterval = 5
)
```

Phase 4's config parser can override these via `AgentConfig` struct fields.

---

## 6. Halt Detection — Delta/Semantic

Located in `internal/loop/halt.go`. Runs every `DefaultDeltaCheckInterval` (5) turns, after all tool results for that turn are processed.

### 6.1 Design

The delta check serializes recent `events` rows into a compact prompt and asks a separate model instance (or the same model — the loop is paused for this call) a narrow yes/no question: "Does this look like a loop?"

### 6.2 SQL Query

```sql
SELECT event_type, tool_name, result_json, created_at
FROM events
WHERE session_id = ?
  AND turn_index >= ?  -- last N turns
ORDER BY turn_index, id
```

**Parameters:**
- `sessionID`: the current session
- `turn_index >= max(0, current_turn - 5)`: last 5 turns of events

### 6.3 Prompt Template

The compact prompt is built in `halt.go`:

```
You are a loop-detection classifier. Given a recent sequence of tool calls and results
from a coding agent, determine whether the agent appears to be looping — repeating the
same actions without making progress toward a goal.

Recent activity (last 5 turns):
---
{turn N}: {tool_name} → {truncated result summary}
{turn N-1}: {tool_name} → {truncated result summary}
...
---

Answer with exactly one word: YES or NO.
If YES, also explain in one sentence why.
```

**Truncated result summary** for each event:
- `edit_file`: `"edited {path}"` or `"FAILED: zero matches for old_str"` or `"FAILED: ambiguous match (N occurrences)"`
- `create_file`: `"created {path}"`
- `read_file`: `"read {path} ({N} lines)"`
- `bash_exec`: `"exit {code}, stdout={N} chars"`
- `list_dir`: `"listed {path} ({N} entries)"`
- `write_log`: `"wrote to phase log"`

### 6.4 How the Answer Feeds Back

The delta check calls the LLM with this compact prompt (and no tools — just a narrow classification request). The response text is parsed:

```go
func (l *TurnLoop) checkDelta(ctx context.Context, sessionID int64, recentMessages []llm.Message) (HaltReason, bool) {
    // 1. Query recent events from DB.
    events, err := l.store.EventsBySession(ctx, sessionID)
    if err != nil || len(events) < 3 {
        return HaltReason{}, false // not enough data to judge
    }

    // 2. Build compact summary.
    summary := buildCompactSummary(events, 5) // last 5 turns

    // 3. Build the narrow classification prompt.
    deltaPrompt := fmt.Sprintf(deltaPromptTemplate, summary)

    // 4. Call the same LLM with just this prompt (no tools).
    resp, err := l.llm.Call(ctx, llm.Request{
        Model:    l.agentCfg.ModelName,
        BaseURL:  l.agentCfg.BaseURL,
        Messages: []llm.Message{{Role: "user", Content: deltaPrompt}},
    })
    if err != nil {
        // If the delta check itself fails (network error, etc.), don't halt.
        // Log the failure and continue — the loop should not fail because
        // the loop-detection check itself failed.
        return HaltReason{}, false
    }

    // 5. Parse the response.
    answer := strings.TrimSpace(strings.ToUpper(resp.Text))
    if strings.HasPrefix(answer, "YES") {
        return HaltReason{
            Code:    HaltDelta,
            Message: fmt.Sprintf("delta halt: model detected looping behavior: %s", truncate(resp.Text, 200)),
        }, true
    }

    return HaltReason{}, false
}
```

### 6.5 Important Design Constraint

The delta check targets the **same model** (not a separate cheap model) because Phase 3 has no multi-model routing. If the deployment uses distinct cheap/expensive models (e.g. DeepSeek Flash for builders, a local Qwen for forensic), the delta check could in principle use the cheap model — but that's a Phase 4+ optimization. For Phase 3, use the same model every time. The delta prompt is tiny (a few hundred tokens), so the cost is negligible relative to the main conversation.

---

## 7. Max Turns — Cumulative Token Stop

### 7.1 Primary Control: Cumulative Tokens

The primary loop termination condition is cumulative token usage exceeding a fraction of the model's context window. This is checked **before every LLM call** (step 5a in the loop) so we never make a call that would exceed the context.

```go
const TokenUsageThreshold = 0.85  // 85% of context_max_tokens

func isTokenLimitReached(cumulativeTokens, contextMaxTokens int) bool {
    threshold := int(float64(contextMaxTokens) * TokenUsageThreshold)
    return cumulativeTokens >= threshold
}
```

**Where `context_max_tokens` comes from for Phase 3 tests:**
- `AgentConfig.ContextMaxTokens` is hand-constructed (e.g., `32768`).
- The cumulative token count is the sum of `resp.Usage.TotalTokens` from each successful `model_call` event.

### 7.2 Backstop: Numeric Max Turns

```go
const DefaultMaxTurns = 50
```

This is a safety net, not the primary control. With a context window of 32K tokens and responses averaging ~500 tokens, the token-limit check (~27K tokens ≈ 50+ turns) will bind before `DefaultMaxTurns` in most cases.

For tests that configure tiny context windows (e.g., `ContextMaxTokens: 500`), the token check binds early at ~425 tokens, which may be 1-3 turns. The backstop exists to catch the pathological case where a model returns 0-token responses (impossible in practice, but the guard exists).

### 7.3 Turn Index Tracking

`turnIndex` is 0-based and tracked per-session. Each `model_call` event stores its turn index. The `events` table's `(turn_index, id)` index provides ordered replay for the delta check and session-reuse queries.

---

## 8. Retry/Backoff — All Six Error Categories

### 8.1 RetryState

```go
type RetryState struct {
    // Attempt counts per category for this session's current LLM call.
    attempts       int
    backoffElapsed time.Duration
    lastCategory   llm.ErrorCategory
    consecutive    int  // consecutive failures within the same LLM call
}

const (
    maxBackoffDuration     = 5 * time.Minute  // cumulative wall time across all retries
    maxAttemptsUnknown     = 3                 // bounded retries for ErrCategoryUnknown
    initialBackoff         = 1 * time.Second
    backoffMultiplier      = 2.0
    jitterFactor           = 0.1  // ±10% jitter
)
```

### 8.2 Per-Category Policy

Located in `internal/loop/backoff.go`.

| Category | Behavior | Retry? | Summary |
|----------|----------|--------|---------|
| `ErrCategoryTimeout` | Exponential backoff with jitter. Cap cumulative wait at 5min. | Yes, bounded | On cap reached: log halt, trigger session-reuse (§9), return `HaltCode=halt_timeout`. |
| `ErrCategoryRateLimit` | Honor `LLMError.RetryAfter` if > 0. If 0 (header not present), fall back to same exponential backoff as Timeout. | Yes, bounded | Same 5min cumulative cap. On cap reached: same as Timeout. |
| `ErrCategoryQuota` | **No retry.** Halt immediately. | No | `HaltCode=halt_quota`. Log the error details. Session status = `error`. Requires user intervention. |
| `ErrCategoryMalformed` | **One immediate retry.** No backoff (the retry happens with the same messages — the server returned 200 OK, the response was garbled, a second attempt may produce valid output). | Yes, once | If second attempt also malformed: halt with `HaltCode=halt_malformed`. Trigger session-reuse (§9). |
| `ErrCategoryAuth` | **No retry.** Halt immediately. | No | `HaltCode=halt_auth`. Log with `StatusCode`. Session status = `error`. User must fix API key/provider config. |
| `ErrCategoryUnknown` | Bounded exponential backoff (max 3 attempts, same 5min cumulative cap). | Yes, bounded | If all 3 attempts fail: halt with `HaltCode=halt_unknown`. Trigger session-reuse (§9). |

### 8.3 Exponential Backoff Calculation

```go
// nextBackoff returns the duration to wait before the next retry attempt.
// Implements: base * multiplier^attempt + jitter
// Cap: cumulative backoff does not exceed maxBackoffDuration.
func nextBackoff(state *RetryState) time.Duration {
    base := float64(initialBackoff) * math.Pow(backoffMultiplier, float64(state.attempts))
    jitter := base * jitterFactor * (rand.Float64()*2 - 1) // ±10%
    d := time.Duration(base + jitter)
    if d < 0 {
        d = 0
    }
    return d
}

// hasExceededMaxBackoff checks if the cumulative backoff across all attempts
// has exceeded the 5-minute cap.
func hasExceededMaxBackoff(state *RetryState) bool {
    return state.backoffElapsed >= maxBackoffDuration
}
```

### 8.4 `callWithRetry` Method

```go
func (l *TurnLoop) callWithRetry(ctx context.Context, req llm.Request, state *RetryState) (llm.Response, error) {
    for {
        resp, err := l.llm.Call(ctx, req)
        if err == nil {
            return resp, nil
        }

        var llmErr *llm.LLMError
        if !errors.As(err, &llmErr) {
            return llm.Response{}, fmt.Errorf("non-llm error: %w", err)
        }

        state.lastCategory = llmErr.Category
        state.consecutive++

        switch llmErr.Category {
        case llm.ErrCategoryQuota:
            return llm.Response{}, &FatalError{Reason: "quota exhausted", haltCode: HaltQuota}

        case llm.ErrCategoryAuth:
            return llm.Response{}, &FatalError{
                Reason:   fmt.Sprintf("auth failure (HTTP %d): %s", llmErr.StatusCode, llmErr.Err.Error()),
                haltCode: HaltAuth,
            }

        case llm.ErrCategoryMalformed:
            if state.consecutive >= 2 {
                // Second malformed response in a row — halt.
                return llm.Response{}, &FatalError{
                    Reason:   fmt.Sprintf("repeated malformed response after retry: %s", llmErr.Err.Error()),
                    haltCode: HaltMalformed,
                }
            }
            // One immediate retry — no backoff.
            continue

        case llm.ErrCategoryTimeout, llm.ErrCategoryRateLimit:
            backoff := nextBackoff(state)
            if llmErr.Category == llm.ErrCategoryRateLimit && llmErr.RetryAfter > 0 {
                backoff = llmErr.RetryAfter // honor Retry-After header
            }

            state.backoffElapsed += backoff
            if hasExceededMaxBackoff(state) {
                return llm.Response{}, &FatalError{
                    Reason:   fmt.Sprintf("%s backoff exhausted after %v: %s",
                        categoryName(llmErr.Category), state.backoffElapsed, llmErr.Err.Error()),
                    haltCode: HaltTimeout, // same halt code for timeout+ratelimit cap
                }
            }

            // Sleep for backoff duration (respecting context cancellation).
            select {
            case <-time.After(backoff):
            case <-ctx.Done():
                return llm.Response{}, ctx.Err()
            }
            state.attempts++
            continue

        case llm.ErrCategoryUnknown:
            if state.consecutive >= maxAttemptsUnknown {
                return llm.Response{}, &FatalError{
                    Reason:   fmt.Sprintf("%d consecutive unknown errors, halting: %s",
                        state.consecutive, llmErr.Err.Error()),
                    haltCode: HaltUnknown,
                }
            }
            backoff := nextBackoff(state)
            state.backoffElapsed += backoff
            if hasExceededMaxBackoff(state) {
                return llm.Response{}, &FatalError{
                    Reason:   fmt.Sprintf("unknown error backoff exhausted: %s", llmErr.Err.Error()),
                    haltCode: HaltUnknown,
                }
            }
            select {
            case <-time.After(backoff):
            case <-ctx.Done():
                return llm.Response{}, ctx.Err()
            }
            state.attempts++
            continue
        }
    }
}
```

### 8.5 FatalError Type

```go
type FatalError struct {
    Reason   string
    haltCode HaltCode
}

func (e *FatalError) Error() string {
    return e.Reason
}
```

### 8.6 `handleLLMFatalError` — Converts FatalError to HaltReason

```go
func (l *TurnLoop) handleLLMFatalError(ctx context.Context, sessionID int64, turnIndex int, err error, state *RetryState) (HaltReason, error) {
    var fatal *FatalError
    if errors.As(err, &fatal) {
        // Log the halt event.
        haltMsg := fmt.Sprintf("%s (turn %d)", fatal.Reason, turnIndex)
        l.store.InsertEvent(ctx, store.Event{
            SessionID: sessionID,
            TurnIndex: &turnIndex,
            EventType: "halt",
            ResultJSON: &haltMsg,
            CreatedAt: store.NowUTC(),
        })

        return HaltReason{
            Code:    fatal.haltCode,
            Message: haltMsg,
        }, nil // fatal errors are handled, not propagated
    }

    // Non-fatal, non-retryable error — propagate.
    return HaltReason{}, err
}
```

---

## 9. Session-Reuse Sequence (§9)

### 9.1 When Session-Reuse is Triggered

Session-reuse runs when a halt occurs with one of these codes:
- `HaltTimeout` (timeout or rate-limit backoff exhausted)
- `HaltMalformed` (repeated malformed response)
- `HaltUnknown` (repeated unknown errors)

These are halts where the failure could be transient — a different context/message history might succeed where the current one failed.

### 9.2 The Session-Reuse Flow

```go
// in TurnLoop.Run(), after the main loop breaks:
if haltOnResume {
    newSessionID, err := l.resumeSession(ctx, sessionID, finalHalt)
    if err != nil {
        return finalHalt, fmt.Errorf("session reuse failed: %w", err)
    }
    // Update the halt reason with the new session info.
    finalHalt.ResumeCount = l.readResumeCount(ctx, newSessionID)
}
```

The `resumeSession` method:

```go
func (l *TurnLoop) resumeSession(ctx context.Context, oldSessionID int64, halt HaltReason) (int64, error) {
    // 1. Mark old session as halted (not done).
    now := store.NowUTC()
    err := l.store.UpdateSession(ctx, store.Session{
        ID:      oldSessionID,
        Status:  "halted",
        EndedAt: &now,
    })
    if err != nil {
        return 0, fmt.Errorf("update halted session: %w", err)
    }

    // 2. Read the old session to get resume_count.
    oldSess, err := l.store.SessionByID(ctx, oldSessionID)
    if err != nil || oldSess == nil {
        return 0, fmt.Errorf("read session for reuse: %w", err)
    }

    // 3. Create a new session with the same session_id... wait, the spec says
    //    "the same session_id row is reused" — meaning we update the existing
    //    row rather than creating a new one.
    newResumeCount := oldSess.ResumeCount + 1
    newStartedAt := store.NowUTC()

    // 4. Build the resume summary from events/files.
    summary := l.buildResumeSummary(ctx, oldSessionID, halt)

    // 5. Update the existing session row:
    //    - Reset started_at (new attempt)
    //    - Set ended_at back to nil (it's running again)
    //    - Increment resume_count
    //    - Set status back to "running"
    err = l.store.UpdateSession(ctx, store.Session{
        ID:          oldSessionID,
        Status:      "running",
        StartedAt:   newStartedAt,  // NOTE: UpdateSession currently only updates status, ended_at, resume_count
        EndedAt:     nil,           // clear the ended_at
        ResumeCount: newResumeCount,
    })
    if err != nil {
        return 0, fmt.Errorf("resume session: %w", err)
    }

    // 6. Replace message history with fresh context:
    //    - Same system prompt
    //    - Resume summary as user prompt (instead of the original kickoff)
    //    This happens in Run() by rebuilding l.messages from scratch.
    //
    //    The builder must handle this: the Run() method needs to accept
    //    that on resume, messages = [system, resumeSummary] rather than
    //    [system, kickoff].

    return oldSessionID, nil
}
```

**Important:** `store.UpdateSession` currently only updates `status`, `ended_at`, and `resume_count`. To support session-reuse, it needs to also update `started_at` and allow clearing `ended_at` (setting it to nil). The builder must update the `UpdateSession` query in `internal/store/queries.go` to:

```sql
UPDATE sessions
SET status = ?, ended_at = ?, resume_count = ?, started_at = ?
WHERE id = ?
```

And call it with `started_at` always set (even on first-run, it's a no-op since it was already set).

### 9.3 Build Resume Summary

```go
func (l *TurnLoop) buildResumeSummary(ctx context.Context, sessionID int64, halt HaltReason) string {
    // 1. Query files touched in this session.
    files, _ := l.store.FilesBySession(ctx, sessionID)

    // 2. Query recent events (last 10 tool_call/tool_result events).
    events, _ := l.store.EventsBySession(ctx, sessionID)

    // 3. Build context via text/template.
    data := ResumeData{
        SessionID:   sessionID,
        HaltReason:  halt.Message,
        FilesTouched: summarizeFiles(files),
        LastEvents:  summarizeEvents(lastN(events, 10)),
    }

    var buf bytes.Buffer
    tmpl := template.Must(template.New("resume").Parse(resumeTemplate))
    tmpl.Execute(&buf, data)
    return buf.String()
}

type ResumeData struct {
    SessionID    int64
    HaltReason   string
    FilesTouched []string
    LastEvents   []string
}
```

### 9.4 The Template

```
Previous session #{{.SessionID}} halted.

Reason: {{.HaltReason}}

Files touched:
{{range .FilesTouched}}  - {{.}}
{{else}}  (none)
{{end}}

Recent activity:
{{range .LastEvents}}  - {{.}}
{{else}}  (none)
{{end}}

Please continue the work where you left off. Focus on completing the original task.
```

### 9.5 summarizeFiles and summarizeEvents

```go
func summarizeFiles(files []store.File) []string {
    out := make([]string, 0, len(files))
    for _, f := range files {
        s := fmt.Sprintf("%s (%d writes)", f.Path, f.WriteCount)
        if f.ContentHash != nil {
            s += fmt.Sprintf(", hash=%s", *f.ContentHash)
        }
        out = append(out, s)
    }
    return out
}

func summarizeEvents(events []store.Event) []string {
    out := make([]string, 0, len(events))
    for _, e := range events {
        var s string
        switch e.EventType {
        case "tool_call":
            s = fmt.Sprintf("turn %d: called %s", *e.TurnIndex, *e.ToolName)
        case "tool_result":
            s = fmt.Sprintf("turn %d: %s result", *e.TurnIndex, *e.ToolName)
        case "halt":
            s = fmt.Sprintf("turn %d: HALT", *e.TurnIndex)
        default:
            continue // skip model_call, text events in summary
        }
        out = append(out, s)
    }
    return out
}
```

### 9.6 How Run() Handles Resume

The loop needs to distinguish first-run from resume. The simplest approach: check `resume_count` when building the initial messages.

```go
func (l *TurnLoop) buildInitialMessages(session *store.Session) []llm.Message {
    if session.ResumeCount > 0 {
        // Resume: build summary from previous attempt.
        // The summary is already stored as a user message.
        // (The resume action happened before this call.)
        return []llm.Message{
            {Role: "system", Content: l.agentCfg.SystemPrompt},
            {Role: "user", Content: l.resumeSummary}, // stored during resumeSession
        }
    }
    return []llm.Message{
        {Role: "system", Content: l.agentCfg.SystemPrompt},
        {Role: "user", Content: l.agentCfg.UserPrompt},
    }
}
```

---

## 10. Carried-Forward Fix: ToolConfig.ProjectRoot Symlink Resolution

### 10.1 The Gap (from Phase 2, §12.5)

Phase 2's Fix Cycle 2 identified that `ToolConfig.AllowPath` uses `ToolConfig.ProjectRoot` to compute `filepath.Rel` for glob matching. If `ProjectRoot` is an unresolved path (e.g., `/home/user/project` that points via symlink to `/data/real/project`), but `resolvedPath` is already symlink-resolved (from `resolveScoped`), then `filepath.Rel` produces a wrong relative path, causing glob matching to fail or produce false matches.

The Phase 2 builder deferred this fix because `ToolConfig` is constructed by Phase 3's turn loop, not Phase 2.

### 10.2 The Fix

**Step 1: Update `NewDefaultRegistry`** in `internal/tools/tools.go` to resolve the project root via `filepath.EvalSymlinks` at construction time:

```go
func NewDefaultRegistry(projectRoot, logPath string) Registry {
    resolvedRoot, err := filepath.EvalSymlinks(projectRoot)
    if err != nil {
        resolvedRoot = projectRoot // fallback: use original
    }
    return Registry{
        "read_file":   &ReadFileTool{root: resolvedRoot},
        "edit_file":   &EditFileTool{root: resolvedRoot},
        "create_file": &CreateFileTool{root: resolvedRoot},
        "list_dir":    &ListDirTool{root: resolvedRoot},
        "bash_exec":   &BashExecTool{root: resolvedRoot},
        "write_log":   &WriteLogTool{logPath: logPath},
    }
}
```

**Step 2: The turn loop stores the resolved root and uses it for `ToolConfig.ProjectRoot`:**

```go
// TurnLoop stores the resolved root.
type TurnLoop struct {
    // ... other fields ...
    resolvedRoot string // EvalSymlinks-resolved project root
}

func New(llmClient llm.LLM, st *store.Store, registry tools.Registry, cfg AgentConfig, logPath string) *TurnLoop {
    // Extract the resolved root from any file tool in the filtered registry.
    // All file tools share the same root.
    resolvedRoot := extractResolvedRoot(registry)
    return &TurnLoop{
        // ...
        resolvedRoot: resolvedRoot,
    }
}

func extractResolvedRoot(registry tools.Registry) string {
    // Read the resolved root from any file-touching tool.
    // Since the registry may have been filtered, check each possibility.
    for _, name := range []string{"read_file", "edit_file", "create_file", "list_dir", "bash_exec"} {
        if tool, ok := registry[name]; ok {
            // Use a type assertion to access the root field.
            // This is a bit fragile — see Open Decision #1.
            switch t := tool.(type) {
            case *tools.ReadFileTool:
                return t.Root() // see §10.3
            // ... similar for other tools
            }
        }
    }
    return "" // should not happen with a valid registry
}
```

### 10.3 Adding a Root Accessor

The tool structs currently have unexported `root` fields. The loop needs to read them. Two options:

**Option A** (chosen): Add an exported `Root() string` method to each file-touching tool:

```go
func (t *ReadFileTool) Root() string { return t.root }
func (t *EditFileTool) Root() string { return t.root }
// ... etc.
```

**Option B**: Pass `resolvedRoot` as a separate parameter to `TurnLoop.New` instead of extracting it from the registry.

The builder may prefer Option B for simplicity — the caller (e.g., `cmd/harness`) resolves the root once and passes it to both `NewDefaultRegistry` and `TurnLoop.New`. This avoids the fragile type-assertion pattern. **Recommendation:** Use Option B.

```go
// In cmd/harness/main.go or wherever the loop is constructed:
resolvedRoot, _ := filepath.EvalSymlinks(projectRoot)
registry := tools.NewDefaultRegistry(resolvedRoot, logPath)
loop := loop.New(llmClient, store, registry.FilterByAgentConfig(cfg.Tools), cfg, logPath, resolvedRoot)
```

```go
// TurnLoop.New accepts resolvedRoot as a parameter:
func New(llm llm.LLM, st *store.Store, registry tools.Registry, cfg AgentConfig, logPath, resolvedRoot string) *TurnLoop {
    return &TurnLoop{
        llm:          llm,
        store:        st,
        registry:     registry,
        agentCfg:     cfg,
        logPath:      logPath,
        resolvedRoot: resolvedRoot,
    }
}
```

### 10.4 ToolConfig Construction in dispatchTool

```go
toolCfg := tools.ToolConfig{
    ProjectRoot:  l.resolvedRoot, // always the EvalSymlinks-resolved root
    AllowedPaths: agentRestrictions.PathGlobs,
}
```

This ensures `AllowPath`'s `filepath.Rel` call operates on matching resolution bases — both `resolvedRoot` and the `resolvedPath` coming from the tool's `resolveScoped` call are symlink-resolved.

---

## 11. Carried-Forward Fix: Content Hash in Tool Results

### 11.1 The Gap (from Phase 2 Builder's Deviation)

The Phase 2 builder noted: "No content hash returned in edit_file/create_file results. The ADR mentioned including the hash — this is deferred."

For Phase 3's hardcoded halt detection (unchanged-content detection in §5.2), the content hash needs to be in the tool result so the loop can pass it to `store.UpsertFile` without re-reading the file.

### 11.2 The Fix

Extend `EditFileResult` and `CreateFileResult` to include the content hash:

```go
// In internal/tools/edit_file.go:
type EditFileResult struct {
    Path         string `json:"path"`
    MatchesFound int    `json:"matches_found"`
    ContentHash  string `json:"content_hash"` // SHA-256 hex of the file after write
}

// In internal/tools/create_file.go:
type CreateFileResult struct {
    Path        string `json:"path"`
    ContentHash string `json:"content_hash"` // SHA-256 hex of the written content
}
```

Update the `Execute` methods to compute and include the hash:

For **edit_file**: after `os.WriteFile(resolvedPath, []byte(newBody), 0644)`, compute `fileHash(newBody)` and include it in the result.

For **create_file**: after writing, compute `fileHash(a.Content)` and include it in the result.

The `fileHash` helper already exists in `internal/tools/tools.go` — it is unexported but usable from within the `tools` package.

### 11.3 No Changes to Other Tools

`read_file`, `list_dir`, `bash_exec`, and `write_log` do not write file content, so no content hash is needed.

---

## 12. Open Decisions for Builder

1. **Root extraction method**: The builder may prefer Option A (Root() accessor on tool structs) or Option B (pass resolvedRoot as a separate parameter to TurnLoop.New). This ADR recommends Option B for simplicity.

2. **`store.UpdateSession` expansion**: To support session-reuse, the `UpdateSession` query and method must be updated to allow setting `started_at` and clearing `ended_at` (setting to nil). The builder should also update the `store_test.go` tests for this method.

3. **Delta check calls the same model**: For Phase 3, the delta/semantic check uses the same LLM client (and therefore the same model). If this introduces significant cost or latency during testing, the builder may add a `DeltaCheckLLM llm.LLM` field to `TurnLoop` that defaults to the main LLM — making it overridable in tests via a `Fake` that returns "NO" without a real API call.

4. **Delta check uses no tools**: The LLM call for delta detection sends a user message with no tool definitions. The builder should verify the model doesn't try to call tools in this context (since the delta prompt doesn't reference them, and tools are omitted from the request).

5. **`EventsBySession` for delta check**: The current query returns ALL events for a session, not just recent ones. For sessions with hundreds of events, this is wasteful. The builder should either (a) add a `Limit` parameter to `EventsBySession`, or (b) add a separate `RecentEventsBySession(ctx, sessionID, limit int)` query. **Recommendation:** Add `RecentEventsBySession(ctx, sessionID, limit int) ([]Event, error)` to `internal/store/queries.go` with a `LIMIT ?` clause.

6. **`FormatResultData` truncation**: For `bash_exec` results, stdout/stderr can be very large. The builder should cap the content returned to the model at ~4000 characters, with a `(truncated)` suffix. This prevents the message history from being dominated by large command outputs.

7. **Event timestamps**: The `CreatedAt` field in events uses `store.NowUTC()` which calls `time.Now().UTC().Format(time.RFC3339)`. This has second-level precision. For tests needing deterministic ordering, events are ordered by `(turn_index, id)` which is sufficient. The builder should verify that events inserted in rapid succession within the same turn have correct ordering via auto-increment `id`.

8. **Fake LLM for loop tests**: The Phase 1 `Fake` LLM is the primary test dependency. The builder should create test helpers that simulate multi-turn conversations: e.g., a `Responder` function that returns tool calls on the first N calls, then a text response. This exercises the full dispatch → halt cycle without a real API.

9. **`DefaultMaxTurns` value**: Set to 50 as a generous backstop. If testing against models with very large context windows (128K+), the token-limit check will bind much later — the builder may increase `DefaultMaxTurns` to 100. This is a negligible cost for a safety net.

10. **`write_count` on failed edits**: When `edit_file` returns `ErrNoMatch` or `ErrAmbiguousMatch`, the tool returns an error — no file write occurs. However, `store.UpsertFile` is still called (to track the attempt) because `trackFile` runs regardless of error. The `write_count` increments even on failed attempts. This is intentional: repeating the same failed edit is a loop signal. The builder should verify this behavior with a test.

---

## 13. Risk Register

| Risk | Impact | Mitigation |
|------|--------|------------|
| **Message history grows unbounded** | Context limit overflow, model drops early messages. | Cumulative token check (§7) halts before overflow. Each turn appends 1 model_call + N tool_result messages, which is bounded by the token threshold. |
| **Delta check floods API** | Every 5th turn adds an extra model call, doubling API cost. | The delta prompt is tiny (~200 tokens). At 50 turns, that's 10 extra calls, each ~500 tokens total — negligible vs. the main conversation's 27K+ tokens. |
| **Session-reuse builds stale summary** | If many files/events exist, the summary becomes too long to be useful. | Template limits to "last 10 events" and "files touched" (base names + write counts). The builder should verify this stays under 1K tokens. |
| **Tool result JSON too large** | Model's context consumed by bash_exec output. | Use truncation in `formatResultData` (§12, #6) — cap at 4000 chars. |
| **`UpdateSession` can't clear `ended_at`** | Session-reuse breaks because DB still has an ended_at. | Explicitly fix `store.UpdateSession` to allow nil `ended_at` and update `started_at`. Builder handles this. |
| **Symlink resolution adds startup latency** | One `EvalSymlinks` call on project root. | Negligible — see Phase 2 ADR §12.7. |
| **Content hash mismatch race** | Between tool writing file and loop reading hash, another process changes the file. | Not a v1 concern — single-agent serial execution. |
| **Tool not found in filtered registry** | Model calls a tool it was not granted (shouldn't happen, but guard code exists). | Guard clause in `dispatchTool` returns an error message to the model, not a crash. |
| **Phase 3 test suite volume** | The loop has many moving parts (retry, halt, session-reuse, delta). | Phase 1's `Fake` LLM with `Responder` patterns. Builder should write targeted unit tests (one per section) rather than trying to cover every combination in integration tests. |

---

## Appendix A: Changes to Existing Files

| File | Change |
|------|--------|
| `internal/tools/tools.go` | Update `NewDefaultRegistry` to resolve `projectRoot` via `filepath.EvalSymlinks`. Add `Root() string` accessor to each file tool (if Option A chosen), or no change (if Option B). |
| `internal/tools/edit_file.go` | Add `ContentHash string` to `EditFileResult`. Compute and populate after successful write. |
| `internal/tools/create_file.go` | Add `ContentHash string` to `CreateFileResult`. Compute and populate after successful write. |
| `internal/tools/resolve.go` | No changes needed — Fix Cycle 2 already resolved the root-symlink bug in `checkPrefix`. |
| `internal/store/queries.go` | Add `RecentEventsBySession(ctx, sessionID, limit int)` query. Update `UpdateSession` to accept optional `started_at` and nil `ended_at`. |
| `internal/store/store_test.go` | Add tests for `RecentEventsBySession` and updated `UpdateSession`. |
| `internal/store/models.go` | No changes — `ResumeCount` already exists on `Session`. |
| `internal/llm/llm.go` | No changes — all 6 error categories already defined. |
| `internal/llm/openai.go` | No changes — capture transport and error classification already wired. |
| `cmd/harness/main.go` | Updated to construct `TurnLoop` with resolved root, call `Run()`. |

## Appendix B: New Files

| File | Purpose |
|------|---------|
| `internal/loop/loop.go` | `TurnLoop` struct, `Run()` method, `AgentConfig`, `HaltReason`, `HaltCode`. |
| `internal/loop/halt.go` | `CheckHardcoded`, `CheckDelta`, threshold constants, delta prompt template. |
| `internal/loop/backoff.go` | `RetryState`, `nextBackoff`, `callWithRetry`, per-category policy table, `FatalError`. |
| `internal/loop/resume.go` | `buildResumeSummary`, `resumeSession`, `ResumeData`, resume template. |
| `internal/loop/loop_test.go` | Tests for all loop components using Fake LLM + in-memory Store. |

## Appendix C: Halt Code → Session Status Mapping

| HaltCode | Session Status | Triggers Session-Reuse? |
|----------|---------------|------------------------|
| `HaltCompleted` | `done` | No |
| `HaltHardcoded` | `halted` | No (model behavior issue, not transient) |
| `HaltDelta` | `halted` | No (model behavior issue, not transient) |
| `HaltTokenLimit` | `halted` | No (context exhausted; resume would repeat the same problem) |
| `HaltMaxTurns` | `halted` | No (same reasoning as token limit) |
| `HaltError` | `error` | No (unrecoverable infrastructure issue) |
| `HaltQuota` | `error` | No (needs user intervention) |
| `HaltAuth` | `error` | No (needs user to fix config) |
| `HaltTimeout` | `halted` | **Yes** — may succeed with fresh context |
| `HaltMalformed` | `halted` | **Yes** — may resolve with different context |
| `HaltUnknown` | `halted` | **Yes** — transient, may succeed with fresh context |

---

## Appendix D: Sequence Diagram — Single Turn

```
┌──────────────────────────────────────────────────────────┐
│  TurnLoop.Run()                                           │
│                                                           │
│  1. check cumulative tokens ≤ threshold                   │
│     │                                                     │
│     ▼                                                     │
│  2. callWithRetry (LLM.Call with messages + tools)        │
│     │                                                     │
│     ├── on error: classify → retry or halt                │
│     │                                                     │
│     ▼                                                     │
│  3. log model_call event                                  │
│     │                                                     │
│     ▼                                                     │
│  4. resp.ToolCalls == 0? → break (HaltCompleted)          │
│     │                                                     │
│     ▼                                                     │
│  5. for each ToolCall (serial):                           │
│     │                                                     │
│     ├── 5a. lookup tool in filtered registry              │
│     ├── 5b. build ToolConfig (resolved ProjectRoot)       │
│     ├── 5c. log tool_call event                           │
│     ├── 5d. tool.Execute(ctx, args, config)               │
│     ├── 5e. log tool_result event                         │
│     ├── 5f. trackFile → UpsertFile (for edit/create)      │
│     ├── 5g. formatResult → append as Message{Role:"tool"} │
│     ├── 5h. checkHardcoded → halt if triggered            │
│     │                                                     │
│     ▼                                                     │
│  6. every 5th turn: checkDelta → halt if triggered        │
│     │                                                     │
│     ▼                                                     │
│  7. update cumulativeTokens, reset retryState              │
│     │                                                     │
│     ▼ (back to 1)                                         │
└──────────────────────────────────────────────────────────┘

---

## 14. Fix Cycle 2 — Structural Root-Mismatch Prevention

### 14.1 The Gap (Post-Builder Audit)

§10 of this ADR correctly identified the `ToolConfig.ProjectRoot` symlink-resolution requirement and recommended **Option B**: the caller resolves the project root once via `filepath.EvalSymlinks` and passes the same resolved value to both `tools.NewDefaultRegistry` and `loop.New`. The builder implemented this as documented.

However, the design as specified relies on the **caller** doing that resolution correctly and consistently, with nothing structurally preventing a mismatch. The gap:

- `NewDefaultRegistry(projectRoot, logPath) Registry` resolves `projectRoot` internally via `EvalSymlinks` for every tool's own `root` field, but does not expose the resolved value.
- `loop.New(..., resolvedRoot string)` takes a separate `resolvedRoot` parameter and stores it as-is — trusting it already matches the value the tools resolved internally.
- Nothing forces these two values to be the same. The caller must manually remember the convention "resolve the root once, pass the result to both calls" — a fragile convention, not a structural guarantee.

**Real-world failure:** In `TestTester_SymlinkEndToEndThroughLoop`, the raw (unresolved) `symDir` is passed to both calls. `NewDefaultRegistry` resolves it internally to the real target directory, but `loop.New` stores the raw symlink path verbatim as `l.resolvedRoot`. This means `ToolConfig.ProjectRoot` (built from `l.resolvedRoot` in `dispatchAndCheck`) can diverge from the path basis the tools use internally. The existing test doesn't catch this because it uses an unrestricted agent (empty `AllowedPaths`), so `AllowPath` short-circuits before ever computing `filepath.Rel` against the mismatched root.

### 14.2 The Fix

Change `NewDefaultRegistry`'s signature to return the resolved root alongside the registry. This makes the resolved root a **first-class output** of the function that computes it, rather than a convention callers must remember.

```go
// NewDefaultRegistry creates the registry with all six built-in tools and
// returns the EvalSymlinks-resolved project root.
//
// The second return value MUST be used as the resolvedRoot argument to
// loop.New. This ensures the root used inside the tools (for resolveScoped)
// and the root used by the loop (for ToolConfig.ProjectRoot) are identical,
// making AllowPath's filepath.Rel computation structurally correct.
func NewDefaultRegistry(projectRoot, logPath string) (Registry, string) {
    resolvedRoot := projectRoot
    if r, err := filepath.EvalSymlinks(projectRoot); err == nil {
        resolvedRoot = r
    }
    return Registry{
        "read_file":   &ReadFileTool{root: resolvedRoot},
        "edit_file":   &EditFileTool{root: resolvedRoot},
        "create_file": &CreateFileTool{root: resolvedRoot},
        "list_dir":    &ListDirTool{root: resolvedRoot},
        "bash_exec":   &BashExecTool{root: resolvedRoot},
        "write_log":   &WriteLogTool{logPath: logPath},
    }, resolvedRoot
}
```

### 14.3 Propagation Rules

**`loop.New` signature: unchanged.** It continues to accept `resolvedRoot string` as its last parameter. The fix is entirely at call sites: callers now source the resolved root from `NewDefaultRegistry`'s second return value and pass it to `loop.New`, rather than passing a variable they resolved (or failed to resolve) themselves.

This is **not** a return to Option A (type-assertion-based extraction from the registry). It is a structural hardening of Option B: the resolved root is still passed as a parameter to `loop.New`, but now it provably comes from the same `EvalSymlinks` call that set the tools' internal roots.

**Call-site rule:** Every caller that constructs a `TurnLoop` MUST use the returned resolved root from `NewDefaultRegistry` for `loop.New`'s last argument. No other source is correct.

### 14.4 Call-Site Enumeration (Mandatory Updates)

Every existing call site of `NewDefaultRegistry` must be updated for the new two-return-value signature. They fall into two categories:

#### Category 1: Discard second value (no loop construction)

These call sites use the registry only (tool tests, e2e tests). Change `reg := NewDefaultRegistry(...)` to `reg, _ := NewDefaultRegistry(...)`.

**File: `internal/tools/tools_test.go`** — 5 sites:

| Line | Current | Change |
|------|---------|--------|
| 999 | `r := NewDefaultRegistry("/test/root", "/test/phase.log")` | `r, _ := NewDefaultRegistry("/test/root", "/test/phase.log")` |
| 1012 | `r := NewDefaultRegistry("/test/root", "/test/phase.log")` | `r, _ := NewDefaultRegistry("/test/root", "/test/phase.log")` |
| 1042 | `r := NewDefaultRegistry("/test/root", "/test/phase.log")` | `r, _ := NewDefaultRegistry("/test/root", "/test/phase.log")` |
| 1057 | `r := NewDefaultRegistry("/test/root", "/test/phase.log")` | `r, _ := NewDefaultRegistry("/test/root", "/test/phase.log")` |
| 1070 | `r := NewDefaultRegistry("/root", "/phase.log")` | `r, _ := NewDefaultRegistry("/root", "/phase.log")` |

**File: `e2e/phase2_test.go`** — 27 sites, all following the pattern `reg := tools.NewDefaultRegistry(tmpDir, logPath)` or `reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))`:

Change every one from `reg := tools.NewDefaultRegistry(...)` to `reg, _ := tools.NewDefaultRegistry(...)`. No test logic changes needed.

Lines: 32, 49, 72, 116, 155, 195, 230, 262, 303, 337, 359, 382, 420, 456, 499, 525, 548, 573, 597, 645, 672, 693, 711, 732, 763, 792, 850.

#### Category 2: Use second value for loop construction

**File: `internal/loop/loop_test.go`** — 2 sites:

1. `newFixtureWithCfg` (line 89):
```go
// Current:
reg := tools.NewDefaultRegistry(projectDir, "/tmp/test-phase.log")
// ...
loop := New(fake, st, filtered, cfg, "/tmp/test-phase.log", projectDir)

// Fixed:
reg, resolvedRoot := tools.NewDefaultRegistry(projectDir, "/tmp/test-phase.log")
// ...
loop := New(fake, st, filtered, cfg, "/tmp/test-phase.log", resolvedRoot)
```

2. `TestSymlink_AllowPathWithSymlinkedRoot` (line 1129):
```go
// Current:
reg := tools.NewDefaultRegistry(symDir, "/tmp/test.log")

// Fixed:
reg, resolvedRoot := tools.NewDefaultRegistry(symDir, "/tmp/test.log")
```
Additionally, this test constructs `ToolConfig{ProjectRoot: symDir, ...}` at line 1141, passing the **unresolved** `symDir` as `ProjectRoot`. This is a secondary bug — the resolved root must be used instead:
```go
// Current (BUG: symDir is unresolved):
cfg := tools.ToolConfig{
    ProjectRoot:  symDir,
    AllowedPaths: []string{"*"},
}

// Fixed:
cfg := tools.ToolConfig{
    ProjectRoot:  resolvedRoot,
    AllowedPaths: []string{"*"},
}
```

**File: `internal/loop/tester_phase3_test.go`** — 1 site:

`TestTester_SymlinkEndToEndThroughLoop` (lines 44, 63):
```go
// Current (chained call — cannot extract resolved root):
filteredReg := tools.NewDefaultRegistry(symDir, "/tmp/test.log").FilterByAgentConfig(...)
// ...
loop := New(fake, st, filteredReg, AgentConfig{...}, "/tmp/test.log", symDir)

// Fixed (unchain to capture resolvedRoot):
reg, resolvedRoot := tools.NewDefaultRegistry(symDir, "/tmp/test.log")
filteredReg := reg.FilterByAgentConfig(...)
// ...
loop := New(fake, st, filteredReg, AgentConfig{...}, "/tmp/test.log", resolvedRoot)
```

### 14.5 Summary of Changes

| File | Type | Changes |
|------|------|---------|
| `internal/tools/tools.go` | **Signature change** | `NewDefaultRegistry` returns `(Registry, string)` instead of `Registry`. |
| `internal/tools/tools_test.go` | Update 5 call sites | Add `_` discard for second return value. |
| `internal/loop/loop_test.go` | Update 2 call sites | Capture resolvedRoot, pass to `New()`. Also fix `ToolConfig.ProjectRoot` in `TestSymlink_AllowPathWithSymlinkedRoot`. |
| `internal/loop/tester_phase3_test.go` | Update 1 call site | Unchain `.FilterByAgentConfig()` call to capture resolvedRoot, pass to `New()`. |
| `e2e/phase2_test.go` | Update 27 call sites | Add `_` discard for second return value. |

### 14.6 Verification

After applying all changes:
1. `go build ./...` — must compile (no "too many results" errors).
2. `go vet ./...` — must pass.
3. All tests pass: `go test ./...`
4. `TestTester_SymlinkEndToEndThroughLoop` now truly tests symlink end-to-end (the loop's `resolvedRoot` matches the tools' internal root).

### 14.7 Why This Doesn't Reopen §10 Ambiguity

§10 identified two options: Option A (extract root from registry via type assertions) and Option B (pass root as a parameter). The ADR recommended Option B. This fix **strengthens** Option B by making the resolved root a first-class output of `NewDefaultRegistry` — but the mechanism remains the same (pass the root as a parameter to `loop.New`). We are not reverting to the type-assertion approach. The ambiguity is closed because:

- The resolved root is computed in exactly one place: inside `NewDefaultRegistry`.
- It flows to both consumers (tools' internal `root` fields and the loop's `resolvedRoot`) from that single computation.
- There is no second, separate resolution path that could diverge.
- The caller cannot accidentally use an unresolved root for `loop.New` because the only source for that parameter is `NewDefaultRegistry`'s second return value.
