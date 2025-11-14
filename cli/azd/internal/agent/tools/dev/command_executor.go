// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"

	"github.com/azure/azure-dev/internal/agent/tools/common"
	"github.com/mark3labs/mcp-go/mcp"
)

// CommandExecutorTool implements the Tool interface for executing commands and scripts
type CommandExecutorTool struct {
	common.BuiltInTool
}

func (t CommandExecutorTool) Name() string {
	return "execute_command"
}

func (t CommandExecutorTool) Annotations() mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           "Execute Terminal Command",
		ReadOnlyHint:    common.ToPtr(false),
		DestructiveHint: common.ToPtr(true),
		IdempotentHint:  common.ToPtr(false),
		OpenWorldHint:   common.ToPtr(true),
	}
}

func (t CommandExecutorTool) Description() string {
	return `Execute any command with arguments through the system shell for better compatibility.

Input should be a JSON object with these fields:
{
  "command": "git",
  "args": ["status", "--porcelain"]
}

Required fields:
- command: The executable/command to run

Optional fields:
- args: Array of arguments to pass (default: [])

Returns a JSON response with execution details:
- Success responses include: command, fullCommand, exitCode, success, stdout, stderr
- Error responses include: error (true), message

The tool automatically uses the appropriate shell:
- Windows: cmd.exe /C for built-in commands and proper path resolution
- Unix/Linux/macOS: sh -c for POSIX compatibility

Examples:
- {"command": "git", "args": ["status"]}
- {"command": "npm", "args": ["install"]}
- {"command": "dir"} (Windows built-in command)
- {"command": "ls", "args": ["-la"]} (Unix command)
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

type CommandResponse struct {
	Command     string `json:"command"`
	FullCommand string `json:"fullCommand"`
	ExitCode    int    `json:"exitCode"`
	Success     bool   `json:"success"`
	Stdout      string `json:"stdout,omitempty"`
	Stderr      string `json:"stderr,omitempty"`
}

func (t CommandExecutorTool) Call(ctx context.Context, input string) (string, error) {
	if input == "" {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: "command execution request is required",
		}

		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	// Parse the JSON request
	var req CommandRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("failed to parse command request: %s", err.Error()),
		}

		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	// Validate required fields
	if req.Command == "" {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: "command is required",
		}

		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	if req.Command == "azd" {
		// Ensure --no-prompt is included in args to prevent interactive prompts
		// that would block execution in agent/automation scenarios. The azd CLI
		// may prompt for user input (confirmations, selections, etc.) which cannot
		// be handled in a non-interactive agent context, so we force non-interactive mode.
		if !slices.Contains(req.Args, "--no-prompt") {
			req.Args = append(req.Args, "--no-prompt")
		}
	}

	// Set defaults
	if req.Args == nil {
		req.Args = []string{}
	}

	// Execute the command (runs in current working directory)
	result, err := t.executeCommand(ctx, req.Command, req.Args)
	if err != nil {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("execution failed: %s", err.Error()),
		}

		jsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(jsonData), nil
	}

	// Create the success response (even if command had non-zero exit code)
	response := t.createSuccessResponse(req.Command, req.Args, result)

	// Convert to JSON
	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		errorResponse := common.ErrorResponse{
			Error:   true,
			Message: fmt.Sprintf("failed to marshal JSON response: %s", err.Error()),
		}

		errorJsonData, _ := json.MarshalIndent(errorResponse, "", "  ")
		return string(errorJsonData), nil
	}

	return string(jsonData), nil
}

func (t CommandExecutorTool) executeCommand(ctx context.Context, command string, args []string) (*executionResult, error) {
	// Handle shell-specific command execution for better compatibility
	var cmd *exec.Cmd

	if runtime.GOOS == "windows" {
		// On Windows, use cmd.exe to handle built-in commands and path resolution
		allArgs := append([]string{"/C", command}, args...)
		// #nosec G204 - Command execution is the intended functionality of this tool
		cmd = exec.CommandContext(ctx, "cmd", allArgs...)
	} else {
		// On Unix-like systems, use sh for better command resolution
		fullCommand := command
		if len(args) > 0 {
			fullCommand += " " + strings.Join(args, " ")
		}
		// #nosec G204 - Command execution is the intended functionality of this tool
		cmd = exec.CommandContext(ctx, "sh", "-c", fullCommand)
	}

	// Set working directory explicitly to current directory
	if wd, err := os.Getwd(); err == nil {
		cmd.Dir = wd
	}

	// Inherit environment variables
	cmd.Env = os.Environ()

	var stdout, stderr strings.Builder

	// Always capture output for the tool to return
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	// Get exit code and determine if this is a system error vs command error
	exitCode := 0
	var cmdError error

	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			// Command ran but exited with non-zero code - this is normal
			exitCode = exitError.ExitCode()
			cmdError = nil // Don't treat non-zero exit as a system error
		} else {
			// System error (command not found, permission denied, etc.)
			cmdError = err
		}
	}

	return &executionResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Error:    cmdError, // Only system errors, not command exit codes
	}, cmdError // Return system errors to caller
}

func (t CommandExecutorTool) createSuccessResponse(command string, args []string, result *executionResult) CommandResponse {
	// Create full command string
	fullCommand := command
	if len(args) > 0 {
		fullCommand += " " + strings.Join(args, " ")
	}

	// Limit output to prevent overwhelming the response
	stdout := result.Stdout
	if len(stdout) > 2000 {
		stdout = stdout[:2000] + "\n... (output truncated)"
	}

	stderr := result.Stderr
	if len(stderr) > 1000 {
		stderr = stderr[:1000] + "\n... (error output truncated)"
	}

	return CommandResponse{
		Command:     command,
		FullCommand: fullCommand,
		ExitCode:    result.ExitCode,
		Success:     result.ExitCode == 0,
		Stdout:      stdout,
		Stderr:      stderr,
	}
}

type executionResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Error    error
}
