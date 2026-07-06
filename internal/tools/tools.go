// Package tools provides the six fixed tools available to coding agents:
// read_file, edit_file, create_file, bash_exec, list_dir, and write_log.
//
// It also defines the Tool interface, the Registry, and per-agent per-tool
// access-control types used by the Phase 3 turn loop.
package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"agent-harness/internal/llm"
)

// Tool is the interface each tool must implement.
type Tool interface {
	// Name returns the canonical tool name (e.g. "read_file", "edit_file").
	// Must match the name used in ToolDef.Name and in the model's ToolCall.Function.Name.
	Name() string

	// Definition returns the ToolDef sent to the model's API.
	Definition() llm.ToolDef

	// Execute runs the tool with the given arguments (raw JSON from the model's
	// ToolCall.Function.Arguments field). Returns the result as a JSON-serializable value.
	//
	// The ctx may carry a deadline (from bash_exec's hard timeout).
	// The config carries per-agent restrictions for this specific tool call.
	Execute(ctx context.Context, args json.RawMessage, config ToolConfig) (Result, error)
}

// Result is the structured output of a tool execution.
type Result struct {
	// Data is the tool-specific result payload. Must be JSON-serializable.
	Data any `json:"data"`

	// HumanText is a plain-text summary suitable for the phase-N.log or CLI display.
	HumanText string `json:"-"`
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// Registry holds all available tools keyed by name.
type Registry map[string]Tool

// NewDefaultRegistry creates the registry with all six built-in tools.
func NewDefaultRegistry(projectRoot, logPath string) Registry {
	return Registry{
		"read_file":   &ReadFileTool{root: projectRoot},
		"edit_file":   &EditFileTool{root: projectRoot},
		"create_file": &CreateFileTool{root: projectRoot},
		"list_dir":    &ListDirTool{root: projectRoot},
		"bash_exec":   &BashExecTool{root: projectRoot},
		"write_log":   &WriteLogTool{logPath: logPath},
	}
}

// FilterByAgentConfig returns a new Registry containing only the tools the
// agent is granted according to the given AgentToolConfig.
func (r Registry) FilterByAgentConfig(config AgentToolConfig) Registry {
	filtered := make(Registry)
	for name, tool := range r {
		if _, ok := config[name]; ok {
			filtered[name] = tool
		}
	}
	return filtered
}

// Definitions returns the ToolDefs for all tools in the registry, sorted by name.
func (r Registry) Definitions() []llm.ToolDef {
	names := make([]string, 0, len(r))
	for n := range r {
		names = append(names, n)
	}
	sort.Strings(names)

	defs := make([]llm.ToolDef, 0, len(names))
	for _, n := range names {
		defs = append(defs, r[n].Definition())
	}
	return defs
}

// ---------------------------------------------------------------------------
// Per-agent per-tool access control
// ---------------------------------------------------------------------------

// ToolRestrictions define per-tool access control for an agent.
// Full config parsing (YAML frontmatter) is Phase 4 — this is the runtime
// representation that the Phase 4 parser will produce.
type ToolRestrictions struct {
	// PathGlobs restricts the tool to matching paths only.
	// If nil/empty AND the tool is present, all paths under project root are allowed.
	// Only meaningful for file-touching tools (read_file, edit_file, create_file, list_dir).
	PathGlobs []string `json:"paths,omitempty"`
}

// AgentToolConfig maps tool names to their restrictions.
// A tool name absent from this map means the agent does NOT have that tool.
type AgentToolConfig map[string]ToolRestrictions

// ToolConfig is passed to every tool.Execute call.
// It carries the per-agent restrictions for this tool at this call site.
type ToolConfig struct {
	// AllowedPaths, if non-empty, lists the glob patterns that paths must match.
	// If empty, all paths under project root are allowed (for this tool).
	AllowedPaths []string

	// ProjectRoot is the resolved project root path.
	ProjectRoot string
}

// AllowPath checks whether a resolved absolute path is permitted by glob restrictions.
func (c ToolConfig) AllowPath(resolvedPath string) error {
	if len(c.AllowedPaths) == 0 {
		return nil
	}

	rel, err := filepath.Rel(c.ProjectRoot, resolvedPath)
	if err != nil {
		return fmt.Errorf("path scoping: compute relative path: %w", err)
	}

	for _, glob := range c.AllowedPaths {
		if filepath.IsAbs(glob) {
			if matched, _ := filepath.Match(glob, resolvedPath); matched {
				return nil
			}
		} else {
			if matched, _ := filepath.Match(glob, rel); matched {
				return nil
			}
			// Also try matching just the base name for convenience.
			if matched, _ := filepath.Match(glob, filepath.Base(resolvedPath)); matched {
				return nil
			}
		}
	}

	return &DisallowedPathError{Path: resolvedPath, Globs: c.AllowedPaths}
}

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

// PathEscapeError is returned when a file path escapes the project root.
type PathEscapeError struct {
	Path string
	Root string
}

func (e *PathEscapeError) Error() string {
	return fmt.Sprintf("path %q escapes project root %q", e.Path, e.Root)
}

// DisallowedPathError is returned when a path does not match per-agent glob restrictions.
type DisallowedPathError struct {
	Path  string
	Globs []string
}

func (e *DisallowedPathError) Error() string {
	return fmt.Sprintf("path %q is not allowed by per-agent restrictions (globs: %s)",
		e.Path, strings.Join(e.Globs, ", "))
}

// ErrNoMatch is returned by edit_file when old_str is not found in the file.
type ErrNoMatch struct {
	Path string
}

func (e *ErrNoMatch) Error() string {
	return fmt.Sprintf("edit_file: zero matches for old_str in %q — provide more surrounding context", e.Path)
}

// ErrAmbiguousMatch is returned by edit_file when old_str matches more than once.
type ErrAmbiguousMatch struct {
	Path         string
	MatchesFound int
}

func (e *ErrAmbiguousMatch) Error() string {
	return fmt.Sprintf("edit_file: old_str matches %d times in %q — provide more surrounding context",
		e.MatchesFound, e.Path)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fileHash computes the SHA-256 hash of the given content, hex-encoded.
func fileHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h[:])
}

// Compile-time interface checks: ensure all six tools satisfy the Tool interface.
var (
	_ Tool = (*ReadFileTool)(nil)
	_ Tool = (*EditFileTool)(nil)
	_ Tool = (*CreateFileTool)(nil)
	_ Tool = (*ListDirTool)(nil)
	_ Tool = (*BashExecTool)(nil)
	_ Tool = (*WriteLogTool)(nil)
)

// newToolDef is a convenience constructor for producing an llm.ToolDef
// from a tool's name, description, and JSON Schema parameters map.
func newToolDef(name, desc string, params map[string]any) llm.ToolDef {
	return llm.ToolDef{
		Function: llm.ToolFunction{
			Name:        name,
			Description: desc,
			Parameters:  params,
		},
	}
}
