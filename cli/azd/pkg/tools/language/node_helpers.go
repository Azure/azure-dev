// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/node"
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

// nodeHookConfig holds the typed configuration for Node.js-
// based hook executors (JavaScript and TypeScript). Unmarshalled
// from [tools.ExecutionContext.Config] via
// [tools.UnmarshalHookConfig].
type nodeHookConfig struct {
	// PackageManager overrides the default package manager
	// (npm) used for dependency installation. Valid values:
	// "npm", "pnpm", "yarn". When empty, the default CLI
	// is used.
	PackageManager string `json:"packageManager"`
}

// validNodePackageManagers maps user-facing config values to
// [node.PackageManagerKind] constants. Matches the values
// accepted by [nodePackageManagerFromConfig].
var validNodePackageManagers = map[string]node.PackageManagerKind{
	"npm":  node.PackageManagerNpm,
	"pnpm": node.PackageManagerPnpm,
	"yarn": node.PackageManagerYarn,
}

// nodePackageManagerFromConfig reads and validates the optional
// "packageManager" override from hook config. Returns empty
// string when not set. Returns an error for invalid values.
func nodePackageManagerFromConfig(
	config map[string]any,
) (node.PackageManagerKind, error) {
	cfg, err := tools.UnmarshalHookConfig[nodeHookConfig](
		config,
	)
	if err != nil {
		return "", fmt.Errorf(
			"reading node hook config: %w", err,
		)
	}

	if cfg.PackageManager == "" {
		return "", nil
	}

	pm, ok := validNodePackageManagers[cfg.PackageManager]
	if !ok {
		return "", fmt.Errorf(
			"invalid packageManager config value %q: "+
				"must be npm, pnpm, or yarn",
			cfg.PackageManager,
		)
	}

	return pm, nil
}

// prepareNodeProject handles the shared Prepare phase for
// Node.js-based executors (JavaScript and TypeScript):
//  1. Verify Node.js is installed.
//  2. Discover package.json via [DiscoverNodeProject].
//  3. If found, run the package manager's install command.
//
// When execCtx.Config contains a "packageManager" key, the
// specified package manager CLI is used instead of the
// default. Returns the project context (may be nil for
// standalone scripts that have no package.json).
func prepareNodeProject(
	ctx context.Context,
	nodeCli nodeTools,
	commandRunner exec.CommandRunner,
	scriptPath string,
	execCtx tools.ExecutionContext,
) (*ProjectContext, error) {
	// Check for a packageManager config override. When set,
	// create a CLI for the specified package manager instead
	// of using the default (npm) from the IoC container.
	pm, err := nodePackageManagerFromConfig(execCtx.Config)
	if err != nil {
		return nil, err
	}
	if pm != "" {
		nodeCli = node.NewCliWithPackageManager(
			commandRunner, pm,
		)
	}

	// 1. Verify Node.js is installed.
	if err := nodeCli.CheckInstalled(ctx); err != nil {
		// If the error already carries rich context (e.g.
		// from the error-handling pipeline), pass it through
		// rather than wrapping with a generic message.
		if sugErr, ok := errors.AsType[*errorhandler.ErrorWithSuggestion](err); ok {
			return nil, sugErr
		}

		// For other errors (missing from PATH, version
		// mismatch, etc.), provide install guidance.
		return nil, &errorhandler.ErrorWithSuggestion{
			Err: err,
			Message: "Node.js is required to run " +
				"JavaScript/TypeScript hooks.",
			Suggestion: "Install Node.js 18.0.0 or " +
				"later from https://nodejs.org/",
			Links: []errorhandler.ErrorLink{{
				Title: "Download Node.js",
				URL:   "https://nodejs.org/en/download/",
			}},
		}
	}

	// 2. Discover Node.js project context (package.json only).
	// Uses DiscoverNodeProject instead of the generic
	// DiscoverProjectFile to avoid Python/DotNet project files
	// shadowing package.json in mixed-language directories.
	projCtx, err := DiscoverNodeProject(
		scriptPath, execCtx.BoundaryDir,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"discovering Node.js project file: %w", err,
		)
	}

	// No package.json found — standalone script.
	if projCtx == nil {
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
