// Package e2e provides end-to-end tests for the agent-harness foundation layer.
// These tests verify the storage layer and model-client interface behave
// correctly when operated together, including against real HTTP endpoints
// (httptest-based) and as a compiled binary.
package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-harness/internal/llm"
	"agent-harness/internal/store"
)

// ---------------------------------------------------------------------------
// 1. Schema correctness — column-level verification
// ---------------------------------------------------------------------------

// expectedColumns maps table names to their expected column definitions.
// Derived from spec §4.
var expectedColumns = map[string][]struct {
	name string
	typ  string // SQLite type affinity
	notnull bool
	pk      bool
}{
	"sessions": {
		{name: "id", typ: "INTEGER", pk: true},
		{name: "project", typ: "TEXT"},
		{name: "phase", typ: "INTEGER"},
		{name: "mode", typ: "TEXT"},
		{name: "model_name", typ: "TEXT"},
		{name: "base_url", typ: "TEXT"},
		{name: "context_max_tokens", typ: "INTEGER"},
		{name: "resume_count", typ: "INTEGER"},
		{name: "started_at", typ: "TEXT"},
		{name: "ended_at", typ: "TEXT"},
		{name: "status", typ: "TEXT"},
	},
	"files": {
		{name: "id", typ: "INTEGER", pk: true},
		{name: "session_id", typ: "INTEGER"},
		{name: "path", typ: "TEXT", notnull: true},
		{name: "content_hash", typ: "TEXT"},
		{name: "last_event_id", typ: "INTEGER"},
		{name: "write_count", typ: "INTEGER"},
	},
	"events": {
		{name: "id", typ: "INTEGER", pk: true},
		{name: "session_id", typ: "INTEGER"},
		{name: "turn_index", typ: "INTEGER"},
		{name: "event_type", typ: "TEXT"},
		{name: "tool_name", typ: "TEXT"},
		{name: "file_id", typ: "INTEGER"},
		{name: "args_json", typ: "TEXT"},
		{name: "result_json", typ: "TEXT"},
		{name: "tokens_used", typ: "INTEGER"},
		{name: "created_at", typ: "TEXT"},
	},
}

func TestSchema_ColumnDetails(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "schema-check.db")
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	for tableName, wantCols := range expectedColumns {
		t.Run(tableName, func(t *testing.T) {
			rows, err := s.DB().QueryContext(ctx,
				"SELECT cid, name, type, `notnull`, pk FROM pragma_table_info(?) ORDER BY cid",
				tableName)
			if err != nil {
				t.Fatalf("pragma_table_info(%q): %v", tableName, err)
			}
			defer rows.Close()

			var gotCols []struct {
				name    string
				typ     string
				notnull bool
				pk      bool
			}
			for rows.Next() {
				var cid int
				var name, typ string
				var notnull, pk int
				if err := rows.Scan(&cid, &name, &typ, &notnull, &pk); err != nil {
					t.Fatalf("scan pragma row: %v", err)
				}
				gotCols = append(gotCols, struct {
					name    string
					typ     string
					notnull bool
					pk      bool
				}{name, typ, notnull == 1, pk == 1})
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("rows iteration: %v", err)
			}

			if len(gotCols) != len(wantCols) {
				t.Fatalf("expected %d columns, got %d\n  got: %v",
					len(wantCols), len(gotCols), colNames(gotCols))
			}

			for i, want := range wantCols {
				got := gotCols[i]
				if got.name != want.name {
					t.Errorf("column %d: name got %q, want %q", i, got.name, want.name)
				}
				if !strings.EqualFold(got.typ, want.typ) {
					t.Errorf("column %s: type got %q, want %q", want.name, got.typ, want.typ)
				}
				if got.notnull != want.notnull {
					t.Errorf("column %s: notnull got %v, want %v", want.name, got.notnull, want.notnull)
				}
				if got.pk != want.pk {
					t.Errorf("column %s: pk got %v, want %v", want.name, got.pk, want.pk)
				}
			}
		})
	}
}

func colNames(cols []struct {
	name    string
	typ     string
	notnull bool
	pk      bool
}) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.name
	}
	return out
}

// ---------------------------------------------------------------------------
// 2. Index verification
// ---------------------------------------------------------------------------

// expectedIndexes maps table names to expected index names.
// Derived from spec §4.
var expectedIndexes = map[string][]string{
	"events": {"idx_events_session"},
	"files":  {"idx_files_session_path"},
}

func TestSchema_Indexes(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index-check.db")
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	for tableName, wantIdx := range expectedIndexes {
		t.Run(tableName, func(t *testing.T) {
			rows, err := s.DB().QueryContext(ctx,
				"SELECT name FROM sqlite_master WHERE type='index' AND tbl_name=? AND name NOT LIKE 'sqlite_auto%%'",
				tableName)
			if err != nil {
				t.Fatalf("query indexes for %s: %v", tableName, err)
			}
			defer rows.Close()

			var gotIdx []string
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					t.Fatalf("scan index name: %v", err)
				}
				gotIdx = append(gotIdx, name)
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("rows iteration: %v", err)
			}

			if len(gotIdx) != len(wantIdx) {
				t.Errorf("expected %d indexes, got %d: %v", len(wantIdx), len(gotIdx), gotIdx)
			}
			for _, want := range wantIdx {
				found := false
				for _, got := range gotIdx {
					if got == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("index %q not found on table %s", want, tableName)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 3. WAL mode verification (on disk)
// ---------------------------------------------------------------------------

func TestE2E_WALMode(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "wal-test.db")
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	var journalMode string
	err = s.DB().QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal_mode=wal, got %q", journalMode)
	}

	// Verify WAL files exist on disk.
	walPath := dbPath + "-wal"
	if _, err := os.Stat(walPath); os.IsNotExist(err) {
		t.Errorf("WAL file %s does not exist — WAL mode may not be active", walPath)
	}
}

// ---------------------------------------------------------------------------
// 4. Idempotent schema — double open on same file
// ---------------------------------------------------------------------------

func TestE2E_IdempotentOpen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "idempotent.db")

	s1, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()

	s2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("second Open on same file (not idempotent): %v", err)
	}
	s2.Close()
}

// ---------------------------------------------------------------------------
// 5. Foreign key enforcement: all three FK relationships
// ---------------------------------------------------------------------------

func TestE2E_ForeignKeys_EventSessionFK(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fk-eventsession.db")
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	// Attempt to insert an event referencing a non-existent session.
	_, err = s.InsertEvent(ctx, store.Event{
		SessionID: 99999,
		EventType: "text",
		CreatedAt: store.NowUTC(),
	})
	if err == nil {
		t.Error("expected FK violation for events.session_id → sessions.id, got nil")
	}
}

func TestE2E_ForeignKeys_FileSessionFK(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fk-filesession.db")
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	// Attempt to upsert a file referencing a non-existent session.
	_, err = s.UpsertFile(ctx, store.File{
		SessionID: 99999,
		Path:      "test.go",
	})
	if err == nil {
		t.Error("expected FK violation for files.session_id → sessions.id, got nil")
	}
}

func TestE2E_ForeignKeys_EventFileFK(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "fk-eventfile.db")
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	// First, insert a valid session.
	sessionID, err := s.InsertSession(ctx, store.Session{
		Project:   "test",
		Phase:     1,
		Mode:      "builder",
		StartedAt: store.NowUTC(),
		Status:    "running",
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	// Attempt to insert an event with a non-existent file_id but valid session_id.
	fileID := int64(99999)
	_, err = s.InsertEvent(ctx, store.Event{
		SessionID: sessionID,
		FileID:    &fileID,
		EventType: "tool_call",
		CreatedAt: store.NowUTC(),
	})
	if err == nil {
		t.Error("expected FK violation for events.file_id → files.id, got nil")
	}
}

// ---------------------------------------------------------------------------
// 6. End-to-end integration flow
// ---------------------------------------------------------------------------

func TestE2E_FullFlow(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "full-flow.db")
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	// 1. Create session.
	now := store.NowUTC()
	sessionID, err := s.InsertSession(ctx, store.Session{
		Project:          "e2e-test",
		Phase:            1,
		Mode:             "tester",
		ModelName:        "test-model",
		BaseURL:          "https://api.example.com/v1",
		ContextMaxTokens: 4096,
		ResumeCount:      0,
		StartedAt:        now,
		Status:           "running",
	})
	if err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	if sessionID == 0 {
		t.Fatal("InsertSession returned ID 0")
	}

	// 2. Insert events for two turns.
	turn0 := 0
	evt1ID, err := s.InsertEvent(ctx, store.Event{
		SessionID: sessionID,
		TurnIndex: &turn0,
		EventType: "model_call",
		CreatedAt: store.NowUTC(),
	})
	if err != nil {
		t.Fatalf("InsertEvent turn0: %v", err)
	}

	turn1 := 1
	toolName := "read_file"
	argsJSON := `{"path":"main.go"}`
	resultJSON := `{"content":"package main\nfunc main() {}"}`
	tokens := 42
	_, err = s.InsertEvent(ctx, store.Event{
		SessionID:  sessionID,
		TurnIndex:  &turn1,
		EventType:  "tool_call",
		ToolName:   &toolName,
		ArgsJSON:   &argsJSON,
		ResultJSON: &resultJSON,
		TokensUsed: &tokens,
		CreatedAt:  store.NowUTC(),
	})
	if err != nil {
		t.Fatalf("InsertEvent turn1: %v", err)
	}

	// 3. Create and upsert a file.
	contentHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // SHA-256 of empty
	fID, err := s.UpsertFile(ctx, store.File{
		SessionID:   sessionID,
		Path:        "main.go",
		ContentHash: &contentHash,
		LastEventID: &evt1ID,
		WriteCount:  1,
	})
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if fID == 0 {
		t.Error("UpsertFile returned ID 0")
	}

	// 4. Verify session was stored correctly.
	sess, err := s.SessionByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if sess == nil {
		t.Fatal("SessionByID returned nil")
	}
	want := map[string]interface{}{
		"Project": "e2e-test", "Phase": 1, "Mode": "tester",
		"ModelName": "test-model", "BaseURL": "https://api.example.com/v1",
		"ContextMaxTokens": 4096, "Status": "running",
	}
	for field, wantVal := range want {
		switch field {
		case "Project":
			if sess.Project != wantVal {
				t.Errorf("Session.%s = %q, want %q", field, sess.Project, wantVal)
			}
		case "Phase":
			if sess.Phase != wantVal {
				t.Errorf("Session.%s = %d, want %d", field, sess.Phase, wantVal)
			}
		case "Mode":
			if sess.Mode != wantVal {
				t.Errorf("Session.%s = %q, want %q", field, sess.Mode, wantVal)
			}
		case "ModelName":
			if sess.ModelName != wantVal {
				t.Errorf("Session.%s = %q, want %q", field, sess.ModelName, wantVal)
			}
		case "BaseURL":
			if sess.BaseURL != wantVal {
				t.Errorf("Session.%s = %q, want %q", field, sess.BaseURL, wantVal)
			}
		case "ContextMaxTokens":
			if sess.ContextMaxTokens != wantVal {
				t.Errorf("Session.%s = %d, want %d", field, sess.ContextMaxTokens, wantVal)
			}
		case "Status":
			if sess.Status != wantVal {
				t.Errorf("Session.%s = %q, want %q", field, sess.Status, wantVal)
			}
		}
	}

	// 5. Verify events returned in turn order.
	events, err := s.EventsBySession(ctx, sessionID)
	if err != nil {
		t.Fatalf("EventsBySession: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].EventType != "model_call" {
		t.Errorf("event[0].EventType = %q, want %q", events[0].EventType, "model_call")
	}
	if events[1].EventType != "tool_call" {
		t.Errorf("event[1].EventType = %q, want %q", events[1].EventType, "tool_call")
	}
	if events[1].ToolName == nil || *events[1].ToolName != "read_file" {
		t.Errorf("event[1].ToolName = %v, want %q", events[1].ToolName, "read_file")
	}
	if events[1].ArgsJSON == nil || *events[1].ArgsJSON != argsJSON {
		t.Errorf("event[1].ArgsJSON mismatch")
	}
	if events[1].TokensUsed == nil || *events[1].TokensUsed != tokens {
		t.Errorf("event[1].TokensUsed = %v, want %d", events[1].TokensUsed, tokens)
	}

	// 6. Verify file by path.
	f, err := s.FileByPath(ctx, sessionID, "main.go")
	if err != nil {
		t.Fatalf("FileByPath: %v", err)
	}
	if f == nil {
		t.Fatal("FileByPath returned nil")
	}
	if f.ContentHash == nil || *f.ContentHash != contentHash {
		t.Errorf("ContentHash = %v, want %q", f.ContentHash, contentHash)
	}
	if f.WriteCount != 1 {
		t.Errorf("WriteCount = %d, want %d", f.WriteCount, 1)
	}

	// 7. Verify FileWriteCount helper.
	wc, err := s.FileWriteCount(ctx, sessionID, "main.go")
	if err != nil {
		t.Fatalf("FileWriteCount: %v", err)
	}
	if wc != 1 {
		t.Errorf("FileWriteCount = %d, want %d", wc, 1)
	}

	// 8. Update session status.
	endedAt := store.NowUTC()
	if err := s.UpdateSession(ctx, store.Session{
		ID:      sessionID,
		Status:  "done",
		EndedAt: &endedAt,
	}); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	sess2, err := s.SessionByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("SessionByID after update: %v", err)
	}
	if sess2.Status != "done" {
		t.Errorf("Status after update = %q, want %q", sess2.Status, "done")
	}
	if sess2.EndedAt == nil || *sess2.EndedAt != endedAt {
		t.Errorf("EndedAt after update = %v, want %v", sess2.EndedAt, endedAt)
	}
}

// ---------------------------------------------------------------------------
// 7. LLM Fake: satisfies interface and is controllable
// ---------------------------------------------------------------------------

func TestE2E_LLMFake_InterfaceCompliance(t *testing.T) {
	var _ llm.LLM = (*llm.Fake)(nil)
}

func TestE2E_LLMFake_DeterministicResponse(t *testing.T) {
	f := &llm.Fake{
		Response: llm.Response{
			Text: "fixed response",
			Usage: llm.TokenUsage{
				PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
			},
		},
	}
	ctx := context.Background()

	// Multiple calls return the same result.
	for i := 0; i < 3; i++ {
		resp, err := f.Call(ctx, llm.Request{Model: "test"})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if resp.Text != "fixed response" {
			t.Errorf("call %d: Text = %q, want %q", i, resp.Text, "fixed response")
		}
		if resp.Usage.TotalTokens != 15 {
			t.Errorf("call %d: TotalTokens = %d, want %d", i, resp.Usage.TotalTokens, 15)
		}
	}
	if f.CallCount != 3 {
		t.Errorf("CallCount = %d, want %d", f.CallCount, 3)
	}
}

// ---------------------------------------------------------------------------
// 8. OpenAIClient error classification via httptest
// ---------------------------------------------------------------------------

func TestE2E_OpenAIClient_RateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit","code":"rate_limit"}}`))
	}))
	defer server.Close()

	client := llm.NewOpenAIClient()
	ctx := context.Background()
	_, err := client.Call(ctx, llm.Request{
		Model:   "test-model",
		BaseURL: server.URL,
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
	})

	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) {
		t.Fatalf("expected *LLMError, got %T: %v", err, err)
	}
	if llmErr.Category != llm.ErrCategoryRateLimit {
		t.Errorf("Category = %d, want %d (ErrCategoryRateLimit)", llmErr.Category, llm.ErrCategoryRateLimit)
	}
	if llmErr.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want %d", llmErr.StatusCode, 429)
	}
	if llmErr.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want %v", llmErr.RetryAfter, 30*time.Second)
	}
}

func TestE2E_OpenAIClient_BadAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key","type":"invalid_request_error","code":"invalid_api_key"}}`))
	}))
	defer server.Close()

	client := llm.NewOpenAIClient()
	ctx := context.Background()
	_, err := client.Call(ctx, llm.Request{
		Model:   "test-model",
		BaseURL: server.URL,
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
	})

	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) {
		t.Fatalf("expected *LLMError, got %T: %v", err, err)
	}
	// 401 without "quota" in body → ErrCategoryAuth
	if llmErr.Category != llm.ErrCategoryAuth {
		t.Errorf("Category = %d, want %d (ErrCategoryAuth)", llmErr.Category, llm.ErrCategoryAuth)
	}
	if llmErr.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want %d", llmErr.StatusCode, 401)
	}
}

func TestE2E_OpenAIClient_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the context deadline to trigger a timeout.
		time.Sleep(500 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	client := llm.NewOpenAIClient()
	// Very short timeout — 5ms.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err := client.Call(ctx, llm.Request{
		Model:   "test-model",
		BaseURL: server.URL,
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
	})

	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) {
		t.Fatalf("expected *LLMError, got %T: %v", err, err)
	}
	if llmErr.Category != llm.ErrCategoryTimeout {
		t.Errorf("Category = %d, want %d (ErrCategoryTimeout)", llmErr.Category, llm.ErrCategoryTimeout)
	}
}

func TestE2E_OpenAIClient_SuccessfulRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Decode and verify the request body.
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		resp := map[string]interface{}{
			"id":      "chatcmpl-e2e-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   "test-model",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "Hello from the test server!",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 20,
				"total_tokens":      30,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := llm.NewOpenAIClient()
	ctx := context.Background()
	resp, err := client.Call(ctx, llm.Request{
		Model:   "test-model",
		BaseURL: server.URL,
		Messages: []llm.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello!"},
		},
	})
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}
	if resp.Text != "Hello from the test server!" {
		t.Errorf("Text = %q, want %q", resp.Text, "Hello from the test server!")
	}
	if resp.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want %d", resp.Usage.PromptTokens, 10)
	}
	if resp.Usage.CompletionTokens != 20 {
		t.Errorf("CompletionTokens = %d, want %d", resp.Usage.CompletionTokens, 20)
	}
	if resp.Usage.TotalTokens != 30 {
		t.Errorf("TotalTokens = %d, want %d", resp.Usage.TotalTokens, 30)
	}
}

func TestE2E_OpenAIClient_QuotaExhausted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"You have insufficient quota","type":"insufficient_quota","code":"insufficient_quota"}}`))
	}))
	defer server.Close()

	client := llm.NewOpenAIClient()
	ctx := context.Background()
	_, err := client.Call(ctx, llm.Request{
		Model:   "test-model",
		BaseURL: server.URL,
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
	})

	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) {
		t.Fatalf("expected *LLMError, got %T: %v", err, err)
	}
	if llmErr.Category != llm.ErrCategoryQuota {
		t.Errorf("Category = %d, want %d (ErrCategoryQuota)", llmErr.Category, llm.ErrCategoryQuota)
	}
}

func TestE2E_OpenAIClient_RateLimitNoRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Retry-After header — verify fallback to zero.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit","code":"rate_limit"}}`))
	}))
	defer server.Close()

	client := llm.NewOpenAIClient()
	ctx := context.Background()
	_, err := client.Call(ctx, llm.Request{
		Model:   "test-model",
		BaseURL: server.URL,
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
	})

	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) {
		t.Fatalf("expected *LLMError, got %T: %v", err, err)
	}
	if llmErr.Category != llm.ErrCategoryRateLimit {
		t.Errorf("Category = %d, want %d (ErrCategoryRateLimit)", llmErr.Category, llm.ErrCategoryRateLimit)
	}
	if llmErr.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want %d", llmErr.StatusCode, 429)
	}
	// Without Retry-After header, RetryAfter must be 0.
	if llmErr.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 (no Retry-After header)", llmErr.RetryAfter)
	}
}

func TestE2E_OpenAIClient_ForbiddenNonQuota(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"You do not have access","type":"access_denied","code":"access_denied"}}`))
	}))
	defer server.Close()

	client := llm.NewOpenAIClient()
	ctx := context.Background()
	_, err := client.Call(ctx, llm.Request{
		Model:   "test-model",
		BaseURL: server.URL,
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
	})

	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) {
		t.Fatalf("expected *LLMError, got %T: %v", err, err)
	}
	// 403 without quota keywords in body → ErrCategoryAuth
	if llmErr.Category != llm.ErrCategoryAuth {
		t.Errorf("Category = %d, want %d (ErrCategoryAuth)", llmErr.Category, llm.ErrCategoryAuth)
	}
	if llmErr.StatusCode != 403 {
		t.Errorf("StatusCode = %d, want %d", llmErr.StatusCode, 403)
	}
}

func TestE2E_OpenAIClient_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"Internal server error","type":"server_error","code":"internal_error"}}`))
	}))
	defer server.Close()

	client := llm.NewOpenAIClient()
	ctx := context.Background()
	_, err := client.Call(ctx, llm.Request{
		Model:   "test-model",
		BaseURL: server.URL,
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
	})

	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) {
		t.Fatalf("expected *LLMError, got %T: %v", err, err)
	}
	// 5xx without quota keywords → ErrCategoryUnknown
	if llmErr.Category != llm.ErrCategoryUnknown {
		t.Errorf("Category = %d, want %d (ErrCategoryUnknown)", llmErr.Category, llm.ErrCategoryUnknown)
	}
	if llmErr.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want %d", llmErr.StatusCode, 500)
	}
}

func TestE2E_OpenAIClient_BadRequestNonQuota(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid parameters","type":"invalid_request_error","code":"invalid_params"}}`))
	}))
	defer server.Close()

	client := llm.NewOpenAIClient()
	ctx := context.Background()
	_, err := client.Call(ctx, llm.Request{
		Model:   "test-model",
		BaseURL: server.URL,
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
	})

	var llmErr *llm.LLMError
	if !errors.As(err, &llmErr) {
		t.Fatalf("expected *LLMError, got %T: %v", err, err)
	}
	// 400 without quota keywords → ErrCategoryUnknown
	if llmErr.Category != llm.ErrCategoryUnknown {
		t.Errorf("Category = %d, want %d (ErrCategoryUnknown)", llmErr.Category, llm.ErrCategoryUnknown)
	}
	if llmErr.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want %d", llmErr.StatusCode, 400)
	}
}

// ---------------------------------------------------------------------------
// 9. cmd/harness binary — end-to-end smoke test
// ---------------------------------------------------------------------------

func TestE2E_HarnessBinary(t *testing.T) {
	// Build the binary.
	binaryPath := filepath.Join(t.TempDir(), "harness")
	buildCmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/harness/")
	buildCmd.Dir = ".." // project root relative to e2e/
	out, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build harness binary: %v\n%s", err, out)
	}

	// Run it in a temp directory.
	runDir := t.TempDir()
	runCmd := exec.Command(binaryPath, runDir)
	runOut, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run harness: %v\n%s", err, runOut)
	}

	output := string(runOut)

	// Check stdout contains expected output.
	checks := []string{
		"Session created: id=",
		"LLM response: Phase 1 foundation layer initialized.",
		"Phase 1 foundation initialized successfully.",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("stdout missing expected text %q\nFull output:\n%s", check, output)
		}
	}

	// Verify the database file was created.
	dbPath := filepath.Join(runDir, "agent-harness.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("database file was not created at %s", dbPath)
	}

	// Note: We do NOT check for -wal file existence here because SQLite may
	// checkpoint and remove the WAL file on clean connection close. WAL mode
	// is verified to be active during operation in TestE2E_WALMode.

	// Re-open the database and verify the session was actually committed.
	ctx := context.Background()
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("re-open store: %v", err)
	}
	defer s.Close()

	var sessionCount int
	err = s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&sessionCount)
	if err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessionCount == 0 {
		t.Error("no sessions found in the database after harness run")
	}

	// Fetch first session to verify status and mode.
	firstSess, err := s.SessionByID(ctx, 1)
	if err != nil {
		t.Fatalf("SessionByID(1): %v", err)
	}
	if firstSess != nil {
		if firstSess.Status != "done" {
			t.Errorf("session status = %q, want %q", firstSess.Status, "done")
		}
		if firstSess.Mode != "builder" {
			t.Errorf("session mode = %q, want %q", firstSess.Mode, "builder")
		}
	}
}
