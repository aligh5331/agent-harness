package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"agent-harness/internal/llm"
)

// CreateFileArgs holds the arguments for the create_file tool.
type CreateFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// CreateFileResult holds the result of a successful create_file execution.
type CreateFileResult struct {
	Path string `json:"path"`
}

// CreateFileTool implements the create_file tool.
type CreateFileTool struct {
	root string
}

// Name returns "create_file".
func (t *CreateFileTool) Name() string { return "create_file" }

// Definition returns the ToolDef for create_file.
func (t *CreateFileTool) Definition() llm.ToolDef {
	return newToolDef(
		"create_file",
		"Create a new file with the given content. Fails if the path already exists — no silent overwrite. Does NOT create intermediate directories.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path where to create the file, relative to the project root. Parent directory must already exist.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "The full content to write to the new file",
				},
			},
			"required": []string{"path", "content"},
		},
	)
}

// Execute runs the create_file tool.
func (t *CreateFileTool) Execute(ctx context.Context, args json.RawMessage, config ToolConfig) (Result, error) {
	var a CreateFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{}, fmt.Errorf("create_file: invalid arguments: %w", err)
	}

	resolvedPath, err := resolveScoped(t.root, a.Path)
	if err != nil {
		return Result{}, err
	}

	if err := config.AllowPath(resolvedPath); err != nil {
		return Result{}, err
	}

	// Check parent directory exists.
	parent := filepath.Dir(resolvedPath)
	parentFI, err := os.Stat(parent)
	if err != nil {
		return Result{}, fmt.Errorf("create_file: parent directory %q does not exist: %w", filepath.Dir(a.Path), err)
	}
	if !parentFI.IsDir() {
		return Result{}, fmt.Errorf("create_file: parent %q is not a directory", filepath.Dir(a.Path))
	}

	// Atomic create: O_EXCL ensures we don't overwrite.
	f, err := os.OpenFile(resolvedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			return Result{}, fmt.Errorf("create_file: %q already exists — use edit_file to modify", a.Path)
		}
		return Result{}, fmt.Errorf("create_file: %w", err)
	}

	if _, err := f.WriteString(a.Content); err != nil {
		_ = f.Close()
		return Result{}, fmt.Errorf("create_file: write: %w", err)
	}
	if err := f.Close(); err != nil {
		return Result{}, fmt.Errorf("create_file: close: %w", err)
	}

	result := CreateFileResult{
		Path: a.Path,
	}

	return Result{
		Data:      result,
		HumanText: fmt.Sprintf("created %s (%d bytes)", a.Path, len(a.Content)),
	}, nil
}

