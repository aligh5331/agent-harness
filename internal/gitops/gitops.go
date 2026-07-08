// Package gitops wraps the installed git binary via os/exec for phase-level
// branch management and per-step commits. It follows the same shell-out pattern
// as internal/tools/bash_exec rather than using a Go git library.
//
// All functions operate on a repository at a given root directory. They assume
// git is installed and reachable in $PATH. If git is not available, the first
// operation will fail with exec.ErrNotFound or similar.
package gitops

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// runGit executes a git command in the repository at root.
// Returns combined output on success, or an error wrapping stderr on failure.
func runGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// IsClean returns true if the working tree has no unstaged or uncommitted
// changes. It does not check for untracked files — only modifications to
// tracked files are considered "changes" for the purpose of this check
// (consistent with the harness starting each phase step from a clean state).
//
// This function does NOT use runGit because it needs to inspect the exit code
// directly — runGit wraps errors and loses the *exec.ExitError type.
func IsClean(root string) (bool, error) {
	cmd := exec.Command("git", "diff-index", "--quiet", "HEAD", "--")
	cmd.Dir = root
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	// diff-index --quiet exits 0 if clean, 1 if dirty.
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("check clean: %w", err)
}

// CurrentBranch returns the name of the currently checked-out branch.
// If HEAD is detached, it returns "HEAD".
func CurrentBranch(root string) (string, error) {
	return runGit(root, "rev-parse", "--abbrev-ref", "HEAD")
}

// EnsureBranch checks out the named branch, creating it first if it does not
// already exist. It is idempotent: if the branch already exists and is already
// checked out, it returns nil. If the branch exists but is not checked out,
// it checks it out.
func EnsureBranch(root, branchName string) error {
	// Check if branch exists locally.
	out, err := runGit(root, "branch", "--list", branchName)
	if err != nil {
		return fmt.Errorf("ensure branch %s: %w", branchName, err)
	}

	// Trim whitespace — git branch --list returns the branch name with possible
	// leading "* " if it is the current branch.
	listed := strings.TrimSpace(out)
	listed = strings.TrimPrefix(listed, "* ")

	if listed == branchName {
		// Branch exists — check it out.
		_, err := runGit(root, "checkout", branchName)
		if err != nil {
			return fmt.Errorf("checkout existing branch %s: %w", branchName, err)
		}
		return nil
	}

	// Branch does not exist — create and check out.
	_, err = runGit(root, "checkout", "-b", branchName)
	if err != nil {
		return fmt.Errorf("create branch %s: %w", branchName, err)
	}
	return nil
}

// Commit creates a commit from all changes in the repository at root, using the
// provided message. It first stages all changes (tracked and untracked) via
// "git add -A", then commits via stdin to avoid shell parsing issues with
// multi-line messages.
//
// If there are no changes to commit (working tree clean after add), Commit
// returns (false, nil) — this is not an error because it is a normal condition
// (e.g., agent only read files during its turn). The caller should log this as
// informational rather than an error.
//
// The returned bool is true if a commit was actually created.
func Commit(root, message string) (bool, error) {
	// Stage all changes.
	_, err := runGit(root, "add", "-A")
	if err != nil {
		return false, fmt.Errorf("git add: %w", err)
	}

	// Commit via stdin to handle multi-line messages.
	cmd := exec.Command("git", "commit", "-F", "-")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(message)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		outMsg := strings.TrimSpace(stdout.String())
		// "nothing to commit" is not a real error — the agent may have only
		// read files during its turn, producing no changes worth committing.
		// Git writes this message to stdout (not stderr) when using -F -.
		if strings.Contains(msg, "nothing to commit") ||
			strings.Contains(outMsg, "nothing to commit") {
			return false, nil
		}
		return false, fmt.Errorf("git commit: %s (stdout: %s)", msg, outMsg)
	}
	return true, nil
}
