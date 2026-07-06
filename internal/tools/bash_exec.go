package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"agent-harness/internal/llm"
)

// BashExecArgs holds the arguments for the bash_exec tool.
type BashExecArgs struct {
	Command        string  `json:"command"`
	TimeoutSeconds float64 `json:"timeout_seconds"`
}

// BashExecResult holds the result of a bash_exec execution.
type BashExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
}

// BashExecTool implements the bash_exec tool.
type BashExecTool struct {
	root string
}

// Name returns "bash_exec".
func (t *BashExecTool) Name() string { return "bash_exec" }

// Definition returns the ToolDef for bash_exec.
func (t *BashExecTool) Definition() llm.ToolDef {
	return newToolDef(
		"bash_exec",
		"Execute a shell command via sh -c. Working directory is set to the project root. A hard timeout cancels the process if it exceeds timeout_seconds. stdout, stderr, and exit code are captured and returned.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
				"timeout_seconds": map[string]any{
					"type":        "number",
					"description": "Hard timeout in seconds (fractional values like 0.5 for 500ms are supported)",
				},
			},
			"required": []string{"command", "timeout_seconds"},
		},
	)
}

// Execute runs the bash_exec tool.
//
// Known gap: cmd.Dir sets the working directory, but a shell command can
// cd/redirect outside of it. This is an accepted, documented limitation
// for v1 — see spec §6.1.
func (t *BashExecTool) Execute(ctx context.Context, args json.RawMessage, config ToolConfig) (Result, error) {
	var a BashExecArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{}, fmt.Errorf("bash_exec: invalid arguments: %w", err)
	}

	// Create a sub-context with the hard timeout.
	timeout := time.Duration(a.TimeoutSeconds * float64(time.Second))
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", a.Command)
	cmd.Dir = t.root

	// Use a process group so that cancelling the context kills the entire
	// process tree (including child processes like sleep), not just the sh
	// process itself.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Kill the process group (negative PID) so children are also killed.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := BashExecResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		// Check for timeout first — the context deadline triggers cmd.Run to
		// return an error, but the real signal is execCtx.Err().
		if execCtx.Err() == context.DeadlineExceeded {
			result.TimedOut = true
			result.ExitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			// Command failed to start (binary not found, etc.)
			return Result{}, fmt.Errorf("bash_exec: %w", err)
		}
	} else {
		result.ExitCode = 0
	}

	return Result{Data: result}, nil
}

