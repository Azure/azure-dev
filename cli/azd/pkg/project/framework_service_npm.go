// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/npm"
	"github.com/otiai10/copy"
)

type npmProject struct {
	env *environment.Environment
	cli npm.NpmCli
}

// NewNpmProject creates a new instance of a NPM project
func NewNpmProject(commandRunner exec.CommandRunner, env *environment.Environment) FrameworkService {
	return &npmProject{
		env: env,
		cli: npm.NewNpmCli(commandRunner),
	}
}

// Gets the required external tools for the project
func (np *npmProject) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{np.cli}
}

// Initializes the NPM project
func (np *npmProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Restores dependencies for the NPM project using npm install command
func (np *npmProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceRestoreResult, ServiceProgress]) {
			// Run NPM install
			task.SetProgress(NewServiceProgress("Installing dependencies"))
			if err := np.cli.Install(ctx, serviceConfig.Path(), false); err != nil {
				task.SetError(err)
				return
			}

			task.SetResult(&ServiceRestoreResult{})
		},
	)
}

// Builds the project executing the npm `build` script defined within the project package.json
func (np *npmProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
			publishRoot, err := os.MkdirTemp("", "azd")
			if err != nil {
				task.SetError(fmt.Errorf("creating package directory for %s: %w", serviceConfig.Name, err))
				return
			}

			// Run Build, injecting env.
			envs := append(np.env.Environ(), "NODE_ENV=production")

			task.SetProgress(NewServiceProgress("Building service"))
			if err := np.cli.Build(ctx, serviceConfig.Path(), envs); err != nil {
				task.SetError(err)
				return
			}

			// Copy directory rooted by dist to publish root.
			publishSource := serviceConfig.Path()

			if serviceConfig.OutputPath != "" {
				publishSource = filepath.Join(publishSource, serviceConfig.OutputPath)
			}

			task.SetProgress(NewServiceProgress("Copying deployment package"))
			if err := copy.Copy(
				publishSource,
				publishRoot,
				skipPatterns(
					filepath.Join(publishSource, "node_modules"), filepath.Join(publishSource, ".azure"))); err != nil {
				task.SetError(fmt.Errorf("publishing for %s: %w", serviceConfig.Name, err))
				return
			}

			task.SetResult(&ServiceBuildResult{
				Restore:         restoreOutput,
				BuildOutputPath: publishRoot,
			})
		},
	)
}
