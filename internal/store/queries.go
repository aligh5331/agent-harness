package store

import (
	"context"
	"database/sql"
	"fmt"
)

// InsertSession creates a new session row. Returns the auto-increment ID.
func (s *Store) InsertSession(ctx context.Context, sess Session) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions
			(project, phase, mode, model_name, base_url, context_max_tokens,
			 resume_count, started_at, ended_at, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.Project, sess.Phase, sess.Mode, sess.ModelName, sess.BaseURL,
		sess.ContextMaxTokens, sess.ResumeCount, sess.StartedAt, sess.EndedAt, sess.Status)
	if err != nil {
		return 0, fmt.Errorf("insert session: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert session last insert id: %w", err)
	}
	return id, nil
}

// UpdateSession updates an existing session (status, ended_at, resume_count, started_at).
// Fields are always written to simplify the query; callers set values accordingly.
// To clear ended_at (for session-reuse), pass a nil EndedAt.
func (s *Store) UpdateSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions
		 SET status = ?, ended_at = ?, resume_count = ?, started_at = ?
		 WHERE id = ?`,
		sess.Status, sess.EndedAt, sess.ResumeCount, sess.StartedAt, sess.ID)
	if err != nil {
		return fmt.Errorf("update session %d: %w", sess.ID, err)
	}
	return nil
}

// InsertEvent logs a new event. Returns the auto-increment ID.
func (s *Store) InsertEvent(ctx context.Context, evt Event) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO events
			(session_id, turn_index, event_type, tool_name, file_id,
			 args_json, result_json, tokens_used, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		evt.SessionID, evt.TurnIndex, evt.EventType, evt.ToolName, evt.FileID,
		evt.ArgsJSON, evt.ResultJSON, evt.TokensUsed, evt.CreatedAt)
	if err != nil {
		return 0, fmt.Errorf("insert event: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert event last insert id: %w", err)
	}
	return id, nil
}

// UpsertFile creates a file row or updates it on (session_id, path).
// On first insert: write_count=0, content_hash=nil, last_event_id=nil.
// On update: increments write_count, sets content_hash and last_event_id.
func (s *Store) UpsertFile(ctx context.Context, f File) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO files
			(session_id, path, content_hash, last_event_id, write_count)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(session_id, path) DO UPDATE SET
			content_hash = COALESCE(EXCLUDED.content_hash, content_hash),
			last_event_id = COALESCE(EXCLUDED.last_event_id, last_event_id),
			write_count = write_count + 1`,
		f.SessionID, f.Path, f.ContentHash, f.LastEventID, f.WriteCount)
	if err != nil {
		return 0, fmt.Errorf("upsert file: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("upsert file last insert id: %w", err)
	}
	return id, nil
}

// FileByPath returns the file row for a given session+path, or nil if not found.
func (s *Store) FileByPath(ctx context.Context, sessionID int64, path string) (*File, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, session_id, path, content_hash, last_event_id, write_count
		 FROM files
		 WHERE session_id = ? AND path = ?`,
		sessionID, path)

	f := &File{}
	err := row.Scan(&f.ID, &f.SessionID, &f.Path, &f.ContentHash, &f.LastEventID, &f.WriteCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("file by path: %w", err)
	}
	return f, nil
}

// FileWriteCount returns the write_count for a given session+path.
// Returns 0 if no row exists for that session+path.
func (s *Store) FileWriteCount(ctx context.Context, sessionID int64, path string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(write_count, 0) FROM files
		 WHERE session_id = ? AND path = ?`,
		sessionID, path).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("file write count: %w", err)
	}
	return count, nil
}

// SessionByID returns the session with the given ID, or nil if not found.
func (s *Store) SessionByID(ctx context.Context, id int64) (*Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, project, phase, mode, model_name, base_url,
				context_max_tokens, resume_count, started_at, ended_at, status
		 FROM sessions WHERE id = ?`, id)

	sess := &Session{}
	err := row.Scan(&sess.ID, &sess.Project, &sess.Phase, &sess.Mode,
		&sess.ModelName, &sess.BaseURL, &sess.ContextMaxTokens,
		&sess.ResumeCount, &sess.StartedAt, &sess.EndedAt, &sess.Status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session by id: %w", err)
	}
	return sess, nil
}

// EventsBySession returns all events for a session, ordered by (turn_index, id).
func (s *Store) EventsBySession(ctx context.Context, sessionID int64) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, turn_index, event_type, tool_name, file_id,
				args_json, result_json, tokens_used, created_at
		 FROM events
		 WHERE session_id = ?
		 ORDER BY turn_index, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("events by session: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var evt Event
		if err := rows.Scan(&evt.ID, &evt.SessionID, &evt.TurnIndex, &evt.EventType,
			&evt.ToolName, &evt.FileID, &evt.ArgsJSON, &evt.ResultJSON,
			&evt.TokensUsed, &evt.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, evt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("events by session rows: %w", err)
	}
	return events, nil
}

// RecentEventsBySession returns the most recent N events for a session,
// ordered by (turn_index, id) descending.
func (s *Store) RecentEventsBySession(ctx context.Context, sessionID int64, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, turn_index, event_type, tool_name, file_id,
				args_json, result_json, tokens_used, created_at
		 FROM events
		 WHERE session_id = ?
		 ORDER BY turn_index DESC, id DESC
		 LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("recent events by session: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var evt Event
		if err := rows.Scan(&evt.ID, &evt.SessionID, &evt.TurnIndex, &evt.EventType,
			&evt.ToolName, &evt.FileID, &evt.ArgsJSON, &evt.ResultJSON,
			&evt.TokensUsed, &evt.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, evt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent events by session rows: %w", err)
	}

	// Reverse to get chronological order.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, nil
}

// FilesBySession returns all file rows for a session.
func (s *Store) FilesBySession(ctx context.Context, sessionID int64) ([]File, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, path, content_hash, last_event_id, write_count
		 FROM files WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("files by session: %w", err)
	}
	defer rows.Close()

	var files []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.SessionID, &f.Path, &f.ContentHash, &f.LastEventID, &f.WriteCount); err != nil {
			return nil, fmt.Errorf("scan file: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("files by session rows: %w", err)
	}
	return files, nil
}
