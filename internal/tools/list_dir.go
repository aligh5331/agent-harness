package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"agent-harness/internal/llm"
)

// ListDirArgs holds the arguments for the list_dir tool.
type ListDirArgs struct {
	Path string `json:"path"`
}

// ListDirResult holds the result of a list_dir execution.
type ListDirResult struct {
	Path    string   `json:"path"`
	Entries []string `json:"entries"`
	IsEmpty bool     `json:"is_empty"`
}

// ListDirTool implements the list_dir tool.
type ListDirTool struct {
	root string
}

// Name returns "list_dir".
func (t *ListDirTool) Name() string { return "list_dir" }

// Definition returns the ToolDef for list_dir.
func (t *ListDirTool) Definition() llm.ToolDef {
	return newToolDef(
		"list_dir",
		"List the immediate children (files and directories) of a directory. Returns base names only, sorted alphabetically. Hidden files (dotfiles) are included. For recursive listings, use bash_exec with find.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the directory, relative to the project root",
				},
			},
			"required": []string{"path"},
		},
	)
}

// Execute runs the list_dir tool.
func (t *ListDirTool) Execute(ctx context.Context, args json.RawMessage, config ToolConfig) (Result, error) {
	var a ListDirArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{}, fmt.Errorf("list_dir: invalid arguments: %w", err)
	}

	resolvedPath, err := resolveScoped(t.root, a.Path)
	if err != nil {
		return Result{}, err
	}

	if err := config.AllowPath(resolvedPath); err != nil {
		return Result{}, err
	}

	entries, err := os.ReadDir(resolvedPath)
	if err != nil {
		return Result{}, fmt.Errorf("list_dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)

	result := ListDirResult{
		Path:    a.Path,
		Entries: names,
		IsEmpty: len(names) == 0,
	}

	humanText := fmt.Sprintf("listed %s (%d entries)", a.Path, len(names))
	return Result{Data: result, HumanText: humanText}, nil
}

