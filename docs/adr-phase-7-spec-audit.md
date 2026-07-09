# ADR 007: Automated Spec Auditing

## Status
Accepted — Fix Cycle 1 implements the three non-functional CLI stubs.

## Context
Manual phase auditing using `forensic-per-phase-audit-template.md` is error-prone and doesn't scale.

## Decision
1. Formalize `--audit-phase N` for per-phase checks.
2. Formalize `--audit-full` for cross-phase consistency checks.
3. Both outputs MUST be structured markdown for automation.

## Consequences
- Automated audit runs.
- Standardized forensic reporting.
- Prompt-generator agent becomes a real turn-loop invocation instead of a stub.

---

## Fix Cycle 1 — Wire Three Stubs Into the Common Turn-Loop Path

### Problem
Three CLI entry points in `cmd/harness/main.go` are non-functional stubs:
1. `--audit-phase N` (lines 50–53): prints phase number and `os.Exit(0)`.
2. `--audit-full` (lines 54–57): prints message and `os.Exit(0)`.
3. `--agent prompt-generator --spec path` (lines 60–68): prints spec path and `os.Exit(0)`.

All three exit before reaching the common execution path (agent config parsing, store open, LLM client creation, tool registry build, turn loop run). The underlying packages (`loop`, `tools`, `store`, `config`, `llm`) are already correct and tested — this is purely a wiring problem in `main.go`.

### Root cause
Phase 7's builder step never ran. The builder briefing (`builder-briefing-phase-7.md`) specified exactly what to implement (CLI modes that load forensic agent configs and audit templates and run a real turn loop), but it was never executed. The `--audit-phase` and `--audit-full` stubs were left as print-and-exit placeholders. The prompt-generator stub was similarly left incomplete — its backend logic (`config.WriteManifest`, etc.) is implemented and unit-tested, but the CLI entry point never dispatches into it.

### Chosen approach: remove early exits, route special modes through the common path

The three special modes share >90% of their execution path with normal agent runs. The fix removes their `os.Exit(0)` calls and instead customizes three variables (agent config name, user prompt, tool restrictions) before falling through to the existing common code path.

**No changes to `internal/loop/`, `internal/tools/`, `internal/store/`, `internal/config/`, or `internal/llm/` are required.** All core packages are already correct.

---

### Item 1: `--audit-phase N` — per-phase forensic audit

#### Behaviour
Run a `forensic`-mode turn loop against the project state for phase N. The user prompt is the `forensic-per-phase-audit-template.md` with `{N}` replaced by the phase number.

#### What to wire in `main.go`

```
if *auditPhase != "" {
    loadAgentName  = "forensic"                    // load .aa/agents/forensic.md
    logFileName    = "phase-" + *auditPhase + ".log"
    auditToolsOnly = true                          // signal read-only tool restriction
    // Build user prompt from template.
    templatePath := filepath.Join(projectRoot, ".aa", "forensic-per-phase-audit-template.md")
    tplBytes, err := os.ReadFile(templatePath)
    if err != nil { log.Fatalf ... }
    prompt = strings.ReplaceAll(string(tplBytes), "{N}", *auditPhase)
}
```

After `config.ParseAgentConfig(loadAgentName)`:
```
if auditToolsOnly {
    cfg.Tools = tools.AgentToolConfig{
        "read_file": {},
        "list_dir":  {},
        "write_log": {},
    }
    cfg.Name = "audit-phase-" + *auditPhase   // for session DB tracking
}
```

#### User prompt derivation
1. Read `.aa/forensic-per-phase-audit-template.md` from disk (already present in every bootstrapped project).
2. Replace every occurrence of `{N}` with the flag value. The `{scope}` placeholder is left as-is — the forensic agent has `list_dir` and can discover the actual ADR filename from the `docs/` directory. No second flag or scope-lookup logic is needed.
3. Use the filled string as `cfg.UserPrompt`.

The template instructs the agent to:
- Read all kickoff briefings for Phase N (they're discoverable via `list_dir` on `.aa/`)
- Read the Phase N ADR
- Read `phase-N.log`
- Inspect the actual code
- Produce a pass/fail verdict per item with structured markdown
- Append a `phase-N.log` entry via `write_log`

#### Tool restrictions
Override the forensic config's tool set to exactly:
- `read_file: {}` — unrestricted read
- `list_dir: {}` — unrestricted directory listing
- `write_log: {}` — append to phase log

No `edit_file`, no `create_file`, no `bash_exec`. This is stricter than the existing `.aa/agents/forensic.md` (which grants `bash_exec` and limited `edit_file`) and matches the spec §6.2 definition of forensic's intended capability set. The override is applied inline in `main.go` after `ParseAgentConfig` returns, not by modifying the config file.

#### Log file
`write_log` writes to `phase-N.log` (not `forensic.log`), matching the project convention that each phase's log is at `phase-N.log`. The log path is passed through the tool registry constructor.

#### Pass-through to common path
After customization, the code falls through to the existing common path:
1. Skills manifest appending
2. Store open
3. LLM client creation
4. `tools.NewDefaultRegistry(projectRoot, logFileName)` — note `logFileName` not `*agentName`
5. `reg.FilterByAgentConfig(cfg.Tools)` — the read-only restriction is enforced here
6. `loop.New(...) + Run()`
7. Git commit (if `--phase` was also specified — unusual for audit but harmless)

---

### Item 2: `--audit-full` — full cross-phase audit

#### Behaviour
Run a `forensic`-mode turn loop whose user prompt is the verbatim content of `.aa/forensic-full-audit.md`. Tool restrictions are identical to the per-phase audit (read-only + write_log).

#### What to wire in `main.go`

```
if *auditFull {
    loadAgentName  = "forensic"
    logFileName    = "full-audit.log"
    auditToolsOnly = true
    // Build user prompt from template.
    templatePath := filepath.Join(projectRoot, ".aa", "forensic-full-audit.md")
    tplBytes, err := os.ReadFile(templatePath)
    if err != nil { log.Fatalf ... }
    prompt = string(tplBytes)
}
```

The `auditToolsOnly` branch in Item 1's post-parse code already applies the same read-only restriction when the flag is set.

#### User prompt derivation
The `.aa/forensic-full-audit.md` file is used verbatim — no placeholder substitution. It already contains the full audit briefing for the cross-phase check, including specific instructions about what to check (carried-forward items, Phase 6's known audit failure mode, non-goals boundary, dogfooding status, git branching reality).

#### Log file
`full-audit.log` — not `phase-N.log`, since the full audit is not specific to any phase number. The template text mentions `phase-7.log` in its deliverable section (it was authored for this project), but the actual `write_log` path is `full-audit.log`; the forensic agent does not control the path.

---

### Item 3: `--agent prompt-generator --spec path`

#### Behaviour
Run the prompt-generator agent's turn loop against the given spec file. The system prompt from `.aa/agents/prompt-generator.md` already contains the full instruction set for manifest + briefing generation. The user prompt simply references the spec file path, consistent with this project's established pattern (phase briefings reference paths, not embedded content).

#### What to wire in `main.go`

```
if *agentName == "prompt-generator" {
    if *specFlag == "" {
        log.Fatalf("prompt-generator requires --spec")
    }
    // Resolve the spec path so the agent gets an absolute reference.
    specAbs, err := filepath.Abs(*specFlag)
    if err != nil { log.Fatalf ... }
    prompt = fmt.Sprintf("Read the project specification at `%s` and "+
        "generate the required artifacts: manifest, kickoff briefings, "+
        "and loop templates.", specAbs)
    loadAgentName = "prompt-generator"
    // logFileName stays as "prompt-generator.log" (default)
    // auditToolsOnly remains false — use prompt-generator's own tool config
}
```

The existing `ParseAgentConfig("prompt-generator")` call loads `.aa/agents/prompt-generator.md`, which grants `read_file`, `create_file` (restricted to `.aa/templates/*.yaml` and `.aa/agents/*.md`), `list_dir`, and `write_log`. These restrictions are already correct per ADR-Phase-6 Fix Cycle 1. No tool override is needed.

The agent reads the spec file itself via `read_file` (the path is in its user prompt), determines phase structure, writes the manifest via `create_file`, and writes briefings via `create_file`. All within its permitted path globs.

#### Why not embed spec content in the user prompt
This matches the pattern established in §12.1: kickoff prompts reference paths, not embedded content. The agent has `read_file` and can fetch the spec itself. This keeps the user prompt bounded and avoids duplicating potentially large spec content into the prompt text. The path reference is absolute to avoid ambiguity regardless of the agent's working directory.

---

### Interaction between `--audit-phase` and `--audit-full` (mutual exclusion)

Both flags could theoretically be set simultaneously, though it doesn't make sense. The wiring should check: if both `--audit-phase` and `--audit-full` are set, treat the combination as a fatal error. If either is set alongside a non-forensic `--agent` value, also fatal.

---

### Required file changes — summary

| File | Change | Risk |
|------|--------|------|
| `cmd/harness/main.go` | Remove early exits (lines 50–68). Add routing logic before the `ParseAgentConfig` call to set `loadAgentName`, `prompt`, `logFileName`, `auditToolsOnly`. After `ParseAgentConfig`, apply read-only tool override if `auditToolsOnly`. Pass `logFileName` to `tools.NewDefaultRegistry` instead of `*agentName+".log"`. | Low — purely additive control flow, no existing path is modified |
| `e2e/audit_e2e_test.go` | The existing test calls `--audit-phase 6` and `--audit-full` expecting exit 0. With real turn loops, these will attempt LLM API calls and fail without credentials. Two options: (a) skip the test via `testing.Short()` or env-var guard, noting it as an integration test, or (b) set up a mock LLM endpoint. Both are valid; choice deferred to builder. | Low — test-only |

No changes to `internal/loop/`, `internal/tools/`, `internal/store/`, `internal/config/`, `internal/llm/`, or any `.aa/` template/config files.

---

### Verification

After implementation:
1. `go build ./cmd/harness/` compiles cleanly
2. `./harness --audit-phase 3` runs a real forensic turn loop (requires network + API key). The agent reads `.aa/forensic-per-phase-audit-template.md`, replaces `{N}` with 3, and follows the audit instructions. The `write_log` tool writes to `phase-3.log`.
3. `./harness --audit-full` runs a real forensic turn loop with the full audit template. `write_log` writes to `full-audit.log`.
4. `./harness --agent prompt-generator --spec agent-harness_spec.md` runs the prompt-generator agent. It reads the spec, writes `manifest.yaml` to `.aa/templates/`, and writes briefing files to `.aa/agents/`.
5. `./harness --audit-phase 3 --audit-full` exits with a fatal error (mutual exclusion).
6. `./harness --audit-phase 3 --agent builder` exits with a fatal error (conflict).
7. `go test ./e2e/ -run TestAuditCapability -short` skips if short mode; otherwise requires API credentials.
