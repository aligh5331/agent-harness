# Phase 5 — Git Integration — Architect Briefing

## Project
Go coding-agent harness. Full spec: `agent-harness_spec.md`. Before starting, read `docs/adr-phase-4-config-bootstrap.md` and `phase-4.log`. This phase wires git operations into the turn loop and CLI — treat all four prior phases' packages as fixed dependencies.

## Reminder on what §15 actually specifies
The spec's Git Management section (§15) describes behavior the *finished harness* enforces on projects it manages — per-step commits, branch-per-phase, `phase-N.log` as the commit-accompanying artifact, per-phase audit gating a merge. This phase builds that capability into the harness; it is not a process constraint on how the harness's own bootstrap phases (1-7) have been built, which is why the harness's own repo has been committed straight to `main` so far without contradiction.

## Your task this phase

1. **Where the git logic lives.** Decide the package (e.g. `internal/gitops` or similar) and its surface — likely something like `Commit(root, message string) error`, `EnsureBranch(root, branchName string) error`, wrapping the installed `git` binary via `os/exec` (consistent with how `bash_exec` already shells out rather than using a Go git library — no new dependency class, same pattern already established).

2. **Commit message sourcing — the actual design question.** Per §15, the commit message is "authored by the agent that made the changes." Rather than inventing a second flow for this, specify that the **same content the agent writes via `write_log` for its `phase-N.log` entry is reused as the commit message** — either verbatim, or a git-conventional trim (short first-line summary + the full entry as commit body). This means no new tool, no new prompt requirement — `write_log`'s existing output becomes the commit message's source of truth. Confirm this is workable or specify why a separate mechanism is actually needed.

3. **When a commit fires.** Per §15, "one commit per full phase step — after the acting agent ends its turn, before the next step begins." Specify exactly where in the turn loop's lifecycle this hooks in — likely right after `Run()` returns a terminal `HaltReason` (done/halted/error), not on every turn. Confirm this doesn't require restructuring `internal/loop`'s tested completion path — it should be a thin post-`Run()` step in `cmd/harness/main.go`, not inside the loop itself.

4. **Branch-per-phase.** Specify how the harness determines "which phase am I on" and creates/checks out the corresponding branch before running — likely a new `--phase` CLI flag (matching the existing `--agent`/`--db`/`--prompt` pattern) that triggers `EnsureBranch` before the turn loop starts. Specify behavior if the branch already exists (checkout, don't error) versus if there are uncommitted changes already present when a run starts (this needs a defined behavior — fail loud rather than silently commit or discard, consistent with the harness's overall safety bias).

5. **What's explicitly NOT this phase's job.** Merging a phase branch into `main` — gated by the per-phase spec-audit check per §14/§15 — is a decision point requiring human or Phase-7-audit involvement, not something this phase automates. Confirm the scope stops at "commit to the current phase branch," not "merge."

## Explicitly out of scope for Phase 5
- Spec audit implementation itself (§14) — Phase 7
- Phase 0 prompt generator — Phase 6
- Any merge automation

## Deliverable
Write your design to `docs/adr-phase-5-git-integration.md`. Cover all five items above with enough specificity for builder to implement without further design input. Append a `phase-5.log` entry via `write_log`.
