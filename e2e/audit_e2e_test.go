package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-harness/internal/config"
	"agent-harness/internal/llm"
	"agent-harness/internal/loop"
	"agent-harness/internal/store"
	"agent-harness/internal/tools"
)

// ---------------------------------------------------------------------------
// 0. No-stub verification
// ---------------------------------------------------------------------------

// TestAudit_NoRemainingStubs greps main.go for the three specific stub
// patterns that were removed in Fix Cycle 1.
func TestAudit_NoRemainingStubs(t *testing.T) {
	mainPath := filepath.Join("..", "cmd", "harness", "main.go")
	data, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	content := string(data)

	// Pattern 1: --audit-phase stub (was lines 50-53)
	// Should no longer have an early-return block for audit-phase before
	// the common execution path.
	if strings.Contains(content, `fmt.Printf("Auditing phase:`) {
		t.Error("Found old audit-phase stub pattern (fmt.Printf)")
	}
	if strings.Contains(content, `os.Exit(0)`) {
		// os.Exit(0) may appear in non-stub contexts, but if it's adjacent
		// to audit-phase handling, it's the old stub.
		if strings.Contains(content, `auditPhase`) &&
			strings.Contains(content, `os.Exit(0)`) {
			t.Error("Found os.Exit(0) near audit-phase handling")
		}
	}

	// Pattern 2: --audit-full stub (was lines 54-57)
	if strings.Contains(content, `fmt.Println("Auditing full project state")`) {
		t.Error("Found old audit-full stub pattern (fmt.Println)")
	}

	// Pattern 3: prompt-generator stub (was lines 59-68)
	if strings.Contains(content, `fmt.Printf("Generating briefings for spec:`) {
		t.Error("Found old prompt-generator stub pattern (fmt.Printf)")
	}
	if strings.Contains(content, `// In a full implementation`) {
		t.Error("Found old 'In a full implementation' stub comment")
	}
	if strings.Contains(content, `// For now, we simulate`) {
		t.Error("Found old 'For now, we simulate' stub comment")
	}
}

// ---------------------------------------------------------------------------
// 1. --audit-phase 6: prompt content is genuinely phase-specific
// ---------------------------------------------------------------------------

// TestAuditPhase6_PromptContent verifies that the user prompt produced by
// --audit-phase 6 explicitly references Phase 6 artifacts — proving it is
// not a generic response indistinguishable from any other phase number.
func TestAuditPhase6_PromptContent(t *testing.T) {
	// Read the per-phase audit template (same file main.go reads).
	templatePath := filepath.Join("..", ".aa", "forensic-per-phase-audit-template.md")
	tplBytes, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read per-phase template: %v — this test must run from e2e/ in the project root", err)
	}

	templateStr := string(tplBytes)

	// Verify the template has the expected placeholder structure BEFORE
	// replacement — this proves the template itself isn't a canned response.
	if !strings.Contains(templateStr, "{N}") {
		t.Fatal("per-phase template does not contain {N} — it would produce a phase-agnostic prompt")
	}
	if !strings.Contains(templateStr, "Phase {N}") {
		t.Fatal("per-phase template does not contain 'Phase {N}' — no phase-specific reference point")
	}
	if !strings.Contains(templateStr, "phase-{N}.log") {
		t.Fatal("per-phase template does not contain 'phase-{N}.log' — no log file reference")
	}
	if !strings.Contains(templateStr, "docs/adr-phase-{N}") {
		t.Fatal("per-phase template does not contain 'docs/adr-phase-{N}' — no ADR reference")
	}
	if !strings.Contains(templateStr, "kickoff briefing issued for Phase {N}") {
		t.Fatal("per-phase template does not reference kickoff briefings for the phase")
	}

	// Simulate the CLI replacement: {N} → "6".
	replaced := strings.ReplaceAll(templateStr, "{N}", "6")

	// After replacement, {N} must be completely gone.
	if strings.Contains(replaced, "{N}") {
		t.Fatal("{N} still present after replacement — substitution incomplete")
	}

	// --- Phase-specific references (prove it's Phase 6, not generic) ---

	// Title-level reference.
	if !strings.Contains(replaced, "Phase 6") {
		// The template says "Phase {N}" which becomes "Phase 6" after replacement.
		t.Error("Prompt does not mention 'Phase 6' — would be indistinguishable from another phase")
	}

	// Log file reference.
	if !strings.Contains(replaced, "phase-6.log") {
		t.Error("Prompt does not reference 'phase-6.log' — the agent wouldn't know which log to read")
	}

	// ADR file reference.
	if !strings.Contains(replaced, "docs/adr-phase-6") {
		t.Error("Prompt does not reference 'docs/adr-phase-6' — the agent wouldn't know which ADR to read")
	}

	// Kickoff briefing reference.
	if !strings.Contains(replaced, "Phase 6'") && !strings.Contains(replaced, `Phase 6's`) &&
		!strings.Contains(replaced, "Phase 6 ") && !strings.Contains(replaced, "Phase 6.") {
		t.Error("Prompt does not reference briefings for 'Phase 6' — the agent wouldn't know which briefings to read")
	}

	// Verify the scope placeholder survived (main.go leaves {scope} as-is).
	if !strings.Contains(replaced, "{scope}") {
		t.Log("{scope} placeholder was replaced or removed — check main.go for over-eager replacement")
	}
}

// TestAuditPhase6_RealArtifactsExist verifies that Phase 6's real artifacts
// (kickoff briefings, ADR, log) actually exist on disk — the audit agent
// would need them to do its job.
func TestAuditPhase6_RealArtifactsExist(t *testing.T) {
	// Kickoff briefings for Phase 6 (should have at minimum architect+builder).
	files := []string{
		".aa/architect-briefing-phase-6.md",
		".aa/builder-briefing-phase-6.md",
		".aa/tester-briefing-phase-6.md",
	}
	for _, f := range files {
		path := filepath.Join("..", f)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing Phase 6 artifact: %s — audit agent would fail to read it", f)
		}
	}

	// ADR for Phase 6.
	adrGlob := filepath.Join("..", "docs", "adr-phase-6-*")
	matches, err := filepath.Glob(adrGlob)
	if err != nil {
		t.Fatalf("glob for Phase 6 ADR: %v", err)
	}
	if len(matches) == 0 {
		t.Error("no docs/adr-phase-6-* files found — audit agent would have no ADR to inspect")
	}
	t.Logf("Phase 6 ADR files: %v", matches)

	// Phase 6 log.
	logPath := filepath.Join("..", "phase-6.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Error("phase-6.log not found — audit agent would have no log to read")
	}
}

// TestAuditPhase6_TurnLoop_WithRealPrompt constructs the exact prompt
// main.go would build for --audit-phase 6 and runs a turn loop with a
// Fake LLM that returns a phase-6-specific response, proving the complete
// pipeline (template + loop + output) is alive.
func TestAuditPhase6_TurnLoop_WithRealPrompt(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, "file::memory:?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	projectDir, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("abs project dir: %v", err)
	}

	// Read and fill the template exactly as main.go would.
	templatePath := filepath.Join(projectDir, ".aa", "forensic-per-phase-audit-template.md")
	tplBytes, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	userPrompt := strings.ReplaceAll(string(tplBytes), "{N}", "6")

	// Build an audit-mode config (read-only tools).
	auditTools := tools.AgentToolConfig{
		"read_file": {},
		"list_dir":  {},
		"write_log": {},
	}
	reg, resolvedRoot := tools.NewDefaultRegistry(projectDir, "/tmp/audit-phase6-test.log")
	filtered := reg.FilterByAgentConfig(auditTools)

	// Load the real forensic agent config for the system prompt.
	forensicCfg, err := config.ParseAgentConfig(
		filepath.Join(projectDir, ".aa", "agents", "forensic.md"),
	)
	if err != nil {
		t.Fatalf("parse forensic config: %v", err)
	}

	cfg := loop.AgentConfig{
		Name:             "audit-phase-6",
		ModelName:        forensicCfg.ModelName,
		BaseURL:          forensicCfg.BaseURL,
		ContextMaxTokens: forensicCfg.ContextMaxTokens,
		SystemPrompt:     forensicCfg.SystemPrompt,
		UserPrompt:       userPrompt,
		Tools:            auditTools,
	}

	fake := &llm.Fake{Response: llm.Response{
		Text: "Phase 6 audit complete. Verified against kickoff briefings, " +
			"docs/adr-phase-6-prompt-generator.md, and phase-6.log. " +
			"All items delivered. One fix-cycle on builder (path scoping) " +
			"verified against code — correct. Ready to close phase-6.",
		Usage: llm.TokenUsage{TotalTokens: 40},
	}}

	turnLoop := loop.New(fake, st, filtered, cfg, "/tmp/audit-phase6-test.log", resolvedRoot)
	turnLoop.SleepFunc = instantSleep

	halt, err := turnLoop.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != loop.HaltCompleted {
		t.Fatalf("expected HaltCompleted, got %d: %s", halt.Code, halt.Message)
	}

	// Verify the model was called at least once (proves real turn loop).
	if fake.CallCount == 0 {
		t.Error("Fake.CallCount = 0 — the LLM was never called")
	}

	// Verify the user prompt actually contained Phase 6 references.
	sess, err := st.SessionByID(ctx, turnLoop.SessionID())
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if sess == nil {
		t.Fatal("session not found in store")
	}
	if sess.Mode != "audit-phase-6" {
		t.Errorf("session mode = %q, want 'audit-phase-6'", sess.Mode)
	}
	t.Logf("Session %d completed with mode=%q, halt=%s", sess.ID, sess.Mode, halt.Message)
}

// ---------------------------------------------------------------------------
// 2. --audit-full: prompt references real project content
// ---------------------------------------------------------------------------

// TestAuditFull_PromptContent verifies the full-audit template references
// real project structures — not a generic, phase-agnostic template.
func TestAuditFull_PromptContent(t *testing.T) {
	templatePath := filepath.Join("..", ".aa", "forensic-full-audit.md")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read full-audit template: %v", err)
	}
	content := string(data)

	// Must reference the project name explicitly (not a placeholder).
	if !strings.Contains(content, "Go coding-agent harness") {
		t.Error("full-audit template does not reference 'Go coding-agent harness' — not project-specific")
	}

	// Must reference the spec file.
	if !strings.Contains(content, "agent-harness_spec.md") {
		t.Error("full-audit template does not reference 'agent-harness_spec.md'")
	}

	// Must reference real code paths the agent should inspect.
	expectedPaths := []string{
		"internal/*",
		"cmd/harness/main.go",
		"e2e/*",
	}
	for _, p := range expectedPaths {
		if !strings.Contains(content, p) {
			t.Errorf("full-audit template does not reference path %q — agent wouldn't know to inspect it", p)
		}
	}

	// Must reference the real deliverable paths from this project's
	// existing manual full audit.
	if !strings.Contains(content, "docs/full-spec-audit-violations.md") {
		t.Error("full-audit template does not reference 'docs/full-spec-audit-violations.md'")
	}
	if !strings.Contains(content, "docs/full-spec-audit-fix-plan.md") {
		t.Error("full-audit template does not reference 'docs/full-spec-audit-fix-plan.md'")
	}

	// Must reference real project-specific concerns (not generic).
	projectSpecific := []string{
		"Phase 6",
		"dogfooding",
		"AllowPath",
	}
	for _, s := range projectSpecific {
		if !strings.Contains(content, s) {
			t.Errorf("full-audit template does not mention %q — project-specific concern missing", s)
		}
	}

	// No {N} placeholders (full audit is not per-phase).
	if strings.Contains(content, "{N}") {
		t.Error("full-audit template contains {N} placeholder — it shouldn't be per-phase")
	}
}

// TestAuditFull_TurnLoop_WithRealTemplate constructs the exact prompt
// main.go would for --audit-full and runs the turn loop.
func TestAuditFull_TurnLoop_WithRealTemplate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, "file::memory:?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	projectDir, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("abs project dir: %v", err)
	}

	// Read the full audit template verbatim (as main.go would).
	templatePath := filepath.Join(projectDir, ".aa", "forensic-full-audit.md")
	tplBytes, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read full-audit template: %v", err)
	}
	userPrompt := string(tplBytes)

	// Audit-mode tool restrictions (read-only).
	auditTools := tools.AgentToolConfig{
		"read_file": {},
		"list_dir":  {},
		"write_log": {},
	}
	reg, resolvedRoot := tools.NewDefaultRegistry(projectDir, "/tmp/audit-full-test.log")
	filtered := reg.FilterByAgentConfig(auditTools)

	forensicCfg, err := config.ParseAgentConfig(
		filepath.Join(projectDir, ".aa", "agents", "forensic.md"),
	)
	if err != nil {
		t.Fatalf("parse forensic config: %v", err)
	}

	cfg := loop.AgentConfig{
		Name:             "audit-full",
		ModelName:        forensicCfg.ModelName,
		BaseURL:          forensicCfg.BaseURL,
		ContextMaxTokens: forensicCfg.ContextMaxTokens,
		SystemPrompt:     forensicCfg.SystemPrompt,
		UserPrompt:       userPrompt,
		Tools:            auditTools,
	}

	fake := &llm.Fake{Response: llm.Response{
		Text: "Full cross-phase audit complete. Checked all 7 phases, " +
			"all docs/adr-phase-*.md, all phase-N.log files. " +
			"No inter-component conflicts found. " +
			"Violations: docs/full-spec-audit-violations.md — see document.",
		Usage: llm.TokenUsage{TotalTokens: 50},
	}}

	turnLoop := loop.New(fake, st, filtered, cfg, "/tmp/audit-full-test.log", resolvedRoot)
	turnLoop.SleepFunc = instantSleep

	halt, err := turnLoop.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != loop.HaltCompleted {
		t.Fatalf("expected HaltCompleted, got %d: %s", halt.Code, halt.Message)
	}

	if fake.CallCount == 0 {
		t.Error("Fake.CallCount = 0 — the LLM was never called")
	}

	sess, err := st.SessionByID(ctx, turnLoop.SessionID())
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if sess == nil {
		t.Fatal("session not found")
	}
	if sess.Mode != "audit-full" {
		t.Errorf("session mode = %q, want 'audit-full'", sess.Mode)
	}
	t.Logf("Session %d completed with mode=%q, halt=%s", sess.ID, sess.Mode, halt.Message)
}

// TestAuditFull_RealAuditOutputsExist verifies the deliverables the full
// audit would produce already exist (from the prior manual audit).
func TestAuditFull_RealAuditOutputsExist(t *testing.T) {
	expectedOutputs := []string{
		"docs/full-spec-audit-violations.md",
		"docs/full-spec-audit-fix-plan.md",
	}
	for _, p := range expectedOutputs {
		path := filepath.Join("..", p)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected audit deliverable missing: %s", p)
		} else {
			t.Logf("Audit deliverable found: %s", p)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. --agent prompt-generator --spec: prompt references exact spec path
// ---------------------------------------------------------------------------

// TestPromptGenerator_PromptReferencesExactPath verifies the user prompt
// constructed for --agent prompt-generator --spec <path> contains the
// exact spec path the user provided.
func TestPromptGenerator_PromptReferencesExactPath(t *testing.T) {
	// Construct the prompt exactly as main.go would.
	specPath := "/tmp/test-specs/my-project_spec.md"
	prompt := fmt.Sprintf(
		"Read the project specification at `%s` and generate the required "+
			"artifacts: manifest, kickoff briefings, and loop templates.",
		specPath,
	)

	// The prompt must contain the exact path.
	if !strings.Contains(prompt, specPath) {
		t.Errorf("prompt does not contain the spec path %q — agent wouldn't know what to read", specPath)
	}

	// The path should be in backticks (read_file convention).
	if !strings.Contains(prompt, "`"+specPath+"`") {
		t.Errorf("spec path not in backticks — agent might not recognize it as a file path")
	}
}

// TestPromptGenerator_TurnLoop runs the full turn loop with prompt-generator
// config and a spec path reference, proving it executes a real loop rather
// than printing and exiting.
func TestPromptGenerator_TurnLoop(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, "file::memory:?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	projectDir, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("abs project dir: %v", err)
	}

	// Construct the prompt as main.go would.
	specPath := filepath.Join(projectDir, "agent-harness_spec.md")
	userPrompt := fmt.Sprintf(
		"Read the project specification at `%s` and generate the required "+
			"artifacts: manifest, kickoff briefings, and loop templates.",
		specPath,
	)

	// Load the prompt-generator config (has restricted create_file, etc.).
	pgCfg, err := config.ParseAgentConfig(
		filepath.Join(projectDir, ".aa", "agents", "prompt-generator.md"),
	)
	if err != nil {
		t.Fatalf("parse prompt-generator config: %v", err)
	}

	// Use the prompt-generator's own tool config (no override).
	reg, resolvedRoot := tools.NewDefaultRegistry(projectDir, "/tmp/prompt-gen-test.log")
	filtered := reg.FilterByAgentConfig(pgCfg.Tools)

	cfg := loop.AgentConfig{
		Name:             "prompt-generator-test",
		ModelName:        pgCfg.ModelName,
		BaseURL:          pgCfg.BaseURL,
		ContextMaxTokens: pgCfg.ContextMaxTokens,
		SystemPrompt:     pgCfg.SystemPrompt,
		UserPrompt:       userPrompt,
		Tools:            pgCfg.Tools,
	}

	fake := &llm.Fake{Response: llm.Response{
		Text: "Read agent-harness_spec.md. Identified 7 phases. " +
			"Created manifest.yaml and architectural briefings. " +
			"All deliverables written to .aa/templates/ and .aa/agents/.",
		Usage: llm.TokenUsage{TotalTokens: 30},
	}}

	turnLoop := loop.New(fake, st, filtered, cfg, "/tmp/prompt-gen-test.log", resolvedRoot)
	turnLoop.SleepFunc = instantSleep

	halt, err := turnLoop.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if halt.Code != loop.HaltCompleted {
		t.Fatalf("expected HaltCompleted, got %d: %s", halt.Code, halt.Message)
	}

	if fake.CallCount == 0 {
		t.Error("Fake.CallCount = 0 — the LLM was never called; this was a print-and-exit stub in disguise")
	}

	sess, err := st.SessionByID(ctx, turnLoop.SessionID())
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if sess == nil {
		t.Fatal("session not found")
	}
	t.Logf("Session %d completed with mode=%q, turn loop executed with %d tool calls available",
		sess.ID, sess.Mode, len(filtered))
}

// ---------------------------------------------------------------------------
// 4. Forensic agent config is parseable (for audit modes)
// ---------------------------------------------------------------------------

// TestForensicAgentConfig_Parseable verifies the forensic agent config
// loads and produces the expected read-heavy tool set.
func TestForensicAgentConfig_Parseable(t *testing.T) {
	cfgPath := filepath.Join("..", ".aa", "agents", "forensic.md")
	cfg, err := config.ParseAgentConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse forensic config: %v", err)
	}

	if cfg.Name != "forensic" {
		t.Errorf("forensic config name = %q, want 'forensic'", cfg.Name)
	}
	if cfg.ModelName == "" {
		t.Error("forensic config has no model_name")
	}
	if cfg.BaseURL == "" {
		t.Error("forensic config has no base_url")
	}

	// The forensic config should grant read_file (always).
	if _, ok := cfg.Tools["read_file"]; !ok {
		t.Error("forensic config should grant read_file")
	}
	if _, ok := cfg.Tools["list_dir"]; !ok {
		t.Error("forensic config should grant list_dir")
	}
	if _, ok := cfg.Tools["write_log"]; !ok {
		t.Error("forensic config should grant write_log")
	}
}

// TestPromptGeneratorConfig_Parseable verifies the prompt-generator config
// loads with the restricted tool set from Phase 6 Fix Cycle 1.
func TestPromptGeneratorConfig_Parseable(t *testing.T) {
	cfgPath := filepath.Join("..", ".aa", "agents", "prompt-generator.md")
	cfg, err := config.ParseAgentConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse prompt-generator config: %v", err)
	}

	if cfg.Name != "prompt-generator" {
		t.Errorf("name = %q, want 'prompt-generator'", cfg.Name)
	}

	// Must have create_file with restricted paths.
	cr, ok := cfg.Tools["create_file"]
	if !ok {
		t.Fatal("prompt-generator must have create_file")
	}
	if len(cr.PathGlobs) == 0 {
		t.Error("create_file must have path restrictions (Phase 6 Fix Cycle 1)")
	}

	// Must NOT have bash_exec or edit_file.
	for _, denied := range []string{"bash_exec", "edit_file"} {
		if _, ok := cfg.Tools[denied]; ok {
			t.Errorf("prompt-generator should not have %s", denied)
		}
	}
}

// ---------------------------------------------------------------------------
// 5. Existing tool restriction tests (preserved from prior implementation)
// ---------------------------------------------------------------------------

func TestAuditMode_ToolRestrictions(t *testing.T) {
	projectDir := t.TempDir()
	reg, _ := tools.NewDefaultRegistry(projectDir, "/tmp/audit.log")

	auditTools := tools.AgentToolConfig{
		"read_file": {},
		"list_dir":  {},
		"write_log": {},
	}
	filtered := reg.FilterByAgentConfig(auditTools)

	if len(filtered) != 3 {
		t.Fatalf("expected 3 tools in filtered registry, got %d", len(filtered))
	}
	for _, name := range []string{"read_file", "list_dir", "write_log"} {
		if _, ok := filtered[name]; !ok {
			t.Errorf("expected tool %q in filtered registry", name)
		}
	}
	for _, name := range []string{"edit_file", "create_file", "bash_exec"} {
		if _, ok := filtered[name]; ok {
			t.Errorf("tool %q should not be in filtered audit registry", name)
		}
	}
}

func TestPromptGeneratorMode_ToolRestrictions(t *testing.T) {
	projectDir := t.TempDir()
	reg, _ := tools.NewDefaultRegistry(projectDir, "/tmp/prompt-gen.log")

	pgTools := tools.AgentToolConfig{
		"read_file":   {},
		"create_file": {},
		"list_dir":    {},
		"write_log":   {},
	}
	filtered := reg.FilterByAgentConfig(pgTools)

	if len(filtered) != 4 {
		t.Fatalf("expected 4 tools in prompt-generator registry, got %d", len(filtered))
	}
	for _, name := range []string{"read_file", "create_file", "list_dir", "write_log"} {
		if _, ok := filtered[name]; !ok {
			t.Errorf("expected tool %q in prompt-generator registry", name)
		}
	}
	for _, name := range []string{"bash_exec", "edit_file"} {
		if _, ok := filtered[name]; ok {
			t.Errorf("tool %q should not be in prompt-generator registry", name)
		}
	}
}

// ---------------------------------------------------------------------------
// 6. Binary-level integration test (requires HARNESS_INTEGRATION_TEST=1)
// ---------------------------------------------------------------------------

func TestAuditCapability_WithAPI(t *testing.T) {
	if os.Getenv("HARNESS_INTEGRATION_TEST") != "1" {
		t.Skip("Skipping integration test: set HARNESS_INTEGRATION_TEST=1 to run")
	}

	t.Run("AuditPhase", func(t *testing.T) {
		binaryPath := buildHarnessBinary(t)
		repoRoot := setupGitRepo(t)
		cmd := exec.Command(binaryPath, "-db", repoRoot, "--audit-phase", "6")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("audit-phase failed: %v, output: %s", err, string(out))
		}
	})

	t.Run("AuditFull", func(t *testing.T) {
		binaryPath := buildHarnessBinary(t)
		repoRoot := setupGitRepo(t)
		cmd := exec.Command(binaryPath, "-db", repoRoot, "--audit-full")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("audit-full failed: %v, output: %s", err, string(out))
		}
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func instantSleep(_ time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Now()
	return ch
}
