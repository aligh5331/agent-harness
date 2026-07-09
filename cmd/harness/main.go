// Command harness is the entry point for the agent-harness binary.
// Phase 4: bootstraps .aa/ config, parses agent config from YAML frontmatter,
// builds a filtered tool registry, and runs the turn loop.
// Phase 5: adds --phase flag for branch-per-phase git integration, with
// automatic commits after each phase step.
// Phase 6: adds --spec flag to trigger the Prompt Generator agent.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"agent-harness/internal/config"
	"agent-harness/internal/gitops"
	"agent-harness/internal/llm"
	"agent-harness/internal/loop"
	"agent-harness/internal/store"
	"agent-harness/internal/tools"
)

func main() {
	ctx := context.Background()

	// CLI flags.
	agentName := flag.String("agent", "builder", "Agent mode (architect|builder|librarian|tester|forensic|prompt-generator)")
	dbDir := flag.String("db", ".", "Directory for the SQLite database")
	userPrompt := flag.String("prompt", "", "Kickoff user prompt (set by Phase 0)")
	phaseFlag := flag.String("phase", "", "Phase identifier for branch-per-phase (e.g. '5' → branch 'phase-5')")
	auditPhase := flag.String("audit-phase", "", "Audit a specific phase (e.g. '6')")
	auditFull := flag.Bool("audit-full", false, "Audit the full project against spec")
	specFlag := flag.String("spec", "", "Path to project specification (for prompt-generator)")
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

	if *auditPhase != "" {
		fmt.Printf("Auditing phase: %s\n", *auditPhase)
		os.Exit(0)
	}
	if *auditFull {
		fmt.Println("Auditing full project state")
		os.Exit(0)
	}

	// Special case: Prompt Generator agent handles its own lifecycle.
	if *agentName == "prompt-generator" {
		if *specFlag == "" {
			log.Fatalf("prompt-generator requires --spec")
		}
		// In a full implementation, this would trigger the actual generation logic.
		// For now, we simulate the invocation.
		fmt.Printf("Generating briefings for spec: %s\n", *specFlag)
		os.Exit(0)
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

	// Step 5: Git branch setup (Phase 5).
	if *phaseFlag != "" {
		branchName := "phase-" + *phaseFlag
		fmt.Printf("Phase branch: %s\n", branchName)

		// Check for uncommitted changes before starting.
		clean, err := gitops.IsClean(projectRoot)
		if err != nil {
			log.Fatalf("git pre-flight check: %v", err)
		}
		if !clean {
			log.Fatalf(
				"uncommitted changes exist in %s — refusing to start. "+
					"Commit or stash your changes before running with --phase.",
				projectRoot,
			)
		}

		// Create or check out the phase branch.
		if err := gitops.EnsureBranch(projectRoot, branchName); err != nil {
			log.Fatalf("git ensure branch %s: %v", branchName, err)
		}
	}

	// Step 6: Open the store.
	dbPath := filepath.Join(projectRoot, "agent-harness.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		log.Fatalf("store open: %v", err)
	}
	defer st.Close()

	// Step 7: Create the LLM client.
	client := llm.NewOpenAIClient()

	// Step 8: Build the tool registry with symlink-resolved project root.
	logPath := filepath.Join(projectRoot, *agentName+".log")
	reg, resolvedRoot := tools.NewDefaultRegistry(projectRoot, logPath)
	filteredReg := reg.FilterByAgentConfig(cfg.Tools)

	// Step 9: Create and run the turn loop.
	turnLoop := loop.New(client, st, filteredReg, cfg, logPath, resolvedRoot)
	halt, err := turnLoop.Run(ctx)
	if err != nil {
		log.Fatalf("loop.Run: %v", err)
	}

	fmt.Printf("Session %d completed: code=%d message=%s resume_count=%d\n",
		turnLoop.SessionID(), halt.Code, halt.Message, halt.ResumeCount)

	// Step 10: Post-Run git commit (Phase 5).
	if *phaseFlag != "" {
		commitMsg := buildCommitMessage(*agentName, halt, logPath)
		created, err := gitops.Commit(projectRoot, commitMsg)
		if err != nil {
			log.Printf("WARNING: git commit failed: %v", err)
		} else if created {
			fmt.Printf("Committed phase step to branch phase-%s\n", *phaseFlag)
		} else {
			fmt.Println("No changes to commit after this phase step.")
		}
	}
}

// buildCommitMessage constructs the git commit message from the halt reason
// and the agent's log file content.
func buildCommitMessage(agentName string, halt loop.HaltReason, logPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", agentName, halt.Message)
	logContent, err := os.ReadFile(logPath)
	if err == nil && len(logContent) > 0 {
		b.WriteString("\n\n")
		b.Write(logContent)
	}
	return b.String()
}
