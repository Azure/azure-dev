// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bash

import (
	"context"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// NewExecutor creates a bash HookExecutor. Takes only IoC-injectable deps.
func NewExecutor(commandRunner exec.CommandRunner) tools.HookExecutor {
	return &bashExecutor{commandRunner: commandRunner}
}

type bashExecutor struct {
	commandRunner exec.CommandRunner
}

// Prepare is a no-op for bash — bash is assumed available on all platforms.
func (b *bashExecutor) Prepare(_ context.Context, _ string, _ tools.ExecutionContext) error {
	return nil
}

// Execute runs the specified bash script.
// When interactive is true will attach to stdin, stdout & stderr.
func (b *bashExecutor) Execute(
	ctx context.Context, path string, execCtx tools.ExecutionContext,
) (exec.RunResult, error) {
	var runArgs exec.RunArgs
	// Bash likes all path separators in POSIX format
	path = strings.ReplaceAll(path, "\\", "/")

	if runtime.GOOS == "windows" {
		runArgs = exec.NewRunArgs("bash", path)
	} else {
		runArgs = exec.NewRunArgs("", path)
	}

	runArgs = runArgs.
		WithCwd(execCtx.Cwd).
		WithEnv(execCtx.EnvVars).
		WithShell(true)

	if execCtx.Interactive != nil {
		runArgs = runArgs.WithInteractive(*execCtx.Interactive)
	}

	if execCtx.StdOut != nil {
		runArgs = runArgs.WithStdOut(execCtx.StdOut)
	}

	return b.commandRunner.Run(ctx, runArgs)
}
