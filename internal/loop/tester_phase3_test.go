package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-harness/internal/llm"
	"agent-harness/internal/store"
	"agent-harness/internal/tools"
)

// ---------------------------------------------------------------------------
// Area 1: Symlink end-to-end through the actual turn loop
// ---------------------------------------------------------------------------

func TestTester_SymlinkEndToEndThroughLoop(t *testing.T) {
	// Create a real directory with a file.
	realDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(realDir, "target.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink pointing to realDir.
	symDir := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(realDir, symDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Build a TurnLoop through the symlink.
	ctx := context.Background()
	st, err := store.Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	// Build registry through the symlink — NewDefaultRegistry resolves it and
	// returns the real path; pass that to loop.New so both sides use the same root.
	reg, resolvedRoot := tools.NewDefaultRegistry(symDir, "/tmp/test.log")
	filteredReg := reg.FilterByAgentConfig(tools.AgentToolConfig{
		"read_file":   {},
		"edit_file":   {},
		"create_file": {},
	})

	fake := &llm.Fake{}

	// Loop uses the resolved root (real path) for ToolConfig construction.
	loop := New(fake, st, filteredReg, AgentConfig{
		Name:             "builder",
		ModelName:        "fake",
		BaseURL:          "http://fake/v1",
		ContextMaxTokens: 1000,
		SystemPrompt:     "You are a builder.",
		UserPrompt:       "Read target.go",
		Tools: tools.AgentToolConfig{
			"read_file": {},
		},
	}, "/tmp/test.log", resolvedRoot)
	loop.SleepFunc = func(_ time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}

	// Respond with read_file then text.
	var callCount atomic.Int32
	fake.Responder = func(_ context.Context, _ llm.Request) (llm.Response, error) {
		switch callCount.Add(1) - 1 {
		case 0:
			return toolCallResponse("read_file", `{"path":"target.go"}`), nil
		default:
			return textResponse("Done."), nil
		}
	}

	halt, err := loop.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != HaltCompleted {
		t.Errorf("want HaltCompleted, got %d: %s", halt.Code, halt.Message)
	}
}

// ---------------------------------------------------------------------------
// Area 2: Interaction scenarios — halt detection
// ---------------------------------------------------------------------------

// TwoHaltConditionsOneEvent verifies that when both write-count and
// content-hash conditions fire on the same turn, the loop reports a
// single HaltHardcoded and does NOT make any LLM call after the halt.
//
// The delta check also calls l.llm.Call, so the total call count
// includes delta checks. This test counts tool dispatches and halt
// events instead of raw LLM calls.
func TestTester_TwoHaltConditionsOneEvent(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	f.mustCreateFile("shared.go", "same old content")

	// Set MaxFileWrites so the write_count check triggers on the 3rd edit.
	// We also set up same-content edits to trigger content-hash check.
	// Both fire on the 2nd write.
	f.agentCfg.MaxFileWrites = 2

	// First edit changes content (new hash), second edit has same content
	// (same hash → content-hash triggers + write_count also triggers).
	f.fake.Response = toolCallResponse("edit_file",
		`{"path":"shared.go","old_str":"same old content","new_str":"same old content"}`)

	halt, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != HaltHardcoded {
		t.Errorf("want HaltHardcoded, got %d: %s", halt.Code, halt.Message)
	}
}

// HaltBeforeExtraModelCall verifies that after a halt is triggered during
// tool processing, no additional LLM call is made. This uses write-count
// threshold: after 5 successful edits on the same file, the halt fires
// during the 6th tool dispatch (before the 6th main LLM call).
//
// The delta check adds 1 extra call (fires after turn 4).
func TestTester_HaltBeforeExtraModelCall(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	f.mustCreateFile("x.go", "a")

	// Each call returns edit_file with different old_str so content changes
	// and no content-hash trigger.
	var callIdx int
	f.fake.Responder = func(_ context.Context, _ llm.Request) (llm.Response, error) {
		callIdx++
		// Each call edits "x.go" with incrementing content so the hash changes.
		oldStr := fmt.Sprintf("iteration %d", callIdx-1)
		newStr := fmt.Sprintf("iteration %d", callIdx)
		return toolCallResponse("edit_file",
			fmt.Sprintf(`{"path":"x.go","old_str":"%s","new_str":"%s"}`, oldStr, newStr)), nil
	}

	halt, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != HaltHardcoded {
		t.Errorf("want HaltHardcoded, got %d: %s", halt.Code, halt.Message)
	}

	// After halt, no main-loop LLM call is made. The delta check fires
	// at (turn+1)%5==0. Turn 0: (0+1)%5=1 no. Turn 1: (1+1)%5=2 no.
	// ... Turn 4: (4+1)%5=0 yes. But the halt fires earlier (at turn 5,
	// the 6th call, when write_count reaches 5). So delta check fires
	// at turn 4 (the 5th main call). Then turn 5 (6th main call) triggers
	// halt during dispatch.
	//
	// So: 6 main calls (turns 0-5) + 1 delta call (after turn 4) = 7.
	// But the 6th main call's halt fires BEFORE the delta check for turn 5
	// (since delta check is at (5+1)%5=1, no check). Actually the halt
	// in the 6th main call fires during tool dispatch, before the delta
	// check. So delta check only fired after turn 4.
	//
	// But hold on — the halt might fire before the delta check. Let's count:
	//   Turn 0: model call 1, dispatch success, no delta
	//   Turn 1: model call 2, dispatch success, no delta
	//   Turn 2: model call 3, dispatch success, no delta
	//   Turn 3: model call 4, dispatch success, no delta
	//   Turn 4: model call 5, dispatch success, delta check → model call 6 (delta)
	//   Turn 5: model call 7, dispatch → write_count hits 5 → HALT!
	//
	// Total: 7 LLM calls.
	if f.fake.CallCount != 7 {
		t.Errorf("expected 7 LLM calls (6 main + 1 delta), got %d", f.fake.CallCount)
	}
}

// DeltaFiresIndependentlyOfHardcoded verifies the delta check fires even when
// hardcoded thresholds are not nearing. Use read-only turns (no writes)
// so write_count stays 0. After 5 read_file turns the delta check triggers.
func TestTester_DeltaFiresIndependentlyOfHardcoded(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	f.mustCreateFile("a.go", "hello")

	deltaFake := &llm.Fake{
		Response: llm.Response{Text: "YES, the agent is looping on reads."},
	}
	f.loop.DeltaCheckLLM = deltaFake

	// 5 read_file turns, then a text response.
	f.setupResponder(
		toolCallResponse("read_file", `{"path":"a.go"}`), // turn 0
		toolCallResponse("read_file", `{"path":"a.go"}`), // turn 1
		toolCallResponse("read_file", `{"path":"a.go"}`), // turn 2
		toolCallResponse("read_file", `{"path":"a.go"}`), // turn 3
		toolCallResponse("read_file", `{"path":"a.go"}`), // turn 4 → delta check fires on turn 4 ((4+1)%5==0)
		textResponse("Done."),                             // not reached if delta fires
	)

	halt, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != HaltDelta {
		t.Errorf("want HaltDelta, got %d: %s", halt.Code, halt.Message)
	}
	if !strings.Contains(halt.Message, "delta halt") {
		t.Errorf("halt message should contain 'delta halt', got: %s", halt.Message)
	}
}

// ---------------------------------------------------------------------------
// Area 3: Token-budget vs max-turns precedence
// ---------------------------------------------------------------------------

// TokenWinsOverMaxTurns verifies that when cumulative tokens cross
// the 85% threshold, HaltTokenLimit fires instead of HaltMaxTurns.
// With ContextMaxTokens=500 (threshold=425) and 100 tokens/call:
//   Turn 0-3: cum=400, passes. Turn 4: cum=400 < 425 → call+delta, cum=500.
//   Turn 5: cum=500 >= 425 → HaltTokenLimit before call.
//   Total main calls: 5 (turns 0-4). Delta check fires once (after turn 4).
//   Total: 6 LLM calls.
func TestTester_TokenWinsOverMaxTurns(t *testing.T) {
	f := newFixtureWithCfg(t, AgentConfig{
		Name:             "builder",
		ModelName:        "fake-model",
		ContextMaxTokens: 500,
	})
	defer f.close()
	f.mustCreateFile("a.go", "x")

	// Each response is an edit_file tool call (100 tokens).
	f.fake.Response = toolCallResponse("edit_file", `{"path":"a.go","old_str":"x","new_str":"y"}`)

	halt, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != HaltTokenLimit {
		t.Errorf("want HaltTokenLimit, got %d: %s", halt.Code, halt.Message)
	}
	if !strings.Contains(halt.Message, "cumulative tokens") {
		t.Errorf("message should mention 'cumulative tokens', got: %s", halt.Message)
	}

	// 5 main calls (turns 0-4) + 1 delta (after turn 4) = 6.
	// The halt fires BEFORE the 6th main call (turn 5), so no 6th main call.
	if f.fake.CallCount != 6 {
		t.Errorf("expected 6 LLM calls (5 main + 1 delta), got %d", f.fake.CallCount)
	}
}

// MaxTurnsBackstopWithSlowTokens verifies that when tokens grow unusually
// slowly and never cross the 85% threshold, HaltMaxTurns fires after
// DefaultMaxTurns (50) turns. Delta check fires every 5 turns (10 times).
// Total: 50 + 10 = 60 LLM calls.
func TestTester_MaxTurnsBackstopWithSlowTokens(t *testing.T) {
	f := newFixtureWithCfg(t, AgentConfig{
		Name:             "builder",
		ModelName:        "fake-model",
		ContextMaxTokens: 1000000, // 1M — threshold at 850K, never reaches
	})
	defer f.close()
	f.mustCreateFile("a.go", "x")

	// Each turn returns a tool call with 0 tokens.
	zeroTokenResp := llm.Response{
		ToolCalls: []llm.ToolCall{
			{
				ID: "call_read",
				Function: llm.ToolCallFunction{
					Name:      "read_file",
					Arguments: `{"path":"a.go"}`,
				},
			},
		},
		Usage: llm.TokenUsage{TotalTokens: 0},
	}
	f.fake.Response = zeroTokenResp

	halt, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != HaltMaxTurns {
		t.Errorf("want HaltMaxTurns, got %d: %s", halt.Code, halt.Message)
	}
	if !strings.Contains(halt.Message, "max turns") {
		t.Errorf("message should mention 'max turns', got: %s", halt.Message)
	}
	// 50 main calls + 10 delta checks (turns 4,9,14,19,24,29,34,39,44,49).
	if f.fake.CallCount != 60 {
		t.Errorf("expected 60 LLM calls (50 main + 10 delta), got %d", f.fake.CallCount)
	}
}

// ---------------------------------------------------------------------------
// Area 4: Retry per error category — deep assertions
// ---------------------------------------------------------------------------

// AuthHaltsImmediatelyWithMessage verifies the auth halt message contains
// the status code and "auth failure" text.
func TestTester_AuthHaltsImmediatelyWithMessage(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	authErr := &llm.LLMError{
		Err:        fmt.Errorf("invalid API key"),
		Category:   llm.ErrCategoryAuth,
		StatusCode: 401,
	}
	f.setupResponderWithErrors([]any{authErr})

	halt, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != HaltAuth {
		t.Errorf("want HaltAuth, got %d: %s", halt.Code, halt.Message)
	}
	if !strings.Contains(halt.Message, "auth failure") {
		t.Errorf("message should contain 'auth failure', got: %s", halt.Message)
	}
	if !strings.Contains(halt.Message, "401") {
		t.Errorf("message should contain HTTP status code, got: %s", halt.Message)
	}
	if f.fake.CallCount != 1 {
		t.Errorf("expected exactly 1 LLM call (no retry), got %d", f.fake.CallCount)
	}
}

// UnknownHaltsWithCountMessage verifies the unknown error halt message
// contains the consecutive count.
func TestTester_UnknownHaltsWithCountMessage(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	unknownErr := &llm.LLMError{
		Err:      fmt.Errorf("unknown server error"),
		Category: llm.ErrCategoryUnknown,
	}
	f.loop.MaxRetryAttemptsOverride = 3
	f.setupResponderWithErrors([]any{unknownErr})

	halt, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != HaltUnknown {
		t.Errorf("want HaltUnknown, got %d: %s", halt.Code, halt.Message)
	}
	if !strings.Contains(halt.Message, "3 consecutive") {
		t.Errorf("message should contain '3 consecutive', got: %s", halt.Message)
	}
}

// QuotaHaltsImmediately verifies quota halt produces correct code and
// no tool calls are dispatched.
func TestTester_QuotaSessionStatusIsError(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	quotaErr := &llm.LLMError{
		Err:      fmt.Errorf("insufficient_quota"),
		Category: llm.ErrCategoryQuota,
	}
	f.setupResponderWithErrors([]any{quotaErr})

	halt, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != HaltQuota {
		t.Errorf("want HaltQuota, got %d: %s", halt.Code, halt.Message)
	}
	if !strings.Contains(halt.Message, "quota") {
		t.Errorf("message should contain 'quota', got: %s", halt.Message)
	}
	if f.fake.CallCount != 1 {
		t.Errorf("expected 1 LLM call (no retry), got %d", f.fake.CallCount)
	}
}

// ---------------------------------------------------------------------------
// Area 5: Session-reuse correctness
// ---------------------------------------------------------------------------

// SessionReuse_MultipleHalts verifies that calling Run() twice with
// transient errors increments ResumeCount across multiple halts.
// NOTE: Uses timeout errors (transient) so resumeSession runs,
// which stores the resume summary on the TurnLoop.
func TestTester_SessionReuse_MultipleHalts(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	timeoutErr := &llm.LLMError{
		Err:      fmt.Errorf("timeout"),
		Category: llm.ErrCategoryTimeout,
	}

	// First run: every call times out → HaltTimeout + resume.
	f.setupResponderWithErrors([]any{timeoutErr})
	halt1, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if halt1.Code != HaltTimeout {
		t.Errorf("first run: want HaltTimeout, got %d: %s", halt1.Code, halt1.Message)
	}

	// Second run: same TurnLoop, same store — should detect resume_count > 0
	// and build a summary. Timeout again → second resume, ResumeCount incremented.
	halt2, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if halt2.Code != HaltTimeout {
		t.Errorf("second run: want HaltTimeout, got %d: %s", halt2.Code, halt2.Message)
	}
	if halt2.ResumeCount < 1 {
		t.Errorf("expected ResumeCount >= 1 after second halt, got %d", halt2.ResumeCount)
	}
}

// ResumeUsesSummaryNotReplay verifies that on a resumed session,
// the message history sent to the LLM is the summary, not a full
// replay of previous turns.
// Uses read-only turns to avoid hardcoded halt (which does not trigger resume).
func TestTester_ResumeUsesSummaryNotReplay(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	f.mustCreateFile("a.go", "x")

	timeoutErr := &llm.LLMError{
		Err:      fmt.Errorf("timeout"),
		Category: llm.ErrCategoryTimeout,
	}

	// Sequence: 2 read_file calls (no hardcoded risk) then timeout.
	entries := []any{
		toolCallResponse("read_file", `{"path":"a.go"}`),
		toolCallResponse("read_file", `{"path":"a.go"}`),
		timeoutErr,
	}
	f.setupResponderWithErrors(entries)

	halt1, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if halt1.Code != HaltTimeout {
		t.Fatalf("first run: want HaltTimeout, got %d: %s", halt1.Code, halt1.Message)
	}

	// Run 2: capture the request sent to the LLM.
	var capturedRequest llm.Request
	f.fake.Responder = func(_ context.Context, req llm.Request) (llm.Response, error) {
		capturedRequest = req
		return textResponse("Done."), nil
	}

	halt2, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if halt2.Code != HaltCompleted {
		t.Errorf("second run: want HaltCompleted, got %d: %s", halt2.Code, halt2.Message)
	}

	// Verify the captured initial message is the resume summary.
	if len(capturedRequest.Messages) < 2 {
		t.Fatalf("expected at least 2 messages (system + resume), got %d", len(capturedRequest.Messages))
	}

	userMsg := capturedRequest.Messages[1]
	if userMsg.Role != "user" {
		t.Errorf("expected user role for resume summary, got %s", userMsg.Role)
	}
	if !strings.Contains(userMsg.Content, "Previous session") {
		t.Errorf("resume message should contain 'Previous session', got: %s", userMsg.Content)
	}
	if strings.Contains(userMsg.Content, "Implement feature X.") {
		t.Errorf("resume message should NOT contain original kickoff prompt, got: %s", userMsg.Content)
	}

	// The message history should have exactly 2 messages (system + resume summary),
	// NOT a full replay of the 2 prior turns.
	if len(capturedRequest.Messages) != 2 {
		t.Errorf("expected exactly 2 messages (system + resume summary) for resumed session, got %d; want NO full replay",
			len(capturedRequest.Messages))
	}
}

// ResumeShowsFileData verifies the resume summary contains actual file
// data from the prior session. Uses edits that produce different content
// hashes so content-hash halt doesn't fire before the timeout halt.
func TestTester_ResumeShowsFileData(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	// Create files with content that we'll edit with unique text each time
	// so content-hash changes on each edit and doesn't trigger a halt.
	f.mustCreateFile("main.go", "// iteration 0\npackage main\nfunc main() {}")
	f.mustCreateFile("utils.go", "// iteration 0\npackage main\nfunc helper() {}")

	timeoutErr := &llm.LLMError{
		Err:      fmt.Errorf("timeout"),
		Category: llm.ErrCategoryTimeout,
	}

	// Edit main.go 3 times, utils.go 1 time, each with different content
	// so hashes differ. Then timeout to trigger resume.
	var callIdx int
	f.fake.Responder = func(_ context.Context, req llm.Request) (llm.Response, error) {
		callIdx++
		switch callIdx {
		case 1: // edit main.go, iteration 0→1
			return toolCallResponse("edit_file",
				`{"path":"main.go","old_str":"// iteration 0","new_str":"// iteration 1"}`), nil
		case 2: // edit main.go, iteration 1→2
			return toolCallResponse("edit_file",
				`{"path":"main.go","old_str":"// iteration 1","new_str":"// iteration 2"}`), nil
		case 3: // edit main.go, iteration 2→3
			return toolCallResponse("edit_file",
				`{"path":"main.go","old_str":"// iteration 2","new_str":"// iteration 3"}`), nil
		case 4: // edit utils.go, iteration 0→1
			return toolCallResponse("edit_file",
				`{"path":"utils.go","old_str":"// iteration 0","new_str":"// iteration 1"}`), nil
		default:
			return llm.Response{}, timeoutErr
		}
	}

	halt1, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if halt1.Code != HaltTimeout {
		t.Fatalf("first run: want HaltTimeout, got %d: %s", halt1.Code, halt1.Message)
	}

	// Run 2: capture messages.
	var capturedRequest llm.Request
	f.fake.Responder = func(_ context.Context, req llm.Request) (llm.Response, error) {
		capturedRequest = req
		return textResponse("Done."), nil
	}

	halt2, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if halt2.Code != HaltCompleted {
		t.Errorf("second run: want HaltCompleted, got %d: %s", halt2.Code, halt2.Message)
	}

	if len(capturedRequest.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(capturedRequest.Messages))
	}
	summary := capturedRequest.Messages[1].Content

	if !strings.Contains(summary, "main.go") {
		t.Errorf("summary should mention main.go, got: %s", summary)
	}
	if !strings.Contains(summary, "utils.go") {
		t.Errorf("summary should mention utils.go, got: %s", summary)
	}
	if !strings.Contains(summary, "timeout") {
		t.Errorf("summary should mention timeout halt, got: %s", summary)
	}
	if strings.Contains(summary, `{"path":"main.go"`) {
		t.Errorf("summary should NOT contain raw JSON arguments, got: %s", summary)
	}
}

// ---------------------------------------------------------------------------
// Area 6: Malformed retry does not increment halt counters
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Area 7: Symlink + glob restriction (Fix Cycle 2 regression)
// ---------------------------------------------------------------------------

// SymlinkWithGlobRestriction verifies that path glob restrictions are correctly
// evaluated when the project root is a symlink and the agent has Active
// AllowedPaths.
//
// Before Fix Cycle 2, this combination would have FAILED: l.resolvedRoot was
// the unresolved symlink path while the tool's internal root was
// EvalSymlinks-resolved. AllowPath's filepath.Rel then computed the wrong
// relative path (e.g. "../realDir/docs/readme.md" instead of "docs/readme.md"),
// causing the glob "docs/*.md" to reject even matching paths — a false negative.
//
// After Fix Cycle 2, NewDefaultRegistry returns the resolved root and loop.New
// stores it, so both sides use the same path basis. A path matching the glob
// is correctly allowed (content is actually written), and a non-matching path
// is correctly rejected (content unchanged).
func TestTester_SymlinkWithGlobRestriction(t *testing.T) {
	// Create real directory structure with two files.
	realDir := t.TempDir()

	// Matching path: docs/readme.md — should be allowed by "docs/*.md" glob.
	docsFile := filepath.Join(realDir, "docs", "readme.md")
	if err := os.MkdirAll(filepath.Dir(docsFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docsFile, []byte("old content"), 0644); err != nil {
		t.Fatal(err)
	}

	// Non-matching path: other/note.md — should be rejected.
	otherFile := filepath.Join(realDir, "other", "note.md")
	if err := os.MkdirAll(filepath.Dir(otherFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherFile, []byte("other content"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create symlink pointing to realDir.
	symDir := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(realDir, symDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Build TurnLoop through symlink with glob restriction.
	ctx := context.Background()
	st, err := store.Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	// Build registry through the symlink — NewDefaultRegistry returns the
	// resolved path. Pass that to loop.New so both sides agree on the root.
	reg, resolvedRoot := tools.NewDefaultRegistry(symDir, "/tmp/test.log")
	globRestriction := tools.AgentToolConfig{
		"edit_file": {
			PathGlobs: []string{"docs/*.md"},
		},
	}
	filteredReg := reg.FilterByAgentConfig(globRestriction)

	fake := &llm.Fake{}

	loop := New(fake, st, filteredReg, AgentConfig{
		Name:             "builder",
		ModelName:        "fake",
		BaseURL:          "http://fake/v1",
		ContextMaxTokens: 1000,
		SystemPrompt:     "You are a builder.",
		UserPrompt:       "Edit files.",
		Tools:            globRestriction,
	}, "/tmp/test.log", resolvedRoot)
	loop.SleepFunc = func(_ time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}

	// One turn with two edit_file calls: one matching the glob, one not.
	// Then text to complete the session.
	var callCount atomic.Int32
	fake.Responder = func(_ context.Context, _ llm.Request) (llm.Response, error) {
		switch callCount.Add(1) - 1 {
		case 0:
			return multiToolCallResponse(
				llm.ToolCall{
					ID: "call_edit_docs",
					Function: llm.ToolCallFunction{
						Name:      "edit_file",
						Arguments: `{"path":"docs/readme.md","old_str":"old content","new_str":"new content"}`,
					},
				},
				llm.ToolCall{
					ID: "call_edit_other",
					Function: llm.ToolCallFunction{
						Name:      "edit_file",
						Arguments: `{"path":"other/note.md","old_str":"other content","new_str":"modified"}`,
					},
				},
			), nil
		default:
			return textResponse("Done."), nil
		}
	}

	halt, err := loop.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != HaltCompleted {
		t.Errorf("want HaltCompleted, got %d: %s", halt.Code, halt.Message)
	}

	// VERIFICATION 1: The matching path was actually modified on disk.
	// This proves AllowPath correctly matched the glob — which would have
	// FAILED before Fix Cycle 2 because filepath.Rel used the unresolved
	// symlink root and computed a wrong relative path.
	editedContent, err := os.ReadFile(docsFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(editedContent) != "new content" {
		t.Errorf("docs/readme.md: expected content 'new content' (glob matched, tool executed), got %q",
			string(editedContent))
	}

	// VERIFICATION 2: The non-matching path was NOT modified on disk.
	// This proves AllowPath correctly rejected a path outside the glob.
	otherContent, err := os.ReadFile(otherFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(otherContent) != "other content" {
		t.Errorf("other/note.md: expected content unchanged (glob should NOT match), got %q",
			string(otherContent))
	}
}

// MalformedRetryNoHaltCounter verifies that a malformed error retried
// successfully does not increment any halt-detection counters.
func TestTester_MalformedRetryNoHaltCounter(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	f.mustCreateFile("a.go", "x")

	malformedErr := &llm.LLMError{
		Err:      fmt.Errorf("malformed"),
		Category: llm.ErrCategoryMalformed,
	}

	// Sequence: malformed error → edit_file → Done.
	entries := []any{
		malformedErr,
		toolCallResponse("edit_file", `{"path":"a.go","old_str":"x","new_str":"y"}`),
		textResponse("Done."),
	}
	f.setupResponderWithErrors(entries)

	halt, err := f.loop.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != HaltCompleted {
		t.Errorf("want HaltCompleted, got %d: %s", halt.Code, halt.Message)
	}

	// 3 LLM calls: 1 malformed, 1 tool, 1 text. The malformed retry
	// produced no tool dispatch, so no write_count was incremented
	// by the malformed attempt.
	if f.fake.CallCount != 3 {
		t.Errorf("expected 3 LLM calls, got %d", f.fake.CallCount)
	}
}
