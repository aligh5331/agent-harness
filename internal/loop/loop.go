// Package loop implements the turn loop — the core orchestration that calls
// the LLM, dispatches tool calls, detects loop conditions, and manages retry
// and session-reuse.
package loop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"text/template"
	"time"

	"agent-harness/internal/llm"
	"agent-harness/internal/store"
	"agent-harness/internal/tools"
)

// ---------------------------------------------------------------------------
// HaltCode
// ---------------------------------------------------------------------------

// HaltCode describes why the loop stopped.
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
	HaltTimeout                     // timeout/rate-limit backoff exhausted
	HaltUnknown                     // repeated ErrCategoryUnknown
)

// HaltReason describes why the loop stopped.
type HaltReason struct {
	Code        HaltCode
	Message     string // human-readable explanation
	ResumeCount int    // incremented on session-reuse
}

// ---------------------------------------------------------------------------
// AgentConfig
// ---------------------------------------------------------------------------

// AgentConfig defines per-agent configuration.
// Phase 4 will parse this from YAML frontmatter.
type AgentConfig struct {
	Name             string
	ModelName        string
	BaseURL          string
	ContextMaxTokens int
	Temperature      float64
	SystemPrompt     string
	UserPrompt       string
	Tools            tools.AgentToolConfig
	MaxFileWrites    int // 0 means use DefaultMaxFileWrites
}

// ---------------------------------------------------------------------------
// TurnLoop
// ---------------------------------------------------------------------------

// TurnLoop is the core orchestration: call LLM, dispatch tools, detect loops.
// One instance per agent session. Not safe for concurrent use.
type TurnLoop struct {
	llm            llm.LLM
	store          *store.Store
	registry       tools.Registry // already filtered for this agent
	agentCfg       AgentConfig
	logPath        string
	resolvedRoot   string
	resumeSummary  string // stored during resumeSession for buildInitialMessages

	// DeltaCheckLLM is the LLM used for delta/semantic halt detection.
	// If nil, the main l.llm is used. Overridable in tests.
	DeltaCheckLLM llm.LLM

	// SleepFunc returns a channel that receives after the given duration.
	// Override in tests to avoid real sleeps. Defaults to time.After.
	SleepFunc func(time.Duration) <-chan time.Time

	// MaxBackoffOverride overrides maxBackoffDuration for testing.
	// 0 means use the package constant.
	MaxBackoffOverride time.Duration

	// MaxRetryAttemptsOverride overrides maxAttemptsUnknown for testing.
	// 0 means use the package constant.
	MaxRetryAttemptsOverride int
}

// New creates a TurnLoop.
func New(
	llmClient llm.LLM,
	st *store.Store,
	filteredRegistry tools.Registry,
	cfg AgentConfig,
	logPath string,
	resolvedRoot string,
) *TurnLoop {
	return &TurnLoop{
		llm:          llmClient,
		store:        st,
		registry:     filteredRegistry,
		agentCfg:     cfg,
		logPath:      logPath,
		resolvedRoot: resolvedRoot,
		SleepFunc:    time.After,
	}
}

// Run implements the full turn loop. It creates a session, runs turns until a
// halt condition is met, finalizes the session, and returns the halt reason.
//
// The caller should inspect the HaltReason: if it has ResumeCount > 0, the
// session was updated for reuse and the caller may call Run() again (a new
// TurnLoop instance with the same store) — the next Run() will detect the
// existing resume_count and build a templated summary as the initial message.
func (l *TurnLoop) Run(ctx context.Context) (HaltReason, error) {
	// --- Step 1: Create session in DB. ---
	sessionID, err := l.createSession(ctx)
	if err != nil {
		return HaltReason{Code: HaltError}, fmt.Errorf("create session: %w", err)
	}

	// --- Step 2: Build initial message history. ---
	messages := l.buildInitialMessages()
	if len(messages) == 0 {
		return HaltReason{Code: HaltError}, fmt.Errorf("build initial messages: no messages")
	}

	// --- Step 3: Track cumulative state. ---
	cumulativeTokens := 0
	maxTurns := DefaultMaxTurns
	haltOnResume := false
	exitedViaBreak := false
	finalHalt := HaltReason{}
	var retryState RetryState

	// --- Step 4: Main loop. ---
	for turn := 0; turn < maxTurns; turn++ {
		// --- 4a. Check cumulative token limit before calling LLM. ---
		if isTokenLimitReached(cumulativeTokens, l.agentCfg.ContextMaxTokens) {
			finalHalt = HaltReason{
				Code: HaltTokenLimit,
				Message: fmt.Sprintf(
					"cumulative tokens %d exceeds %.0f%% of context limit %d",
					cumulativeTokens, TokenUsageThreshold*100, l.agentCfg.ContextMaxTokens,
				),
			}
			exitedViaBreak = true
			break
		}

		// --- 4b. Call LLM with retry on transient errors. ---
		resp, err := l.callWithRetry(ctx, llm.Request{
			Model:     l.agentCfg.ModelName,
			BaseURL:   l.agentCfg.BaseURL,
			Messages:  messages,
			Tools:     l.registry.Definitions(),
			MaxTokens: l.agentCfg.ContextMaxTokens / 2,
		}, &retryState)

		if err != nil {
			// callWithRetry exhausted retries — convert FatalError to HaltReason.
			halt, haltErr := l.handleLLMFatalError(ctx, sessionID, turn, err, &retryState)
			if haltErr != nil {
				return HaltReason{}, haltErr
			}
			finalHalt = halt
			haltOnResume = shouldResume(halt.Code)
			exitedViaBreak = true
			break
		}

		// --- 4c. Log model_call event. ---
		tokensUsed := resp.Usage.TotalTokens
		cumulativeTokens += tokensUsed
		l.logModelCall(ctx, sessionID, turn, tokensUsed, resp)

		// --- 4d. Check if model wants to respond with text (no tool calls). ---
		if len(resp.ToolCalls) == 0 {
			finalHalt = HaltReason{
				Code:    HaltCompleted,
				Message: truncateText(resp.Text, 200),
			}
			exitedViaBreak = true
			break
		}

		// --- 4e. Process each tool call (serial). ---
		for _, tc := range resp.ToolCalls {
			toolResult, toolErr, haltFromTool := l.dispatchAndCheck(ctx, sessionID, turn, tc)
			if haltFromTool != nil {
				finalHalt = *haltFromTool
				haltOnResume = false
				exitedViaBreak = true
				break
			}
			toolMsg := l.formatToolResult(tc, toolResult, toolErr)
			messages = append(messages, toolMsg)
		}
		if exitedViaBreak {
			break
		}

		// --- 4f. Periodic delta/semantic check. ---
		if turn > 0 && (turn+1)%DefaultDeltaCheckInterval == 0 {
			if halt, triggered := l.checkDelta(ctx, sessionID, messages); triggered {
				finalHalt = halt
				haltOnResume = false
				exitedViaBreak = true
				break
			}
		}

		// Reset retry state after a successful turn.
		retryState = RetryState{}
	}

	// Max-turns backstop: if the loop exited via the for-condition rather than
	// a break, it means we hit maxTurns without a halt condition.
	if !exitedViaBreak {
		finalHalt = HaltReason{
			Code:    HaltMaxTurns,
			Message: fmt.Sprintf("max turns (%d) reached without completion", maxTurns),
		}
	}

	// --- Step 5: Session-reuse check. ---
	if haltOnResume {
		newSessionID, err := l.resumeSession(ctx, sessionID, finalHalt)
		if err != nil {
			return finalHalt, fmt.Errorf("session reuse failed: %w", err)
		}
		finalHalt.ResumeCount = l.readResumeCount(ctx, newSessionID)
	}

	// --- Step 6: Finalize session. ---
	l.finalizeSession(ctx, sessionID, finalHalt, cumulativeTokens, haltOnResume)

	return finalHalt, nil
}

// ---------------------------------------------------------------------------
// Session lifecycle helpers
// ---------------------------------------------------------------------------

func (l *TurnLoop) createSession(ctx context.Context) (int64, error) {
	now := store.NowUTC()
	sess := store.Session{
		Project:          l.agentCfg.Name,
		Phase:            3,
		Mode:             l.agentCfg.Name,
		ModelName:        l.agentCfg.ModelName,
		BaseURL:          l.agentCfg.BaseURL,
		ContextMaxTokens: l.agentCfg.ContextMaxTokens,
		ResumeCount:      0,
		StartedAt:        now,
		Status:           "running",
	}
	return l.store.InsertSession(ctx, sess)
}

func (l *TurnLoop) buildInitialMessages() []llm.Message {
	// If we have a stored resume summary (set during resumeSession), use it.
	if l.resumeSummary != "" {
		return []llm.Message{
			{Role: "system", Content: l.agentCfg.SystemPrompt},
			{Role: "user", Content: l.resumeSummary},
		}
	}
	return []llm.Message{
		{Role: "system", Content: l.agentCfg.SystemPrompt},
		{Role: "user", Content: l.agentCfg.UserPrompt},
	}
}

func (l *TurnLoop) finalizeSession(ctx context.Context, sessionID int64, halt HaltReason, _ int, haltOnResume bool) {
	now := store.NowUTC()
	status := haltCodeToStatus(halt.Code)

	// If we're about to resume, don't finalize yet — resumeSession handles it.
	if haltOnResume {
		return
	}

	_ = l.store.UpdateSession(ctx, store.Session{
		ID:      sessionID,
		Status:  status,
		EndedAt: &now,
	})
}

// ---------------------------------------------------------------------------
// Event logging
// ---------------------------------------------------------------------------

func (l *TurnLoop) logModelCall(ctx context.Context, sessionID int64, turnIndex int, tokens int, resp llm.Response) {
	argsJSON := fmt.Sprintf(`{"model":"%s","turn":%d}`, l.agentCfg.ModelName, turnIndex)
	resultJSON := fmt.Sprintf(`{"text_len":%d,"tool_calls":%d}`, len(resp.Text), len(resp.ToolCalls))
	tokensUsed := tokens

	_, _ = l.store.InsertEvent(ctx, store.Event{
		SessionID:  sessionID,
		TurnIndex:  &turnIndex,
		EventType:  "model_call",
		ArgsJSON:   &argsJSON,
		ResultJSON: &resultJSON,
		TokensUsed: &tokensUsed,
		CreatedAt:  store.NowUTC(),
	})
}

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

// ---------------------------------------------------------------------------
// Tool dispatch
// ---------------------------------------------------------------------------

// dispatchAndCheck executes the tool, runs the hardcoded halt check (BEFORE
// upserting file tracking so the hash comparison is against the previous hash),
// then updates file tracking and logs the result.
// Returns the result, error, and an optional HaltReason (non-nil if halted).
func (l *TurnLoop) dispatchAndCheck(ctx context.Context, sessionID int64, turnIndex int, tc llm.ToolCall) (tools.Result, error, *HaltReason) {
	tool, ok := l.registry[tc.Function.Name]
	if !ok {
		return tools.Result{}, fmt.Errorf("tool %q not found in agent's registry", tc.Function.Name), nil
	}

	// Build ToolConfig with symlink-resolved ProjectRoot and per-tool restrictions.
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

	// Run hardcoded halt check BEFORE file tracking (so the hash comparison
	// is against the previous file row, not the one about to be upserted).
	if halt, triggered := l.checkHardcoded(ctx, sessionID, tc, result, err); triggered {
		// Log a minimal tool result event so the event chain is consistent.
		resultJSON := "{}"
		if err != nil {
			resultJSON = fmt.Sprintf(`{"error":"%s"}`, escapeJSON(err.Error()))
		}
		l.logToolResult(ctx, sessionID, turnIndex, tc.Function.Name, resultJSON, nil)
		return tools.Result{}, err, &halt
	}

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

	return result, err, nil
}

// trackFile handles upserting file rows for file-writing tools (edit_file, create_file).
func (l *TurnLoop) trackFile(ctx context.Context, sessionID int64, tc llm.ToolCall, result tools.Result, err error, eventID int64) *int64 {
	if tc.Function.Name != "edit_file" && tc.Function.Name != "create_file" {
		return nil
	}

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
	writeCount := 1 // upsert always increments write_count regardless of error

	if err == nil {
		// Successful write — extract content_hash from the result.
		if tc.Function.Name == "edit_file" {
			if ef, ok := result.Data.(tools.EditFileResult); ok && ef.ContentHash != "" {
				contentHash = &ef.ContentHash
			}
		} else if tc.Function.Name == "create_file" {
			if cf, ok := result.Data.(tools.CreateFileResult); ok && cf.ContentHash != "" {
				contentHash = &cf.ContentHash
			}
		}
	}

	fileID, _ := l.store.UpsertFile(ctx, store.File{
		SessionID:   sessionID,
		Path:        args.Path,
		ContentHash: contentHash,
		LastEventID: &eventID,
		WriteCount:  writeCount,
	})

	return &fileID
}

// ---------------------------------------------------------------------------
// Tool result formatting
// ---------------------------------------------------------------------------

func (l *TurnLoop) formatToolResult(tc llm.ToolCall, result tools.Result, err error) llm.Message {
	var content string

	if err != nil {
		switch e := err.(type) {
		case *tools.ErrNoMatch:
			content = fmt.Sprintf("ERROR: edit_file: zero matches for old_str in %q. Provide more surrounding context.", e.Path)
		case *tools.ErrAmbiguousMatch:
			content = fmt.Sprintf("ERROR: edit_file: old_str matches %d times in %q. Provide more surrounding context.", e.MatchesFound, e.Path)
		case *tools.PathEscapeError:
			content = fmt.Sprintf("ERROR: path %q escapes the project root.", e.Path)
		case *tools.DisallowedPathError:
			content = fmt.Sprintf("ERROR: path %q is not allowed by your tool restrictions.", e.Path)
		default:
			content = fmt.Sprintf("ERROR: %s", err.Error())
		}
	} else {
		content = l.formatResultData(tc.Function.Name, result.Data)
	}

	return llm.Message{
		Role:       "tool",
		ToolCallID: tc.ID,
		Content:    content,
	}
}

func (l *TurnLoop) formatResultData(toolName string, data any) string {
	switch toolName {
	case "read_file":
		if r, ok := data.(tools.ReadFileResult); ok {
			return fmt.Sprintf("%s (%d lines)\n%s", r.Path, r.LineCount, r.Content)
		}
	case "edit_file":
		if r, ok := data.(tools.EditFileResult); ok {
			return fmt.Sprintf("Success: replaced 1 match in %s", r.Path)
		}
	case "create_file":
		if r, ok := data.(tools.CreateFileResult); ok {
			return fmt.Sprintf("Success: created %s", r.Path)
		}
	case "list_dir":
		if r, ok := data.(tools.ListDirResult); ok {
			entries := strings.Join(r.Entries, ", ")
			return fmt.Sprintf("%s entries: [%s]", r.Path, entries)
		}
	case "bash_exec":
		if r, ok := data.(tools.BashExecResult); ok {
			var b strings.Builder
			fmt.Fprintf(&b, "Exit code: %d\n", r.ExitCode)
			if r.TimedOut {
				b.WriteString("(timed out)\n")
			}
			if r.Stdout != "" {
				truncated := truncateText(r.Stdout, 4000)
				fmt.Fprintf(&b, "stdout:\n%s\n", truncated)
			}
			if r.Stderr != "" {
				truncated := truncateText(r.Stderr, 4000)
				fmt.Fprintf(&b, "stderr:\n%s\n", truncated)
			}
			return b.String()
		}
	case "write_log":
		if r, ok := data.(tools.WriteLogResult); ok {
			return fmt.Sprintf("Logged %d bytes to phase log.", r.SizeBytes)
		}
	}
	// Fallback: JSON-encode.
	dataJSON, _ := json.Marshal(data)
	return string(dataJSON)
}

// ---------------------------------------------------------------------------
// Retry/backoff
// ---------------------------------------------------------------------------

// Default constants for retry backoff.
const (
	DefaultMaxFileWrites  = 5
	DefaultDeltaCheckInterval = 5
	DefaultMaxTurns       = 50
	TokenUsageThreshold   = 0.85 // 85% of context_max_tokens
)

// RetryState tracks retry attempts during LLM calls.
type RetryState struct {
	attempts       int
	backoffElapsed time.Duration
	lastCategory   llm.ErrorCategory
	consecutive    int // consecutive failures within the same LLM call
}

// FatalError is returned by callWithRetry when retries are exhausted.
type FatalError struct {
	Reason   string
	HaltCode HaltCode
}

func (e *FatalError) Error() string { return e.Reason }

func isTokenLimitReached(cumulativeTokens, contextMaxTokens int) bool {
	threshold := int(float64(contextMaxTokens) * TokenUsageThreshold)
	return cumulativeTokens >= threshold
}

func shouldResume(code HaltCode) bool {
	return code == HaltTimeout || code == HaltMalformed || code == HaltUnknown
}

func haltCodeToStatus(code HaltCode) string {
	switch code {
	case HaltCompleted:
		return "done"
	case HaltHardcoded, HaltDelta, HaltTokenLimit, HaltMaxTurns, HaltTimeout, HaltMalformed, HaltUnknown:
		return "halted"
	case HaltQuota, HaltAuth, HaltError:
		return "error"
	default:
		return "halted"
	}
}

// ---------------------------------------------------------------------------
// callWithRetry
// ---------------------------------------------------------------------------

const (
	maxBackoffDuration    = 5 * time.Minute
	maxAttemptsUnknown    = 3
	initialBackoff        = 1 * time.Second
	backoffMultiplier     = 2.0
	jitterFactor          = 0.1 // ±10% jitter
)

func nextBackoff(state *RetryState) time.Duration {
	base := float64(initialBackoff) * math.Pow(backoffMultiplier, float64(state.attempts))
	jitter := base * jitterFactor * (rand.Float64()*2 - 1) // ±10%
	d := time.Duration(base + jitter)
	if d < 0 {
		d = 0
	}
	return d
}

func hasExceededMaxBackoff(state *RetryState) bool {
	return state.backoffElapsed >= maxBackoffDuration
}

func (l *TurnLoop) callWithRetry(ctx context.Context, req llm.Request, state *RetryState) (llm.Response, error) {
	maxAttempts := l.maxAttemptsUnknown()
	maxBackoff := l.maxBackoffDuration()

	for {
		resp, err := l.llm.Call(ctx, req)
		if err == nil {
			return resp, nil
		}

		var llmErr *llm.LLMError
		if !errorsAs(err, &llmErr) {
			return llm.Response{}, fmt.Errorf("non-llm error: %w", err)
		}

		state.lastCategory = llmErr.Category
		state.consecutive++

		switch llmErr.Category {
		case llm.ErrCategoryQuota:
			return llm.Response{}, &FatalError{
				Reason:   "quota exhausted",
				HaltCode: HaltQuota,
			}

		case llm.ErrCategoryAuth:
			return llm.Response{}, &FatalError{
				Reason:   fmt.Sprintf("auth failure (HTTP %d): %s", llmErr.StatusCode, llmErr.Err.Error()),
				HaltCode: HaltAuth,
			}

		case llm.ErrCategoryMalformed:
			if state.consecutive >= 2 {
				return llm.Response{}, &FatalError{
					Reason:   fmt.Sprintf("repeated malformed response after retry: %s", llmErr.Err.Error()),
					HaltCode: HaltMalformed,
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
			if state.backoffElapsed >= maxBackoff {
				return llm.Response{}, &FatalError{
					Reason: fmt.Sprintf("%s backoff exhausted after %v: %s",
						categoryName(llmErr.Category), state.backoffElapsed, llmErr.Err.Error()),
					HaltCode: HaltTimeout,
				}
			}

			select {
			case <-l.SleepFunc(backoff):
			case <-ctx.Done():
				return llm.Response{}, ctx.Err()
			}
			state.attempts++
			continue

		case llm.ErrCategoryUnknown:
			if state.consecutive >= maxAttempts {
				return llm.Response{}, &FatalError{
					Reason: fmt.Sprintf("%d consecutive unknown errors, halting: %s",
						state.consecutive, llmErr.Err.Error()),
					HaltCode: HaltUnknown,
				}
			}
			backoff := nextBackoff(state)
			state.backoffElapsed += backoff
			if state.backoffElapsed >= maxBackoff {
				return llm.Response{}, &FatalError{
					Reason:   fmt.Sprintf("unknown error backoff exhausted: %s", llmErr.Err.Error()),
					HaltCode: HaltUnknown,
				}
			}
			select {
			case <-l.SleepFunc(backoff):
			case <-ctx.Done():
				return llm.Response{}, ctx.Err()
			}
			state.attempts++
			continue
		}
	}
}

func (l *TurnLoop) maxBackoffDuration() time.Duration {
	if l.MaxBackoffOverride > 0 {
		return l.MaxBackoffOverride
	}
	return maxBackoffDuration
}

func (l *TurnLoop) maxAttemptsUnknown() int {
	if l.MaxRetryAttemptsOverride > 0 {
		return l.MaxRetryAttemptsOverride
	}
	return maxAttemptsUnknown
}

func (l *TurnLoop) handleLLMFatalError(ctx context.Context, sessionID int64, turnIndex int, err error, _ *RetryState) (HaltReason, error) {
	var fatal *FatalError
	if errorsAs(err, &fatal) {
		haltMsg := fmt.Sprintf("%s (turn %d)", fatal.Reason, turnIndex)
		msg := haltMsg
		_, _ = l.store.InsertEvent(ctx, store.Event{
			SessionID:  sessionID,
			TurnIndex:  &turnIndex,
			EventType:  "halt",
			ResultJSON: &msg,
			CreatedAt:  store.NowUTC(),
		})
		return HaltReason{
			Code:    fatal.HaltCode,
			Message: haltMsg,
		}, nil
	}
	return HaltReason{}, err
}

// ---------------------------------------------------------------------------
// Halts: hardcoded + delta
// ---------------------------------------------------------------------------

func (l *TurnLoop) checkHardcoded(
	ctx context.Context,
	sessionID int64,
	tc llm.ToolCall,
	result tools.Result,
	err error,
) (HaltReason, bool) {
	if tc.Function.Name != "edit_file" && tc.Function.Name != "create_file" {
		return HaltReason{}, false
	}

	path, ok := extractPathArg(tc.Function.Arguments)
	if !ok {
		return HaltReason{}, false
	}

	maxWrites := DefaultMaxFileWrites
	if l.agentCfg.MaxFileWrites > 0 {
		maxWrites = l.agentCfg.MaxFileWrites
	}

	// Get current write count from DB.
	count, _ := l.store.FileWriteCount(ctx, sessionID, path)

	// Check content hash for unchanged-content rewrite.
	if err == nil && tc.Function.Name == "edit_file" {
		if ef, ok := result.Data.(tools.EditFileResult); ok && ef.ContentHash != "" {
			fileRow, _ := l.store.FileByPath(ctx, sessionID, path)
			if fileRow != nil && fileRow.ContentHash != nil && *fileRow.ContentHash == ef.ContentHash {
				return HaltReason{
					Code:    HaltHardcoded,
					Message: fmt.Sprintf("hardcoded halt: %q content unchanged after edit (hash: %s)", path, ef.ContentHash),
				}, true
			}
		}
	}

	// Check write count threshold.
	if count >= maxWrites {
		return HaltReason{
			Code:    HaltHardcoded,
			Message: fmt.Sprintf("hardcoded halt: %q edited %d times (max %d)", path, count, maxWrites),
		}, true
	}

	return HaltReason{}, false
}

// checkDelta performs a delta/semantic check by querying recent events and
// sending a compact summary to the LLM for a yes/no classification.
func (l *TurnLoop) checkDelta(ctx context.Context, sessionID int64, _ []llm.Message) (HaltReason, bool) {
	// Query recent events.
	events, err := l.store.RecentEventsBySession(ctx, sessionID, 10)
	if err != nil || len(events) < 3 {
		return HaltReason{}, false
	}

	// Build compact summary.
	summary := buildCompactSummary(events)

	// Build the narrow classification prompt.
	deltaPrompt := fmt.Sprintf(deltaPromptTemplate, summary)

	// Determine which LLM to use for the delta check.
	deltaLLM := l.llm
	if l.DeltaCheckLLM != nil {
		deltaLLM = l.DeltaCheckLLM
	}

	// Call the LLM with just this prompt (no tools).
	resp, err := deltaLLM.Call(ctx, llm.Request{
		Model:   l.agentCfg.ModelName,
		BaseURL: l.agentCfg.BaseURL,
		Messages: []llm.Message{
			{Role: "user", Content: deltaPrompt},
		},
	})
	if err != nil {
		// If the delta check itself fails, don't halt.
		return HaltReason{}, false
	}

	// Parse the response.
	answer := strings.TrimSpace(strings.ToUpper(resp.Text))
	if strings.HasPrefix(answer, "YES") {
		return HaltReason{
			Code:    HaltDelta,
			Message: fmt.Sprintf("delta halt: model detected looping behavior: %s", truncateText(resp.Text, 200)),
		}, true
	}

	return HaltReason{}, false
}

// ---------------------------------------------------------------------------
// Session-reuse
// ---------------------------------------------------------------------------

func (l *TurnLoop) resumeSession(ctx context.Context, oldSessionID int64, halt HaltReason) (int64, error) {
	now := store.NowUTC()

	// 1. Mark old session as halted.
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

	// 3. Build the resume summary from events/files.
	l.resumeSummary = l.buildResumeSummary(ctx, oldSessionID, halt)

	// 4. Update the existing session row for reuse.
	newResumeCount := oldSess.ResumeCount + 1
	newStartedAt := store.NowUTC()

	err = l.store.UpdateSession(ctx, store.Session{
		ID:          oldSessionID,
		Status:      "running",
		StartedAt:   newStartedAt,
		EndedAt:     nil,
		ResumeCount: newResumeCount,
	})
	if err != nil {
		return 0, fmt.Errorf("resume session: %w", err)
	}

	return oldSessionID, nil
}

func (l *TurnLoop) readResumeCount(ctx context.Context, sessionID int64) int {
	sess, err := l.store.SessionByID(ctx, sessionID)
	if err != nil || sess == nil {
		return 0
	}
	return sess.ResumeCount
}

// buildResumeSummary queries event and file data and renders a text/template summary.
func (l *TurnLoop) buildResumeSummary(ctx context.Context, sessionID int64, halt HaltReason) string {
	files, _ := l.store.FilesBySession(ctx, sessionID)
	events, _ := l.store.RecentEventsBySession(ctx, sessionID, 10)

	data := ResumeData{
		SessionID:    sessionID,
		HaltReason:   halt.Message,
		FilesTouched: summarizeFiles(files),
		LastEvents:   summarizeEvents(events),
	}

	var buf bytes.Buffer
	tmpl := template.Must(template.New("resume").Parse(resumeTemplate))
	_ = tmpl.Execute(&buf, data)
	return buf.String()
}

// ResumeData is the template data for the resume summary.
type ResumeData struct {
	SessionID    int64
	HaltReason   string
	FilesTouched []string
	LastEvents   []string
}

const resumeTemplate = `Previous session #{{.SessionID}} halted.

Reason: {{.HaltReason}}

Files touched:
{{range .FilesTouched}}  - {{.}}
{{else}}  (none)
{{end}}

Recent activity:
{{range .LastEvents}}  - {{.}}
{{else}}  (none)
{{end}}

Please continue the work where you left off. Focus on completing the original task.`

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
		turnIdx := 0
		if e.TurnIndex != nil {
			turnIdx = *e.TurnIndex
		}
		switch e.EventType {
		case "tool_call":
			if e.ToolName != nil {
				s = fmt.Sprintf("turn %d: called %s", turnIdx, *e.ToolName)
			}
		case "tool_result":
			if e.ToolName != nil {
				s = fmt.Sprintf("turn %d: %s result", turnIdx, *e.ToolName)
			}
		case "halt":
			s = fmt.Sprintf("turn %d: HALT", turnIdx)
		default:
			continue
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Delta check helpers
// ---------------------------------------------------------------------------

func buildCompactSummary(events []store.Event) string {
	var b strings.Builder
	for _, e := range events {
		turnIdx := 0
		if e.TurnIndex != nil {
			turnIdx = *e.TurnIndex
		}
		toolName := ""
		if e.ToolName != nil {
			toolName = *e.ToolName
		}

		if e.EventType == "tool_call" {
			var line string
			switch toolName {
			case "edit_file":
				line = fmt.Sprintf("turn %d: edit_file", turnIdx)
			case "create_file":
				line = fmt.Sprintf("turn %d: create_file", turnIdx)
			case "read_file":
				line = fmt.Sprintf("turn %d: read_file", turnIdx)
			case "bash_exec":
				line = fmt.Sprintf("turn %d: bash_exec", turnIdx)
			case "list_dir":
				line = fmt.Sprintf("turn %d: list_dir", turnIdx)
			case "write_log":
				line = fmt.Sprintf("turn %d: write_log", turnIdx)
			default:
				line = fmt.Sprintf("turn %d: %s", turnIdx, toolName)
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(line)
		} else if e.EventType == "tool_result" {
			// Try to extract a concise result summary.
			resultSummary := ""
			if e.ResultJSON != nil {
				var resultData map[string]any
				if json.Unmarshal([]byte(*e.ResultJSON), &resultData) == nil {
					if errStr, ok := resultData["error"].(string); ok {
						resultSummary = fmt.Sprintf(" → FAILED: %s", truncateText(errStr, 60))
					} else if path, ok := resultData["path"].(string); ok {
						switch toolName {
						case "edit_file":
							resultSummary = fmt.Sprintf(" → edited %s", path)
						case "create_file":
							resultSummary = fmt.Sprintf(" → created %s", path)
						case "read_file":
							if lines, ok := resultData["line_count"].(float64); ok {
								resultSummary = fmt.Sprintf(" → read %s (%.0f lines)", path, lines)
							}
						case "bash_exec":
							if exitCode, ok := resultData["exit_code"].(float64); ok {
								if stdout, ok := resultData["stdout"].(string); ok {
									resultSummary = fmt.Sprintf(" → exit %.0f, stdout=%d chars", exitCode, len(stdout))
								}
							}
						case "list_dir":
							if entries, ok := resultData["entries"]; ok {
								if arr, ok := entries.([]any); ok {
									resultSummary = fmt.Sprintf(" → listed %s (%d entries)", path, len(arr))
								}
							}
						case "write_log":
							if size, ok := resultData["size_bytes"].(float64); ok {
								resultSummary = fmt.Sprintf(" → wrote %.0f bytes to phase log", size)
							}
						}
					}
				}
			}
			if resultSummary != "" {
				b.WriteString(resultSummary)
			}
		}
	}
	return b.String()
}

const deltaPromptTemplate = `You are a loop-detection classifier. Given a recent sequence of tool calls and results
from a coding agent, determine whether the agent appears to be looping — repeating the
same actions without making progress toward a goal.

Recent activity:
---
%s
---

Answer with exactly one word: YES or NO.
If YES, also explain in one sentence why.`

// ---------------------------------------------------------------------------
// Utility helpers
// ---------------------------------------------------------------------------

func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func escapeJSON(s string) string {
	// Simple escape: only escape backslash and double-quote.
	s = strings.ReplaceAll(s, "\\", "\\\\")
	return strings.ReplaceAll(s, "\"", "\\\"")
}

func extractPathArg(arguments string) (string, bool) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", false
	}
	return args.Path, args.Path != ""
}

func categoryName(cat llm.ErrorCategory) string {
	switch cat {
	case llm.ErrCategoryTimeout:
		return "timeout"
	case llm.ErrCategoryRateLimit:
		return "rate_limit"
	case llm.ErrCategoryQuota:
		return "quota"
	case llm.ErrCategoryAuth:
		return "auth"
	case llm.ErrCategoryMalformed:
		return "malformed"
	case llm.ErrCategoryUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// errorsAs wraps errors.As to use the standard library implementation.
func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}
