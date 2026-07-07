// Command harness is the entry point for the agent-harness binary.
// Phase 4: bootstraps .aa/ config, parses agent config from YAML frontmatter,
// builds a filtered tool registry, and runs the turn loop.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"path/filepath"

	"agent-harness/internal/config"
	"agent-harness/internal/llm"
	"agent-harness/internal/loop"
	"agent-harness/internal/store"
	"agent-harness/internal/tools"
)

func main() {
	ctx := context.Background()

	// CLI flags.
	agentName := flag.String("agent", "builder", "Agent mode (architect|builder|librarian|tester|forensic)")
	dbDir := flag.String("db", ".", "Directory for the SQLite database")
	userPrompt := flag.String("prompt", "", "Kickoff user prompt (set by Phase 0)")
	flag.Parse()

	// Resolve project root from the db directory.
	projectRoot, err := filepath.Abs(*dbDir)
	if err != nil {
		log.Fatalf("resolve project root: %v", err)
	}

	// Step 1: Bootstrap .aa/ from embedded defaults.
	if err := config.Bootstrap(projectRoot); err != nil {
		log.Fatalf("bootstrap: %v", err)
	}

	// Step 2: Parse agent config from .aa/agents/<agent-name>.md.
	agentConfigPath := filepath.Join(projectRoot, ".aa", "agents", *agentName+".md")
	cfg, err := config.ParseAgentConfig(agentConfigPath)
	if err != nil {
		log.Fatalf("parse agent config %s: %v", agentConfigPath, err)
	}

	// Step 3: Append skills manifest to system prompt.
	manifest, err := config.ReadSkillsManifest(projectRoot)
	if err != nil {
		log.Fatalf("read skills manifest: %v", err)
	}
	if manifest != "" {
		cfg.SystemPrompt = cfg.SystemPrompt + "\n\n" + manifest
	}

	// Step 4: Set user prompt (from Phase 0 or CLI).
	cfg.UserPrompt = *userPrompt
	if cfg.UserPrompt == "" {
		cfg.UserPrompt = "Please complete the task for this phase."
	}

	// Step 5: Open the store.
	dbPath := filepath.Join(projectRoot, "agent-harness.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		log.Fatalf("store open: %v", err)
	}
	defer st.Close()

	// Step 6: Create the LLM client.
	client := llm.NewOpenAIClient()

	// Step 7: Build the tool registry with symlink-resolved project root.
	// Phase 3 §14 fix: NewDefaultRegistry returns the EvalSymlinks-resolved root,
	// which MUST be passed to loop.New to ensure AllowPath is structurally correct.
	logPath := filepath.Join(projectRoot, *agentName+".log")
	reg, resolvedRoot := tools.NewDefaultRegistry(projectRoot, logPath)
	filteredReg := reg.FilterByAgentConfig(cfg.Tools)

	// Step 8: Create and run the turn loop.
	turnLoop := loop.New(client, st, filteredReg, cfg, logPath, resolvedRoot)
	halt, err := turnLoop.Run(ctx)
	if err != nil {
		log.Fatalf("loop.Run: %v", err)
	}

	fmt.Printf("Session %d completed: code=%d message=%s resume_count=%d\n",
		turnLoop.SessionID(), halt.Code, halt.Message, halt.ResumeCount)
}
