// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"io"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

// ExecutionContext provides the per-invocation execution environment
// for a hook. The HooksRunner constructs this from the validated
// HookConfig, resolving secrets, merging directories, and building
// the environment.
type ExecutionContext struct {
	// Cwd is the working directory for execution.
	Cwd string

	// EnvVars is the merged environment for the process, including
	// resolved secrets and the azd environment.
	EnvVars []string

	// BoundaryDir is the project or service root directory.
	// Language executors walk upward from the script to BoundaryDir
	// to discover dependency files (requirements.txt, package.json).
	BoundaryDir string

	// Interactive controls whether stdin is attached.
	Interactive *bool

	// StdOut overrides the default stdout for the process.
	StdOut io.Writer
}

// HookExecutor is the unified interface for all hook execution.
// Every executor follows a two-phase lifecycle:
//  1. Prepare — validate prerequisites, resolve tools, install dependencies
//  2. Execute — run the hook
type HookExecutor interface {
	// Prepare performs pre-execution setup such as runtime validation,
	// virtual environment creation, or dependency installation.
	Prepare(ctx context.Context, scriptPath string, execCtx ExecutionContext) error

	// Execute runs the hook at the given path.
	Execute(ctx context.Context, scriptPath string, execCtx ExecutionContext) (exec.RunResult, error)
}
