# Phase 2 — Tool Execution & Safety — Architect Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-1-foundation.md` and `phase-1.log` in full — this phase builds directly on Phase 1's `internal/store` and `internal/llm` packages, which are done and merged. Do not redesign anything in those packages; treat them as fixed.

## Your task this phase
Design the tool-execution layer: the six fixed tools (§5), project-root path scoping (§6.1), and per-agent per-tool glob-based access control (§6.2). This is the layer that turns the harness's safety philosophy — "hard harness enforcement, never trust the model" — into actual enforced Go code.

## What to design

1. **Tool interface shape.** Decide how a tool is represented in code — likely something like a `Tool` interface or a registry of functions with a consistent signature (`func(ctx, args) (result, error)`), how tool definitions get converted into the `ToolDef`/`FunctionDefinition` JSON-schema shape the `LLM` interface already expects (from Phase 1's `internal/llm` package — reuse those types, don't redefine them), and how a tool call's arguments (JSON string from the model) get unmarshaled into typed Go structs per tool.

2. **The six tools** (§5): `read_file`, `edit_file`, `create_file`, `bash_exec`, `list_dir`, `write_log`. For each, specify the exact argument shape, return/result shape, and error conditions. Pay particular attention to:
   - `edit_file`: exact-string-match replace; must fail loud (specific, distinguishable error) on zero matches AND on multiple matches — per spec, ambiguous matches are never silently resolved by replacing all occurrences.
   - `create_file`: must fail if the path already exists.
   - `bash_exec`: needs `cmd.Dir` set to project root, a hard timeout via `context.WithTimeout`, and captured stdout/stderr/exit code. Flag explicitly in your ADR that this tool cannot be fully path-scoped the same way file tools can (a `cd` inside the command bypasses root-scoping) — this is a known, accepted gap per spec §6.1, not something to solve in this phase.
   - `write_log`: writes only to a fixed, predetermined `phase-N.log` path for the current phase step — never an arbitrary path. This must be structurally incapable of writing anywhere else, since it's granted unconditionally to every agent regardless of their other restrictions (§15).

3. **Project-root path scoping** (§6.1). The spec already gives a reference implementation:
   ```go
   func resolveScoped(root, path string) (string, error) {
       abs, err := filepath.Abs(filepath.Join(root, path))
       ...
   }
   ```
   Decide where this lives (a shared helper all file tools call through), and confirm it handles edge cases: `..` traversal, symlinks (do you resolve symlinks before the prefix check, or after? — a symlink inside the project root pointing outside it is a real bypass vector worth deciding on explicitly), and empty/`.`-relative paths.

4. **Per-agent, per-tool glob scoping** (§6.2). Design how an agent's config (YAML frontmatter, e.g. `edit_file: {paths: ["docs/adr-*.md"]}`) gets loaded and enforced at tool-call time. Note: full config-file parsing is Phase 4's job — for this phase, design the enforcement mechanism assuming the parsed restriction data is already available as a Go struct (e.g. `map[string]ToolConfig` passed into whatever executes tools), so this phase's tests can seed that data directly without needing real config parsing yet. Specify exactly how `bash_exec: null` (tool not granted at all) is represented and enforced — the tool must not be present in what's exposed to the model, not just rejected if called.

5. **Forensic's restricted tool set.** Per spec, forensic is denied `bash_exec`, `edit_file`, and `create_file` entirely — only `read_file`, `list_dir`, and `write_log` (write_log is universal per point 2 above). Confirm your design for point 4 makes this a natural consequence of the per-agent config rather than a special-cased exception in the tool-dispatch code.

## Explicitly out of scope for Phase 2
- No turn loop, no model-driven tool-call dispatch loop — that's Phase 3. This phase's tests call tools directly with hand-constructed arguments; nothing here needs a real model round-trip.
- No loop/halt detection (write-count thresholds, content-hash comparison) — Phase 3, even though `internal/store`'s `FileWriteCount` helper (built in Phase 1) exists to support it later.
- No config file parsing (YAML frontmatter, `.aa/` bootstrap) — Phase 4. Use hand-constructed Go structs for per-agent tool restrictions in this phase's own tests.
- No git integration — Phase 5.

## Deliverable
Write your design to `docs/adr-phase-2-tools-safety.md`. Cover: the tool interface shape, per-tool argument/result/error specification for all six tools, the finalized `resolveScoped` implementation (including symlink/traversal handling), and the per-agent glob-scoping enforcement mechanism. Flag any open decisions for builder rather than leaving them implicit. Append a `phase-2.log` entry summarizing your decisions — note that during these bootstrap phases the harness itself doesn't exist yet to provide a real `write_log` tool, so write this entry the same way Phase 1's log entries were written (a plain file write via whatever dispatch mechanism is running this session), not by calling a tool that's still being designed.
