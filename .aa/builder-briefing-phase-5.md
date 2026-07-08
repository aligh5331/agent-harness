# Phase 5 — Git Integration — Builder Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-5-git-integration.md` and `phase-5.log`. Implement against that ADR, not your own reading of the spec, if the two disagree — flag it instead of picking one.

## Your task this phase
Implement exactly what architect specified:

1. The git-operations package (commit, branch creation/checkout), shelling out to the `git` binary via `os/exec`.
2. The commit-message sourcing mechanism (reusing `write_log`'s content per the ADR).
3. The post-`Run()` hook in `cmd/harness/main.go` that fires a commit after a phase step completes.
4. The `--phase` CLI flag and `EnsureBranch` wiring, including the defined behavior for an existing branch and for uncommitted-changes-already-present at run start.

## Constraints
- Don't modify `internal/llm`, `internal/store`, `internal/tools`, `internal/loop`, or `internal/config` beyond what architect's ADR explicitly calls for — if the commit hook genuinely requires a small loop change (e.g. exposing something beyond `SessionID()`), flag it back rather than assuming.
- Git operations should fail loud and stop execution on error (e.g. `git commit` fails because there's nothing to commit, or a merge conflict-like state exists) — don't swallow git errors silently.
- Test git operations against real temporary git repos (`t.TempDir()` + `git init`) rather than mocking the git binary — this is exactly the kind of external-tool integration where a real invocation catches more than a mock would.

## Deliverable
Working Go code implementing the git-operations package and CLI wiring, compiling cleanly. Append a `phase-5.log` entry via `write_log` summarizing what was implemented and any deviations from the ADR.
