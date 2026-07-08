# Phase 5 — Git Integration — Tester Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-5-git-integration.md` and `phase-5.log` (architect's and builder's entries).

## Your task this phase

## What to check

1. **Commit correctness**
   - A phase step that produces file changes results in exactly one commit after `Run()` completes
   - The commit message matches the `write_log`/`phase-N.log` content per architect's sourcing decision — verify this against a real git log, not just that *a* commit happened
   - A phase step that produces no file changes (only a log entry) — confirm the defined behavior (still commits, per §15's "no changes to commit is still a commit" intent) actually happens, don't assume

2. **Branch handling**
   - `--phase N` on a fresh repo creates and checks out the correct branch
   - `--phase N` on a repo where that branch already exists checks it out without erroring or losing existing commits on that branch
   - Uncommitted changes present at run start — confirm the defined failure behavior fires (fail loud, per architect's spec) rather than silently committing pre-existing unrelated changes or discarding them

3. **Integration with existing suite**
   - Confirm this phase's changes don't affect anything in `internal/loop`'s tested completion path — if architect specified the hook lives entirely in `cmd/harness/main.go`, verify no `internal/loop` behavior changed
   - Regression: full existing suite (Phase 1-4, ~191 tests) still passes

4. **Real git behavior, not mocked**
   - Since builder was asked to test against real temporary git repos, confirm your own tests do the same — a mocked git binary could hide behavior differences between what the code assumes and what git actually does (e.g. exact commit message formatting, branch-already-exists edge cases)

## Deliverable
Test suite and pass/fail report covering all of the above. Append to `phase-5.log` via `write_log`. If this passes cleanly, Phase 5 is ready — note that per this phase's own scope, "ready" means committed to the current phase branch, not merged; merging remains a manual/Phase-7-audit-gated decision, not something this phase's tests should assume happens automatically.
