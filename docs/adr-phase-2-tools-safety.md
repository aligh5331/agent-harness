# ADR-Phase-2 — Tool Execution & Safety

**Status:** Approved (Architect)
**Phase:** 2 of 7
**Date:** 2026-07-05
**Agents:** architect → builder → tester
**Depends on:** Phase 1 (`internal/store`, `internal/llm` — fixed, not redesigned)

---

## 1. Package Layout

```
agent-harness/
├── internal/
│   ├── store/                 # Phase 1 — unchanged
│   ├── llm/                   # Phase 1 — unchanged
│   └── tools/                 # NEW — all tool definitions + safety
│       ├── tools.go           #   Tool interface, Registry, ToolConfig types
│       ├── resolve.go         #   resolveScoped — project-root path enforcement
│       ├── read_file.go       #   read_file tool
│       ├── edit_file.go       #   edit_file tool
│       ├── create_file.go     #   create_file tool
│       ├── list_dir.go        #   list_dir tool
│       ├── bash_exec.go       #   bash_exec tool
│       ├── write_log.go       #   write_log tool
│       └── tools_test.go      #   Tests for all tools + scoping
├── docs/
│   └── adr-phase-2-tools-safety.md  # ← this file
```

### Justification

| Package | Rationale |
|---------|-----------|
| `internal/tools/` | All six tools share the `Tool` interface, the `resolveScoped` helper, and the per-agent restriction mechanism. One package with no sub-packages keeps the dispatcher simple — a single `map[string]Tool` that the Phase 3 turn loop will call into. |
| Per-tool file | Each tool gets its own file (<200 LOC each), keeping the boundary between independent tools explicit. No shared mutable state between tools. |
| `resolve.go` | Shared by all file-touching tools. Extracted to its own file to make its behavior testable in isolation and auditable as the single chokepoint for path scoping. |

---

## 2. Tool Interface Shape

### 2.1 Core interface

```go
// Tool is the interface each tool must implement.
// A tool knows how to produce its ToolDef (for the model's tool-calling API)
// and how to execute a call given raw JSON arguments.
type Tool interface {
    // Name returns the tool's canonical name (e.g. "read_file", "edit_file").
    // Must match the name used in ToolDef.Name and in the model's ToolCall.Function.Name.
    Name() string

    // Definition returns the ToolDef that gets sent to the model's API.
    // This is how a tool advertises its JSON-schema to the model.
    Definition() llm.ToolDef

    // Execute runs the tool with the given arguments (raw JSON from the model's
    // ToolCall.Function.Arguments field). Returns the result as a JSON-serializable
    // value (will be serialized to result_json for DB logging and model consumption).
    //
    // The ctx may carry a deadline (from bash_exec's hard timeout).
    // The config carries per-agent restrictions for this specific tool call.
    Execute(ctx context.Context, args json.RawMessage, config ToolConfig) (Result, error)
}
```

### 2.2 Result type

```go
// Result is the structured output of a tool execution, ready for JSON serialization.
// Each tool defines its own concrete result shape; this is the common envelope.
type Result struct {
    // Data is the tool-specific result payload. Must be JSON-serializable.
    // For read_file: map with "content" and "line_count". For bash_exec: map with
    // "stdout", "stderr", "exit_code". For edit_file: map with "matches".
    Data any `json:"data"`

    // HumanText is a plain-text summary suitable for the phase-N.log or CLI display.
    // Not shown to the model directly — it's for human-readable logging.
    HumanText string `json:"-"`
}
```

### 2.3 Tool registration

```go
// Registry holds all available tools keyed by name.
// Build once at startup; read-only thereafter.
type Registry map[string]Tool

// NewDefaultRegistry creates the registry with all six built-in tools.
// The phase-3 turn loop will filter this registry by per-agent restrictions
// before building the list of ToolDefs to send to the model.
func NewDefaultRegistry(projectRoot string, logPath string) Registry {
    return Registry{
        "read_file":   &ReadFileTool{root: projectRoot},
        "edit_file":   &EditFileTool{root: projectRoot},
        "create_file": &CreateFileTool{root: projectRoot},
        "list_dir":    &ListDirTool{root: projectRoot},
        "bash_exec":   &BashExecTool{root: projectRoot},
        "write_log":   &WriteLogTool{logPath: logPath},
    }
}

// ForAgent filters the registry to only include tools the agent is allowed to use.
// Tools set to null/omitted in the agent's config are excluded entirely.
// The returned slice is what gets sent as llm.Request.Tools and what the turn loop
// will dispatch from.
func (r Registry) ForAgent(restrictions map[string]ToolRestrictions) []Tool {
    var tools []Tool
    for name, t := range r {
        if _, ok := restrictions[name]; ok {
            tools = append(tools, t)
        }
    }
    return tools
}
```

### 2.4 Argument unmarshaling pattern

Each tool defines its own argument struct. The `Execute` method unmarshals `json.RawMessage` into that struct:

```go
type ReadFileArgs struct {
    Path string `json:"path"`
}

func (t *ReadFileTool) Execute(ctx context.Context, args json.RawMessage, config ToolConfig) (Result, error) {
    var a ReadFileArgs
    if err := json.Unmarshal(args, &a); err != nil {
        return Result{}, fmt.Errorf("read_file: invalid arguments: %w", err)
    }
    // ... validate and execute
}
```

This is a deliberate choice over a generic `map[string]any` approach — typed structs give compile-time safety, clear documentation of each tool's parameter surface, and natural integration with Go's `encoding/json`.

### 2.5 Integration with Phase 1's llm.ToolDef

The `Definition()` method on each tool returns an `llm.ToolDef` whose `Function.Parameters` field is a JSON Schema object. This is the bridge between the tool package and the llm package — the model sees the schema, calls the tool by name, and the turn loop dispatches through the registry using that same name.

No types from `internal/llm` need modification. The `ToolDef` type's `Parameters any` field (which stores the JSON Schema as a `map[string]any` or a struct that serializes to JSON Schema) is flexible enough to hold any tool's parameter schema.

---

## 3. Project-Root Path Scoping — `resolveScoped`

### 3.1 Implementation

Located in `internal/tools/resolve.go`. Shared by `ReadFileTool`, `EditFileTool`, `CreateFileTool`, `ListDirTool`, and `BashExecTool` (for its `cmd.Dir`, not for scoping arguments).

```go
// resolveScoped resolves a relative or absolute path against the project root
// and verifies it does not escape the root via ".." traversal or symlink escape.
//
// Resolution order:
// 1. Join path against root.
// 2. Resolve all symlinks in the joined path (filepath.EvalSymlinks).
// 3. Clean the resolved path.
// 4. Check the resolved path is a prefix of the cleaned root.
//
// Symlinks are resolved BEFORE the prefix check. This prevents a symlink inside
// the project root that points outside from bypassing scoping: even though the
// /root/link path passes the string prefix check, /root/link -> /etc/passwd resolves
// to /etc/passwd which fails the prefix check against /root.
//
// If the path does not exist yet (e.g. create_file), EvalSymlinks may fail with a
// path-not-found error. In that case, resolve symlinks on the longest existing
// prefix of the path, then re-join the non-existent remainder.
func resolveScoped(root, path string) (string, error) {
    joined := filepath.Join(root, path)

    // First, try full EvalSymlinks — works if path already exists.
    resolved, err := filepath.EvalSymlinks(joined)
    if err == nil {
        // Full path exists and symlinks are resolved.
        return checkPrefix(root, resolved)
    }

    // Path does not exist (e.g. create_file for a new file).
    // Walk up the path to find the longest existing prefix, resolve that,
    // then re-attach the non-existent tail.
    resolved, remainder, err := resolvePartial(joined)
    if err != nil {
        return "", fmt.Errorf("path %q cannot be resolved against root: %w", path, err)
    }

    abs := filepath.Join(resolved, remainder)
    return checkPrefix(root, abs)
}

// checkPrefix verifies abspath is within (or equal to) root, with symlinks resolved.
func checkPrefix(root, abspath string) (string, error) {
    absRoot, err := filepath.Abs(root)
    if err != nil {
        return "", fmt.Errorf("resolve root %q: %w", root, err)
    }
    absRoot = filepath.Clean(absRoot)

    if abspath == absRoot {
        return abspath, nil
    }

    // Ensure the resolved path starts with root + separator.
    prefix := absRoot + string(os.PathSeparator)
    if !strings.HasPrefix(abspath, prefix) {
        return "", &PathEscapeError{Path: abspath, Root: absRoot}
    }

    return abspath, nil
}

// resolvePartial walks up from path to find the longest existing ancestor,
// resolves symlinks on that ancestor, and returns the resolved ancestor plus
// the remaining non-existent path components.
func resolvePartial(path string) (resolved, remainder string, err error) {
    // filepath.Dir repeatedly until filepath.EvalSymlinks succeeds or we hit the root.
    candidate := path
    for {
        resolved, err := filepath.EvalSymlinks(candidate)
        if err == nil {
            // Found an existing ancestor. Return the resolved path plus whatever
            // remains of the original path after this ancestor.
            tail := strings.TrimPrefix(path, candidate)
            return resolved, tail, nil
        }
        parent := filepath.Dir(candidate)
        if parent == candidate {
            // We've reached the filesystem root without finding an existing ancestor.
            // Return the cleaned path and let checkPrefix reject it.
            return filepath.Clean(path), "", nil
        }
        candidate = parent
    }
}

// PathEscapeError is a distinguishable error type for path-scope violations.
// The model and the harness can both detect and report this specifically.
type PathEscapeError struct {
    Path string
    Root string
}

func (e *PathEscapeError) Error() string {
    return fmt.Sprintf("path %q escapes project root %q", e.Path, e.Root)
}
```

### 3.2 Edge cases explicitly handled

| Case | Behavior |
|------|----------|
| `..` traversal (`path = "../../etc/passwd"`) | `filepath.Join` normalizes, `checkPrefix` rejects because resolved path won't start with root prefix. |
| Symlink inside root pointing outside | Symlink is resolved via `filepath.EvalSymlinks` before prefix check → fails. This is the primary bypass vector this design blocks. |
| Path that doesn't exist yet (new file) | `resolvePartial` walks up the directory tree to find the existing ancestor, resolves symlinks on that, then re-joins the new filename. The parent directory must still be under root. |
| Empty path / path = `"."` | `filepath.Join(root, ".")` returns root → passes the `abs == absRoot` check in `checkPrefix`. |
| Absolute path given as argument | `filepath.Join` ignores earlier elements when a later element is absolute. However, the model should never generate absolute paths — the spec says all paths are project-relative. If one slips through, `filepath.Join` makes it absolute, and `checkPrefix` will verify it's within root. |
| Relative path starting with `/` | `/foo` on Linux is an absolute path. `filepath.Join` will discard `root` and use `/foo` alone. `checkPrefix` will reject it (not under project root). |

### 3.3 Known gap: symlink creation

If the model creates a symlink inside the project root pointing outside (via `bash_exec`), and then reads it via `read_file`, the symlink resolution in `resolvePartial` would detect the escape because the symlink's target would be resolved by `EvalSymlinks` and fail the prefix check. However, an edit to the symlink itself (re-pointing it) is not directly possible via the file tools — only `bash_exec` could create/modify symlinks, and `bash_exec` is already the known gap (§6.1 of spec). This is accepted for v1.

---

## 4. Per-Tool Argument/Result/Error Specification

### 4.1 `read_file(path)`

**Argument struct:**
```go
type ReadFileArgs struct {
    Path string `json:"path"`
}
```

**Result data:**
```go
type ReadFileResult struct {
    Path      string `json:"path"`
    Content   string `json:"content"`    // full file content
    LineCount int    `json:"line_count"` // len(strings.Split(content, "\n"))
    Truncated bool   `json:"truncated"`  // false in v1 (no cap)
}
```

**Error conditions:**
- Path escapes root → `*PathEscapeError`
- File does not exist → `os.ErrNotExist` (wrapped)
- File is a directory → distinguishable error (the builder should use `os.Stat` and check `IsDir` before reading)
- Permission denied → `os.ErrPermission` (wrapped)
- Tool not granted → not reachable (filtered at registration, not at call time)

### 4.2 `edit_file(path, old_str, new_str)`

**Argument struct:**
```go
type EditFileArgs struct {
    Path   string `json:"path"`
    OldStr string `json:"old_str"`  // exact string to find
    NewStr string `json:"new_str"`  // replacement string
}
```

**Result data:**
```go
type EditFileResult struct {
    Path         string `json:"path"`
    MatchesFound int    `json:"matches_found"`  // exactly 1 on success
}
```

**Critical error conditions:**

| Error | Distinguishable type | Explanation |
|-------|---------------------|-------------|
| Zero matches | `ErrNoMatch` | `old_str` not found anywhere in the file. The model must provide more/better surrounding context. |
| Multiple matches | `ErrAmbiguousMatch` | `old_str` found N>1 times. The model must provide more surrounding context to disambiguate. Never silently replaces all occurrences. |
| Path escapes root | `*PathEscapeError` | Standard scoping enforcement. |
| File does not exist | wrapped `os.ErrNotExist` | No file to edit. |

**Error types:**
```go
// ErrNoMatch is returned when edit_file's old_str is not found in the file.
// The model must retry with a more specific old_str (more surrounding context).
type ErrNoMatch struct {
    Path string
}

func (e *ErrNoMatch) Error() string {
    return fmt.Sprintf("edit_file: zero matches for old_str in %q — provide more surrounding context", e.Path)
}

// ErrAmbiguousMatch is returned when edit_file's old_str matches more than once.
// The model must retry with more surrounding context to disambiguate.
type ErrAmbiguousMatch struct {
    Path           string
    MatchesFound   int
}

func (e *ErrAmbiguousMatch) Error() string {
    return fmt.Sprintf("edit_file: old_str matches %d times in %q — provide more surrounding context", e.MatchesFound, e.Path)
}
```

**Implementation notes:**
- Read the entire file into memory.
- Use `strings.Count(content, oldStr)` for the count check (fast, avoids allocation).
- If count == 0 → return `ErrNoMatch`.
- If count > 1 → return `ErrAmbiguousMatch`.
- If count == 1 → `strings.Replace(content, oldStr, newStr, 1)` and write back.
- The `new_str` may itself contain `old_str` as a substring — this is fine because we do a single replacement only.
- After writing, compute new SHA-256 hash for the file's content-hash tracking (Phase 3's loop detection feeds on this, but the hash computation itself lives here since the tool is writing the file).
- File must be writeable (not read-only). Permission errors propagate as wrapped `os.ErrPermission`.

### 4.3 `create_file(path, content)`

**Argument struct:**
```go
type CreateFileArgs struct {
    Path    string `json:"path"`
    Content string `json:"content"`
}
```

**Result data:**
```go
type CreateFileResult struct {
    Path string `json:"path"`
}
```

**Error conditions:**

| Error | Explanation |
|-------|-------------|
| Path already exists | Distinct from `edit_file` usage: the tool must stat the path first; if it exists, fail with `os.ErrExist` (wrapped). This forces explicit intent — no silent overwrite. |
| Path escapes root | `*PathEscapeError` |
| Parent directory does not exist | The tool does NOT create parent directories. If `path = "a/b/c.txt"` and `a/` exists but `a/b/` does not, the tool fails. This is intentional: the model should use `bash_exec mkdir -p` if it needs to create intermediate directories. |
| Permission denied | wrapped `os.ErrPermission` |

**Implementation notes:**
- Use `os.Stat` first → if no error, path exists → return wrapped `os.ErrExist`.
- Use `resolveScoped` (with `resolvePartial` since the file doesn't exist yet).
- Write via `os.WriteFile` with `os.O_CREATE|os.O_EXCL|os.O_WRONLY` flags for atomic existence check and write.
- Compute SHA-256 hash of written content for file tracking.

### 4.4 `bash_exec(command, timeout_seconds)`

**Argument struct:**
```go
type BashExecArgs struct {
    Command        string  `json:"command"`
    TimeoutSeconds float64 `json:"timeout_seconds"` // float for fractional seconds support
}
```

**Result data:**
```go
type BashExecResult struct {
    Stdout   string `json:"stdout"`
    Stderr   string `json:"stderr"`
    ExitCode int    `json:"exit_code"`
    TimedOut bool   `json:"timed_out"` // true if killed by timeout
}
```

**Error conditions:**

| Error | Explanation |
|-------|-------------|
| Context deadline exceeded | Return result with `TimedOut: true`, partial stdout/stderr, exit_code = -1. This is NOT a Go error — it's a valid result with a timeout signal. |
| Shell not found | `exec.LookPath("sh")` fails — this is a deployment error, returned as a Go error. |
| Command start failure | `cmd.Start()` fails (e.g. binary not found inside the command string) — returned as a Go error with details. |

**Implementation:**

```go
func (t *BashExecTool) Execute(ctx context.Context, args json.RawMessage, config ToolConfig) (Result, error) {
    var a BashExecArgs
    if err := json.Unmarshal(args, &a); err != nil {
        return Result{}, fmt.Errorf("bash_exec: invalid arguments: %w", err)
    }

    // Create a sub-context with the hard timeout.
    timeout := time.Duration(a.TimeoutSeconds * float64(time.Second))
    execCtx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()

    cmd := exec.CommandContext(execCtx, "sh", "-c", a.Command)
    cmd.Dir = t.root  // Project root — this is the scoping point
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    err := cmd.Run()

    result := BashExecResult{
        Stdout: stdout.String(),
        Stderr: stderr.String(),
    }

    if err != nil {
        if execCtx.Err() == context.DeadlineExceeded {
            result.TimedOut = true
            result.ExitCode = -1
        } else if exitErr, ok := err.(*exec.ExitError); ok {
            result.ExitCode = exitErr.ExitCode()
        } else {
            // Command failed to start (binary not found, etc.)
            return Result{}, fmt.Errorf("bash_exec: %w", err)
        }
    } else {
        result.ExitCode = 0
    }

    return Result{Data: result, HumanText: formatBashResult(result)}, nil
}
```

**Known gap (documented, accepted):** `cmd.Dir` sets the working directory, but a `cd` or path redirection inside the shell command bypasses this. This is the spec's explicitly accepted gap (§6.1). No attempt is made to prevent it in v1. If airtight isolation is needed, the answer is Docker/firejail — not a custom sandbox built into this package.

**Output cap:** None in v1 per spec §5.1. The builder should add a cap (50KB) marked with `Truncated: true` if observed p95 output warrants it, but should not do so speculatively.

### 4.5 `list_dir(path)`

**Argument struct:**
```go
type ListDirArgs struct {
    Path string `json:"path"`
}
```

**Result data:**
```go
type ListDirResult struct {
    Path    string   `json:"path"`
    Entries []string `json:"entries"` // file/directory names only (no full paths), sorted
    IsEmpty bool     `json:"is_empty"`
}

// Each entry is just the base name. The model can construct full paths by joining.
// This is a deliberate design choice: it keeps the result compact and avoids leaking the
// absolute project root path to the model.
```

**Error conditions:**
- Path escapes root → `*PathEscapeError`
- Path does not exist → wrapped `os.ErrNotExist`
- Path is a file, not a directory → distinguishable error (`os.ReadDir` returns `*os.PathError` with a clear message)
- Permission denied → wrapped `os.ErrPermission`

**Implementation notes:**
- Uses `os.ReadDir` (Go 1.16+) which returns `os.DirEntry` values.
- Entries are the base name only (not full paths).
- Entries are sorted alphabetically (`slices.Sort`).
- Hidden files (dotfiles) ARE included — the model needs to see `.gitignore`, `.env.example`, etc.

### 4.6 `write_log(content)`

**Argument struct:**
```go
type WriteLogArgs struct {
    Content string `json:"content"`
}
```

**Result data:**
```go
type WriteLogResult struct {
    Path string `json:"path"`
    Size int    `json:"size_bytes"`
}
```

**Error conditions:**
- Only structural errors (disk full, permission denied on the fixed log path).
- NEVER path-scoping errors — the tool does not accept a path argument; the path is fixed.

**Critical design constraint:** `WriteLogTool` is instantiated with a fixed `logPath` at construction time. There is no path field in the arguments at all. This is structurally incapable of writing anywhere else.

**Implementation:**
```go
type WriteLogTool struct {
    logPath string // set at construction; e.g. "/home/user/project/phase-2.log"
}

func (t *WriteLogTool) Execute(ctx context.Context, args json.RawMessage, config ToolConfig) (Result, error) {
    var a WriteLogArgs
    if err := json.Unmarshal(args, &a); err != nil {
        return Result{}, fmt.Errorf("write_log: invalid arguments: %w", err)
    }

    // Append-only: open with O_APPEND | O_CREATE | O_WRONLY.
    // Phase-N.log accumulates entries across all agents in a phase.
    f, err := os.OpenFile(t.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        return Result{}, fmt.Errorf("write_log: %w", err)
    }
    defer f.Close()

    n, err := f.WriteString(a.Content)
    if err != nil {
        return Result{}, fmt.Errorf("write_log: %w", err)
    }

    return Result{
        Data: WriteLogResult{Path: t.logPath, Size: n},
    }, nil
}
```

**Why `*ToolConfig` is accepted but ignored for write_log:** The interface requires a `ToolConfig` parameter for uniform dispatch, but `write_log` bypasses glob scoping since it is granted unconditionally (§15 of spec). The parameter is present for interface uniformity but the tool ignores it.

---

## 5. Per-Agent Per-Tool Glob Scoping (§6.2)

### 5.1 Config types (designed now, parsed in Phase 4)

```go
// ToolRestrictions define per-tool access control for an agent.
// Full config parsing is Phase 4's job. This struct is the runtime representation
// that the Phase 4 parser produces, and that Phase 2's enforcement mechanism consumes.
type ToolRestrictions struct {
    // PathGlobs restricts the tool to matching paths only.
    // If nil/empty AND the tool is present in the agent's tool list, all paths
    // under the project root are allowed.
    // If non-empty, at least one glob must match the resolved path.
    //
    // Only meaningful for file-touching tools (read_file, edit_file, create_file,
    // list_dir). For bash_exec and write_log, this field is ignored.
    PathGlobs []string `json:"paths,omitempty"`
}

// AgentToolConfig maps tool names to their restrictions.
// A tool name absent from this map means the agent does NOT have that tool.
// A tool name present with zero-value ToolRestrictions means the agent has
// the tool with no path restrictions (subject only to project-root scoping).
type AgentToolConfig map[string]ToolRestrictions

// Has checks whether a tool is granted to this agent at all.
func (c AgentToolConfig) Has(toolName string) bool {
    _, ok := c[toolName]
    return ok
}

// AllowedPath checks whether a path is permitted for a given tool.
// Returns true if the tool has no path restrictions, or if at least one glob matches.
func (c AgentToolConfig) AllowedPath(toolName, resolvedPath string) bool {
    tr, ok := c[toolName]
    if !ok {
        return false // tool not granted at all
    }
    if len(tr.PathGlobs) == 0 {
        return true // no path restrictions
    }
    for _, glob := range tr.PathGlobs {
        if matched, _ := filepath.Match(glob, resolvedPath); matched {
            return true
        }
        // Also match against project-relative path for convenience — globs in config
        // are written as "docs/adr-*.md" (relative), but resolvedPath is absolute.
        // We check the relative path as well.
    }
    return false
}
```

### 5.2 Where glob matching runs

In each file-touching tool's `Execute` method, AFTER `resolveScoped` succeeds and BEFORE the actual file operation:

```go
func (t *ReadFileTool) Execute(ctx context.Context, args json.RawMessage, config ToolConfig) (Result, error) {
    var a ReadFileArgs
    if err := json.Unmarshal(args, &a); err != nil {
        return Result{}, fmt.Errorf("read_file: invalid arguments: %w", err)
    }

    // Step 1: Resolve path against project root.
    resolvedPath, err := resolveScoped(t.root, a.Path)
    if err != nil {
        return Result{}, err // PathEscapeError propagates as-is
    }

    // Step 2: Check per-agent glob restrictions.
    if err := config.AllowPath(resolvedPath); err != nil {
        return Result{}, err // DisallowedPathError
    }

    // Step 3: Read file.
    // ...
}
```

### 5.3 `ToolConfig` — the per-call restriction bag

```go
// ToolConfig is passed to every tool.Execute call.
// It carries the per-agent restrictions for THIS tool at THIS call site.
type ToolConfig struct {
    // AllowedPaths, if non-empty, lists the glob patterns that paths must match.
    // If empty, all paths under project root are allowed (for this tool).
    AllowedPaths []string

    // ProjectRoot is always set. Used by tools for path resolution.
    ProjectRoot string
}

// AllowPath checks if the resolved absolute path is permitted.
func (c ToolConfig) AllowPath(resolvedPath string) error {
    if len(c.AllowedPaths) == 0 {
        return nil // no restrictions
    }

    // Compute project-relative path for glob matching.
    // Globs in config are written as relative paths (e.g. "docs/adr-*.md").
    rel, err := filepath.Rel(c.ProjectRoot, resolvedPath)
    if err != nil {
        return fmt.Errorf("path scoping: cannot compute relative path: %w", err)
    }

    for _, glob := range c.AllowedPaths {
        // If the glob is absolute, match against resolvedPath directly.
        // Otherwise, match against the relative path.
        if filepath.IsAbs(glob) {
            if matched, _ := filepath.Match(glob, resolvedPath); matched {
                return nil
            }
        } else {
            if matched, _ := filepath.Match(glob, rel); matched {
                return nil
            }
        }
    }

    return &DisallowedPathError{Path: resolvedPath, Globs: c.AllowedPaths}
}

// DisallowedPathError is a distinguishable error for path-scope violations
// caused by per-agent glob restrictions (as opposed to project-root escape).
type DisallowedPathError struct {
    Path  string
    Globs []string
}

func (e *DisallowedPathError) Error() string {
    return fmt.Sprintf("path %q is not allowed by per-agent glob restrictions", e.Path)
}
```

### 5.4 Forensic's restricted tool set — natural consequence

Forensic's config (to be parsed in Phase 4) would look like:
```yaml
tools:
  read_file: {}
  list_dir: {}
  write_log: {}   # universal, but still listed explicitly for symmetry
  # edit_file: null — absent, so not granted
  # create_file: null — absent, so not granted
  # bash_exec: null — absent, so not granted
```

When `NewDefaultRegistry` is filtered via `ForAgent`, only `read_file`, `list_dir`, and `write_log` survive. No special-case code in the tool dispatch is needed.

### 5.5 Enforcement at the Registry/Filter level

```go
// FilterByAgentConfig returns a new Registry containing only the tools the agent is granted.
func (r Registry) FilterByAgentConfig(config AgentToolConfig) Registry {
    filtered := make(Registry)
    for name, tool := range r {
        if _, ok := config[name]; ok {
            filtered[name] = tool
        }
    }
    return filtered
}

// For the model-facing side, produce ToolDefs from the filtered registry:
func (r Registry) Definitions() []llm.ToolDef {
    defs := make([]llm.ToolDef, 0, len(r))
    // Sort by name for deterministic ordering.
    names := make([]string, 0, len(r))
    for n := range r {
        names = append(names, n)
    }
    sort.Strings(names)
    for _, n := range names {
        defs = append(defs, r[n].Definition())
    }
    return defs
}
```

---

## 6. Hash Computation for File Tracking

### 6.1 Location

SHA-256 computation lives in the file-writing tools (`edit_file`, `create_file`) and is called after a successful write. The hash is returned in the result data so the Phase 3 turn loop can pass it to `store.UpsertFile`.

### 6.2 Helper

```go
// fileHash computes the SHA-256 hash of the given content, hex-encoded.
// Placed in resolve.go since it's a small utility used across multiple tools.
func fileHash(content string) string {
    h := sha256.Sum256([]byte(content))
    return hex.EncodeToString(h[:])
}
```

---

## 7. Testing Strategy — Phase 2 Builder

The builder's test suite should cover:

### 7.1 `resolveScoped` tests (unit, no tools involved)

| Test | What it verifies |
|------|------------------|
| Normal relative path | `resolveScoped("/root", "a/b/c")` → `/root/a/b/c` |
| Root path itself | `resolveScoped("/root", ".")` → `/root` |
| Root path itself (empty) | `resolveScoped("/root", "")` → `/root` |
| Escaping via `..` | `resolveScoped("/root", "../../etc/passwd")` → error (`PathEscapeError`) |
| Absolute path argument | `resolveScoped("/root", "/etc/passwd")` → error (absolute path discards root) |
| Symlink inside root to outside | Create temp dir with symlink pointing outside, verify block |
| Non-existent file (create path) | `resolveScoped("/root", "newdir/newfile.go")` on empty root → may fail if parent doesn't exist |
| Non-existent file with existing parent | `resolveScoped("/root/existing/", "newfile.go")` → `/root/existing/newfile.go` |

### 7.2 Per-tool tests

#### read_file
- Normal file: content + line count returned correctly.
- Non-existent file: error.
- Directory: distinguishable error.
- Path escaping root: `PathEscapeError`.
- Glob-restricted path blocked: `DisallowedPathError`.

#### edit_file
- Normal replacement: file content changed, result has `MatchesFound=1`.
- Zero matches: `ErrNoMatch`.
- Multiple matches: `ErrAmbiguousMatch`.
- File does not exist: error.
- No restrictions (allow all): edit succeeds.
- Path outside glob: `DisallowedPathError`.
- `new_str` contains `old_str`: single replacement works correctly (not infinite loop).

#### create_file
- New file: created with correct content.
- File already exists: wrapped `os.ErrExist`.
- Parent directory missing: error, no implicit directory creation.
- Path escaping root: `PathEscapeError`.
- Glob-restricted path: `DisallowedPathError`.

#### list_dir
- Normal directory: entries returned, sorted, base names only.
- Empty directory: `IsEmpty: true`, entries length 0.
- Non-existent path: error.
- File (not directory): error.
- Path escaping root: `PathEscapeError`.
- Hidden files included: `.gitignore`, `.env` appear in entries.

#### bash_exec
- Simple command: stdout captured, exit code 0.
- Stderr output: stderr captured separately.
- Non-zero exit code: exit code reported, no Go error.
- Timeout: command killed, `TimedOut: true`, partial output captured.
- Command start failure (binary not found): Go error.
- `cmd.Dir` is set to project root: verify by running `pwd` and checking output.

#### write_log
- Appends content to the fixed log path.
- Multiple calls append sequentially.
- Path is fixed — no path argument accepted.

### 7.3 Glob scoping tests

| Test | What it verifies |
|------|------------------|
| Agent with no path restrictions: tool works | `ToolConfig{AllowedPaths: nil}` allows any path |
| Agent with matching glob: tool works | `ToolConfig{AllowedPaths: ["docs/adr-*.md"]}` allows `docs/adr-phase-2-tools-safety.md` |
| Agent with non-matching glob: blocked | Same config blocks `internal/foo.go` |
| Agent without tool: not in filtered registry | `AgentToolConfig` without "create_file" → `FilterByAgentConfig` excludes it |
| Forensic config: only read_file, list_dir, write_log survive | Only those three tools in filtered registry |

### 7.4 Concurrency safety

Multi-threaded access to file tools is not a v1 concern (serial turn loop, parallel tool calls deferred). The builder should verify that individual tool implementations do not use shared state (they don't — each tool struct has no mutable fields), but no formal data-race test is needed.

### 7.5 Test pattern

Since Phase 2 tests call tools directly (no model, no turn loop), the test pattern is:
```go
func TestReadFile_Success(t *testing.T) {
    // Create a temp directory as our project root.
    tmpDir := t.TempDir()
    os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("hello\nworld"), 0644)

    tool := &ReadFileTool{root: tmpDir}
    args, _ := json.Marshal(ReadFileArgs{Path: "test.txt"})
    config := ToolConfig{ProjectRoot: tmpDir, AllowedPaths: nil}

    result, err := tool.Execute(context.Background(), args, config)
    assert.NoError(t, err)
    assert.Equal(t, "hello\nworld", result.Data.(ReadFileResult).Content)
}
```

---

## 8. Open Decisions / Items for Builder to Flag

1. **`filepath.Match` vs `doublestar` for glob patterns:** `filepath.Match` does NOT support `**` (double-star) recursion. The spec's example `docs/adr-*.md` works fine with `filepath.Match`, but if Phase 4's config format wants `**/*.md` patterns, the builder must either add a third-party glob library (e.g. `github.com/bmatcuk/doublestar`) or implement double-star matching. **Recommendation:** Start with `filepath.Match` for v1; only add double-star if actually needed. The spec examples only use single `*`.

2. **Relative-path glob matching:** The builder must decide whether globs in config are always project-relative or whether absolute-path globs are also supported. This ADR assumes project-relative with a fallback to absolute-path matching in `ToolConfig.AllowPath`. If this causes confusion in Phase 3's turn loop, simplify to project-relative only.

3. **Line numbering in read_file output:** The spec says "Full content + line numbers" but doesn't specify format. Should each line be prefixed with `N: ` like standard debug output? That's the most useful format for the model. The builder should implement `N: content` prefix per line, with `LineCount` in the result struct.

4. **bash_exec timeout precision:** `float64` for `timeout_seconds` allows sub-second precision (e.g. 0.5 for 500ms). If the model endpoints always pass integer seconds, the builder may simplify to `int`. Keep `float64` for flexibility unless it creates parsing issues with the model.

5. **write_log entry format:** The Phase-N.log format from Phase 1 uses `=== AgentType entry (date) ===` headers. This formatting is the model's responsibility (it writes the content via `write_log`). The tool does not add formatting. Verify this understanding is consistent with the Phase 1 log format used in `phase-1.log`.

6. **Content hash in edit_file/create_file results:** Should the hash be included in the result data, or should the turn loop compute it separately? **Recommendation:** Include it in the result. The turn loop (Phase 3) can then pass it directly to `store.UpsertFile` without re-reading the file. The extra cost of hashing at write time is negligible.

7. **SHA-256 vs faster hash:** If many consecutive edit_file calls on large files cause measurable slowdown, the builder may switch to a faster non-cryptographic hash (e.g. `xxhash`). Flag this if it becomes a problem — do not optimize preemptively.

8. **read_file size limit:** Spec §5.1 says no cap in v1. If a file is hundreds of megabytes (e.g. a compiled binary), `read_file` would OOM. The builder should add a large-but-not-infinite cap (e.g. 10MB) with a `Truncated: true` flag, returning the first 10MB. Flag this decision.

9. **List_dir recursion depth:** Spec says "shallow listing" — the tool returns direct children only, not recursive. Clarify in the tool's result that this is intentional and the model should use `bash_exec find` for deep listings.

---

## 9. Constraints and Risks

1. **bash_exec is the uncontained escape hatch.** A model with `bash_exec` granted can read/write anywhere the OS user can. The only mitigations in v1 are: (a) forensic agent does not have bash_exec at all, and (b) the harness controls which agents get it. No attempt is made to sandbox `sh -c` commands.

2. **Symlink resolution adds a filesystem round-trip.** Every file tool must stat the path (via `EvalSymlinks` or `resolvePartial`), adding latency. This is negligible for local filesystems. For NFS or FUSE mounts, the builder should flag if the overhead is noticeable.

3. **No atomicity across tools.** `edit_file` followed by `create_file` is two separate filesystem operations with no transaction. If the harness crashes between them, the filesystem may be in an intermediate state. This is acceptable for v1 — the harness is a personal tool, not a production database.

4. **write_log append is not safe under concurrent agents.** Phase 3 uses serial agents (one at a time), so log-line interleaving between concurrent writes is not possible. If agents ever run in parallel (v2), `write_log` would need locking.

5. **`os.O_EXCL` for create_file is racy on NFS.** On local filesystems it's atomic. This is acceptable for v1 targeting local development only.

---

## 10. ADR Revision Log

| Version | Date | Change |
|---------|------|--------|
| 1.0 | 2026-07-05 | Initial ADR for Phase 2 tool execution and safety design. |

---

## 11. Cross-Reference: How Phase 2 Connects to Phase 3

The turn loop (Phase 3) will:
1. Call `NewDefaultRegistry(root, logPath)` to create the six tools.
2. Call `registry.FilterByAgentConfig(agentConfig)` with the parsed per-agent restrictions.
3. Call `registry.Definitions()` to get the `[]llm.ToolDef` sent to the model.
4. On receiving a `llm.ToolCall`, look up `toolName` in the filtered registry.
5. Build `ToolConfig` with `ProjectRoot` and `AllowedPaths` from the agent's restrictions.
6. Call `tool.Execute(ctx, toolCall.Function.Arguments, toolConfig)`.
7. Serialize the result back into a `llm.Message` to send back to the model.
8. Log the event via `store.InsertEvent`.

The tool package implements #1-6. The turn loop implements #7-8. The boundary is clean: the turn loop imports `internal/tools` and `internal/llm`, never the reverse.
