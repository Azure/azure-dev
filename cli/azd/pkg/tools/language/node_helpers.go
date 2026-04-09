// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// nodeTools abstracts the Node.js CLI operations needed by
// the JS and TS executors, decoupling them from the concrete
// [node.Cli] for testability. [node.Cli] satisfies this
// interface.
type nodeTools interface {
	CheckInstalled(ctx context.Context) error
	Install(
		ctx context.Context,
		projectPath string,
		env []string,
	) error
}

// prepareNodeProject handles the shared Prepare phase for
// Node.js-based executors (JavaScript and TypeScript):
//  1. Verify Node.js is installed.
//  2. Discover package.json via [DiscoverProjectFile].
//  3. If found, run the package manager's install command.
//
// Returns the project context (may be nil for standalone
// scripts that have no package.json).
func prepareNodeProject(
	ctx context.Context,
	nodeCli nodeTools,
	scriptPath string,
	execCtx tools.ExecutionContext,
) (*ProjectContext, error) {
	// 1. Verify Node.js is installed.
	if err := nodeCli.CheckInstalled(ctx); err != nil {
		return nil, &errorhandler.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"Node.js is required to run this hook "+
					"but was not found on PATH: %w",
				err,
			),
			Message: "Node.js is required to run this hook " +
				"but was not found.",
			Suggestion: "Install Node.js 18+ from " +
				"https://nodejs.org/",
			Links: []errorhandler.ErrorLink{{
				Title: "Download Node.js",
				URL:   "https://nodejs.org/",
			}},
		}
	}

	// 2. Discover project context for dependency installation.
	projCtx, err := DiscoverProjectFile(
		scriptPath, execCtx.BoundaryDir,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"discovering project file: %w", err,
		)
	}

	// No package.json found — standalone script.
	if projCtx == nil {
		return nil, nil
	}

	// Only install when the discovered project file is a
	// package.json (Language == HookKindJavaScript). Skip
	// if a Python or .NET project file was found instead.
	if projCtx.Language != HookKindJavaScript {
		return nil, nil
	}

	// 3. Install dependencies.
	if err := nodeCli.Install(
		ctx, projCtx.ProjectDir, execCtx.EnvVars,
	); err != nil {
		return nil, fmt.Errorf(
			"installing Node.js dependencies in %s: %w",
			projCtx.ProjectDir, err,
		)
	}

	return projCtx, nil
}

// buildNodeRunArgs constructs the [exec.RunArgs] for running a
// Node.js script with the correct cwd, environment, interactive
// mode, and stdout configuration. Used by both JS and TS
// executors to avoid duplicating the same argument assembly.
func buildNodeRunArgs(
	cmd string,
	args []string,
	scriptPath string,
	execCtx tools.ExecutionContext,
) exec.RunArgs {
	allArgs := slices.Concat(args, []string{scriptPath})
	runArgs := exec.
		NewRunArgs(cmd, allArgs...).
		WithEnv(execCtx.EnvVars)

	// Prefer configured cwd; fall back to script's directory.
	cwd := execCtx.Cwd
	if cwd == "" {
		cwd = filepath.Dir(scriptPath)
	}
	runArgs = runArgs.WithCwd(cwd)

	if execCtx.Interactive != nil {
		runArgs = runArgs.WithInteractive(
			*execCtx.Interactive,
		)
	}
	if execCtx.StdOut != nil {
		runArgs = runArgs.WithStdOut(execCtx.StdOut)
	}

	return runArgs
}
