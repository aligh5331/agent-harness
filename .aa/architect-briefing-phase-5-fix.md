# Phase 5 — Git Integration — Fix Cycle 1 — Architect Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-5-git-integration.md` and `phase-5.log`. This is a fix cycle on already-tested Phase 5 work, not new design.

## Context (found during per-phase audit review, not by tester)
`tools.test` — a compiled Go test binary produced by `go test -c` — is currently tracked in the repository (`git ls-files tools.test` confirms it; working tree is otherwise clean, meaning it's already committed history, not just an untracked stray file). `.gitignore` currently only excludes `/harness` (the harness's own build output), not the general `*.test` pattern Go produces whenever `go test -c` is run in any package.

This matters more now than before Phase 5: `gitops.Commit` stages everything via `git add -A` before committing. Any future stray build artifact left in the working tree at the end of a phase step — a `*.test` binary from manual debugging, a stray `go build` output, an IDE-generated file not yet covered by `.gitignore` — would get silently swept into that step's commit with nothing catching it, since there's no review step between "agent's turn ends" and "commit fires."

## Your task this cycle
1. Confirm `.gitignore` should be broadened to cover Go's general build-artifact patterns (`*.test` at minimum — Go's convention for `go test -c` output across any package, not just `tools`), and specify what else should be added given this is a Go project where `go build`/`go test -c` could produce artifacts in any package directory, not just the root.
2. Decide whether `git add -A` in `Commit()` remains the right approach given a more complete `.gitignore`, or whether it's worth adding a lightweight safety check (e.g., warn if `git status --porcelain` shows an untracked file matching a suspicious binary pattern, before staging) — weigh this against the harness's "keep it simple" bias; a more complete `.gitignore` alone may be sufficient without extra runtime logic.
3. Specify how to remove `tools.test` from git's tracking going forward (`git rm --cached tools.test`, keep or delete the local file, is a rewrite of prior commit history warranted or is a clean removal-going-forward sufficient — given this is a personal-use repo, prior history rewriting is likely unnecessary overhead; confirm).

## Deliverable
Amend `docs/adr-phase-5-git-integration.md` with a "Fix Cycle 1" section specifying the `.gitignore` additions and the tracking-removal approach. Append a `phase-5.log` entry.
