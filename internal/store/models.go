// Package store provides SQLite persistence for agent sessions, file tracking,
// and event logging. Uses modernc.org/sqlite (pure Go, no cgo) with WAL mode
// for concurrent read access during continuous writes.
package store

// Session represents a single agent session — the lifecycle of one agent
// (architect, builder, tester, etc.) executing one phase.
type Session struct {
	ID               int64   // PRIMARY KEY, auto-increment
	Project          string  // project name/identifier
	Phase            int     // phase number
	Mode             string  // architect|builder|tester|forensic|librarian
	ModelName        string
	BaseURL          string
	ContextMaxTokens int
	ResumeCount      int     // defaults to 0; incremented on session reuse (§9)
	StartedAt        string  // ISO 8601 (RFC3339), set at creation
	EndedAt          *string // nullable, set when session terminates
	Status           string  // running|done|halted|error
}

// File tracks a file touched during a session. The unique key is (session_id, path).
// WriteCount is the loop-detection signal used by Phase 3's hardcoded halt check.
type File struct {
	ID          int64   // PRIMARY KEY, auto-increment
	SessionID   int64   // FK → sessions(id)
	Path        string  // NOT NULL; project-relative path
	ContentHash *string // nullable SHA-256 hex; nil before first write
	LastEventID *int64  // nullable FK → events(id); nil before first write
	WriteCount  int     // defaults to 0; incremented on each edit/write
}

// Event records one action or observation during a session turn.
// Every model call, tool call, tool result, text output, or halt produces exactly one event row.
type Event struct {
	ID         int64   // PRIMARY KEY, auto-increment
	SessionID  int64   // FK → sessions(id)
	TurnIndex  *int    // nullable (events may occur pre-turn); 0-based
	EventType  string  // model_call|tool_call|tool_result|text|halt
	ToolName   *string // nullable; edit_file|bash_exec|read_file|create_file|list_dir|write_log
	FileID     *int64  // nullable FK → files(id); target file of this event
	ArgsJSON   *string // nullable; raw JSON of tool arguments
	ResultJSON *string // nullable; raw JSON of tool result
	TokensUsed *int    // nullable; populated for model_call events
	CreatedAt  string  // ISO 8601 (RFC3339)
}
