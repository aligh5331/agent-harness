package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"agent-harness/internal/config"
)

func TestPhase6_PromptGenerator_Manifest(t *testing.T) {
	root := t.TempDir()
	
	m := config.Manifest{
		Phases: map[int][]string{
			1: {"architect", "builder"},
		},
	}
	
	err := config.WriteManifest(root, m)
	if err != nil {
		t.Fatalf("WriteManifest failed: %v", err)
	}
	
	manifestPath := filepath.Join(root, ".aa", "templates", "manifest.yaml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("Manifest file not found: %v", err)
	}
}

func TestPhase6_PromptGenerator_BriefingStructure(t *testing.T) {
	root := t.TempDir()
	briefingDir := filepath.Join(root, ".aa", "agents")
	os.MkdirAll(briefingDir, 0755)
	
	content := "# Phase 1 Briefing: architect\n\nReference prior: docs/adr-phase-1-foundation.md\n\n## Tasks\n- Task 1\n\n## Out of Scope\n- None\n"
	path := filepath.Join(briefingDir, "architect-briefing-phase-1.md")
	os.WriteFile(path, []byte(content), 0644)
	
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	
	s := string(data)
	if !strings.Contains(s, "Reference prior: docs/adr-phase-1-foundation.md") {
		t.Error("Missing reference path")
	}
	if !strings.Contains(s, "## Tasks") {
		t.Error("Missing ## Tasks header")
	}
	if !strings.Contains(s, "## Out of Scope") {
		t.Error("Missing ## Out of Scope header")
	}
}
