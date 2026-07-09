// Package config provides YAML-frontmatter parsing for agent config files,
// embedded defaults extraction (.aa/ bootstrap), and skills manifest discovery.
//
// The parser reads Markdown files with YAML frontmatter (delimited by ---) and
// produces loop.AgentConfig values ready for use with the turn loop.
package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"agent-harness/internal/loop"
	"agent-harness/internal/tools"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Frontmatter delimiter constants
// ---------------------------------------------------------------------------

var (
	frontmatterOpen     = []byte("---\n")
	frontmatterOpenCRLF = []byte("---\r\n")
	frontmatterClose    = []byte("\n---\n")
	frontmatterCloseCRLF = []byte("\r\n---\r\n")
)

// ---------------------------------------------------------------------------
// YAML frontmatter structs
// ---------------------------------------------------------------------------

// AgentConfigFile is the YAML frontmatter of an agent config file.
// Fields map directly to loop.AgentConfig and tools.AgentToolConfig.
type AgentConfigFile struct {
	Name             string                `yaml:"name"`
	Model            string                `yaml:"model"`
	BaseURL          string                `yaml:"base_url"`
	APIKeyEnv        string                `yaml:"api_key_env"` // name of env var holding the API key (optional)
	ContextMaxTokens int                   `yaml:"context_max_tokens"`
	Temperature      float64               `yaml:"temperature"`
	MaxFileWrites    int                   `yaml:"max_file_writes,omitempty"`
	Tools            map[string]*ToolEntry `yaml:"tools"`
}

// ToolEntry holds optional path restrictions for a tool.
// An empty ToolEntry (no Paths) means the tool is granted with no path restrictions.
//
// KEY SEMANTIC: *ToolEntry pointer values in the Tools map distinguish:
//   - key absent          → tool is NOT granted
//   - key present, nil    → tool is explicitly denied (bash_exec: null)
//   - key present, set    → tool is granted with these restrictions
type ToolEntry struct {
	Paths []string `yaml:"paths,omitempty"`
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// ParseAgentConfig reads a Markdown file with YAML frontmatter from disk
// and returns a parsed loop.AgentConfig ready for use with the turn loop.
func ParseAgentConfig(path string) (loop.AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return loop.AgentConfig{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	return ParseAgentConfigBytes(data)
}

// ParseAgentConfigBytes parses a complete agent config file (YAML frontmatter
// + Markdown body) from in-memory bytes.
func ParseAgentConfigBytes(data []byte) (loop.AgentConfig, error) {
	trimmed := bytes.TrimSpace(data)

	if len(trimmed) == 0 {
		return loop.AgentConfig{}, fmt.Errorf("config: empty file")
	}

	// Must start with "---\n" or "---\r\n" to have frontmatter.
	if !bytes.HasPrefix(trimmed, frontmatterOpen) &&
		!bytes.HasPrefix(trimmed, frontmatterOpenCRLF) {
		// No frontmatter: entire file is the system prompt body.
		return loop.AgentConfig{
			SystemPrompt: string(trimmed),
			Tools:        make(tools.AgentToolConfig),
		}, nil
	}

	// Skip past the opening "---\n" (or "---\r\n").
	openLen := len(frontmatterOpen)
	if bytes.HasPrefix(trimmed, frontmatterOpenCRLF) {
		openLen = len(frontmatterOpenCRLF)
	}
	rest := trimmed[openLen:]

	// Find the closing delimiter (try LF first, then CRLF).
	idx := bytes.Index(rest, frontmatterClose)
	closeLen := len(frontmatterClose)
	if idx < 0 {
		idx = bytes.Index(rest, frontmatterCloseCRLF)
		if idx >= 0 {
			closeLen = len(frontmatterCloseCRLF)
		}
	}

	if idx < 0 {
		// Try end-of-file "---" with no trailing content (LF or CRLF).
		if bytes.HasSuffix(rest, []byte("\n---")) {
			// YAML content ends at \n---, no body follows.
			idx = len(rest) - 4
			yamlBlock := rest[:idx]
			body := ""
			return parseAndTranslate(yamlBlock, body)
		}
		if bytes.HasSuffix(rest, []byte("\r\n---")) {
			idx = len(rest) - 5
			yamlBlock := rest[:idx]
			body := ""
			return parseAndTranslate(yamlBlock, body)
		}
		return loop.AgentConfig{}, fmt.Errorf("config: invalid frontmatter: no closing --- delimiter")
	}

	// YAML block is everything before \n---\n.
	yamlBlock := rest[:idx]

	// Body is everything after the closing delimiter.
	bodyStart := idx + closeLen
	var body string
	if bodyStart < len(rest) {
		body = strings.TrimSpace(string(rest[bodyStart:]))
	}

	return parseAndTranslate(yamlBlock, body)
}

// parseAndTranslate is the shared inner logic: unmarshal YAML, validate, translate.
func parseAndTranslate(yamlBlock []byte, body string) (loop.AgentConfig, error) {
	var file AgentConfigFile
	if err := yaml.Unmarshal(yamlBlock, &file); err != nil {
		return loop.AgentConfig{}, fmt.Errorf("config: frontmatter YAML: %w", err)
	}

	// Validate required fields.
	if file.Name == "" {
		return loop.AgentConfig{}, fmt.Errorf("config: 'name' is required")
	}
	if file.Model == "" {
		return loop.AgentConfig{}, fmt.Errorf("config: 'model' is required")
	}
	if file.BaseURL == "" {
		return loop.AgentConfig{}, fmt.Errorf("config: 'base_url' is required")
	}

	cfg := file.ToAgentConfig(body)
	return cfg, nil
}

// ToAgentConfig converts parsed YAML frontmatter (+ body) to a loop.AgentConfig.
// This is a translation layer — the output types are defined in internal/loop
// and internal/tools.
func (f *AgentConfigFile) ToAgentConfig(systemBody string) loop.AgentConfig {
	cfg := loop.AgentConfig{
		Name:             f.Name,
		ModelName:        f.Model,
		BaseURL:          f.BaseURL,
		APIKeyEnv:        f.APIKeyEnv,
		ContextMaxTokens: f.ContextMaxTokens,
		Temperature:      f.Temperature,
		SystemPrompt:     systemBody,
		MaxFileWrites:    f.MaxFileWrites,
		Tools:            make(tools.AgentToolConfig),
	}

	// Translate tools: nil means denied, non-nil means granted.
	for toolName, entry := range f.Tools {
		if entry == nil {
			continue // explicitly denied — skip (same effect as absent)
		}
		restrictions := tools.ToolRestrictions{}
		if len(entry.Paths) > 0 {
			restrictions.PathGlobs = entry.Paths
		}
		cfg.Tools[toolName] = restrictions
	}

	return cfg
}
