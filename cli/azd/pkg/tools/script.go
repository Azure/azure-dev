// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"io"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

// ExecOptions provide configuration for how scripts are executed.
type ExecOptions struct {
	Interactive *bool
	StdOut      io.Writer
}

// ScriptExecutor is the unified interface for all hook script execution.
// Every executor follows a two-phase lifecycle:
//  1. Prepare — validate prerequisites, resolve tools, install dependencies
//  2. Execute — run the script
type ScriptExecutor interface {
	// Prepare performs pre-execution setup. For shell scripts this may
	// validate tool availability; for language scripts this may create
	// virtual environments and install dependencies.
	Prepare(ctx context.Context, scriptPath string) error

	// Execute runs the script at the given path.
	Execute(ctx context.Context, scriptPath string, options ExecOptions) (exec.RunResult, error)
}
