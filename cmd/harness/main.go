// Command harness is the entry point for the agent-harness binary.
// Phase 1 stub: opens a SQLite database, creates a fake LLM client,
// logs a startup event, and exits cleanly.
//
// Later phases will replace the fake client with a real one and wire
// the turn loop, tools, and configuration.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"agent-harness/internal/llm"
	"agent-harness/internal/store"
)

func main() {
	ctx := context.Background()

	// Determine database path.
	dbDir := "."
	if len(os.Args) > 1 {
		dbDir = os.Args[1]
	}
	dbPath := filepath.Join(dbDir, "agent-harness.db")

	// Open the store.
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		log.Fatalf("Failed to open store: %v", err)
	}
	defer s.Close()

	// Create a session.
	now := store.NowUTC()
	sessionID, err := s.InsertSession(ctx, store.Session{
		Project:   "agent-harness",
		Phase:     1,
		Mode:      "builder",
		StartedAt: now,
		Status:    "running",
	})
	if err != nil {
		log.Fatalf("Failed to create session: %v", err)
	}
	fmt.Printf("Session created: id=%d\n", sessionID)

	// Create a fake LLM client (Phase 2+ will use the real one).
	client := &llm.Fake{
		Response: llm.Response{
			Text: "Phase 1 foundation layer initialized.",
		},
	}

	// Log a startup event.
	_, err = s.InsertEvent(ctx, store.Event{
		SessionID: sessionID,
		EventType: "text",
		CreatedAt: store.NowUTC(),
		ArgsJSON:  strPtr(`{"message":"harness startup"}`),
	})
	if err != nil {
		log.Fatalf("Failed to insert event: %v", err)
	}

	// Make a test call to the fake LLM.
	resp, err := client.Call(ctx, llm.Request{
		Model:   "stub",
		BaseURL: "http://localhost:7890/v1",
		Messages: []llm.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "What phase is this?"},
		},
	})
	if err != nil {
		log.Fatalf("LLM call failed: %v", err)
	}
	fmt.Printf("LLM response: %s\n", resp.Text)

	// Mark session done.
	ended := store.NowUTC()
	if err := s.UpdateSession(ctx, store.Session{
		ID:      sessionID,
		Status:  "done",
		EndedAt: &ended,
	}); err != nil {
		log.Fatalf("Failed to update session: %v", err)
	}

	fmt.Println("Phase 1 foundation initialized successfully.")
}

func strPtr(s string) *string {
	return &s
}
