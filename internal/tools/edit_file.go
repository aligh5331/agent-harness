package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"agent-harness/internal/llm"
)

// EditFileArgs holds the arguments for the edit_file tool.
type EditFileArgs struct {
	Path   string `json:"path"`
	OldStr string `json:"old_str"`
	NewStr string `json:"new_str"`
}

// EditFileResult holds the result of a successful edit_file execution.
type EditFileResult struct {
	Path         string `json:"path"`
	MatchesFound int    `json:"matches_found"`
}

// EditFileTool implements the edit_file tool.
type EditFileTool struct {
	root string
}

// Name returns "edit_file".
func (t *EditFileTool) Name() string { return "edit_file" }

// Definition returns the ToolDef for edit_file.
func (t *EditFileTool) Definition() llm.ToolDef {
	return newToolDef(
		"edit_file",
		"Edit a file by replacing an exact string match. The old_str must match exactly once — if it matches zero or multiple times the tool fails with a specific error. Provide enough surrounding context to make old_str unique.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file, relative to the project root",
				},
				"old_str": map[string]any{
					"type":        "string",
					"description": "The exact string to find and replace",
				},
				"new_str": map[string]any{
					"type":        "string",
					"description": "The replacement string",
				},
			},
			"required": []string{"path", "old_str", "new_str"},
		},
	)
}

// Execute runs the edit_file tool.
func (t *EditFileTool) Execute(ctx context.Context, args json.RawMessage, config ToolConfig) (Result, error) {
	var a EditFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{}, fmt.Errorf("edit_file: invalid arguments: %w", err)
	}

	resolvedPath, err := resolveScoped(t.root, a.Path)
	if err != nil {
		return Result{}, err
	}

	if err := config.AllowPath(resolvedPath); err != nil {
		return Result{}, err
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return Result{}, fmt.Errorf("edit_file: %w", err)
	}

	body := string(content)

	count := strings.Count(body, a.OldStr)
	switch {
	case count == 0:
		return Result{}, &ErrNoMatch{Path: a.Path}
	case count > 1:
		return Result{}, &ErrAmbiguousMatch{Path: a.Path, MatchesFound: count}
	}

	// Exactly one match — replace it.
	newBody := strings.Replace(body, a.OldStr, a.NewStr, 1)

	if err := os.WriteFile(resolvedPath, []byte(newBody), 0644); err != nil {
		return Result{}, fmt.Errorf("edit_file: write: %w", err)
	}

	result := EditFileResult{
		Path:         a.Path,
		MatchesFound: 1,
	}

	return Result{
		Data:      result,
		HumanText: fmt.Sprintf("edit_file: replaced 1 match in %s", a.Path),
	}, nil
}

