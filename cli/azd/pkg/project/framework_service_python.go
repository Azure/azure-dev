// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/otiai10/copy"
)

type pythonProject struct {
	env *environment.Environment
	cli *python.PythonCli
}

func NewPythonProject(commandRunner exec.CommandRunner, env *environment.Environment) FrameworkService {
	return &pythonProject{
		env: env,
		cli: python.NewPythonCli(commandRunner),
	}
}

func (pp *pythonProject) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{pp.cli}
}

func (pp *pythonProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

func (pp *pythonProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceRestoreResult, ServiceProgress]) {
			vEnvName := pp.getVenvName(serviceConfig)
			vEnvPath := path.Join(serviceConfig.Path(), vEnvName)

			_, err := os.Stat(vEnvPath)
			if err != nil {
				if os.IsNotExist(err) {
					err = pp.cli.CreateVirtualEnv(ctx, serviceConfig.Path(), vEnvName)
					if err != nil {
						task.SetError(fmt.Errorf(
							"python virtual environment for project '%s' could not be created: %w",
							serviceConfig.Path(),
							err,
						))
						return
					}
				} else {
					task.SetError(fmt.Errorf("python virtual environment for project '%s' is not accessible: %w", serviceConfig.Path(), err))
					return
				}
			}

			err = pp.cli.InstallRequirements(ctx, serviceConfig.Path(), vEnvName, "requirements.txt")
			if err != nil {
				task.SetError(
					fmt.Errorf("requirements for project '%s' could not be installed: %w", serviceConfig.Path(), err),
				)
				return
			}

			task.SetResult(&ServiceRestoreResult{})
		},
	)
}

func (pp *pythonProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
			publishRoot, err := os.MkdirTemp("", "azd")
			if err != nil {
				task.SetError(fmt.Errorf("creating package directory for %s: %w", serviceConfig.Name, err))
				return
			}

			publishSource := serviceConfig.Path()

			if serviceConfig.OutputPath != "" {
				publishSource = filepath.Join(publishSource, serviceConfig.OutputPath)
			}

			task.SetProgress(NewServiceProgress("Copying deployment package"))

			if err := copy.Copy(
				publishSource,
				publishRoot,
				skipPatterns(
					filepath.Join(publishSource, "__pycache__"), filepath.Join(publishSource, ".venv"),
					filepath.Join(publishSource, ".azure"))); err != nil {
				task.SetError(fmt.Errorf("publishing for %s: %w", serviceConfig.Name, err))
				return
			}

			task.SetResult(&ServiceBuildResult{
				BuildOutputPath: publishRoot,
			})
		},
	)
}

func (pp *pythonProject) getVenvName(serviceConfig *ServiceConfig) string {
	trimmedPath := strings.TrimSpace(serviceConfig.Path())
	if len(trimmedPath) > 0 && trimmedPath[len(trimmedPath)-1] == os.PathSeparator {
		trimmedPath = trimmedPath[:len(trimmedPath)-1]
	}
	_, projectDir := filepath.Split(trimmedPath)
	return projectDir + "_env"
}

// skipPatterns returns a `copy.Options` which will skip any files
// that match a given pattern. Matching is done with `filepath.Match`.
func skipPatterns(patterns ...string) copy.Options {
	return copy.Options{
		Skip: func(src string) (bool, error) {
			for _, pattern := range patterns {
				skip, err := filepath.Match(pattern, src)
				switch {
				case err != nil:
					return false, fmt.Errorf("error matching pattern %s: %w", pattern, err)
				case skip:
					return true, nil
				}
			}

			return false, nil
		},
	}
}
