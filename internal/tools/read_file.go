package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"agent-harness/internal/llm"
)

// ReadFileArgs holds the arguments for the read_file tool.
type ReadFileArgs struct {
	Path string `json:"path"`
}

// ReadFileResult holds the result of a read_file execution.
type ReadFileResult struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	LineCount int    `json:"line_count"`
	Truncated bool   `json:"truncated"`
}

// ReadFileTool implements the read_file tool.
type ReadFileTool struct {
	root string
}

// Name returns "read_file".
func (t *ReadFileTool) Name() string { return "read_file" }

// Definition returns the ToolDef for read_file.
func (t *ReadFileTool) Definition() llm.ToolDef {
	return newToolDef(
		"read_file",
		"Read the full content of a file with line numbers. Returns content and line count.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file, relative to the project root",
				},
			},
			"required": []string{"path"},
		},
	)
}

// Execute runs the read_file tool.
func (t *ReadFileTool) Execute(ctx context.Context, args json.RawMessage, config ToolConfig) (Result, error) {
	var a ReadFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{}, fmt.Errorf("read_file: invalid arguments: %w", err)
	}

	resolvedPath, err := resolveScoped(t.root, a.Path)
	if err != nil {
		return Result{}, err
	}

	if err := config.AllowPath(resolvedPath); err != nil {
		return Result{}, err
	}

	fi, err := os.Stat(resolvedPath)
	if err != nil {
		return Result{}, fmt.Errorf("read_file: %w", err)
	}
	if fi.IsDir() {
		return Result{}, fmt.Errorf("read_file: %q is a directory, not a file", a.Path)
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return Result{}, fmt.Errorf("read_file: %w", err)
	}

	body := string(content)
	lines := strings.Split(body, "\n")

	// Line-numbered content: "1: line1\n2: line2\n..."
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d: %s", i+1, line)
	}

	result := ReadFileResult{
		Path:      a.Path,
		Content:   b.String(),
		LineCount: len(lines),
		Truncated: false,
	}

	return Result{
		Data:      result,
		HumanText: fmt.Sprintf("read %s (%d lines)", a.Path, len(lines)),
	}, nil
}

