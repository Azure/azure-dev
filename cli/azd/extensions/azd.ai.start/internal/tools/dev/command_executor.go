package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tmc/langchaingo/callbacks"
)

// CommandExecutorTool implements the Tool interface for executing commands and scripts
type CommandExecutorTool struct {
	CallbacksHandler callbacks.Handler
}

func (t CommandExecutorTool) Name() string {
	return "execute_command"
}

func (t CommandExecutorTool) Description() string {
	return `Execute any command with arguments. Simple command execution without inference.

Input should be a JSON object with these fields:
{
  "command": "git",
  "args": ["status", "--porcelain"]
}

Required fields:
- command: The executable/command to run

Optional fields:
- args: Array of arguments to pass (default: [])

Examples:
- {"command": "git", "args": ["status"]}
- {"command": "npm", "args": ["install"]}
- {"command": "bash", "args": ["./build.sh", "--env", "prod"]}
- {"command": "powershell", "args": ["-ExecutionPolicy", "Bypass", "-File", "deploy.ps1"]}
- {"command": "python", "args": ["main.py", "--debug"]}
- {"command": "node", "args": ["server.js", "--port", "3000"]}
- {"command": "docker", "args": ["ps", "-a"]}
- {"command": "az", "args": ["account", "show"]}
- {"command": "kubectl", "args": ["get", "pods"]}`
}

type CommandRequest struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

func (t CommandExecutorTool) Call(ctx context.Context, input string) (string, error) {
	// Invoke callback for tool start
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, fmt.Sprintf("execute_command: %s", input))
	}

	if input == "" {
		err := fmt.Errorf("command execution request is required")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Parse the JSON request
	var req CommandRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		toolErr := fmt.Errorf("failed to parse command request: %w", err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Validate required fields
	if req.Command == "" {
		err := fmt.Errorf("command is required")
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, err)
		}
		return "", err
	}

	// Set defaults
	if req.Args == nil {
		req.Args = []string{}
	}

	// Execute the command (runs in current working directory)
	result, err := t.executeCommand(ctx, req.Command, req.Args)
	if err != nil {
		toolErr := fmt.Errorf("execution failed: %w", err)
		if t.CallbacksHandler != nil {
			t.CallbacksHandler.HandleToolError(ctx, toolErr)
		}
		return "", toolErr
	}

	// Format the output
	output := t.formatOutput(req.Command, req.Args, result)

	// Invoke callback for tool end
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, output)
	}

	return output, nil
}

func (t CommandExecutorTool) executeCommand(ctx context.Context, command string, args []string) (*executionResult, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	// cmd.Dir is not set, so it uses the current working directory
	// cmd.Env is not set, so it inherits the current environment

	var stdout, stderr strings.Builder

	// Always capture output for the tool to return
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	// Get exit code
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		}
	}

	return &executionResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Error:    err,
	}, nil
}

type executionResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Error    error
}

func (t CommandExecutorTool) formatOutput(command string, args []string, result *executionResult) string {
	var output strings.Builder

	// Show the full command that was executed
	fullCommand := command
	if len(args) > 0 {
		fullCommand += " " + strings.Join(args, " ")
	}

	output.WriteString(fmt.Sprintf("Executed: %s\n", fullCommand))
	output.WriteString(fmt.Sprintf("Exit code: %d\n", result.ExitCode))

	if result.ExitCode == 0 {
		output.WriteString("Status: ✅ Success\n")
	} else {
		output.WriteString("Status: ❌ Failed\n")
	}

	if result.Stdout != "" {
		output.WriteString("\n--- Standard Output ---\n")
		// Limit output to prevent overwhelming the LLM
		stdout := result.Stdout
		if len(stdout) > 2000 {
			stdout = stdout[:2000] + "\n... (output truncated)"
		}
		output.WriteString(stdout)
		output.WriteString("\n")
	}

	if result.Stderr != "" {
		output.WriteString("\n--- Standard Error ---\n")
		// Limit error output
		stderr := result.Stderr
		if len(stderr) > 1000 {
			stderr = stderr[:1000] + "\n... (error output truncated)"
		}
		output.WriteString(stderr)
		output.WriteString("\n")
	}

	if result.Error != nil && result.ExitCode != 0 {
		output.WriteString(fmt.Sprintf("\nError details: %s\n", result.Error.Error()))
	}

	return output.String()
}
