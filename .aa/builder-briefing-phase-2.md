# Phase 2 — Tool Execution & Safety — Builder Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-2-tools-safety.md` (architect's design for this phase) and `phase-2.log` for context. Also confirm you're building on top of Phase 1's `internal/store` and `internal/llm` packages as-is — don't modify them; if something about them genuinely blocks this phase, flag it rather than changing merged code.

## Your task this phase
Implement exactly what architect specified:

1. The tool interface / dispatch shape.
2. All six tools: `read_file`, `edit_file`, `create_file`, `bash_exec`, `list_dir`, `write_log` — argument parsing, execution, result/error shapes per the ADR.
3. `resolveScoped` (project-root path enforcement), including whatever symlink/traversal handling architect specified.
4. Per-agent, per-tool glob-based access enforcement, using hand-constructed Go structs for the restriction data (real config parsing is Phase 4 — don't build YAML parsing now).

## Constraints
- `edit_file` must fail loud and distinguishably on both "no match" and "multiple matches" — write these as genuinely different, inspectable error conditions, not the same generic error string, since Phase 3's model-facing tool-result messages will need to tell the model which case it hit so it can react correctly (add more context vs. something else entirely).
- `bash_exec`'s timeout must actually cancel a running process, not just stop waiting for it — verify `context.WithTimeout` is wired all the way through to `exec.CommandContext`, not just used to time out your own wait loop while leaving a runaway process behind.
- `write_log` must be structurally incapable of writing anywhere except the current phase's log path — this isn't just "don't pass it a bad path," the function signature itself shouldn't accept an arbitrary path parameter at all.
- Keep this scoped to tool execution only — no turn loop, no model wiring, no halt detection.

## Deliverable
Working Go code for all six tools plus the scoping/enforcement layer, compiling cleanly. Append a `phase-2.log` entry (same bootstrap-era logging note as architect's briefing — no real `write_log` tool exists yet within this very phase's own process) summarizing what was implemented and any deviations from the ADR. Hand off to tester once it builds.
