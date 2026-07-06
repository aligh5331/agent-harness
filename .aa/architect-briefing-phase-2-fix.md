# Phase 2 — Tool Execution & Safety — Fix Cycle 2 — Architect Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-2-tools-safety.md`, `phase-2.log`, and `internal/tools/resolve.go` in full — this is a fix cycle on already-tested, merged-pending Phase 2 work, not new design.

## Context
In `checkPrefix`, every candidate path being checked against the project root has already had its symlinks resolved (`resolveScoped` calls `filepath.EvalSymlinks` before `checkPrefix` runs), but `root` itself is only resolved via `filepath.Abs`, never `EvalSymlinks`:

```go
absRoot, err := filepath.Abs(root)
```

If the project root sits behind a symlink anywhere in its own ancestry, this compares a symlink-resolved child path against a non-symlink-resolved root — legitimate paths could spuriously fail the prefix check, since the two sides of the comparison went through different resolution. This wasn't caught by Phase 2's tester because the existing symlink test covers a symlink *inside* the project pointing outside it (a child-path case), not a symlink in the root's own ancestry.

## Your task this cycle
Specify the fix for `checkPrefix` (and confirm whether `resolveScoped`'s other call sites need any corresponding change): resolve `root` via `filepath.EvalSymlinks` before computing `absRoot`, with a defined fallback to the current `filepath.Abs` behavior if `EvalSymlinks` fails (e.g. root doesn't exist as a real path — shouldn't happen in practice since the project root always exists, but the function should behave predictably rather than panic or silently misresolve). Specify exactly where in the existing function this resolution should happen, and confirm this doesn't change behavior for the already-tested non-symlink cases (plain paths, `..` traversal, child symlinks) — this should be a narrow, targeted fix to one comparison, not a rewrite of `resolve.go`.

## Constraints
- This is a fix to existing, tested code. Don't restructure `resolveScoped`, `resolvePartial`, or the error types unless the fix genuinely requires it.
- Confirm your fix doesn't require re-resolving `root` on every single call (it's computed once per call currently) in a way that meaningfully hurts performance — this is called on every file-tool invocation, so keep it cheap, though correctness comes first.

## Deliverable
Amend `docs/adr-phase-2-tools-safety.md` with a "Fix Cycle 2" section specifying the exact fix. Append a `phase-2.log` entry summarizing it.
