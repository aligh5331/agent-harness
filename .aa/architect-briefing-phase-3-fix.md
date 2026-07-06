# Phase 3 — Turn Loop & Loop Detection — Fix Cycle 2 — Architect Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-3-turn-loop.md` (§10 especially) and `phase-3.log` — this is a fix cycle on already-tested Phase 3 work, not new design.

## Context
§10 of your own ADR correctly identified the `ToolConfig.ProjectRoot` symlink-resolution requirement and recommended Option B: the caller resolves the project root once via `filepath.EvalSymlinks` and passes the same resolved value to both `tools.NewDefaultRegistry` and `loop.New`. Builder implemented this as documented — but the design as specified relies on the *caller* doing that resolution correctly and consistently, with nothing structurally preventing a mismatch.

This gap is real: `NewDefaultRegistry(projectRoot, logPath)` resolves `projectRoot` internally via `EvalSymlinks` for every tool's own `root` field, but `loop.New(..., resolvedRoot)` takes a separate `resolvedRoot` parameter and stores it as-is — trusting it already matches. In `TestTester_SymlinkEndToEndThroughLoop`, the raw (unresolved) `symDir` is passed to both calls: `NewDefaultRegistry` resolves it internally to the real target directory, but `loop.New` stores the raw symlink path verbatim as `l.resolvedRoot`. This means `ToolConfig.ProjectRoot` (built from `l.resolvedRoot` in `dispatchAndCheck`) can diverge from the path basis the tools actually use internally. The existing test doesn't catch this because it uses an unrestricted agent (empty `AllowedPaths`), so `AllowPath` short-circuits before ever computing `filepath.Rel` against the mismatched root.

## Your task this cycle
Specify a fix that makes this mismatch structurally impossible, not just correctly documented. Recommended approach: change `NewDefaultRegistry`'s signature to return the resolved root alongside the registry, so there is no way for a caller to construct a registry and a `TurnLoop` with two different root values:

```go
func NewDefaultRegistry(projectRoot, logPath string) (Registry, string) {
    resolvedRoot, err := filepath.EvalSymlinks(projectRoot)
    if err != nil {
        resolvedRoot = projectRoot
    }
    return Registry{...}, resolvedRoot
}
```

Specify exactly how this propagates: the return-value change to `NewDefaultRegistry`, whether `loop.New`'s signature should change at all (it may not need to, if callers now always source `resolvedRoot` from `NewDefaultRegistry`'s second return value), and confirm this doesn't reintroduce the Option-A-vs-B ambiguity from §10 — pick one approach and make it the only possible path, rather than documenting a convention callers must remember to follow.

## Constraints
- This is a fix to existing, tested code. Don't restructure the registry/loop relationship beyond what's needed to close this specific gap.
- Every existing call site of `NewDefaultRegistry` (test files and any production code) will need updating for the new signature — enumerate them in your ADR addendum so builder doesn't miss one.

## Deliverable
Amend `docs/adr-phase-3-turn-loop.md` with a "Fix Cycle 2" section specifying the exact signature change and every call site that needs updating. Append a `phase-3.log` entry via `write_log`.
