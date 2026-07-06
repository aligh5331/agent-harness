package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"agent-harness/internal/llm"
)

// WriteLogArgs holds the arguments for the write_log tool.
// Note: there is NO path field — the log path is fixed at construction time.
type WriteLogArgs struct {
	Content string `json:"content"`
}

// WriteLogResult holds the result of a write_log execution.
type WriteLogResult struct {
	Path     string `json:"path"`
	SizeBytes int   `json:"size_bytes"`
}

// WriteLogTool implements the write_log tool.
//
// The log path is set at construction time and is structurally fixed — there
// is no path parameter in the arguments. This tool is structurally incapable
// of writing anywhere except its configured path.
type WriteLogTool struct {
	logPath string // fixed at construction; e.g. "/home/user/project/phase-2.log"
}

// Name returns "write_log".
func (t *WriteLogTool) Name() string { return "write_log" }

// Definition returns the ToolDef for write_log.
func (t *WriteLogTool) Definition() llm.ToolDef {
	return newToolDef(
		"write_log",
		"Append an entry to the current phase's log file (phase-N.log). The log path is fixed — this tool cannot write to arbitrary locations. Each phase step appends one entry in the agent's own words.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{
					"type":        "string",
					"description": "The log entry content to append",
				},
			},
			"required": []string{"content"},
		},
	)
}

// Execute runs the write_log tool.
//
// Note: config is accepted for interface uniformity but per-agent path
// restrictions do not apply to write_log — it is granted unconditionally
// to every agent per spec §15.
func (t *WriteLogTool) Execute(ctx context.Context, args json.RawMessage, config ToolConfig) (Result, error) {
	var a WriteLogArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{}, fmt.Errorf("write_log: invalid arguments: %w", err)
	}

	// Append-only: open with O_APPEND | O_CREATE | O_WRONLY.
	f, err := os.OpenFile(t.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return Result{}, fmt.Errorf("write_log: %w", err)
	}
	defer f.Close()

	n, err := f.WriteString(a.Content)
	if err != nil {
		return Result{}, fmt.Errorf("write_log: %w", err)
	}

	if _, err := f.WriteString("\n"); err != nil {
		return Result{}, fmt.Errorf("write_log: %w", err)
	}
	n++ // count the newline

	result := WriteLogResult{
		Path:      t.logPath,
		SizeBytes: n,
	}

	return Result{
		Data:      result,
		HumanText: fmt.Sprintf("wrote %d bytes to phase log", n),
	}, nil
}

