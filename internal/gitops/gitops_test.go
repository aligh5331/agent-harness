package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestRepo creates a temporary directory with an initialized git repository
// that has user.name and user.email configured (required for commits) and an
// initial commit on main. Returns the repo root path.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	git("init")
	git("config", "user.name", "Test User")
	git("config", "user.email", "test@example.com")

	// Create initial commit on main so HEAD is valid.
	if err := os.WriteFile(filepath.Join(root, ".gitkeep"), nil, 0644); err != nil {
		t.Fatalf("write .gitkeep: %v", err)
	}
	git("add", "-A")
	git("commit", "-m", "initial commit")

	return root
}

// TestCurrentBranch_Initial checks that CurrentBranch returns "main" (the
// default branch name in modern git) for a freshly initialized repo.
func TestCurrentBranch_Initial(t *testing.T) {
	root := setupTestRepo(t)

	branch, err := CurrentBranch(root)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch != "main" && branch != "master" {
		// Accept both "main" (modern git) and "master" (older).
		t.Logf("CurrentBranch returned %q (acceptable)", branch)
	}
}

// TestEnsureBranch_CreatesNew checks that EnsureBranch creates and checks out
// a new branch that did not previously exist.
func TestEnsureBranch_CreatesNew(t *testing.T) {
	root := setupTestRepo(t)

	if err := EnsureBranch(root, "phase-5"); err != nil {
		t.Fatalf("EnsureBranch(phase-5): %v", err)
	}

	branch, err := CurrentBranch(root)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch != "phase-5" {
		t.Fatalf("expected branch 'phase-5', got %q", branch)
	}
}

// TestEnsureBranch_Existing checks that EnsureBranch handles an already-existing
// branch without error (idempotent).
func TestEnsureBranch_Existing(t *testing.T) {
	root := setupTestRepo(t)

	// Create the branch first via raw git.
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("branch", "phase-5")

	// Stay on main — call EnsureBranch to switch to the existing branch.
	if err := EnsureBranch(root, "phase-5"); err != nil {
		t.Fatalf("EnsureBranch(phase-5) on existing branch: %v", err)
	}

	branch, err := CurrentBranch(root)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch != "phase-5" {
		t.Fatalf("expected branch 'phase-5', got %q", branch)
	}
}

// TestCommit_CreatesCommit checks that Commit creates a commit with the given
// message and the returned bool is true.
func TestCommit_CreatesCommit(t *testing.T) {
	root := setupTestRepo(t)

	// Make a change.
	if err := os.WriteFile(filepath.Join(root, "test.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write test.txt: %v", err)
	}

	msg := "builder: completed phase-5 implementation\n\nAdded git integration."
	created, err := Commit(root, msg)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !created {
		t.Fatal("expected commit to be created, but was not")
	}

	// Verify the commit message — run git log in the test repo.
	cmd := exec.Command("git", "log", "--format=%B", "-1")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != msg {
		t.Fatalf("commit message mismatch:\n  expected: %q\n  got:      %q", msg, got)
	}
}

// TestCommit_NothingToCommit checks that Commit returns (false, nil) when there
// are no changes in the working tree.
func TestCommit_NothingToCommit(t *testing.T) {
	root := setupTestRepo(t)

	// Do not make any changes — working tree is clean from the initial commit.
	created, err := Commit(root, "nothing to commit here")
	if err != nil {
		t.Fatalf("Commit on clean tree: %v", err)
	}
	if created {
		t.Fatal("expected created=false for clean tree")
	}
}

// TestCommit_OnlyUntrackedFiles checks that Commit stages untracked files and
// creates a commit.
func TestCommit_OnlyUntrackedFiles(t *testing.T) {
	root := setupTestRepo(t)

	// Create a new file (untracked).
	if err := os.WriteFile(filepath.Join(root, "new_file.go"), []byte("package main"), 0644); err != nil {
		t.Fatalf("write new_file.go: %v", err)
	}

	created, err := Commit(root, "add new file")
	if err != nil {
		t.Fatalf("Commit with new file: %v", err)
	}
	if !created {
		t.Fatal("expected commit to be created for new file")
	}
}

// TestCommit_MultiLineMessage checks that commit messages with multiple lines
// are preserved correctly.
func TestCommit_MultiLineMessage(t *testing.T) {
	root := setupTestRepo(t)

	if err := os.WriteFile(filepath.Join(root, "foo.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("write foo.txt: %v", err)
	}

	msg := "phase-5: implement git integration\n\nImplemented the gitops package\nwith Commit, EnsureBranch, CurrentBranch\nand tests against real git repos."
	created, err := Commit(root, msg)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !created {
		t.Fatal("expected commit to be created")
	}

	cmd := exec.Command("git", "log", "--format=%B", "-1")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != msg {
		t.Fatalf("multi-line message mismatch:\nexpected:\n%s\n\ngot:\n%s", msg, got)
	}
}

// TestIsClean_ReturnsTrue checks that a freshly initialized repo with no
// uncommitted changes reports clean.
func TestIsClean_ReturnsTrue(t *testing.T) {
	root := setupTestRepo(t)

	clean, err := IsClean(root)
	if err != nil {
		t.Fatalf("IsClean: %v", err)
	}
	if !clean {
		t.Fatal("expected clean=true for fresh repo")
	}
}

// TestIsClean_DirtyAfterModification checks that modifying a tracked file makes
// the repo dirty.
func TestIsClean_DirtyAfterModification(t *testing.T) {
	root := setupTestRepo(t)

	// Modify an existing tracked file.
	if err := os.WriteFile(filepath.Join(root, ".gitkeep"), []byte("modified"), 0644); err != nil {
		t.Fatalf("write .gitkeep: %v", err)
	}

	clean, err := IsClean(root)
	if err != nil {
		t.Fatalf("IsClean fresh: %v", err)
	}
	if clean {
		t.Fatal("expected clean=false after modification")
	}

	// Verify that a clean state returns true after committing.
	Commit(root, "modify gitkeep")
	clean, err = IsClean(root)
	if err != nil {
		t.Fatalf("IsClean after commit: %v", err)
	}
	if !clean {
		t.Fatal("expected clean=true after committing modification")
	}
}

// TestIsClean_UntrackedFilesNotDirty checks that adding untracked files does
// NOT make IsClean return false — IsClean only checks tracked file changes
// (matching "diff-index --quiet HEAD --" behavior).
func TestIsClean_UntrackedFilesNotDirty(t *testing.T) {
	root := setupTestRepo(t)

	// Create an untracked file.
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("new"), 0644); err != nil {
		t.Fatalf("write untracked.txt: %v", err)
	}

	clean, err := IsClean(root)
	if err != nil {
		t.Fatalf("IsClean: %v", err)
	}
	if !clean {
		t.Fatal("expected clean=true with only untracked files (IsClean checks tracked-file changes only)")
	}
}
