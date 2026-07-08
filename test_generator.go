package main

import (
	"fmt"
	"os"
	"path/filepath"
	"agent-harness/internal/config"
)

func main() {
	// Test data matching Worked Example: Short Spec
	// Spec requires Phase 1: architect, builder
	projectRoot, _ := os.Getwd() // Assume current dir for simplicity
	
	// Write manifest
	m := config.Manifest{
		Phases: map[int][]string{
			1: {"architect", "builder"},
		},
	}
	
	err := config.WriteManifest(projectRoot, m)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Create briefings
	for _, agent := range []string{"architect", "builder"} {
		path := filepath.Join(projectRoot, ".aa", "agents", fmt.Sprintf("%s-briefing-phase-1.md", agent))
		content := fmt.Sprintf("# Phase 1 Briefing: %s\n\nProject: TestProject\n\nReference prior: docs/adr-phase-1-foundation.md\n", agent)
		os.WriteFile(path, []byte(content), 0644)
	}

	fmt.Println("Worked example output generated successfully.")
}
