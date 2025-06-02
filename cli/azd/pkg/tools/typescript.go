// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.


package tools

import (
	   "context"
	   "fmt"
	   "strings"

	   "github.com/azure/azure-dev/cli/azd/pkg/exec"
)

// RunCommand runs a command with the given environment and working directory, returns stdout or error.
func RunCommand(ctx context.Context, cmd string, args []string, envVars []string, cwd string) (string, error) {
	runner := exec.NewCommandRunner(nil)
	runArgs := exec.RunArgs{
		Cmd:  cmd,
		Args: args,
		Cwd:  cwd,
		Env:  envVars,
	}
	result, err := runner.Run(ctx, runArgs)
	if err != nil {
		return "", fmt.Errorf("command failed: %w\nstdout: %s\nstderr: %s", err, result.Stdout, result.Stderr)
	}
	return strings.TrimSpace(result.Stdout), nil
}
