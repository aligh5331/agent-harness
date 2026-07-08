## Decision
Git operations will be implemented in a new package `internal/gitops`, wrapping the `git` binary via `os/exec`. Commit messages will be sourced from the `phase-N.log` entries generated during the agent's turn. Commits will be automated at the end of each full phase step, after the turn loop completes.

## Context
Phase 5 mandates git integration as specified in §15 of the `agent-harness_spec.md`. The harness needs to enforce per-step commits and maintain phase-specific branches while reusing existing harness artifacts.

## Options considered
- **Git library (go-git):** Rejected — adds significant external dependencies, potential for mismatched behavior with the system's `git` configuration, and deviates from the existing `bash_exec` pattern.
- **`os/exec` wrapper:** Chosen — aligns with existing patterns, utilizes the installed `git` binary, and keeps the harness lightweight.

## Chosen approach

### 1. Git Logic Package
Create `internal/gitops` with the following interface:
```go
package gitops

// Commit creates a new commit in the repository at root, with the given message.
func Commit(root, message string) error

// EnsureBranch creates or checks out the specified branch.
func EnsureBranch(root, branchName string) error

// CurrentBranch returns the name of the current branch.
func CurrentBranch(root string) (string, error)
```

### 2. Commit Message Sourcing
The agent’s `phase-N.log` entry created during the turn via `write_log` will serve as the commit message. The `internal/loop` completion handler will extract the latest log entry (or the entire new entry) and pass it to `gitops.Commit`.

### 3. Commit Trigger
The commit will fire in `cmd/harness/main.go` immediately after `loop.Run()` returns. This keeps `internal/loop` agnostic of git operations while ensuring every phase step—successful or halted—is captured if changes occurred.

### 4. Branch-per-phase
Add a `--phase` CLI flag. Before the turn loop starts, `cmd/harness` will call `gitops.EnsureBranch(root, "phase-" + phaseFlag)`. If the branch exists, it will checkout; if not, it will create it. If uncommitted changes exist, the harness will fail with an error to preserve safety.

### 5. Scope
The scope is strictly limited to committing changes to the phase branch. Merging branches or auditing is explicitly out of scope for this phase.

### Fix Cycle 1: Git Hygiene & Artifact Management
- **.gitignore Updates:** The repository `.gitignore` has been updated to exclude Go build artifacts (`*.test`, `*.out`, `*.tmp`, `*.exe`, `*.so`, `*.dylib`, `*.a`).
- **Removal of `tools.test`:** Executed `git rm --cached tools.test` to stop tracking the test binary.
- **Commit Strategy:** Maintain `git add -A` in `internal/gitops/gitops.go`. Comprehensive `.gitignore` patterns are the primary control against accidental inclusion of artifacts.

## Constraints and risks
- **Uncommitted changes:** If uncommitted changes exist at startup, the harness must stop to prevent unexpected commits.
- **`git` availability:** The harness assumes `git` is installed and reachable in `$PATH`.
- **Log entry consistency:** Relying on `phase-N.log` assumes the agent correctly writes the log entry before the loop halts.
