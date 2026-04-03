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

// NewBashScript creates a new ScriptExecutor for bash scripts.
func NewBashScript(commandRunner exec.CommandRunner, cwd string, envVars []string) tools.ScriptExecutor {
	return &bashScript{
		commandRunner: commandRunner,
		cwd:           cwd,
		envVars:       envVars,
	}
}

type bashScript struct {
	commandRunner exec.CommandRunner
	cwd           string
	envVars       []string
}

// Prepare is a no-op for bash — bash is assumed available on all platforms.
func (bs *bashScript) Prepare(_ context.Context, _ string) error {
	return nil
}

// Execute runs the specified bash script.
// When interactive is true will attach to stdin, stdout & stderr.
func (bs *bashScript) Execute(ctx context.Context, path string, options tools.ExecOptions) (exec.RunResult, error) {
	var runArgs exec.RunArgs
	// Bash likes all path separators in POSIX format
	path = strings.ReplaceAll(path, "\\", "/")

	if runtime.GOOS == "windows" {
		runArgs = exec.NewRunArgs("bash", path)
	} else {
		runArgs = exec.NewRunArgs("", path)
	}

	runArgs = runArgs.
		WithCwd(bs.cwd).
		WithEnv(bs.envVars).
		WithShell(true)

	if options.Interactive != nil {
		runArgs = runArgs.WithInteractive(*options.Interactive)
	}

	if options.StdOut != nil {
		runArgs = runArgs.WithStdOut(options.StdOut)
	}

	return bs.commandRunner.Run(ctx, runArgs)
}
