# ADR-Phase-4 — Config Parsing, Bootstrap, and Skills Convention

**Status:** Approved (Architect)
**Phase:** 4 of 7
**Date:** 2026-07-07
**Agents:** architect → builder → tester
**Depends on:** Phase 1 (`internal/store`, `internal/llm`), Phase 2 (`internal/tools`), Phase 3 (`internal/loop`) — all fixed, merged dependencies

---

## Table of Contents

1. [Carried-Forward Decisions from Phase 3](#1-carried-forward-decisions-from-phase-3)
2. [Package Layout](#2-package-layout)
3. [Config Parser — YAML Frontmatter + Markdown Body](#3-config-parser--yaml-frontmatter--markdown-body)
4. [Parser Implementation Design](#4-parser-implementation-design)
5. [Bootstrap — Embedded Defaults and .aa/ Extraction](#5-bootstrap--embedded-defaults-and-aa-extraction)
6. [Skills Convention](#6-skills-convention)
7. [Changes to Existing Files](#7-changes-to-existing-files)
8. [New Files](#8-new-files)
9. [Constraints and Risks](#9-constraints-and-risks)

---

## 1. Carried-Forward Decisions from Phase 3

### 1.1 Session ID Exposure

**Decision: Add `SessionID() int64` accessor on `TurnLoop`, storing the last session ID as an unexported field.**

Phase 3's tester noted that `loop.Run()` returns `HaltReason` (Code, Message, ResumeCount) but does **not** expose the session ID, preventing tests (and future cost/call accounting) from querying session status or event counts from the store.

Three options were considered:

| Option | Description | Verdict |
|--------|-------------|---------|
| (a) Add `SessionID int64` to `HaltReason` | Mixes session lifecycle data into a struct whose purpose is "why the loop stopped" | Rejected — conceptual mismatch; `HaltReason` is about halt causes, not session metadata |
| (b) Return `(HaltReason, int64, error)` from `Run()` | Explicit, clean semantics | Rejected — breaks every existing caller (43 call sites in tests) for a single new field; API churn outweighs benefit |
| (c) Add `SessionID() int64` accessor on `TurnLoop` | Backward-compatible, minimal change, loop already stores the ID internally | **Chosen** |

**Change required in `internal/loop/loop.go`:**

- Add `lastSessionID int64` field to `TurnLoop` struct
- Set `l.lastSessionID = sessionID` at the end of `createSession` and at the end of `resumeSession`
- Add exported method:

```go
// SessionID returns the session ID from the most recent Run() call.
// Returns 0 if Run() has not been called yet.
func (l *TurnLoop) SessionID() int64 { return l.lastSessionID }
```

This is a one-field addition + two setter sites. No existing test code breaks; new tests and cost-accounting code can call `loop.SessionID()` after `Run()` returns.

### 1.2 Delta Check Accounting

**Decision: Log delta checks as `event_type=delta_check` events in the events table, with token counts.**

The delta/semantic check performs an additional LLM call every 5th turn. These calls cost tokens but were previously invisible in the event stream — they were not logged as `model_call` events, creating a blind spot for cost accounting.

Three options considered:

| Option | Description | Verdict |
|--------|-------------|---------|
| (a) Document in package comments | Zero code change, cheapest | Rejected — documentation is invisible to programmatic cost accounting; DB queries cannot distinguish delta calls from absent data |
| (b) Add a dedicated `delta_check` event type | One `InsertEvent` call inside `checkDelta`, makes delta calls visible and distinguishable | **Chosen** |
| (c) Track as a separate counter on `TurnLoop` | More structured but duplicates what the DB already stores | Rejected — the DB is the single source of truth for session data |

**Change required in `checkDelta` (in `internal/loop/loop.go`):**

After the successful delta-check LLM call and before returning, log a `delta_check` event:

```go
// After successfully receiving a response from the delta LLM:
tokensUsed := resp.Usage.TotalTokens
resultBrief := strings.TrimSpace(resp.Text)
if len(resultBrief) > 200 {
    resultBrief = resultBrief[:200] + "..."
}
l.store.InsertEvent(ctx, store.Event{
    SessionID:  sessionID,
    TurnIndex:  &turnIndex,
    EventType:  "delta_check",
    TokensUsed: &tokensUsed,
    ResultJSON: &resultBrief,
    CreatedAt:  store.NowUTC(),
})
```

This does **not** change the loop's control flow — it is purely observability. The delta check's LLM call remains separate from the main conversation's message history. Cost-accounting queries can now count `delta_check` events alongside `model_call` events with full token visibility.

---

## 2. Package Layout

```
agent-harness/
├── internal/
│   ├── config/                  # NEW — config parsing + bootstrap
│   │   ├── parser.go            #   YAML frontmatter parser → loop.AgentConfig
│   │   ├── bootstrap.go         #   go:embed + .aa/ extraction + skills manifest
│   │   ├── config_test.go       #   Tests for parser + bootstrap
│   │   └── embedded/            #   NOT a Go package — data directory for //go:embed
│   │       ├── agents/          #   Default agent config files (git-tracked sources)
│   │       │   ├── architect.md
│   │       │   ├── builder.md
│   │       │   ├── librarian.md
│   │       │   ├── tester.md
│   │       │   └── forensic.md
│   │       └── skills/          #   Default skill files
│   │           ├── gopls-mcp/
│   │           │   └── SKILL.md
│   │           └── golang-code-style/
│   │               └── SKILL.md
│   ├── loop/                    # Phase 3 — +SessionID() accessor, +delta_check event
│   ├── store/                   # Phase 1 — unchanged
│   ├── llm/                     # Phase 1 — unchanged
│   └── tools/                   # Phase 2 — unchanged
├── cmd/
│   └── harness/                 # Updated to call config.Bootstrap + ParseAgentConfig
├── docs/
│   └── adr-phase-4-config-bootstrap.md  # ← this file
```

### Justification

| Package | Rationale |
|---------|-----------|
| `internal/config/` | Config parsing and bootstrap are independent of the turn loop — they produce `loop.AgentConfig` values but do not *consume* loop types beyond the parsed result. A separate package keeps the dependency one-way: `cmd/harness → config → loop/tools`. |
| `internal/config/embedded/` | Source directory for `go:embed`. Not a Go package (no `*.go` files; the embedded directory is a data-only tree). The `//go:embed` directive lives in `bootstrap.go` and references `embedded/agents/*.md` and `embedded/skills/*/SKILL.md`. |
| `internal/config/parser.go` | Single file containing the YAML frontmatter struct, `ParseAgentConfig`/`ParseAgentConfigBytes`, and the `ToAgentConfig` translation method. |
| `internal/config/bootstrap.go` | Single file containing `//go:embed`, `Bootstrap()`, `ReadSkillsManifest()`, and internal helpers. |

---
## 3. Config Parser — YAML Frontmatter + Markdown Body

### 3.1 Format (per Spec §10.1)

```markdown
---
name: builder
model: deepseek-v4-flash
base_url: https://api.metisai.ir/v1
context_max_tokens: 32768
temperature: 0.2
max_file_writes: 5            # optional, overrides DefaultMaxFileWrites
tools:
  read_file: {}
  list_dir: {}
  edit_file: {paths: ["docs/adr-*.md"]}
  bash_exec: null
  # create_file not listed — implicitly denied
---
<system prompt body in markdown>
```

### 3.2 YAML Frontmatter Struct

```go
// AgentConfigFile is the YAML frontmatter of an agent config file.
// Fields map directly to loop.AgentConfig and tools.AgentToolConfig.
type AgentConfigFile struct {
    Name             string                 `yaml:"name"`
    Model            string                 `yaml:"model"`
    BaseURL          string                 `yaml:"base_url"`
    ContextMaxTokens int                    `yaml:"context_max_tokens"`
    Temperature      float64                `yaml:"temperature"`
    MaxFileWrites    int                    `yaml:"max_file_writes,omitempty"`

    // Tools maps tool names to their optional path restrictions.
    //
    // KEY SEMANTIC: The map value type is *ToolEntry (pointer), not ToolEntry.
    // This is critical for distinguishing three cases:
    //   - Key absent            → tool is NOT granted to the agent
    //   - Key present, value nil → tool is explicitly denied (bash_exec: null)
    //   - Key present, value set → tool is granted with these restrictions
    //
    // yaml.v3 decodes a null value in a map[*] into a nil pointer,
    // while a missing key simply does not appear in the map. An empty
    // object {} decodes as &ToolEntry{} (non-nil pointer to zero struct).
    Tools map[string]*ToolEntry `yaml:"tools"`
}

// ToolEntry holds optional path restrictions for a tool.
// An empty ToolEntry (with no Paths) means the tool is granted
// with no path restrictions (subject only to project-root scoping).
type ToolEntry struct {
    Paths []string `yaml:"paths,omitempty"`
}
```

### 3.3 Translation to Runtime Types

The translation function `ToAgentConfig` converts the parsed YAML struct into `loop.AgentConfig` (and its embedded `tools.AgentToolConfig`):

```go
// ToAgentConfig converts parsed YAML frontmatter (+ body) to a loop.AgentConfig.
// This is a translation layer, not a redefinition — the output types are
// defined in internal/loop and internal/tools.
func (f *AgentConfigFile) ToAgentConfig(systemBody string) loop.AgentConfig {
    cfg := loop.AgentConfig{
        Name:             f.Name,
        ModelName:        f.Model,
        BaseURL:          f.BaseURL,
        ContextMaxTokens: f.ContextMaxTokens,
        Temperature:      f.Temperature,
        SystemPrompt:     systemBody,
        MaxFileWrites:    f.MaxFileWrites,
        Tools:            make(tools.AgentToolConfig),
    }

    // Translate tools: nil means denied, non-nil means granted.
    for toolName, entry := range f.Tools {
        if entry == nil {
            continue // explicitly denied — skip (same as absent)
        }
        restrictions := tools.ToolRestrictions{}
        if len(entry.Paths) > 0 {
            restrictions.PathGlobs = entry.Paths
        }
        cfg.Tools[toolName] = restrictions
    }

    return cfg
}
```

**Translation matrix:**

| YAML | `entry` value | `cfg.Tools` entry | Effect |
|------|--------------|-------------------|--------|
| `read_file: {}` | `&ToolEntry{}` | `"read_file" → ToolRestrictions{}` | Granted, all paths allowed |
| `edit_file: {paths: ["docs/*.md"]}` | `&ToolEntry{Paths: ["docs/*.md"]}` | `"edit_file" → ToolRestrictions{PathGlobs: ["docs/*.md"]}` | Granted, restricted to glob |
| `bash_exec: null` | `nil` | (skipped) | Not granted |
| *(key absent)* | *(not iterated)* | (skipped) | Not granted |

**Note:** `bash_exec: null` and a missing key produce the same runtime output (tool not granted). `ToAgentConfig` treats both as "denied." This matches spec §6.2: "A tool entirely omitted or set to `null` means the agent does not have that capability."

### 3.4 Confirmed YAML Library Behavior

The chosen library is **`gopkg.in/yaml.v3`** (standard Go YAML library, actively maintained, used by Kubernetes and most Go projects).

**Behavior for `map[string]*ToolEntry` with `null` vs absent:**

| Input | `yaml.v3` behavior |
|-------|-------------------|
| `bash_exec: null` | Key `"bash_exec"` added to map with value `nil` |
| `read_file: {}` | Key `"read_file"` added to map with value `&ToolEntry{}` (non-nil) |
| `create_file:` *(no value)* | Same as `null` — key added with value `nil` |
| *(key not in YAML)* | Key not present in map |

This is confirmed behavior of `gopkg.in/yaml.v3` as of v3.0.1 (released 2022, no breaking changes since). The builder should `go get gopkg.in/yaml.v3@latest` at implementation time.

**Alternative considered and rejected:** Using `map[string]ToolEntry` (non-pointer map value type). This would not distinguish `null` from `{}` because both would decode as a zero-value `ToolEntry`. Explicit rejection: `*ToolEntry` pointer values are required for the three-way distinction.

### 3.5 Body Extraction

The system prompt body is **everything after the second `---` delimiter**, trimmed:

```go
// Delimiter constants for frontmatter parsing.
var (
    frontmatterOpen    = []byte("---\n")
    frontmatterOpenCRLF = []byte("---\r\n")
    frontmatterClose   = []byte("\n---\n")
)

// ParseAgentConfigBytes parses a complete agent config file (YAML frontmatter
// + Markdown body) and returns the parsed AgentConfig.
func ParseAgentConfigBytes(data []byte) (loop.AgentConfig, error) {
    trimmed := bytes.TrimSpace(data)

    // Must start with "---\n" or "---\r\n" to have frontmatter.
    if !bytes.HasPrefix(trimmed, frontmatterOpen) &&
       !bytes.HasPrefix(trimmed, frontmatterOpenCRLF) {
        // No frontmatter: entire file is the system prompt body.
        return loop.AgentConfig{
            SystemPrompt: string(trimmed),
            Tools:        make(tools.AgentToolConfig),
        }, nil
    }

    // Find the second --- delimiter.
    // Skip past the first "---\n" (4 bytes).
    rest := trimmed[len(frontmatterOpen):]
    idx := bytes.Index(rest, frontmatterClose)
    if idx < 0 {
        // Try end-of-file "---" with no trailing content.
        if bytes.HasSuffix(rest, []byte("\n---")) {
            idx = len(rest) - 4
            rest = rest[:idx]
        } else {
            return loop.AgentConfig{}, fmt.Errorf(
                "config: invalid frontmatter: no closing --- delimiter")
        }
    }

    // YAML block is between the two --- delimiters.
    yamlBlock := rest[:idx]

    // Body is everything after the second "---\n".
    bodyStart := idx + len(frontmatterClose)
    body := strings.TrimSpace(string(rest[bodyStart:]))

    // Parse YAML.
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
```

### 3.6 File-Based Entry Point

```go
// ParseAgentConfig reads a config file from disk and parses it.
func ParseAgentConfig(path string) (loop.AgentConfig, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return loop.AgentConfig{}, fmt.Errorf("config: read %s: %w", path, err)
    }
    return ParseAgentConfigBytes(data)
}
```

### 3.7 UserPrompt — Not in Config File

`AgentConfig.UserPrompt` is **never populated by the config parser**. Per spec §12, system prompts are fixed/git-tracked config file content, while user prompts (kickoff prompts) are authored by Phase 0 per project per phase. The caller (`cmd/harness` or a future orchestrator) sets `UserPrompt` independently.

The config parser produces a `loop.AgentConfig` with `UserPrompt: ""`. The caller must set it before passing the config to `loop.New`.

---
## 4. Parser Implementation Design

### 4.1 Public API

```go
package config

// ParseAgentConfig reads a Markdown file with YAML frontmatter from disk
// and returns a parsed loop.AgentConfig ready for use with the turn loop.
func ParseAgentConfig(path string) (loop.AgentConfig, error)

// ParseAgentConfigBytes is the same but operates on in-memory bytes.
// Useful for testing and for parsing embedded defaults.
func ParseAgentConfigBytes(data []byte) (loop.AgentConfig, error)
```

### 4.2 Edge Cases Handled

| Input | Behavior |
|-------|----------|
| No frontmatter (no `---` at start) | Entire file treated as system prompt body; all other fields get zero values |
| Only frontmatter, no body | System prompt is empty string |
| Extra whitespace before `---` | `TrimSpace` handles it; leading `---` must be at the start after trimming |
| Windows line endings (`\r\n`) | Detected and handled via separate delimiter constants |
| Body has its own `---` (e.g., Markdown HR) | Not a problem — search for the *first* `\n---\n` after the opening delimiter |
| Empty file | Error: no name, model, or base_url |
| Missing required field (`name`, `model`, `base_url`) | Error with clear message |
| `tools:` block entirely absent | `AgentToolConfig` remains empty — no tools granted |
| Unknown keys in YAML | `yaml.v3` silently ignores unknown keys — acceptable for forward compatibility |
| `temperature` absent | Defaults to `0.0` (caller may apply a sensible default before use) |
| `max_file_writes: 0` or absent | `MaxFileWrites` stays 0, meaning `DefaultMaxFileWrites` (5) applies at loop level |

### 4.3 Dependency Added

`gopkg.in/yaml.v3` — pure Go, no cgo, widely used. The builder should pin to the latest tagged version.

---

## 5. Bootstrap — Embedded Defaults and .aa/ Extraction

### 5.1 Embed Directive Location

The `//go:embed` directive lives in `internal/config/bootstrap.go`:

```go
package config

import (
    "embed"
    "io/fs"
    "os"
    "path/filepath"
)

//go:embed embedded/agents/*.md embedded/skills/*/SKILL.md
var embeddedFS embed.FS
```

The embedded directory structure is relative to `internal/config/`:

```
internal/config/
├── parser.go
├── bootstrap.go
├── config_test.go
└── embedded/
    ├── agents/
    │   ├── architect.md
    │   ├── builder.md
    │   ├── librarian.md
    │   ├── tester.md
    │   └── forensic.md
    └── skills/
        ├── gopls-mcp/
        │   └── SKILL.md
        └── golang-code-style/
            └── SKILL.md
```

The builder creates stub files with minimal frontmatter (just `name` and tool grants) and placeholder system prompts. The actual content of these embedded files will be refined in later iterations — Phase 4 establishes the mechanism, not the content.

### 5.2 Extraction Logic

```go
// Bootstrap ensures embedded defaults are extracted to the .aa/ directory
// in targetDir. Safe to call on every startup.
//
// Extraction policy:
// - If .aa/ does not exist          → full extraction of all embedded files
// - If .aa/ exists, file present    → disk content is authoritative (never overwrite)
// - If .aa/ exists, file missing    → re-extract that single file from embedded
func Bootstrap(targetDir string) error {
    aaDir := filepath.Join(targetDir, ".aa")

    _, err := os.Stat(aaDir)
    if err == nil {
        // .aa/ exists — check for missing files only.
        return ensureMissingFiles(embeddedFS, "embedded", aaDir)
    }
    if !os.IsNotExist(err) {
        return fmt.Errorf("config: stat .aa: %w", err)
    }

    // .aa/ does not exist — full extraction.
    return extractAll(embeddedFS, "embedded", aaDir)
}
```

#### Full Extraction (`extractAll`)

```go
func extractAll(efs embed.FS, sourcePrefix, targetDir string) error {
    return fs.WalkDir(efs, sourcePrefix, func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return err
        }
        relPath, _ := filepath.Rel(sourcePrefix, path)
        targetPath := filepath.Join(targetDir, relPath)

        if d.IsDir() {
            return os.MkdirAll(targetPath, 0755)
        }

        data, err := efs.ReadFile(path)
        if err != nil {
            return fmt.Errorf("config: read embedded %s: %w", path, err)
        }
        if err := os.WriteFile(targetPath, data, 0644); err != nil {
            return fmt.Errorf("config: write %s: %w", targetPath, err)
        }
        return nil
    })
}
```

#### Missing-File Recovery (`ensureMissingFiles`)

```go
func ensureMissingFiles(efs embed.FS, sourcePrefix, aaDir string) error {
    return fs.WalkDir(efs, sourcePrefix, func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return err
        }
        relPath, _ := filepath.Rel(sourcePrefix, path)
        targetPath := filepath.Join(aaDir, relPath)

        if d.IsDir() {
            return os.MkdirAll(targetPath, 0755)
        }

        _, err = os.Stat(targetPath)
        if err == nil {
            return nil // file exists — disk wins, do not overwrite
        }
        if !os.IsNotExist(err) {
            return fmt.Errorf("config: stat %s: %w", targetPath, err)
        }

        // File missing — re-extract from embedded defaults.
        data, err := efs.ReadFile(path)
        if err != nil {
            return fmt.Errorf("config: read embedded %s: %w", path, err)
        }
        return os.WriteFile(targetPath, data, 0644)
    })
}
```

### 5.3 First-Run Flow

```
Startup:
  1. Call config.Bootstrap(projectRoot)
  2. If .aa/ exists:
       Walk embedded FS, stat each file in .aa/
       For missing files: write from embedded
       For existing files: skip (disk wins)
  3. If .aa/ doesn't exist:
       Walk embedded FS, create directories, write all files
  4. If any error (permissions, disk full, etc.): return error, halt startup
```

### 5.4 Missing-File Edge Case — Design Decision

**Question:** If `.aa/` exists but a specific expected file is missing (e.g., user deleted `builder.md` but kept `architect.md`), should the harness:

| Option | Behavior | Rationale |
|--------|----------|-----------|
| (a) Error — refuse to start | "You deleted a required config file" | Rejected — hostile to users; forces total deletion of `.aa/` to recover |
| (b) Silently skip — operate without that agent | Missing file = missing agent | Possible but surprising; user may not notice an agent is gone |
| (c) Re-extract only the missing file | Gentle recovery, disk-wins for all existing files | **Chosen** |

**Chosen approach: Option (c) — re-extract missing files only, never overwrite existing files.**

This is **not** hash-diffing or partial-merge logic (which the spec explicitly rules out). It is a pure existence check:
- Does the file exist on disk? → Use it as-is.
- Does the file not exist on disk? → Write the embedded copy.

This provides gentle recovery from accidental deletion while strictly honoring "disk wins for content" for all files that actually exist.

### 5.5 Bootstrap Public API

```go
// Bootstrap ensures embedded defaults are extracted to .aa/ in targetDir.
// Safe to call on every startup. Idempotent after first extraction.
func Bootstrap(targetDir string) error

// ReadSkillsManifest returns a compact string listing all available skills
// (name + description) by reading each SKILL.md frontmatter from .aa/skills/.
// Returns empty string if the skills directory does not exist or has no files.
func ReadSkillsManifest(projectRoot string) (string, error)
```

---

## 6. Skills Convention

### 6.1 Directory Structure

Skills live in `.aa/skills/`, following the same bootstrap mechanism as agent configs:

```
.aa/
├── agents/
│   ├── architect.md
│   ├── builder.md
│   ├── librarian.md
│   ├── tester.md
│   └── forensic.md
└── skills/
    ├── gopls-mcp/
    │   └── SKILL.md
    └── golang-code-style/
        └── SKILL.md
```

Each skill is a subdirectory containing a single `SKILL.md` file. The subdirectory name is the skill's canonical identifier (used for referencing via `read_file`).

### 6.2 SKILL.md Format

Each `SKILL.md` has the same Markdown + YAML frontmatter format as agent configs:

```markdown
---
name: gopls-mcp
description: Mandatory gopls workflow and gopls-specific tools for Go workspaces.
---

# gopls-mcp Skill

Full content body...
```

The frontmatter has exactly two required fields:
- `name`: Canonical skill name (matches the subdirectory name).
- `description`: One-line description for manifest display.

The body contains the full skill content (markdown), loaded on demand by the agent via `read_file`.
### 6.3 Manifest Injection at Session Start

Before the first LLM call, the harness reads the skills manifest and injects it into the agent's context:

```go
// ReadSkillsManifest scans .aa/skills/ for SKILL.md files, reads each
// frontmatter, and returns a compact string listing available skills.
func ReadSkillsManifest(projectRoot string) (string, error) {
    skillsDir := filepath.Join(projectRoot, ".aa", "skills")

    entries, err := os.ReadDir(skillsDir)
    if err != nil {
        if os.IsNotExist(err) {
            return "", nil
        }
        return "", fmt.Errorf("config: read skills dir: %w", err)
    }

    var lines []string
    for _, entry := range entries {
        if !entry.IsDir() {
            continue
        }
        skillPath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
        data, err := os.ReadFile(skillPath)
        if err != nil {
            continue
        }
        name, desc := parseSkillManifest(data)
        if name != "" {
            lines = append(lines, fmt.Sprintf("- **%s**: %s", name, desc))
        }
    }

    if len(lines) == 0 {
        return "", nil
    }
    return "Available skills:\n" + strings.Join(lines, "\n"), nil
}
```

The frontmatter extraction for skills reuses the same `---` splitting logic but decodes into a minimal struct:

```go
// parseSkillManifest extracts just the name and description from SKILL.md
// frontmatter. Uses the same frontmatter-splitting logic as ParseAgentConfigBytes.
func parseSkillManifest(data []byte) (name, desc string) {
    trimmed := bytes.TrimSpace(data)
    if !bytes.HasPrefix(trimmed, frontmatterOpen) &&
       !bytes.HasPrefix(trimmed, frontmatterOpenCRLF) {
        return "", ""
    }

    rest := trimmed[len(frontmatterOpen):]
    idx := bytes.Index(rest, frontmatterClose)
    if idx < 0 {
        if bytes.HasSuffix(rest, []byte("\n---")) {
            idx = len(rest) - 4
            rest = rest[:idx]
        } else {
            return "", ""
        }
    }

    yamlBlock := rest[:idx]

    var manifest struct {
        Name        string `yaml:"name"`
        Description string `yaml:"description"`
    }
    if err := yaml.Unmarshal(yamlBlock, &manifest); err != nil {
        return "", ""
    }
    return manifest.Name, manifest.Description
}
```

The manifest string is appended to the **system prompt** automatically:

```go
// In session setup, after parsing the agent config:
manifest, _ := config.ReadSkillsManifest(projectRoot)
if manifest != "" {
    cfg.SystemPrompt = cfg.SystemPrompt + "\n\n" + manifest
}
```

This means every LLM call sees the skills list in the system prompt. The agent can call `read_file(.aa/skills/<skill-name>/SKILL.md)` to load full content.

### 6.4 How It Works End-to-End

1. **Startup**: `config.Bootstrap(projectRoot)` ensures `.aa/skills/` exists with embedded skill files.
2. **Before session**: `config.ReadSkillsManifest(projectRoot)` reads all `SKILL.md` frontmatter entries and produces a compact manifest string.
3. **Session setup**: The manifest string is appended to the agent's system prompt.
4. **During session**: The model sees the skills list and can request full content by calling `read_file(.aa/skills/<name>/SKILL.md)` — an existing tool, no new tool needed.

### 6.5 Skills Are Sourced Identically to Agent Configs

Both agent configs and skills use:
- Same `go:embed` source tree (under `internal/config/embedded/`)
- Same bootstrap mechanism (`config.Bootstrap()`)
- Same extraction to `.aa/` directory
- Same "disk wins, recover missing files" policy
- Same frontmatter format (Markdown + YAML)

No separate pathway exists for skills. Per spec §11: "Sourced the same way as agent configs."

---

## 7. Changes to Existing Files

### 7.1 `internal/loop/loop.go`

| Change | Location | Description |
|--------|----------|-------------|
| Add `lastSessionID int64` field | TurnLoop struct | Stores the most recent session ID from Run() |
| Modify `createSession` | Last line of method | Set `l.lastSessionID = sessionID` after successful insert |
| Modify `resumeSession` | Before returning | Set `l.lastSessionID = oldSessionID` |
| Add `SessionID() int64` method | Anywhere on TurnLoop | Returns `l.lastSessionID`; 0 if Run() not yet called |
| Modify `checkDelta` | After successful LLM call, before returning | Insert `delta_check` event with tokens used and response text (truncated to 200 chars) |

Minimal changes — no restructuring of the loop logic, no new imports beyond `store` (already imported).

### 7.2 `cmd/harness/main.go`

| Change | Description |
|--------|-------------|
| Import `agent-harness/internal/config` | New dependency |
| Call `config.Bootstrap(projectRoot)` | Before any other startup, ensures `.aa/` exists |
| Call `config.ReadSkillsManifest(projectRoot)` | Get skills manifest string |
| Call `config.ParseAgentConfig(path)` | Replace hand-constructed `AgentConfig` |
| Append manifest to `cfg.SystemPrompt` | If manifest is non-empty |
| Wire parsed config to `loop.New` | Same as before, but config is parsed not hardcoded |

**Note on CLI:** The exact mechanism for selecting which agent config to load is a builder-level design choice. Options include a CLI flag (`--agent builder`), an env var, or loading all configs and choosing by a CLI subcommand. The ADR specifies the parsing/bootstrap functions; CLI argument parsing is implementation detail.

### 7.3 `go.mod`

Add `gopkg.in/yaml.v3` dependency.

---

## 8. New Files

| File | Purpose |
|------|---------|
| `internal/config/parser.go` | `AgentConfigFile`, `ToolEntry`, `ParseAgentConfig`, `ParseAgentConfigBytes`, `ToAgentConfig` translation. Frontmatter-splitting logic shared with `parseSkillManifest`. |
| `internal/config/bootstrap.go` | `//go:embed embedded/agents/*.md embedded/skills/*/SKILL.md`, `Bootstrap()`, `ReadSkillsManifest()`, `parseSkillManifest()`. Internal helpers: `extractAll`, `ensureMissingFiles`. |
| `internal/config/config_test.go` | Tests for parser (frontmatter split, YAML decode, null handling, required-field validation, edge cases) and bootstrap (extraction, missing-file recovery, idempotency). |
| `internal/config/embedded/agents/*.md` | Five stub agent config files with minimal frontmatter and placeholder system prompts. Content refined in later iterations. |
| `internal/config/embedded/skills/*/SKILL.md` | Stub skill files (at minimum `gopls-mcp` for reference). Content refined later. |

---

## 9. Constraints and Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| **yaml.v3 treats `null` and absent identically for `map[string]ToolEntry`** | Would conflate `bash_exec: null` (explicit denial) with `bash_exec: {}` (granted) | Use `map[string]*ToolEntry` (pointer to struct). yaml.v3 distinguishes null (nil pointer) from `{}` (non-nil pointer). Verified in §3.4. |
| **Frontmatter parsing brittle to `---` in body** | If system prompt body contains `---`, second-delimiter search could split incorrectly | Search for `\n---\n` after opening delimiter targets the first complete YAML close. A `---` not on its own line won't match. Acceptable for v1. If false matches occur, switch to regex `^\n---\n$` anchored to start-of-line. |
| **Embedded defaults content is placeholders** | Until Phase 0 or manual authoring produces real content, embedded configs are stubs | The mechanism is the deliverable; content is refined later. Builder creates minimal working stubs for `go build` and tests. |
| **Bootstrap walks filesystem on every startup** | Adds ~10ms on cold FS cache to stat each embedded file | Negligible; no caching needed for v1. WAL mode ensures concurrent DB reads aren't impacted. |
| **Skills manifest appended to system prompt** | Increases system prompt token count by ~100-200 tokens | Acceptable — the manifest is compact and useful. If token pressure becomes an issue, Phase 6+ can make it opt-in. |
| **`internal/config` depends on `internal/loop` and `internal/tools`** | Creates a new dependency edge | This is one-way: config → loop/tools. Neither loop nor tools import config. The import graph remains acyclic (`cmd/harness → config → loop/tools`). Verified by Go import cycle checker. |
| **Skills directory might not exist (first run)** | `ReadSkillsManifest` would fail | `Bootstrap()` runs first and creates `.aa/skills/`. If it still doesn't exist (e.g., no skills embedded), `ReadSkillsManifest` returns empty string — not an error. |
| **`parseSkillManifest` silently ignores malformed SKILL.md** | A broken skill file silently vanishes from the manifest | Acceptable — the harness should not crash due to a bad skill file. The skill simply won't appear in the manifest; the agent won't know about it. |
