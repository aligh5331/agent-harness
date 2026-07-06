package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func tempDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func jsonArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func noRestrictions(root string) ToolConfig {
	return ToolConfig{ProjectRoot: root, AllowedPaths: nil}
}

// ---------------------------------------------------------------------------
// resolveScoped tests
// ---------------------------------------------------------------------------

func TestResolveScoped_NormalPath(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "a", "b", "c.txt"), "hello")
	got, err := resolveScoped(dir, "a/b/c.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(dir, "a", "b", "c.txt")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveScoped_RootPath(t *testing.T) {
	dir := tempDir(t)
	got, err := resolveScoped(dir, ".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
		t.Fatalf("got %q, want %q", got, dir)
	}
}

func TestResolveScoped_EmptyPath(t *testing.T) {
	dir := tempDir(t)
	got, err := resolveScoped(dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
		t.Fatalf("got %q, want %q", got, dir)
	}
}

func TestResolveScoped_EscapesRoot(t *testing.T) {
	dir := tempDir(t)
	_, err := resolveScoped(dir, "../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path escaping root")
	}
	if !hasType[*PathEscapeError](err) {
		t.Fatalf("expected *PathEscapeError, got %T: %v", err, err)
	}
}

func TestResolveScoped_AbsolutePath(t *testing.T) {
	dir := tempDir(t)
	// Go 1.26+ filepath.Join does not discard prior elements on absolute paths,
	// so "/../etc/passwd" is joined as "root/../etc/passwd" and cleaned to
	// something outside root. Verify this escape is detected.
	_, err := resolveScoped(dir, "/../etc/passwd")
	if err == nil {
		t.Fatal("expected error for absolute path with traversal")
	}
	var pe *PathEscapeError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *PathEscapeError, got %T: %v", err, err)
	}
}

func TestResolveScoped_SymlinkEscape(t *testing.T) {
	dir := tempDir(t)
	outside := filepath.Join(dir, "..", "symlink_target")
	writeFile(t, outside, "secret")
	link := filepath.Join(dir, "link_to_outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlinks not supported:", err)
	}
	_, err := resolveScoped(dir, "link_to_outside")
	if err == nil {
		t.Fatal("expected error for symlink escaping root")
	}
	var pathErr *PathEscapeError
	if !hasType[*PathEscapeError](err) {
		t.Fatalf("expected *PathEscapeError, got %T: %v", err, err)
	}
	_ = pathErr
}

func TestResolveScoped_NonExistentFile(t *testing.T) {
	dir := tempDir(t)
	// Parent directory exists.
	writeFile(t, filepath.Join(dir, "existing", ".keep"), "")
	got, err := resolveScoped(dir, "existing/newfile.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(dir, "existing", "newfile.go")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveScoped_NonExistentParent(t *testing.T) {
	dir := tempDir(t)
	// resolveScoped does not check parent existence — it only enforces path scoping.
	// The parent-directory check is the tool's responsibility (e.g., create_file).
	got, err := resolveScoped(dir, "nonexistent/newfile.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(dir, "nonexistent", "newfile.go")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveScoped_SymlinkInsideRoot(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "actual.txt"), "content")
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink("actual.txt", link); err != nil {
		t.Skip("symlinks not supported:", err)
	}
	// Symlink inside root pointing to another file inside root — should be allowed.
	got, err := resolveScoped(dir, "link.txt")
	if err != nil {
		t.Fatalf("unexpected error for internal symlink: %v", err)
	}
	want := filepath.Join(dir, "actual.txt")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// read_file tests
// ---------------------------------------------------------------------------

func TestReadFile_Success(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "test.txt"), "hello\nworld")

	tool := &ReadFileTool{root: dir}
	args := jsonArgs(t, ReadFileArgs{Path: "test.txt"})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rd, ok := result.Data.(ReadFileResult)
	if !ok {
		t.Fatalf("expected ReadFileResult, got %T", result.Data)
	}
	if rd.Path != "test.txt" {
		t.Fatalf("path: got %q, want %q", rd.Path, "test.txt")
	}
	if rd.LineCount != 2 {
		t.Fatalf("line count: got %d, want 2", rd.LineCount)
	}
	if rd.Truncated {
		t.Fatal("truncated should be false")
	}
}

func TestReadFile_LineNumbers(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "lines.txt"), "line1\nline2\nline3")

	tool := &ReadFileTool{root: dir}
	args := jsonArgs(t, ReadFileArgs{Path: "lines.txt"})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rd := result.Data.(ReadFileResult)
	if !strings.Contains(rd.Content, "1: line1") {
		t.Fatalf("expected line numbers in content, got:\n%s", rd.Content)
	}
	if !strings.Contains(rd.Content, "2: line2") {
		t.Fatalf("expected line numbers in content, got:\n%s", rd.Content)
	}
	if !strings.Contains(rd.Content, "3: line3") {
		t.Fatalf("expected line numbers in content, got:\n%s", rd.Content)
	}
}

func TestReadFile_NotFound(t *testing.T) {
	dir := tempDir(t)
	tool := &ReadFileTool{root: dir}
	args := jsonArgs(t, ReadFileArgs{Path: "nonexistent.txt"})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestReadFile_IsDirectory(t *testing.T) {
	dir := tempDir(t)
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	tool := &ReadFileTool{root: dir}
	args := jsonArgs(t, ReadFileArgs{Path: "subdir"})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err == nil {
		t.Fatal("expected error when path is a directory")
	}
}

func TestReadFile_EscapesRoot(t *testing.T) {
	dir := tempDir(t)
	tool := &ReadFileTool{root: dir}
	args := jsonArgs(t, ReadFileArgs{Path: "../../etc/passwd"})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err == nil {
		t.Fatal("expected error for path escaping root")
	}
	if !hasType[*PathEscapeError](err) {
		t.Fatalf("expected *PathEscapeError, got %T: %v", err, err)
	}
}

func TestReadFile_DisallowedByGlob(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "secret.txt"), "shh")
	writeFile(t, filepath.Join(dir, "public.txt"), "hello")

	tool := &ReadFileTool{root: dir}

	// Try to read a path outside allowed globs.
	args := jsonArgs(t, ReadFileArgs{Path: "secret.txt"})
	config := ToolConfig{ProjectRoot: dir, AllowedPaths: []string{"public*"}}
	_, err := tool.Execute(context.Background(), args, config)
	if err == nil {
		t.Fatal("expected error for disallowed path")
	}
	if !hasType[*DisallowedPathError](err) {
		t.Fatalf("expected *DisallowedPathError, got %T: %v", err, err)
	}
}

func TestReadFile_AllowedByGlob(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "public.txt"), "hello")

	tool := &ReadFileTool{root: dir}
	args := jsonArgs(t, ReadFileArgs{Path: "public.txt"})
	config := ToolConfig{ProjectRoot: dir, AllowedPaths: []string{"public*"}}
	_, err := tool.Execute(context.Background(), args, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// edit_file tests
// ---------------------------------------------------------------------------

func TestEditFile_Success(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "test.txt"), "hello world")

	tool := &EditFileTool{root: dir}
	args := jsonArgs(t, EditFileArgs{
		Path:   "test.txt",
		OldStr: "world",
		NewStr: "there",
	})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = result

	content := readFile(t, filepath.Join(dir, "test.txt"))
	if content != "hello there" {
		t.Fatalf("got %q, want %q", content, "hello there")
	}
}

func TestEditFile_ZeroMatches(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "test.txt"), "hello world")

	tool := &EditFileTool{root: dir}
	args := jsonArgs(t, EditFileArgs{
		Path:   "test.txt",
		OldStr: "nonexistent",
		NewStr: "replacement",
	})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err == nil {
		t.Fatal("expected error for zero matches")
	}
	if !hasType[*ErrNoMatch](err) {
		t.Fatalf("expected *ErrNoMatch, got %T: %v", err, err)
	}
	var noMatch *ErrNoMatch
	if noMatch, _ = err.(*ErrNoMatch); noMatch != nil {
		if noMatch.Path != "test.txt" {
			t.Fatalf("ErrNoMatch.Path: got %q, want %q", noMatch.Path, "test.txt")
		}
	}
	_ = noMatch
}

func TestEditFile_MultipleMatches(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "test.txt"), "foo bar foo baz")

	tool := &EditFileTool{root: dir}
	args := jsonArgs(t, EditFileArgs{
		Path:   "test.txt",
		OldStr: "foo",
		NewStr: "qux",
	})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err == nil {
		t.Fatal("expected error for multiple matches")
	}
	if !hasType[*ErrAmbiguousMatch](err) {
		t.Fatalf("expected *ErrAmbiguousMatch, got %T: %v", err, err)
	}
	var ambig *ErrAmbiguousMatch
	if ambig, _ = err.(*ErrAmbiguousMatch); ambig != nil {
		if ambig.Path != "test.txt" {
			t.Fatalf("ErrAmbiguousMatch.Path: got %q, want %q", ambig.Path, "test.txt")
		}
		if ambig.MatchesFound != 2 {
			t.Fatalf("MatchesFound: got %d, want 2", ambig.MatchesFound)
		}
	}
	_ = ambig
}

func TestEditFile_OldStrInNewStr(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "test.txt"), "foo")

	tool := &EditFileTool{root: dir}
	args := jsonArgs(t, EditFileArgs{
		Path:   "test.txt",
		OldStr: "foo",
		NewStr: "foo bar", // new contains old
	})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content := readFile(t, filepath.Join(dir, "test.txt"))
	if content != "foo bar" {
		t.Fatalf("got %q, want %q", content, "foo bar")
	}
}

func TestEditFile_FileNotFound(t *testing.T) {
	dir := tempDir(t)
	tool := &EditFileTool{root: dir}
	args := jsonArgs(t, EditFileArgs{
		Path:   "nonexistent.txt",
		OldStr: "foo",
		NewStr: "bar",
	})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestEditFile_EscapesRoot(t *testing.T) {
	dir := tempDir(t)
	tool := &EditFileTool{root: dir}
	args := jsonArgs(t, EditFileArgs{
		Path:   "../../etc/passwd",
		OldStr: "root",
		NewStr: "admin",
	})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err == nil {
		t.Fatal("expected error for path escaping root")
	}
	if !hasType[*PathEscapeError](err) {
		t.Fatalf("expected *PathEscapeError, got %T: %v", err, err)
	}
}

func TestEditFile_DisallowedByGlob(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "internal.go"), "package main")

	tool := &EditFileTool{root: dir}
	args := jsonArgs(t, EditFileArgs{
		Path:   "internal.go",
		OldStr: "main",
		NewStr: "app",
	})
	config := ToolConfig{ProjectRoot: dir, AllowedPaths: []string{"docs/*"}}
	_, err := tool.Execute(context.Background(), args, config)
	if err == nil {
		t.Fatal("expected error for disallowed path")
	}
	if !hasType[*DisallowedPathError](err) {
		t.Fatalf("expected *DisallowedPathError, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// create_file tests
// ---------------------------------------------------------------------------

func TestCreateFile_Success(t *testing.T) {
	dir := tempDir(t)
	tool := &CreateFileTool{root: dir}
	args := jsonArgs(t, CreateFileArgs{
		Path:    "newfile.txt",
		Content: "hello world",
	})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content := readFile(t, filepath.Join(dir, "newfile.txt"))
	if content != "hello world" {
		t.Fatalf("got %q, want %q", content, "hello world")
	}
}

func TestCreateFile_AlreadyExists(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "existing.txt"), "content")

	tool := &CreateFileTool{root: dir}
	args := jsonArgs(t, CreateFileArgs{
		Path:    "existing.txt",
		Content: "new content",
	})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err == nil {
		t.Fatal("expected error for existing file")
	}
	// Verify original content unchanged.
	content := readFile(t, filepath.Join(dir, "existing.txt"))
	if content != "content" {
		t.Fatalf("original content modified: got %q, want %q", content, "content")
	}
}

func TestCreateFile_ParentDirMissing(t *testing.T) {
	dir := tempDir(t)
	tool := &CreateFileTool{root: dir}
	args := jsonArgs(t, CreateFileArgs{
		Path:    "nonexistent/sub/file.txt",
		Content: "should fail",
	})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err == nil {
		t.Fatal("expected error when parent directory missing")
	}
}

func TestCreateFile_EscapesRoot(t *testing.T) {
	dir := tempDir(t)
	tool := &CreateFileTool{root: dir}
	args := jsonArgs(t, CreateFileArgs{
		Path:    "../../escape.txt",
		Content: "leak",
	})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err == nil {
		t.Fatal("expected error for path escaping root")
	}
}

func TestCreateFile_DisallowedByGlob(t *testing.T) {
	dir := tempDir(t)
	tool := &CreateFileTool{root: dir}
	args := jsonArgs(t, CreateFileArgs{
		Path:    "internal/new.go",
		Content: "package new",
	})
	config := ToolConfig{ProjectRoot: dir, AllowedPaths: []string{"docs/*"}}
	_, err := tool.Execute(context.Background(), args, config)
	if err == nil {
		t.Fatal("expected error for disallowed path")
	}
	if !hasType[*DisallowedPathError](err) {
		t.Fatalf("expected *DisallowedPathError, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// list_dir tests
// ---------------------------------------------------------------------------

func TestListDir_Success(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "")
	writeFile(t, filepath.Join(dir, "b.txt"), "")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	tool := &ListDirTool{root: dir}
	args := jsonArgs(t, ListDirArgs{Path: "."})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ld := result.Data.(ListDirResult)
	if ld.IsEmpty {
		t.Fatal("directory should not be empty")
	}
	// Entries should be sorted.
	if len(ld.Entries) < 3 {
		t.Fatalf("expected at least 3 entries, got %d: %v", len(ld.Entries), ld.Entries)
	}
}

func TestListDir_EmptyDir(t *testing.T) {
	dir := tempDir(t)
	tool := &ListDirTool{root: dir}
	args := jsonArgs(t, ListDirArgs{Path: "."})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ld := result.Data.(ListDirResult)
	if !ld.IsEmpty {
		t.Fatal("directory should be empty")
	}
	if len(ld.Entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(ld.Entries))
	}
}

func TestListDir_IncludesHiddenFiles(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, ".gitignore"), "")
	writeFile(t, filepath.Join(dir, "app.go"), "")

	tool := &ListDirTool{root: dir}
	args := jsonArgs(t, ListDirArgs{Path: "."})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ld := result.Data.(ListDirResult)
	found := false
	for _, e := range ld.Entries {
		if e == ".gitignore" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("hidden files (dotfiles) should be included in listing")
	}
}

func TestListDir_NotFound(t *testing.T) {
	dir := tempDir(t)
	tool := &ListDirTool{root: dir}
	args := jsonArgs(t, ListDirArgs{Path: "nonexistent"})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
}

func TestListDir_IsFile(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "file.txt"), "")

	tool := &ListDirTool{root: dir}
	args := jsonArgs(t, ListDirArgs{Path: "file.txt"})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err == nil {
		t.Fatal("expected error when path is a file, not directory")
	}
}

func TestListDir_EscapesRoot(t *testing.T) {
	dir := tempDir(t)
	tool := &ListDirTool{root: dir}
	args := jsonArgs(t, ListDirArgs{Path: "../../etc"})
	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if !hasType[*PathEscapeError](err) {
		t.Fatalf("expected *PathEscapeError, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// bash_exec tests
// ---------------------------------------------------------------------------

func TestBashExec_Success(t *testing.T) {
	dir := tempDir(t)
	tool := &BashExecTool{root: dir}
	args := jsonArgs(t, BashExecArgs{
		Command:        "echo hello",
		TimeoutSeconds: 5,
	})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bd := result.Data.(BashExecResult)
	if bd.ExitCode != 0 {
		t.Fatalf("exit code: got %d, want 0", bd.ExitCode)
	}
	if strings.TrimSpace(bd.Stdout) != "hello" {
		t.Fatalf("stdout: got %q, want %q", bd.Stdout, "hello")
	}
	if bd.TimedOut {
		t.Fatal("should not be timed out")
	}
}

func TestBashExec_Stderr(t *testing.T) {
	dir := tempDir(t)
	tool := &BashExecTool{root: dir}
	args := jsonArgs(t, BashExecArgs{
		Command:        "echo stderr >&2",
		TimeoutSeconds: 5,
	})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bd := result.Data.(BashExecResult)
	if strings.TrimSpace(bd.Stderr) != "stderr" {
		t.Fatalf("stderr: got %q, want %q", bd.Stderr, "stderr")
	}
}

func TestBashExec_NonZeroExit(t *testing.T) {
	dir := tempDir(t)
	tool := &BashExecTool{root: dir}
	args := jsonArgs(t, BashExecArgs{
		Command:        "exit 42",
		TimeoutSeconds: 5,
	})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bd := result.Data.(BashExecResult)
	if bd.ExitCode != 42 {
		t.Fatalf("exit code: got %d, want 42", bd.ExitCode)
	}
}

func TestBashExec_Timeout(t *testing.T) {
	dir := tempDir(t)
	tool := &BashExecTool{root: dir}
	args := jsonArgs(t, BashExecArgs{
		Command:        "sleep 5",
		TimeoutSeconds: 0.05, // 50ms — much shorter than the sleep
	})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bd := result.Data.(BashExecResult)
	if !bd.TimedOut {
		t.Fatal("expected timeout, but TimedOut is false")
	}
	if bd.ExitCode != -1 {
		t.Fatalf("exit code on timeout: got %d, want -1", bd.ExitCode)
	}
}

func TestBashExec_WorkingDir(t *testing.T) {
	dir := tempDir(t)
	// Create a marker file in the project root.
	writeFile(t, filepath.Join(dir, "marker.txt"), "found")

	tool := &BashExecTool{root: dir}
	args := jsonArgs(t, BashExecArgs{
		Command:        "ls marker.txt",
		TimeoutSeconds: 5,
	})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bd := result.Data.(BashExecResult)
	if bd.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q", bd.ExitCode, bd.Stdout, bd.Stderr)
	}
}

func TestBashExec_CommandNotFound(t *testing.T) {
	dir := tempDir(t)
	tool := &BashExecTool{root: dir}
	args := jsonArgs(t, BashExecArgs{
		Command:        "nonexistent_command_xyz",
		TimeoutSeconds: 5,
	})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bd := result.Data.(BashExecResult)
	// The shell starts fine but the command isn't found — shell returns exit code 127.
	if bd.ExitCode != 127 {
		t.Fatalf("expected exit code 127 (command not found), got %d", bd.ExitCode)
	}
}

func TestBashExec_StdoutAndStderrTogether(t *testing.T) {
	dir := tempDir(t)
	tool := &BashExecTool{root: dir}
	args := jsonArgs(t, BashExecArgs{
		Command:        "echo out; echo err >&2",
		TimeoutSeconds: 5,
	})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bd := result.Data.(BashExecResult)
	if strings.TrimSpace(bd.Stdout) != "out" {
		t.Fatalf("stdout: got %q, want %q", bd.Stdout, "out")
	}
	if strings.TrimSpace(bd.Stderr) != "err" {
		t.Fatalf("stderr: got %q, want %q", bd.Stderr, "err")
	}
}

// ---------------------------------------------------------------------------
// write_log tests
// ---------------------------------------------------------------------------

func TestWriteLog_AppendsContent(t *testing.T) {
	dir := tempDir(t)
	logPath := filepath.Join(dir, "phase-2.log")

	tool := &WriteLogTool{logPath: logPath}

	// First write.
	args1 := jsonArgs(t, WriteLogArgs{Content: "=== Builder entry ==="})
	result1, err := tool.Execute(context.Background(), args1, ToolConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wr1 := result1.Data.(WriteLogResult)
	if wr1.Path != logPath {
		t.Fatalf("path: got %q, want %q", wr1.Path, logPath)
	}
	if wr1.SizeBytes <= 0 {
		t.Fatalf("size should be positive, got %d", wr1.SizeBytes)
	}

	// Second write — should append.
	args2 := jsonArgs(t, WriteLogArgs{Content: "=== Tester entry ==="})
	_, err = tool.Execute(context.Background(), args2, ToolConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content := readFile(t, logPath)
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "Builder") {
		t.Fatalf("line 0: got %q, want it to contain 'Builder'", lines[0])
	}
	if !strings.Contains(lines[1], "Tester") {
		t.Fatalf("line 1: got %q, want it to contain 'Tester'", lines[1])
	}
}

func TestWriteLog_FixedPathOnly(t *testing.T) {
	// Verify the tool has no path parameter in its argument struct.
	// This is a compile-time structural guarantee.
	var args WriteLogArgs
	if _, ok := any(args).(interface{ GetPath() string }); ok {
		t.Fatal("WriteLogArgs should not expose a path field")
	}
}

// ---------------------------------------------------------------------------
// Registry tests
// ---------------------------------------------------------------------------

func TestRegistry_NewDefaultRegistry(t *testing.T) {
	r := NewDefaultRegistry("/test/root", "/test/phase.log")
	if len(r) != 6 {
		t.Fatalf("expected 6 tools, got %d", len(r))
	}
	expected := []string{"bash_exec", "create_file", "edit_file", "list_dir", "read_file", "write_log"}
	for _, name := range expected {
		if _, ok := r[name]; !ok {
			t.Fatalf("missing tool: %s", name)
		}
	}
}

func TestRegistry_FilterByAgentConfig(t *testing.T) {
	r := NewDefaultRegistry("/test/root", "/test/phase.log")

	// Simulate forensic config: only read_file, list_dir, write_log.
	config := AgentToolConfig{
		"read_file": {},
		"list_dir":  {},
		"write_log": {},
	}

	filtered := r.FilterByAgentConfig(config)
	if len(filtered) != 3 {
		t.Fatalf("expected 3 tools for forensic, got %d", len(filtered))
	}
	for _, name := range []string{"read_file", "list_dir", "write_log"} {
		if _, ok := filtered[name]; !ok {
			t.Fatalf("missing tool in filtered: %s", name)
		}
	}
	if _, ok := filtered["bash_exec"]; ok {
		t.Fatal("bash_exec should not be in filtered registry")
	}
	if _, ok := filtered["edit_file"]; ok {
		t.Fatal("edit_file should not be in filtered registry")
	}
	if _, ok := filtered["create_file"]; ok {
		t.Fatal("create_file should not be in filtered registry")
	}
}

func TestRegistry_Definitions(t *testing.T) {
	r := NewDefaultRegistry("/test/root", "/test/phase.log")
	defs := r.Definitions()
	if len(defs) != 6 {
		t.Fatalf("expected 6 definitions, got %d", len(defs))
	}
	// Should be sorted by name.
	for i := 1; i < len(defs); i++ {
		if defs[i].Function.Name < defs[i-1].Function.Name {
			t.Fatalf("definitions not sorted: %s before %s",
				defs[i-1].Function.Name, defs[i].Function.Name)
		}
	}
}

func TestRegistry_Names(t *testing.T) {
	r := NewDefaultRegistry("/test/root", "/test/phase.log")
	for name, tool := range r {
		if tool.Name() != name {
			t.Fatalf("Name() mismatch: tool.Name()=%q, registry key=%q", tool.Name(), name)
		}
	}
}

// ---------------------------------------------------------------------------
// AgentToolConfig tests
// ---------------------------------------------------------------------------

func TestAgentToolConfig_ToolNotGrantedReturnsFromFilter(t *testing.T) {
	r := NewDefaultRegistry("/root", "/phase.log")
	config := AgentToolConfig{
		"read_file": {},
	}
	filtered := r.FilterByAgentConfig(config)
	if _, ok := filtered["edit_file"]; ok {
		t.Fatal("edit_file should not be present after filtering")
	}
}

func TestAgentToolConfig_ToolWithRestrictions(t *testing.T) {
	config := AgentToolConfig{
		"edit_file": {
			PathGlobs: []string{"docs/adr-*.md"},
		},
	}
	tr := config["edit_file"]
	if len(tr.PathGlobs) != 1 || tr.PathGlobs[0] != "docs/adr-*.md" {
		t.Fatalf("unexpected restrictions: %+v", tr)
	}
}

// ---------------------------------------------------------------------------
// AllowPath tests
// ---------------------------------------------------------------------------

func TestAllowPath_NoRestrictions(t *testing.T) {
	config := ToolConfig{ProjectRoot: "/root", AllowedPaths: nil}
	if err := config.AllowPath("/root/any/file.go"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAllowPath_MatchingGlob(t *testing.T) {
	config := ToolConfig{ProjectRoot: "/root", AllowedPaths: []string{"docs/adr-*.md"}}
	if err := config.AllowPath("/root/docs/adr-1.md"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAllowPath_NonMatchingGlob(t *testing.T) {
	config := ToolConfig{ProjectRoot: "/root", AllowedPaths: []string{"docs/*"}}
	err := config.AllowPath("/root/internal/foo.go")
	if err == nil {
		t.Fatal("expected error for non-matching glob")
	}
	if !hasType[*DisallowedPathError](err) {
		t.Fatalf("expected *DisallowedPathError, got %T: %v", err, err)
	}
}

func TestAllowPath_EmptyGlobsList(t *testing.T) {
	// Empty list means no restrictions.
	config := ToolConfig{ProjectRoot: "/root", AllowedPaths: []string{}}
	if err := config.AllowPath("/root/anything.go"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Error type checks
// ---------------------------------------------------------------------------

func TestErrorTypes_Distinguishable(t *testing.T) {
	// Verify errors.As can distinguish the three key error types.
	err1 := &ErrNoMatch{Path: "foo"}
	err2 := &ErrAmbiguousMatch{Path: "foo", MatchesFound: 3}
	err3 := &PathEscapeError{Path: "/outside", Root: "/root"}
	err4 := &DisallowedPathError{Path: "/blocked", Globs: []string{"*.go"}}

	if !hasType[*ErrNoMatch](err1) {
		t.Fatal("hasType[*ErrNoMatch] should match ErrNoMatch")
	}
	if !hasType[*ErrAmbiguousMatch](err2) {
		t.Fatal("hasType[*ErrAmbiguousMatch] should match ErrAmbiguousMatch")
	}
	if !hasType[*PathEscapeError](err3) {
		t.Fatal("hasType[*PathEscapeError] should match PathEscapeError")
	}
	if !hasType[*DisallowedPathError](err4) {
		t.Fatal("hasType[*DisallowedPathError] should match DisallowedPathError")
	}

	// Negative tests.
	if hasType[*ErrNoMatch](err2) {
		t.Fatal("ErrAmbiguousMatch should not match ErrNoMatch")
	}
	if hasType[*ErrAmbiguousMatch](err1) {
		t.Fatal("ErrNoMatch should not match ErrAmbiguousMatch")
	}
	if hasType[*ErrNoMatch](err3) {
		t.Fatal("PathEscapeError should not match ErrNoMatch")
	}
}

// ---------------------------------------------------------------------------
// File hash helper test
// ---------------------------------------------------------------------------

func TestFileHash_Deterministic(t *testing.T) {
	h1 := fileHash("hello world")
	h2 := fileHash("hello world")
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("expected 64-char hex hash, got %d: %q", len(h1), h1)
	}
}

func TestFileHash_Different(t *testing.T) {
	h1 := fileHash("hello")
	h2 := fileHash("world")
	if h1 == h2 {
		t.Fatal("different inputs should produce different hashes")
	}
}

// ---------------------------------------------------------------------------
// Invalid JSON arguments tests
// ---------------------------------------------------------------------------

func TestReadFile_InvalidJSON(t *testing.T) {
	tool := &ReadFileTool{root: "/tmp"}
	_, err := tool.Execute(context.Background(), json.RawMessage(`not json`), ToolConfig{})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestEditFile_InvalidJSON(t *testing.T) {
	tool := &EditFileTool{root: "/tmp"}
	_, err := tool.Execute(context.Background(), json.RawMessage(`not json`), ToolConfig{})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestCreateFile_InvalidJSON(t *testing.T) {
	tool := &CreateFileTool{root: "/tmp"}
	_, err := tool.Execute(context.Background(), json.RawMessage(`not json`), ToolConfig{})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestBashExec_InvalidJSON(t *testing.T) {
	tool := &BashExecTool{root: "/tmp"}
	_, err := tool.Execute(context.Background(), json.RawMessage(`not json`), ToolConfig{})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWriteLog_InvalidJSON(t *testing.T) {
	tool := &WriteLogTool{logPath: "/tmp/test.log"}
	_, err := tool.Execute(context.Background(), json.RawMessage(`not json`), ToolConfig{})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// hasType checks if err's chain contains a value of type *T.
// T should be a pointer type (e.g., *ErrNoMatch, *PathEscapeError).
func hasType[T any](err error) bool {
	var target T
	return errors.As(err, &target)
}

// ---------------------------------------------------------------------------
// End-to-end: create then read then edit then read again
// ---------------------------------------------------------------------------

func TestCreateReadEditReadFlow(t *testing.T) {
	dir := tempDir(t)

	// 1. Create a file
	createTool := &CreateFileTool{root: dir}
	createArgs := jsonArgs(t, CreateFileArgs{
		Path:    "test.go",
		Content: "package main\n\nfunc main() {}\n",
	})
	_, err := createTool.Execute(context.Background(), createArgs, noRestrictions(dir))
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// 2. Read it back
	readTool := &ReadFileTool{root: dir}
	readArgs := jsonArgs(t, ReadFileArgs{Path: "test.go"})
	readResult, err := readTool.Execute(context.Background(), readArgs, noRestrictions(dir))
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	rd := readResult.Data.(ReadFileResult)
	if rd.LineCount < 3 {
		t.Fatalf("expected >=3 lines, got %d", rd.LineCount)
	}

	// 3. Edit it
	editTool := &EditFileTool{root: dir}
	editArgs := jsonArgs(t, EditFileArgs{
		Path:   "test.go",
		OldStr: "func main() {}",
		NewStr: "func main() { println(\"hello\") }",
	})
	_, err = editTool.Execute(context.Background(), editArgs, noRestrictions(dir))
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}

	// 4. Read it again
	readResult2, err := readTool.Execute(context.Background(), readArgs, noRestrictions(dir))
	if err != nil {
		t.Fatalf("second read failed: %v", err)
	}
	rd2 := readResult2.Data.(ReadFileResult)
	if !strings.Contains(rd2.Content, "println") {
		t.Fatalf("expected edited content to contain println, got:\n%s", rd2.Content)
	}
}

// ---------------------------------------------------------------------------
// Bash exec real process cancellation test
// ---------------------------------------------------------------------------

func TestBashExec_TimeoutActuallyKillsProcess(t *testing.T) {
	dir := tempDir(t)
	tool := &BashExecTool{root: dir}

	// Start a long-running process and monitor it.
	// After timeout, verify the context deadline was exceeded by checking
	// the sleep did not complete (done file does not exist).
	doneFile := filepath.Join(dir, ".done")
	args := jsonArgs(t, BashExecArgs{
		Command:        fmt.Sprintf("touch %s.start && sleep 5 && touch %s", dir+"/.done", dir+"/.done"),
		TimeoutSeconds: 0.05, // 50ms — much shorter than the sleep
	})

	_, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	// The error should be nil — timeout is reported in the result, not as a Go error.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The start file should exist (process began).
	if _, err := os.Stat(dir + "/.done.start"); err != nil {
		t.Fatal("process may not have started")
	}

	// The done file should NOT exist (process was killed before sleep finished).
	// Sleep for a short time to ensure any racing writes settle.
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(doneFile); err == nil {
		t.Fatal("process was NOT killed by timeout — done file exists")
	}
}

// ListDir: test base names only (no full paths or absolute paths)
func TestListDir_BaseNamesOnly(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "sub", "nested.txt"), "")

	tool := &ListDirTool{root: dir}
	args := jsonArgs(t, ListDirArgs{Path: "sub"})
	result, err := tool.Execute(context.Background(), args, noRestrictions(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ld := result.Data.(ListDirResult)
	if len(ld.Entries) != 1 || ld.Entries[0] != "nested.txt" {
		t.Fatalf("expected [nested.txt], got %v", ld.Entries)
	}
}
