// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

type InvokeOptions struct {
	Args        []string
	Env         []string
	StdIn       io.Reader
	StdOut      io.Writer
	StdErr      io.Writer
	Interactive bool
}

type Runner struct {
	commandRunner exec.CommandRunner
}

func NewRunner(commandRunner exec.CommandRunner) *Runner {
	return &Runner{
		commandRunner: commandRunner,
	}
}

// Invoke runs the extension with the provided arguments
func (r *Runner) Invoke(ctx context.Context, extension *Extension, options *InvokeOptions) (*exec.RunResult, error) {
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config directory: %w", err)
	}

	extension.ensureInit()

	extensionPath := filepath.Join(userConfigDir, extension.Path)
	if _, err := os.Stat(extensionPath); err != nil {
		return nil, fmt.Errorf("extension path '%s' not found: %w", extensionPath, err)
	}

	runArgs := exec.NewRunArgs(extensionPath, options.Args...)
	if len(options.Env) > 0 {
		runArgs = runArgs.WithEnv(options.Env)
	}

	if options.Interactive {
		runArgs = runArgs.WithInteractive(true)
	} else {
		if options.StdIn != nil {
			runArgs = runArgs.WithStdIn(options.StdIn)
		}

		if options.StdOut != nil {
			runArgs = runArgs.WithStdOut(options.StdOut)
		}

		if options.StdErr != nil {
			runArgs = runArgs.WithStdErr(options.StdErr)
		}
	}

	runResult, err := r.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return &runResult, &ExtensionRunError{Err: err, ExtensionId: extension.Id}
	}
	return &runResult, nil
}

// ExtensionRunError represents an error that occurred while running an extension.
type ExtensionRunError struct {
	ExtensionId string
	Err         error
}

func (e *ExtensionRunError) Error() string {
	return fmt.Sprintf("extension '%s' run failed: %v", e.ExtensionId, e.Err)
}

func (e *ExtensionRunError) Unwrap() error {
	return e.Err
}
