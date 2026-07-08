# Phase 6 — Phase 0 Generator — Fix Cycle 1 — Builder Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read the "Fix Cycle 1" section of `docs/adr-phase-6-prompt-generator.md` and architect's latest `phase-6.log` entry.

## Your task this cycle
Update `internal/config/embedded/agents/prompt-generator.md`'s `create_file` grant to include the `paths:` restriction architect specified, and update `.aa/agents/prompt-generator.md` (the extracted copy, if this project's own `.aa/` already has one) to match — confirm whether Phase 4's bootstrap disk-wins rule means the extracted copy needs manual updating too, or whether re-running bootstrap with a cleared `.aa/` is the right move here; don't silently leave a stale, unscoped copy on disk while only fixing the embedded source.

## Constraints
- This is a small, targeted config change — don't touch `internal/config/generator.go` or the agent's system-prompt body unless architect's fix requires wording changes too.
- Write a test proving the restriction actually holds: construct a scenario where the prompt-generator agent (or a direct tool-dispatch call using its `AgentToolConfig`) attempts to `create_file` outside the allowed glob and confirm it's rejected, the same way Phase 2's tests proved `AllowPath` worked for other agents.

## Deliverable
Updated config with the path restriction applied everywhere it needs to be, a passing test proving the restriction is enforced, existing test suite still green. Append a `phase-6.log` entry via `write_log`.
