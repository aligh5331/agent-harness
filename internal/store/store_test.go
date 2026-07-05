package store

import (
	"context"
	"testing"
)

const testDSN = "file::memory:?mode=memory&cache=shared"

func TestOpen_TableCreation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	// Verify all three tables exist.
	tables := []string{"sessions", "files", "events"}
	for _, name := range tables {
		var count int
		err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&count)
		if err != nil {
			t.Fatalf("query sqlite_master for %s: %v", name, err)
		}
		if count != 1 {
			t.Errorf("table %s not found", name)
		}
	}
}

func TestOpen_WALMode(t *testing.T) {
	// WAL mode requires a file on disk — an in-memory DB always reports "memory".
	// Use a temp file for this test.
	ctx := context.Background()
	dbPath := t.TempDir() + "/test-wal.db"
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	var journalMode string
	err = s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal_mode=wal, got %q", journalMode)
	}
}

func TestOpen_ForeignKeys(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	var fkEnabled int
	err = s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fkEnabled)
	if err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fkEnabled != 1 {
		t.Errorf("expected foreign_keys=1, got %d", fkEnabled)
	}
}

func TestOpen_Idempotent(t *testing.T) {
	ctx := context.Background()

	// Open twice against the same in-memory DB. The second open must not error.
	s1, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("first Open failed: %v", err)
	}
	s1.Close()

	s2, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("second Open failed (not idempotent): %v", err)
	}
	s2.Close()
}

func TestInsertSession_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	sess := Session{
		Project:          "test-project",
		Phase:            1,
		Mode:             "builder",
		ModelName:        "test-model",
		BaseURL:          "https://api.example.com/v1",
		ContextMaxTokens: 4096,
		ResumeCount:      0,
		StartedAt:        NowUTC(),
		Status:           "running",
	}

	id, err := s.InsertSession(ctx, sess)
	if err != nil {
		t.Fatalf("InsertSession failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero session ID")
	}

	got, err := s.SessionByID(ctx, id)
	if err != nil {
		t.Fatalf("SessionByID failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected session, got nil")
	}

	if got.Project != sess.Project {
		t.Errorf("Project: got %q, want %q", got.Project, sess.Project)
	}
	if got.Phase != sess.Phase {
		t.Errorf("Phase: got %d, want %d", got.Phase, sess.Phase)
	}
	if got.Mode != sess.Mode {
		t.Errorf("Mode: got %q, want %q", got.Mode, sess.Mode)
	}
	if got.Status != sess.Status {
		t.Errorf("Status: got %q, want %q", got.Status, sess.Status)
	}
	if got.StartedAt != sess.StartedAt {
		t.Errorf("StartedAt: got %q, want %q", got.StartedAt, sess.StartedAt)
	}
}

func TestUpdateSession(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	sess := Session{
		Project:   "test",
		Phase:     1,
		Mode:      "tester",
		StartedAt: NowUTC(),
		Status:    "running",
	}
	id, err := s.InsertSession(ctx, sess)
	if err != nil {
		t.Fatalf("InsertSession failed: %v", err)
	}

	endedAt := NowUTC()
	updated := Session{
		ID:          id,
		Status:      "done",
		EndedAt:     &endedAt,
		ResumeCount: 2,
	}
	if err := s.UpdateSession(ctx, updated); err != nil {
		t.Fatalf("UpdateSession failed: %v", err)
	}

	got, err := s.SessionByID(ctx, id)
	if err != nil {
		t.Fatalf("SessionByID failed: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("Status: got %q, want %q", got.Status, "done")
	}
	if got.EndedAt == nil || *got.EndedAt != endedAt {
		t.Errorf("EndedAt: got %v, want %v", got.EndedAt, endedAt)
	}
	if got.ResumeCount != 2 {
		t.Errorf("ResumeCount: got %d, want %d", got.ResumeCount, 2)
	}
}

func TestInsertEvent_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	sessionID := insertTestSession(t, s)

	turnIdx := 0
	evt := Event{
		SessionID: sessionID,
		TurnIndex: &turnIdx,
		EventType: "model_call",
		CreatedAt: NowUTC(),
	}

	id, err := s.InsertEvent(ctx, evt)
	if err != nil {
		t.Fatalf("InsertEvent failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero event ID")
	}

	events, err := s.EventsBySession(ctx, sessionID)
	if err != nil {
		t.Fatalf("EventsBySession failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	got := events[0]
	if got.EventType != evt.EventType {
		t.Errorf("EventType: got %q, want %q", got.EventType, evt.EventType)
	}
	if got.SessionID != sessionID {
		t.Errorf("SessionID: got %d, want %d", got.SessionID, sessionID)
	}
}

func TestUpsertFile_Insert(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	sessionID := insertTestSession(t, s)

	f := File{
		SessionID:   sessionID,
		Path:        "test/file.go",
		ContentHash: strPtr("abc123"),
		WriteCount:  1,
	}
	id, err := s.UpsertFile(ctx, f)
	if err != nil {
		t.Fatalf("UpsertFile failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero file ID")
	}

	got, err := s.FileByPath(ctx, sessionID, "test/file.go")
	if err != nil {
		t.Fatalf("FileByPath failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected file, got nil")
	}
	if got.Path != "test/file.go" {
		t.Errorf("Path: got %q, want %q", got.Path, "test/file.go")
	}
	if got.WriteCount != 1 {
		t.Errorf("WriteCount: got %d, want %d", got.WriteCount, 1)
	}
}

func TestUpsertFile_UpdateIncrementsCount(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	sessionID := insertTestSession(t, s)

	// First insert — write_count starts at 1.
	f := File{
		SessionID:   sessionID,
		Path:        "test/counter.go",
		ContentHash: strPtr("hash-v1"),
		WriteCount:  1,
	}
	_, err = s.UpsertFile(ctx, f)
	if err != nil {
		t.Fatalf("first UpsertFile failed: %v", err)
	}

	// Second upsert — should increment write_count to 2 and update content_hash.
	f2 := File{
		SessionID:   sessionID,
		Path:        "test/counter.go",
		ContentHash: strPtr("hash-v2"),
		LastEventID: int64Ptr(42),
		WriteCount:  0, // not used on upsert — the ON CONFLICT clause increments
	}
	_, err = s.UpsertFile(ctx, f2)
	if err != nil {
		t.Fatalf("second UpsertFile failed: %v", err)
	}

	got, err := s.FileByPath(ctx, sessionID, "test/counter.go")
	if err != nil {
		t.Fatalf("FileByPath failed: %v", err)
	}
	if got.WriteCount != 2 {
		t.Errorf("WriteCount: got %d, want %d (should have incremented)", got.WriteCount, 2)
	}
	if got.ContentHash == nil || *got.ContentHash != "hash-v2" {
		t.Errorf("ContentHash: got %v, want %q", got.ContentHash, "hash-v2")
	}
	if got.LastEventID == nil || *got.LastEventID != 42 {
		t.Errorf("LastEventID: got %v, want %d", got.LastEventID, 42)
	}
}

func TestFileWriteCount(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	sessionID := insertTestSession(t, s)

	// No file yet — should return 0.
	count, err := s.FileWriteCount(ctx, sessionID, "nonexistent.go")
	if err != nil {
		t.Fatalf("FileWriteCount on missing file: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 for missing file, got %d", count)
	}

	// Insert and upsert.
	f := File{
		SessionID:   sessionID,
		Path:        "test/metric.go",
		ContentHash: strPtr("h1"),
		WriteCount:  1,
	}
	_, err = s.UpsertFile(ctx, f)
	if err != nil {
		t.Fatalf("UpsertFile failed: %v", err)
	}

	count, err = s.FileWriteCount(ctx, sessionID, "test/metric.go")
	if err != nil {
		t.Fatalf("FileWriteCount failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected write_count=1, got %d", count)
	}

	// Upsert again increments.
	f2 := File{
		SessionID:   sessionID,
		Path:        "test/metric.go",
		ContentHash: strPtr("h2"),
	}
	_, err = s.UpsertFile(ctx, f2)
	if err != nil {
		t.Fatalf("second UpsertFile failed: %v", err)
	}

	count, err = s.FileWriteCount(ctx, sessionID, "test/metric.go")
	if err != nil {
		t.Fatalf("FileWriteCount after second upsert: %v", err)
	}
	if count != 2 {
		t.Errorf("expected write_count=2, got %d", count)
	}
}

func TestEventsBySession_Ordering(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	sessionID := insertTestSession(t, s)

	now := NowUTC()
	for i := 0; i < 3; i++ {
		ti := i
		e := Event{
			SessionID: sessionID,
			TurnIndex: &ti,
			EventType: "text",
			CreatedAt: now,
		}
		if _, err := s.InsertEvent(ctx, e); err != nil {
			t.Fatalf("InsertEvent turn %d: %v", i, err)
		}
	}

	events, err := s.EventsBySession(ctx, sessionID)
	if err != nil {
		t.Fatalf("EventsBySession failed: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	for i, evt := range events {
		if evt.TurnIndex == nil || *evt.TurnIndex != i {
			t.Errorf("event %d: expected turn_index=%d, got %v", i, i, evt.TurnIndex)
		}
	}
}

func TestFilesBySession(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	sessionID := insertTestSession(t, s)

	paths := []string{"a.go", "b.go", "c.go"}
	for _, p := range paths {
		f := File{
			SessionID:   sessionID,
			Path:        p,
			ContentHash: strPtr("h"),
			WriteCount:  1,
		}
		if _, err := s.UpsertFile(ctx, f); err != nil {
			t.Fatalf("UpsertFile %s: %v", p, err)
		}
	}

	files, err := s.FilesBySession(ctx, sessionID)
	if err != nil {
		t.Fatalf("FilesBySession failed: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
}

func TestFK_Enforcement(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	// Inserting an event with a non-existent session_id should fail due to FK constraint.
	evt := Event{
		SessionID: 999,
		EventType: "text",
		CreatedAt: NowUTC(),
	}
	_, err = s.InsertEvent(ctx, evt)
	if err == nil {
		t.Error("expected FK violation for invalid session_id, got nil")
	}
}

// --- helpers ---

func insertTestSession(t *testing.T, s *Store) int64 {
	t.Helper()
	ctx := context.Background()
	sess := Session{
		Project:   "test",
		Phase:     1,
		Mode:      "builder",
		StartedAt: NowUTC(),
		Status:    "running",
	}
	id, err := s.InsertSession(ctx, sess)
	if err != nil {
		t.Fatalf("insertTestSession: %v", err)
	}
	return id
}

func strPtr(s string) *string {
	return &s
}

func int64Ptr(n int64) *int64 {
	return &n
}
