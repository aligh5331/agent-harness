package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-harness/internal/loop"
	"agent-harness/internal/tools"
)

// ---------------------------------------------------------------------------
// Parser tests
// ---------------------------------------------------------------------------

func TestParseAgentConfigBytes_Basic(t *testing.T) {
	data := []byte(`---
name: builder
model: deepseek-v4-flash
base_url: https://api.test/v1
context_max_tokens: 32768
temperature: 0.2
tools:
  read_file: {}
  edit_file: {paths: ["*.go"]}
  bash_exec: null
---

You are a builder.
`)
	cfg, err := ParseAgentConfigBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Name != "builder" {
		t.Errorf("Name = %q, want %q", cfg.Name, "builder")
	}
	if cfg.ModelName != "deepseek-v4-flash" {
		t.Errorf("ModelName = %q, want %q", cfg.ModelName, "deepseek-v4-flash")
	}
	if cfg.BaseURL != "https://api.test/v1" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "https://api.test/v1")
	}
	if cfg.ContextMaxTokens != 32768 {
		t.Errorf("ContextMaxTokens = %d, want %d", cfg.ContextMaxTokens, 32768)
	}
	if cfg.Temperature != 0.2 {
		t.Errorf("Temperature = %f, want %f", cfg.Temperature, 0.2)
	}
	if cfg.SystemPrompt != "You are a builder." {
		t.Errorf("SystemPrompt = %q, want %q", cfg.SystemPrompt, "You are a builder.")
	}

	// Tools: read_file granted, edit_file granted with path, bash_exec denied.
	if _, ok := cfg.Tools["read_file"]; !ok {
		t.Error("read_file should be granted")
	}
	if _, ok := cfg.Tools["bash_exec"]; ok {
		t.Error("bash_exec should NOT be granted (null)")
	}
	if _, ok := cfg.Tools["create_file"]; ok {
		t.Error("create_file should NOT be granted (absent)")
	}

	// edit_file should have path restrictions.
	editRest, ok := cfg.Tools["edit_file"]
	if !ok {
		t.Fatal("edit_file should be granted")
	}
	if len(editRest.PathGlobs) != 1 || editRest.PathGlobs[0] != "*.go" {
		t.Errorf("edit_file.PathGlobs = %v, want [\"*.go\"]", editRest.PathGlobs)
	}
}

func TestParseAgentConfigBytes_CreateFileRestriction(t *testing.T) {
	data := []byte(`---
name: restricted
model: m1
base_url: http://localhost/v1
tools:
  create_file:
    paths:
      - ".aa/templates/*.yaml"
      - ".aa/agents/*.md"
---
`)
	cfg, err := ParseAgentConfigBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	createRest, ok := cfg.Tools["create_file"]
	if !ok {
		t.Fatal("create_file should be granted")
	}
	if len(createRest.PathGlobs) != 2 {
		t.Fatalf("expected 2 path globs, got %d", len(createRest.PathGlobs))
	}
	if createRest.PathGlobs[0] != ".aa/templates/*.yaml" || createRest.PathGlobs[1] != ".aa/agents/*.md" {
		t.Errorf("create_file.PathGlobs = %v, want [\"%s\", \"%s\"]",
			createRest.PathGlobs, ".aa/templates/*.yaml", ".aa/agents/*.md")
	}
}

func TestParseAgentConfigBytes_NoFrontmatter(t *testing.T) {
	data := []byte("Just a plain system prompt.")
	cfg, err := ParseAgentConfigBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SystemPrompt != "Just a plain system prompt." {
		t.Errorf("SystemPrompt = %q, want %q", cfg.SystemPrompt, "Just a plain system prompt.")
	}
	if cfg.Tools == nil {
		t.Error("Tools should be non-nil (empty map)")
	}
	if len(cfg.Tools) != 0 {
		t.Errorf("Tools should be empty, got %d entries", len(cfg.Tools))
	}
}

func TestParseAgentConfigBytes_OnlyFrontmatterNoBody(t *testing.T) {
	data := []byte(`---
name: minimal
model: m1
base_url: http://localhost/v1
---
`)
	cfg, err := ParseAgentConfigBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "minimal" {
		t.Errorf("Name = %q, want %q", cfg.Name, "minimal")
	}
	if cfg.SystemPrompt != "" {
		t.Errorf("SystemPrompt should be empty, got %q", cfg.SystemPrompt)
	}
}

func TestParseAgentConfigBytes_EndOfFileClose(t *testing.T) {
	// Closing --- at end of file with no trailing content.
	data := []byte(`---
name: eof
model: m2
base_url: http://localhost/v1
---`)
	cfg, err := ParseAgentConfigBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "eof" {
		t.Errorf("Name = %q, want %q", cfg.Name, "eof")
	}
}

func TestParseAgentConfigBytes_WindowsLineEndings(t *testing.T) {
	data := []byte("---\r\nname: win\r\nmodel: m3\r\nbase_url: http://localhost/v1\r\n---\r\nBody\r\n")
	cfg, err := ParseAgentConfigBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "win" {
		t.Errorf("Name = %q, want %q", cfg.Name, "win")
	}
	if cfg.SystemPrompt != "Body" {
		t.Errorf("SystemPrompt = %q, want %q", cfg.SystemPrompt, "Body")
	}
}

func TestParseAgentConfigBytes_MissingName(t *testing.T) {
	data := []byte(`---
model: m1
base_url: http://localhost/v1
---
body
`)
	_, err := ParseAgentConfigBytes(data)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("expected error about missing 'name', got: %v", err)
	}
}

func TestParseAgentConfigBytes_MissingModel(t *testing.T) {
	data := []byte(`---
name: test
base_url: http://localhost/v1
---
body
`)
	_, err := ParseAgentConfigBytes(data)
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Errorf("expected error about missing 'model', got: %v", err)
	}
}

func TestParseAgentConfigBytes_MissingBaseURL(t *testing.T) {
	data := []byte(`---
name: test
model: m1
---
body
`)
	_, err := ParseAgentConfigBytes(data)
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("expected error about missing 'base_url', got: %v", err)
	}
}

func TestParseAgentConfigBytes_EmptyFile(t *testing.T) {
	data := []byte{}
	_, err := ParseAgentConfigBytes(data)
	if err == nil {
		t.Error("expected error for empty file")
	}
}

func TestParseAgentConfigBytes_NoClosingDelimiter(t *testing.T) {
	data := []byte(`---
name: test
model: m1
base_url: http://localhost/v1
`)
	_, err := ParseAgentConfigBytes(data)
	if err == nil || !strings.Contains(err.Error(), "closing ---") {
		t.Errorf("expected error about missing closing ---, got: %v", err)
	}
}

func TestParseAgentConfigBytes_ToolsBlockAbsent(t *testing.T) {
	data := []byte(`---
name: notools
model: m1
base_url: http://localhost/v1
---
Body
`)
	cfg, err := ParseAgentConfigBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Tools) != 0 {
		t.Errorf("expected empty Tools, got %d entries", len(cfg.Tools))
	}
}

func TestParseAgentConfigBytes_MaxFileWritesOptional(t *testing.T) {
	data := []byte(`---
name: writes
model: m1
base_url: http://localhost/v1
max_file_writes: 3
---
`)
	cfg, err := ParseAgentConfigBytes(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxFileWrites != 3 {
		t.Errorf("MaxFileWrites = %d, want 3", cfg.MaxFileWrites)
	}

	// Without max_file_writes, should be 0 (loop applies default).
	data2 := []byte(`---
name: writes2
model: m1
base_url: http://localhost/v1
---
`)
	cfg2, err := ParseAgentConfigBytes(data2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg2.MaxFileWrites != 0 {
		t.Errorf("MaxFileWrites default = %d, want 0", cfg2.MaxFileWrites)
	}
}

// ---------------------------------------------------------------------------
// Null vs absent distinction tests (Phase 4 ADR §3.4)
// ---------------------------------------------------------------------------

func TestParseAgentConfigBytes_NullDeniedVsAbsent(t *testing.T) {
	// Test 1: bash_exec: null is denied (same as absent).
	data1 := []byte(`---
name: nulltest1
model: m1
base_url: http://localhost/v1
tools:
  read_file: {}
  bash_exec: null
---
`)
	cfg1, err := ParseAgentConfigBytes(data1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cfg1.Tools["read_file"]; !ok {
		t.Error("read_file should be granted")
	}
	if _, ok := cfg1.Tools["bash_exec"]; ok {
		t.Error("bash_exec should be denied (null)")
	}

	// Test 2: bash_exec key absent is also denied.
	data2 := []byte(`---
name: nulltest2
model: m1
base_url: http://localhost/v1
tools:
  read_file: {}
---
`)
	cfg2, err := ParseAgentConfigBytes(data2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cfg2.Tools["read_file"]; !ok {
		t.Error("read_file should be granted")
	}
	if _, ok := cfg2.Tools["bash_exec"]; ok {
		t.Error("bash_exec should be denied (absent)")
	}

	// Test 3: bash_exec: {} (empty object) is granted with no restrictions.
	data3 := []byte(`---
name: nulltest3
model: m1
base_url: http://localhost/v1
tools:
  read_file: {}
  bash_exec: {}
---
`)
	cfg3, err := ParseAgentConfigBytes(data3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := cfg3.Tools["bash_exec"]; !ok {
		t.Fatal("bash_exec should be granted ({})")
	}
	if cfg3.Tools["bash_exec"].PathGlobs != nil {
		t.Error("bash_exec PathGlobs should be nil for empty restrictions")
	}

	// Test 4: Verify ToAgentConfig() produces the same AgentToolConfig
	// for null vs absent — both result in the tool being absent from the map.
	cfg1Tools := cfg1.Tools
	cfg2Tools := cfg2.Tools

	if len(cfg1Tools) != len(cfg2Tools) {
		t.Errorf("Tool counts differ: null case has %d, absent case has %d",
			len(cfg1Tools), len(cfg2Tools))
	}
	for name := range cfg1Tools {
		if _, ok := cfg2Tools[name]; !ok {
			t.Errorf("tool %q present in null case but not in absent case", name)
		}
	}
	for name := range cfg2Tools {
		if _, ok := cfg1Tools[name]; !ok {
			t.Errorf("tool %q present in absent case but not in null case", name)
		}
	}
}

// ---------------------------------------------------------------------------
// File-based parsing tests
// ---------------------------------------------------------------------------

func TestParseAgentConfig_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-agent.md")
	content := `---
name: filetest
model: m1
base_url: http://localhost/v1
tools:
  read_file: {}
---

Hello from file.
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := ParseAgentConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "filetest" {
		t.Errorf("Name = %q, want %q", cfg.Name, "filetest")
	}
	if cfg.SystemPrompt != "Hello from file." {
		t.Errorf("SystemPrompt = %q, want %q", cfg.SystemPrompt, "Hello from file.")
	}
}

func TestParseAgentConfig_FileNotFound(t *testing.T) {
	_, err := ParseAgentConfig("/nonexistent/path.md")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

// ---------------------------------------------------------------------------
// Bootstrap tests
// ---------------------------------------------------------------------------

func TestBootstrap_FullExtraction(t *testing.T) {
	dir := t.TempDir()

	err := Bootstrap(dir)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// Verify .aa/agents/ has all 5 files.
	agentsDir := filepath.Join(dir, ".aa", "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		t.Fatalf("read agents dir: %v", err)
	}
	agentNames := make(map[string]bool)
	for _, e := range entries {
		agentNames[e.Name()] = true
	}
	for _, name := range []string{"architect.md", "builder.md", "librarian.md", "tester.md", "forensic.md"} {
		if !agentNames[name] {
			t.Errorf("missing agent file: %s", name)
		}
	}

	// Verify .aa/skills/ has both skills.
	skillsDir := filepath.Join(dir, ".aa", "skills")
	skillEntries, err := os.ReadDir(skillsDir)
	if err != nil {
		t.Fatalf("read skills dir: %v", err)
	}
	skillDirs := make(map[string]bool)
	for _, e := range skillEntries {
		if e.IsDir() {
			skillDirs[e.Name()] = true
		}
	}
	for _, name := range []string{"gopls-mcp", "golang-code-style"} {
		if !skillDirs[name] {
			t.Errorf("missing skill dir: %s", name)
		}
	}

	// Verify SKILL.md exists in each skill dir.
	for _, name := range []string{"gopls-mcp", "golang-code-style"} {
		skillPath := filepath.Join(skillsDir, name, "SKILL.md")
		if _, err := os.Stat(skillPath); err != nil {
			t.Errorf("SKILL.md missing for %s: %v", name, err)
		}
	}
}

func TestBootstrap_Idempotent(t *testing.T) {
	dir := t.TempDir()

	// First call — full extraction.
	if err := Bootstrap(dir); err != nil {
		t.Fatalf("first Bootstrap: %v", err)
	}

	// Modify one file on disk (simulating user edit).
	builderPath := filepath.Join(dir, ".aa", "agents", "builder.md")
	modifiedContent := []byte("MODIFIED BY USER")
	if err := os.WriteFile(builderPath, modifiedContent, 0644); err != nil {
		t.Fatalf("write modified builder.md: %v", err)
	}

	// Delete one file on disk (simulating accidental deletion).
	forensicPath := filepath.Join(dir, ".aa", "agents", "forensic.md")
	if err := os.Remove(forensicPath); err != nil {
		t.Fatalf("remove forensic.md: %v", err)
	}

	// Second call — should not overwrite modified file, should restore deleted file.
	if err := Bootstrap(dir); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}

	// Check modified file is unchanged (disk wins).
	data, err := os.ReadFile(builderPath)
	if err != nil {
		t.Fatalf("read builder.md: %v", err)
	}
	if string(data) != "MODIFIED BY USER" {
		t.Errorf("builder.md was overwritten; got %q, want %q", string(data), "MODIFIED BY USER")
	}

	// Check deleted file is restored.
	if _, err := os.Stat(forensicPath); err != nil {
		t.Errorf("forensic.md should have been restored, but got: %v", err)
	}
}

func TestBootstrap_EmptyDir(t *testing.T) {
	// Bootstrap into an empty directory with no .aa/ creates it.
	dir := t.TempDir()
	if err := Bootstrap(dir); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	aaDir := filepath.Join(dir, ".aa")
	if _, err := os.Stat(aaDir); err != nil {
		t.Errorf(".aa dir not created: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Skills manifest tests
// ---------------------------------------------------------------------------

func TestReadSkillsManifest_AfterBootstrap(t *testing.T) {
	dir := t.TempDir()

	if err := Bootstrap(dir); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	manifest, err := ReadSkillsManifest(dir)
	if err != nil {
		t.Fatalf("ReadSkillsManifest: %v", err)
	}

	if manifest == "" {
		t.Fatal("expected non-empty manifest after Bootstrap")
	}

	// Should contain both skills.
	if !strings.Contains(manifest, "gopls-mcp") {
		t.Errorf("manifest should contain gopls-mcp, got: %s", manifest)
	}
	if !strings.Contains(manifest, "golang-code-style") {
		t.Errorf("manifest should contain golang-code-style, got: %s", manifest)
	}

	// Should start with "Available skills:".
	if !strings.HasPrefix(manifest, "Available skills:") {
		t.Errorf("manifest should start with 'Available skills:', got: %q", manifest[:20])
	}
}

func TestReadSkillsManifest_NoSkillsDir(t *testing.T) {
	dir := t.TempDir()
	manifest, err := ReadSkillsManifest(dir)
	if err != nil {
		t.Fatalf("ReadSkillsManifest: %v", err)
	}
	if manifest != "" {
		t.Errorf("expected empty manifest for dir with no skills, got: %q", manifest)
	}
}

func TestReadSkillsManifest_MalformedSKILLIgnored(t *testing.T) {
	dir := t.TempDir()

	// Create a skill dir with a malformed SKILL.md (no frontmatter).
	skillDir := filepath.Join(dir, ".aa", "skills", "broken-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("No frontmatter here"), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	manifest, err := ReadSkillsManifest(dir)
	if err != nil {
		t.Fatalf("ReadSkillsManifest: %v", err)
	}
	if manifest != "" {
		t.Errorf("expected empty manifest with only malformed skill, got: %q", manifest)
	}
}

// ---------------------------------------------------------------------------
// Integration: parser produces correct types for loop usage
// ---------------------------------------------------------------------------

func TestParsedConfig_ProducesLoopAgentConfig(t *testing.T) {
	data := []byte(`---
name: integration
model: integ-model
base_url: https://integ.test/v1
context_max_tokens: 16000
temperature: 0.5
max_file_writes: 3
tools:
  read_file: {}
  edit_file: {paths: ["*.md"]}
  bash_exec: null
---

System prompt body.
`)
	cfg, err := ParseAgentConfigBytes(data)
	if err != nil {
		t.Fatalf("ParseAgentConfigBytes: %v", err)
	}

	// Verify it's a valid loop.AgentConfig.
	if cfg.Name != "integration" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if cfg.ModelName != "integ-model" {
		t.Errorf("ModelName = %q", cfg.ModelName)
	}
	if cfg.BaseURL != "https://integ.test/v1" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.ContextMaxTokens != 16000 {
		t.Errorf("ContextMaxTokens = %d", cfg.ContextMaxTokens)
	}
	if cfg.Temperature != 0.5 {
		t.Errorf("Temperature = %f", cfg.Temperature)
	}
	if cfg.MaxFileWrites != 3 {
		t.Errorf("MaxFileWrites = %d", cfg.MaxFileWrites)
	}
	if cfg.SystemPrompt != "System prompt body." {
		t.Errorf("SystemPrompt = %q", cfg.SystemPrompt)
	}
	if cfg.UserPrompt != "" {
		t.Errorf("UserPrompt should be empty (not in config file), got %q", cfg.UserPrompt)
	}
}

func TestParsedConfig_ToolFiltering(t *testing.T) {
	// Parse a config and verify that it can be used with
	// Registry.FilterByAgentConfig to restrict tool access.
	data := []byte(`---
name: filtertest
model: fm
base_url: http://localhost/v1
tools:
  read_file: {}
  bash_exec: {}
---
`)
	cfg, err := ParseAgentConfigBytes(data)
	if err != nil {
		t.Fatalf("ParseAgentConfigBytes: %v", err)
	}

	// Create a full registry and filter by parsed config.
	reg, _ := tools.NewDefaultRegistry(t.TempDir(), "/tmp/test.log")
	filtered := reg.FilterByAgentConfig(cfg.Tools)

	// Should have read_file and bash_exec.
	if _, ok := filtered["read_file"]; !ok {
		t.Error("read_file should be in filtered registry")
	}
	if _, ok := filtered["bash_exec"]; !ok {
		t.Error("bash_exec should be in filtered registry")
	}
	// Should NOT have edit_file, create_file, list_dir, write_log.
	for _, name := range []string{"edit_file", "create_file", "list_dir", "write_log"} {
		if _, ok := filtered[name]; ok {
			t.Errorf("%s should NOT be in filtered registry", name)
		}
	}
}

func TestParsedConfig_DeniedToolFiltering(t *testing.T) {
	// Parse a config with bash_exec: null and verify it's excluded from filtered registry.
	data := []byte(`---
name: denytest
model: dm
base_url: http://localhost/v1
tools:
  read_file: {}
  bash_exec: null
---
`)
	cfg, err := ParseAgentConfigBytes(data)
	if err != nil {
		t.Fatalf("ParseAgentConfigBytes: %v", err)
	}

	reg, _ := tools.NewDefaultRegistry(t.TempDir(), "/tmp/test.log")
	filtered := reg.FilterByAgentConfig(cfg.Tools)

	if _, ok := filtered["bash_exec"]; ok {
		t.Error("bash_exec should be excluded (null in config)")
	}
	if _, ok := filtered["read_file"]; !ok {
		t.Error("read_file should be present")
	}
}

// ---------------------------------------------------------------------------
// Verify config package can be imported by loop without cycle
// ---------------------------------------------------------------------------

func TestConfigImports(t *testing.T) {
	// This is a compile-time check — if the imports are wrong, the test won't
	// compile. We also verify that loop.AgentConfig is the concrete type returned.
	var _ loop.AgentConfig
	_ = ParseAgentConfigBytes
	_ = Bootstrap
	_ = ReadSkillsManifest
}
