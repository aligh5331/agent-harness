# Phase 4 — Config & Bootstrap — Builder Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-4-config-bootstrap.md` and `phase-4.log`. Implement against that ADR, not your own reading of the spec, if the two disagree — flag it instead of picking one.

## Your task this phase

1. **Config parser**: implement the YAML-frontmatter parser exactly as architect specified, producing `loop.AgentConfig` and `tools.AgentToolConfig`/`tools.ToolRestrictions` — don't modify those target types, only the parser that populates them.
2. **`go:embed` + `.aa/` bootstrap**: implement the embed package, extraction logic, and the missing-file-fallback behavior architect specified.
3. **Skills convention**: implement manifest-line discovery via existing `list_dir`/`read_file` tools, per the ADR.
4. **`cmd/harness/main.go` wiring**: build out the CLI flow architect specified — resolve root, bootstrap `.aa/`, load config by agent name, construct registry (correctly threading the resolved root per Phase 3 §14's fix), construct and run `TurnLoop`.
5. **Both carried-forward Phase 3 decisions**: implement whatever architect decided for session-ID exposure and delta-call-count accounting. If either requires a small, scoped change to `internal/loop`, make exactly that change and nothing more — don't restructure Phase 3's tested logic.

## Constraints
- Don't modify `internal/llm`, `internal/store`, `internal/tools` beyond what architect's ADR explicitly calls for.
- Test the YAML edge case architect flagged (`bash_exec: null` vs. key absent) explicitly — write a test proving your parser produces the same `AgentToolConfig` result for both, if that's what architect specified, or documents the difference if architect specified they should differ.
- If `.aa/` bootstrap or skills discovery touches the filesystem in tests, use `t.TempDir()` patterns consistent with Phase 2/3's test style — don't leave test artifacts on disk.

## Deliverable
Working Go code for the parser, bootstrap, skills discovery, and CLI wiring, compiling cleanly. Append a `phase-4.log` entry via `write_log` summarizing what was implemented, confirming both carried-forward decisions were addressed, and noting any deviations from the ADR.
