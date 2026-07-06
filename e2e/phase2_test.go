// Package e2e provides end-to-end tests for the agent-harness Phase 2 tool
// execution and safety layer. These tests verify the six built-in tools
// (read_file, edit_file, create_file, list_dir, bash_exec, write_log) behave
// correctly under the full Registry pipeline — from NewDefaultRegistry through
// FilterByAgentConfig to Execute — including path scoping, per-agent glob
// enforcement, and distinguishable error types.
package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-harness/internal/tools"
)

// ---------------------------------------------------------------------------
// Section 1: Happy path — all 6 tools work via the full Registry pipeline
// ---------------------------------------------------------------------------

// TestE2E_Tools_NewDefaultRegistry verifies NewDefaultRegistry creates all 6 tools.
func TestE2E_Tools_NewDefaultRegistry(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "phase-2.log")
	reg := tools.NewDefaultRegistry(tmpDir, logPath)

	expectedTools := []string{"read_file", "edit_file", "create_file", "list_dir", "bash_exec", "write_log"}
	for _, name := range expectedTools {
		if _, ok := reg[name]; !ok {
			t.Errorf("NewDefaultRegistry missing tool %q", name)
		}
	}
	if len(reg) != len(expectedTools) {
		t.Errorf("expected %d tools in registry, got %d", len(expectedTools), len(reg))
	}
}

// TestE2E_Tools_Definitions verifies Definitions() returns sorted ToolDefs.
func TestE2E_Tools_Definitions(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "phase-2.log")
	reg := tools.NewDefaultRegistry(tmpDir, logPath)

	defs := reg.Definitions()
	if len(defs) != 6 {
		t.Fatalf("expected 6 definitions, got %d", len(defs))
	}

	expectedOrder := []string{"bash_exec", "create_file", "edit_file", "list_dir", "read_file", "write_log"}
	for i, d := range defs {
		if d.Function.Name != expectedOrder[i] {
			t.Errorf("definition[%d]: name = %q, want %q", i, d.Function.Name, expectedOrder[i])
		}
	}
}

// TestE2E_Tools_ReadFile_HappyPath creates a file then reads it via the tool.
func TestE2E_Tools_ReadFile_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	content := "line one\nline two\nline three"
	if err := os.WriteFile(filepath.Join(tmpDir, "hello.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	readFile := reg["read_file"]
	args := mustJSON(t, map[string]string{"path": "hello.txt"})
	config := allPathsAllowed(tmpDir)

	result, err := readFile.Execute(context.Background(), args, config)
	if err != nil {
		t.Fatalf("read_file failed: %v", err)
	}

	rd, ok := result.Data.(tools.ReadFileResult)
	if !ok {
		t.Fatalf("result.Data is %T, want tools.ReadFileResult", result.Data)
	}
	if rd.Path != "hello.txt" {
		t.Errorf("Path = %q, want %q", rd.Path, "hello.txt")
	}
	if rd.LineCount != 3 {
		t.Errorf("LineCount = %d, want %d", rd.LineCount, 3)
	}
	if rd.Truncated {
		t.Error("Truncated should be false")
	}
	// Content should be line-numbered: "1: line one\n2: line two\n3: line three"
	if !strings.Contains(rd.Content, "1: line one") {
		t.Errorf("content missing line 1:\n%s", rd.Content)
	}
	if !strings.Contains(rd.Content, "2: line two") {
		t.Errorf("content missing line 2:\n%s", rd.Content)
	}
	if !strings.Contains(rd.Content, "3: line three") {
		t.Errorf("content missing line 3:\n%s", rd.Content)
	}
}

// TestE2E_Tools_EditFile_HappyPath creates a file, edits it, and verifies content changed.
func TestE2E_Tools_EditFile_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	original := "func main() { fmt.Println(\"hello\") }"
	expected := "func main() { fmt.Println(\"goodbye\") }"
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	editFile := reg["edit_file"]

	args := mustJSON(t, map[string]any{
		"path":    "main.go",
		"old_str": "hello",
		"new_str": "goodbye",
	})
	result, err := editFile.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err != nil {
		t.Fatalf("edit_file failed: %v", err)
	}

	ed, ok := result.Data.(tools.EditFileResult)
	if !ok {
		t.Fatalf("result.Data is %T, want tools.EditFileResult", result.Data)
	}
	if ed.MatchesFound != 1 {
		t.Errorf("MatchesFound = %d, want 1", ed.MatchesFound)
	}
	if ed.Path != "main.go" {
		t.Errorf("Path = %q, want %q", ed.Path, "main.go")
	}

	// Verify content on disk.
	got := readFileStr(t, filepath.Join(tmpDir, "main.go"))
	if got != expected {
		t.Errorf("file content after edit = %q, want %q", got, expected)
	}
}

// TestE2E_Tools_CreateFile_HappyPath creates a new file and verifies it exists.
func TestE2E_Tools_CreateFile_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a subdir so the parent exists.
	if err := os.MkdirAll(filepath.Join(tmpDir, "src"), 0755); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	createFile := reg["create_file"]

	args := mustJSON(t, map[string]any{
		"path":    "src/newfile.txt",
		"content": "hello world",
	})
	result, err := createFile.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err != nil {
		t.Fatalf("create_file failed: %v", err)
	}

	cd, ok := result.Data.(tools.CreateFileResult)
	if !ok {
		t.Fatalf("result.Data is %T, want tools.CreateFileResult", result.Data)
	}
	if cd.Path != "src/newfile.txt" {
		t.Errorf("Path = %q, want %q", cd.Path, "src/newfile.txt")
	}

	// Verify file exists with correct content.
	got := readFileStr(t, filepath.Join(tmpDir, "src", "newfile.txt"))
	if got != "hello world" {
		t.Errorf("file content = %q, want %q", got, "hello world")
	}
}

// TestE2E_Tools_ListDir_HappyPath creates files and lists the directory.
func TestE2E_Tools_ListDir_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".hidden"), []byte("."), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	listDir := reg["list_dir"]

	args := mustJSON(t, map[string]string{"path": "."})
	result, err := listDir.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err != nil {
		t.Fatalf("list_dir failed: %v", err)
	}

	ld, ok := result.Data.(tools.ListDirResult)
	if !ok {
		t.Fatalf("result.Data is %T, want tools.ListDirResult", result.Data)
	}
	if ld.Path != "." {
		t.Errorf("Path = %q, want %q", ld.Path, ".")
	}
	if ld.IsEmpty {
		t.Error("IsEmpty should be false")
	}
	// Should include .hidden (dotfiles included).
	expected := []string{".hidden", "a.txt", "b.txt"}
	if len(ld.Entries) != len(expected) {
		t.Fatalf("Entries = %v, want %v", ld.Entries, expected)
	}
	for i, e := range expected {
		if ld.Entries[i] != e {
			t.Errorf("Entries[%d] = %q, want %q", i, ld.Entries[i], e)
		}
	}
}

// TestE2E_Tools_BashExec_HappyPath runs a simple command and checks output.
func TestE2E_Tools_BashExec_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	bashExec := reg["bash_exec"]

	args := mustJSON(t, map[string]any{
		"command":         "echo hello world",
		"timeout_seconds": 5,
	})
	result, err := bashExec.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err != nil {
		t.Fatalf("bash_exec failed: %v", err)
	}

	bd, ok := result.Data.(tools.BashExecResult)
	if !ok {
		t.Fatalf("result.Data is %T, want tools.BashExecResult", result.Data)
	}
	if bd.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", bd.ExitCode)
	}
	if bd.TimedOut {
		t.Error("TimedOut should be false")
	}
	if strings.TrimSpace(bd.Stdout) != "hello world" {
		t.Errorf("Stdout = %q, want %q", bd.Stdout, "hello world")
	}
}

// TestE2E_Tools_WriteLog_HappyPath appends to the log file and verifies content.
func TestE2E_Tools_WriteLog_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "phase-2.log")

	reg := tools.NewDefaultRegistry(tmpDir, logPath)
	writeLog := reg["write_log"]

	args := mustJSON(t, map[string]string{"content": "=== Agent entry ==="})
	result, err := writeLog.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err != nil {
		t.Fatalf("write_log failed: %v", err)
	}

	wd, ok := result.Data.(tools.WriteLogResult)
	if !ok {
		t.Fatalf("result.Data is %T, want tools.WriteLogResult", result.Data)
	}
	if wd.Path != logPath {
		t.Errorf("Path = %q, want %q", wd.Path, logPath)
	}
	if wd.SizeBytes <= 0 {
		t.Errorf("SizeBytes = %d, want > 0", wd.SizeBytes)
	}

	// Write a second entry and verify both appear.
	args2 := mustJSON(t, map[string]string{"content": "=== Second entry ==="})
	_, err = writeLog.Execute(context.Background(), args2, allPathsAllowed(tmpDir))
	if err != nil {
		t.Fatalf("second write_log failed: %v", err)
	}

	got := readFileStr(t, logPath)
	if !strings.Contains(got, "=== Agent entry ===") {
		t.Errorf("log missing first entry:\n%s", got)
	}
	if !strings.Contains(got, "=== Second entry ===") {
		t.Errorf("log missing second entry:\n%s", got)
	}
}

// TestE2E_Tools_FilterByAgentConfig verifies that filtering by agent config
// correctly includes/excludes tools.
func TestE2E_Tools_FilterByAgentConfig(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "phase-2.log")
	reg := tools.NewDefaultRegistry(tmpDir, logPath)

	// Create a config that only grants read_file, list_dir, and write_log
	// (like the forensic agent).
	config := tools.AgentToolConfig{
		"read_file": {},
		"list_dir":  {},
		"write_log": {},
	}

	filtered := reg.FilterByAgentConfig(config)
	if len(filtered) != 3 {
		t.Fatalf("filtered registry has %d tools, want 3", len(filtered))
	}

	// Verify the right tools are present.
	for _, name := range []string{"read_file", "list_dir", "write_log"} {
		if _, ok := filtered[name]; !ok {
			t.Errorf("filtered registry missing %q", name)
		}
	}
	// Verify excluded tools are absent.
	for _, name := range []string{"edit_file", "create_file", "bash_exec"} {
		if _, ok := filtered[name]; ok {
			t.Errorf("filtered registry should NOT contain %q", name)
		}
	}
}

// TestE2E_Tools_WriteLog_NoPathParam verifies write_log has no path parameter.
func TestE2E_Tools_WriteLog_NoPathParam(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "phase-2.log")

	reg := tools.NewDefaultRegistry(tmpDir, logPath)
	writeLog := reg["write_log"]

	// The definition should NOT have a "path" property.
	def := writeLog.Definition()
	params, ok := def.Function.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("Parameters is %T, want map[string]any", def.Function.Parameters)
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties is %T, want map[string]any", params["properties"])
	}
	if _, exists := props["path"]; exists {
		t.Error("write_log should NOT have a 'path' property in its definition")
	}
}

// TestE2E_Tools_BashExec_WorkingDir verifies cmd.Dir is set to project root.
func TestE2E_Tools_BashExec_WorkingDir(t *testing.T) {
	tmpDir := t.TempDir()

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	bashExec := reg["bash_exec"]

	args := mustJSON(t, map[string]any{
		"command":         "pwd",
		"timeout_seconds": 5,
	})
	result, err := bashExec.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err != nil {
		t.Fatalf("bash_exec failed: %v", err)
	}

	bd := result.Data.(tools.BashExecResult)
	got := strings.TrimSpace(bd.Stdout)
	if got != tmpDir {
		t.Errorf("working directory = %q, want %q", got, tmpDir)
	}
}

// TestE2E_Tools_BashExec_StderrAndExitCode verifies stderr capture and non-zero exit.
func TestE2E_Tools_BashExec_StderrAndExitCode(t *testing.T) {
	tmpDir := t.TempDir()

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	bashExec := reg["bash_exec"]

	args := mustJSON(t, map[string]any{
		"command":         "echo stderr >&2; exit 42",
		"timeout_seconds": 5,
	})
	result, err := bashExec.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err != nil {
		t.Fatalf("bash_exec failed: %v", err)
	}

	bd := result.Data.(tools.BashExecResult)
	if bd.ExitCode != 42 {
		t.Errorf("ExitCode = %d, want %d", bd.ExitCode, 42)
	}
	if strings.TrimSpace(bd.Stderr) != "stderr" {
		t.Errorf("Stderr = %q, want %q", bd.Stderr, "stderr")
	}
	if bd.TimedOut {
		t.Error("TimedOut should be false")
	}
}

// ---------------------------------------------------------------------------
// Section 2: edit_file failure modes — zero and multiple matches
// ---------------------------------------------------------------------------

// TestE2E_EditFile_ZeroMatch_FileUnchanged verifies that when old_str matches
// zero times, the file content is NOT modified.
func TestE2E_EditFile_ZeroMatch_FileUnchanged(t *testing.T) {
	tmpDir := t.TempDir()
	content := "exact content here"
	path := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	editFile := reg["edit_file"]

	args := mustJSON(t, map[string]any{
		"path":    "test.txt",
		"old_str": "nonexistent string that does not exist",
		"new_str": "replacement",
	})
	_, err := editFile.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err == nil {
		t.Fatal("expected error for zero matches, got nil")
	}

	// Verify error is ErrNoMatch.
	var noMatch *tools.ErrNoMatch
	if !errors.As(err, &noMatch) {
		t.Fatalf("expected *tools.ErrNoMatch, got %T: %v", err, err)
	}

	// Verify file content is UNCHANGED.
	got := readFileStr(t, path)
	if got != content {
		t.Errorf("file content CHANGED after zero-match error:\ngot:  %q\nwant: %q", got, content)
	}
}

// TestE2E_EditFile_MultipleMatch_FileUnchanged verifies that when old_str matches
// multiple times, the file content is NOT modified (no silent mass replacement).
func TestE2E_EditFile_MultipleMatch_FileUnchanged(t *testing.T) {
	tmpDir := t.TempDir()
	content := "foo bar foo baz foo qux"
	path := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	editFile := reg["edit_file"]

	args := mustJSON(t, map[string]any{
		"path":    "test.txt",
		"old_str": "foo",
		"new_str": "REPLACED",
	})
	_, err := editFile.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err == nil {
		t.Fatal("expected error for multiple matches, got nil")
	}

	// Verify error is ErrAmbiguousMatch.
	var ambig *tools.ErrAmbiguousMatch
	if !errors.As(err, &ambig) {
		t.Fatalf("expected *tools.ErrAmbiguousMatch, got %T: %v", err, err)
	}
	if ambig.MatchesFound != 3 {
		t.Errorf("MatchesFound = %d, want %d", ambig.MatchesFound, 3)
	}

	// Verify file content is UNCHANGED.
	got := readFileStr(t, path)
	if got != content {
		t.Errorf("file content CHANGED after multiple-match error:\ngot:  %q\nwant: %q", got, content)
	}
}

// ---------------------------------------------------------------------------
// Section 3: Path scoping — each file tool blocks escape attempts
// ---------------------------------------------------------------------------

// TestE2E_PathScoping_ReadFile verifies read_file blocks path escape.
func TestE2E_PathScoping_ReadFile(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a file OUTSIDE the project root.
	outside := filepath.Join(os.TempDir(), "outside-"+t.Name()+".txt")
	if err := os.WriteFile(outside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	readFile := reg["read_file"]

	// Try to traverse to it.
	args := mustJSON(t, map[string]string{"path": "../../" + filepath.Base(os.TempDir()) + "/outside-" + t.Name() + ".txt"})
	_, err := readFile.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err == nil {
		t.Fatal("expected error for path escape, got nil")
	}

	var pathErr *tools.PathEscapeError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected *tools.PathEscapeError, got %T: %v", err, err)
	}
}

// TestE2E_PathScoping_EditFile verifies edit_file blocks path escape.
// Go 1.26.4 filepath.Join does not discard prior elements on absolute paths,
// so "/../etc/passwd" is needed to trigger an actual escape.
func TestE2E_PathScoping_EditFile(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a file inside root so edit_file has something to attempt.
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("root:x:0:0"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	editFile := reg["edit_file"]

	args := mustJSON(t, map[string]any{
		"path":    "/../etc/passwd",
		"old_str": "root",
		"new_str": "hacker",
	})
	_, err := editFile.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err == nil {
		t.Fatal("expected error for path escape, got nil")
	}

	var pathErr *tools.PathEscapeError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected *tools.PathEscapeError, got %T: %v", err, err)
	}
}

// TestE2E_PathScoping_CreateFile verifies create_file blocks path escape.
func TestE2E_PathScoping_CreateFile(t *testing.T) {
	tmpDir := t.TempDir()

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	createFile := reg["create_file"]

	// Try to create a file outside via traversal.
	args := mustJSON(t, map[string]any{
		"path":    "../../escape.txt",
		"content": "escaped!",
	})
	_, err := createFile.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err == nil {
		t.Fatal("expected error for path escape, got nil")
	}

	var pathErr *tools.PathEscapeError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected *tools.PathEscapeError, got %T: %v", err, err)
	}
}

// TestE2E_PathScoping_ListDir verifies list_dir blocks path escape.
// Go 1.26.4 filepath.Join does not discard prior elements on absolute paths,
// so "/../etc" is needed to trigger an actual escape.
func TestE2E_PathScoping_ListDir(t *testing.T) {
	tmpDir := t.TempDir()

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	listDir := reg["list_dir"]

	args := mustJSON(t, map[string]string{"path": "/../etc"})
	_, err := listDir.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err == nil {
		t.Fatal("expected error for absolute path escape, got nil")
	}

	var pathErr *tools.PathEscapeError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected *tools.PathEscapeError, got %T: %v", err, err)
	}
}

// TestE2E_PathScoping_RootPath verifies "." resolves to root without PathEscapeError.
// Reading the root via read_file should fail with "is a directory" (not path escape).
// Listing the root via list_dir should succeed.
func TestE2E_PathScoping_RootPath(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "root.txt"), []byte("at root"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))

	// list_dir "." on the root should work — no path escape.
	listDir := reg["list_dir"]
	args := mustJSON(t, map[string]string{"path": "."})
	result, err := listDir.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err != nil {
		t.Fatalf("list_dir(\".\") failed: %v", err)
	}
	ld := result.Data.(tools.ListDirResult)
	if ld.IsEmpty {
		t.Error("root directory should not be empty")
	}
	if !contains(ld.Entries, "root.txt") {
		t.Errorf("root listing should contain root.txt, got %v", ld.Entries)
	}

	// read_file "." should fail with "is a directory", NOT PathEscapeError.
	readFile := reg["read_file"]
	_, err = readFile.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err == nil {
		t.Fatal("read_file(\".\") should fail (is a directory)")
	}
	if strings.Contains(err.Error(), "escapes project root") {
		t.Fatalf("read_file(\".\") should NOT produce PathEscapeError, got: %v", err)
	}
	if !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("read_file(\".\") should fail with 'is a directory', got: %v", err)
	}
}

// TestE2E_PathScoping_SymlinkEscape verifies a symlink pointing outside root is blocked.
func TestE2E_PathScoping_SymlinkEscape(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file outside root.
	outside := filepath.Join(os.TempDir(), "symlink-target-"+t.Name()+".txt")
	if err := os.WriteFile(outside, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	// Create a symlink inside root pointing outside.
	symlinkPath := filepath.Join(tmpDir, "link.txt")
	if err := os.Symlink(outside, symlinkPath); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	readFile := reg["read_file"]

	// Try to read the symlink — should be blocked.
	args := mustJSON(t, map[string]string{"path": "link.txt"})
	_, err := readFile.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err == nil {
		t.Fatal("expected error for symlink escape, got nil")
	}

	var pathErr *tools.PathEscapeError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected *tools.PathEscapeError, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// Section 4: Per-agent glob scoping — ToolConfig.AllowPath enforcement
// ---------------------------------------------------------------------------

// TestE2E_GlobScoping_NoRestrictions verifies empty AllowedPaths allows all.
func TestE2E_GlobScoping_NoRestrictions(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "any.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	readFile := reg["read_file"]

	config := tools.ToolConfig{ProjectRoot: tmpDir, AllowedPaths: nil}
	args := mustJSON(t, map[string]string{"path": "any.txt"})
	_, err := readFile.Execute(context.Background(), args, config)
	if err != nil {
		t.Fatalf("read_file with no restrictions should succeed, got: %v", err)
	}
}

// TestE2E_GlobScoping_MatchingGlob allows access when glob matches.
func TestE2E_GlobScoping_MatchingGlob(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "docs", "adr-2.md"), []byte("ADR"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	readFile := reg["read_file"]

	config := tools.ToolConfig{ProjectRoot: tmpDir, AllowedPaths: []string{"docs/*.md"}}
	args := mustJSON(t, map[string]string{"path": "docs/adr-2.md"})
	_, err := readFile.Execute(context.Background(), args, config)
	if err != nil {
		t.Fatalf("read_file with matching glob should succeed, got: %v", err)
	}
}

// TestE2E_GlobScoping_NonMatchingGlob blocks access when glob doesn't match.
func TestE2E_GlobScoping_NonMatchingGlob(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "internal.go"), []byte("internal"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	readFile := reg["read_file"]

	config := tools.ToolConfig{ProjectRoot: tmpDir, AllowedPaths: []string{"docs/*.md"}}
	args := mustJSON(t, map[string]string{"path": "internal.go"})
	_, err := readFile.Execute(context.Background(), args, config)
	if err == nil {
		t.Fatal("expected DisallowedPathError for non-matching glob, got nil")
	}

	var disallowed *tools.DisallowedPathError
	if !errors.As(err, &disallowed) {
		t.Fatalf("expected *tools.DisallowedPathError, got %T: %v", err, err)
	}
}

// TestE2E_GlobScoping_ForensicAgentTools verifies forensic agents get only
// read_file, list_dir, and write_log.
func TestE2E_GlobScoping_ForensicAgentTools(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "phase-2.log")
	reg := tools.NewDefaultRegistry(tmpDir, logPath)

	// Forensic agent config from ADR §5.4.
	forensicConfig := tools.AgentToolConfig{
		"read_file": {},
		"list_dir":  {},
		"write_log": {},
	}

	filtered := reg.FilterByAgentConfig(forensicConfig)

	// Verify correct tools are present.
	for _, name := range []string{"read_file", "list_dir", "write_log"} {
		if _, ok := filtered[name]; !ok {
			t.Errorf("forensic agent missing tool %q", name)
		}
	}

	// Verify read-only: no create, edit, or bash.
	for _, name := range []string{"create_file", "edit_file", "bash_exec"} {
		if _, ok := filtered[name]; ok {
			t.Errorf("forensic agent should NOT have tool %q", name)
		}
	}
}

// TestE2E_GlobScoping_OmittedToolNotInFilteredRegistry verifies a tool is
// simply absent from the filtered registry when not granted.
func TestE2E_GlobScoping_OmittedToolNotInFilteredRegistry(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "phase-2.log")
	reg := tools.NewDefaultRegistry(tmpDir, logPath)

	// Config without create_file.
	config := tools.AgentToolConfig{
		"read_file": {},
		"edit_file": {},
		"list_dir":  {},
		"bash_exec": {},
		"write_log": {},
	}

	filtered := reg.FilterByAgentConfig(config)
	if _, ok := filtered["create_file"]; ok {
		t.Error("create_file should NOT be in filtered registry when omitted")
	}
	if _, ok := filtered["read_file"]; !ok {
		t.Error("read_file should be in filtered registry")
	}
}

// ---------------------------------------------------------------------------
// Section 5: bash_exec timeout — verify process is actually killed
// ---------------------------------------------------------------------------

// TestE2E_BashExec_TimeoutKillsProcess verifies that a timed-out command's
// process is truly killed (not just orphaned).
func TestE2E_BashExec_TimeoutKillsProcess(t *testing.T) {
	tmpDir := t.TempDir()

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	bashExec := reg["bash_exec"]

	// Create a temp file path the command will touch.
	startFile := filepath.Join(tmpDir, "started.marker")
	doneFile := filepath.Join(tmpDir, "done.marker")

	// Command: create marker, sleep long, create done marker.
	// With timeout much shorter than sleep, the process should be killed.
	// We'll also check process listing to confirm the process is gone.
	cmd := fmt.Sprintf("touch %s && sleep 30 && touch %s", startFile, doneFile)

	args := mustJSON(t, map[string]any{
		"command":         cmd,
		"timeout_seconds": 0.1, // 100ms — much shorter than 30s sleep
	})
	result, err := bashExec.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err != nil {
		t.Fatalf("bash_exec should return result (not error) on timeout: %v", err)
	}

	bd := result.Data.(tools.BashExecResult)
	if !bd.TimedOut {
		t.Errorf("TimedOut should be true, got false")
	}
	if bd.ExitCode != -1 {
		t.Errorf("ExitCode on timeout should be -1, got %d", bd.ExitCode)
	}

	// The start file should exist (process started before timeout).
	if _, err := os.Stat(startFile); os.IsNotExist(err) {
		t.Error("started.marker does not exist — process may not have started")
	}

	// The done file should NOT exist (process was killed before sleep finished).
	time.Sleep(50 * time.Millisecond) // let any racing writes settle
	if _, err := os.Stat(doneFile); err == nil {
		t.Fatal("done.marker exists — process was NOT killed by timeout")
	}

	// Verify no sleep process from our PID is still running.
	// Use "ps" to check for our specific sleep marker file.
	psCheck := exec.Command("sh", "-c",
		fmt.Sprintf("ps aux | grep 'touch %s' | grep -v grep || true", startFile))
	psOut, err := psCheck.Output()
	if err != nil {
		// ps check itself failed; this is informational, not a test failure.
		t.Logf("ps check failed (non-fatal): %v", err)
	} else if strings.TrimSpace(string(psOut)) != "" {
		t.Errorf("process may still be running:\n%s", string(psOut))
	}
}

// TestE2E_BashExec_TimeoutCapturesPartialOutput verifies partial stdout/stderr
// is captured after timeout.
func TestE2E_BashExec_TimeoutCapturesPartialOutput(t *testing.T) {
	tmpDir := t.TempDir()

	reg := tools.NewDefaultRegistry(tmpDir, filepath.Join(tmpDir, "phase-2.log"))
	bashExec := reg["bash_exec"]

	// Command that prints something, then sleeps, then prints more.
	cmd := "echo 'before sleep'; sleep 30; echo 'after sleep'"
	args := mustJSON(t, map[string]any{
		"command":         cmd,
		"timeout_seconds": 0.2, // 200ms
	})
	result, err := bashExec.Execute(context.Background(), args, allPathsAllowed(tmpDir))
	if err != nil {
		t.Fatalf("bash_exec should return result (not error) on timeout: %v", err)
	}

	bd := result.Data.(tools.BashExecResult)
	if !bd.TimedOut {
		t.Error("TimedOut should be true")
	}
	if bd.ExitCode != -1 {
		t.Errorf("ExitCode should be -1 on timeout, got %d", bd.ExitCode)
	}
	if !strings.Contains(bd.Stdout, "before sleep") {
		t.Errorf("partial stdout should contain 'before sleep', got: %q", bd.Stdout)
	}
	if strings.Contains(bd.Stdout, "after sleep") {
		t.Errorf("stdout should NOT contain 'after sleep' (process was killed): %q", bd.Stdout)
	}
}

// ---------------------------------------------------------------------------
// Section 6: bash_exec known gap is documented
// ---------------------------------------------------------------------------

// TestE2E_BashExec_GapDocumented verifies the known path-scoping gap is
// documented in the source code comments.
func TestE2E_BashExec_GapDocumented(t *testing.T) {
	// Read the bash_exec source file and verify it contains the gap documentation.
	src, err := os.ReadFile("../internal/tools/bash_exec.go")
	if err != nil {
		t.Fatalf("cannot read bash_exec.go: %v", err)
	}

	source := string(src)

	checks := []struct {
		keyword string
		reason  string
	}{
		{"Known gap", "must mention 'Known gap' or similar"},
		{"cd", "must mention cd as a bypass vector"},
		{"spec §6.1", "must reference the spec section documenting the accepted gap"},
		{"accepted", "must state the gap is accepted"},
		{"documented", "must state the gap is documented"},
	}
	for _, c := range checks {
		if !strings.Contains(source, c.keyword) {
			t.Errorf("bash_exec.go: missing %q — %s", c.keyword, c.reason)
		}
	}
}

// TestE2E_BashExec_GapInADR verifies the ADR documents the known gap.
func TestE2E_BashExec_GapInADR(t *testing.T) {
	src, err := os.ReadFile("../docs/adr-phase-2-tools-safety.md")
	if err != nil {
		t.Fatalf("cannot read ADR: %v", err)
	}

	source := string(src)

	checks := []struct {
		keyword string
		reason  string
	}{
		{"known gap", "ADR must document the known gap"},
		{"bash_exec", "gap discussion must reference bash_exec"},
		{"accepted", "must state gap is accepted"},
	}
	for _, c := range checks {
		if !strings.Contains(source, c.keyword) {
			t.Errorf("ADR missing %q — %s", c.keyword, c.reason)
		}
	}
}

// ---------------------------------------------------------------------------
// Section 7: Error types are distinguishable (register-level verification)
// ---------------------------------------------------------------------------

// TestE2E_ErrorTypes_Distinguishable verifies that all custom error types
// can be distinguished via errors.As.
func TestE2E_ErrorTypes_Distinguishable(t *testing.T) {
	// PathEscapeError
	var pathErr *tools.PathEscapeError
	if !errors.As(&tools.PathEscapeError{Path: "/a", Root: "/b"}, &pathErr) {
		t.Error("PathEscapeError should be distinguishable via errors.As")
	}

	// DisallowedPathError
	var disallowedErr *tools.DisallowedPathError
	if !errors.As(&tools.DisallowedPathError{Path: "/a", Globs: []string{"*"}}, &disallowedErr) {
		t.Error("DisallowedPathError should be distinguishable via errors.As")
	}

	// ErrNoMatch
	var noMatch *tools.ErrNoMatch
	if !errors.As(&tools.ErrNoMatch{Path: "a"}, &noMatch) {
		t.Error("ErrNoMatch should be distinguishable via errors.As")
	}

	// ErrAmbiguousMatch
	var ambig *tools.ErrAmbiguousMatch
	if !errors.As(&tools.ErrAmbiguousMatch{Path: "a", MatchesFound: 2}, &ambig) {
		t.Error("ErrAmbiguousMatch should be distinguishable via errors.As")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return data
}

func readFileStr(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return string(data)
}

func allPathsAllowed(root string) tools.ToolConfig {
	return tools.ToolConfig{ProjectRoot: root, AllowedPaths: nil}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
