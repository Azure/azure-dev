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
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
)

type pythonProject struct {
	env *environment.Environment
	cli *python.PythonCli
}

// NewPythonProject creates a new instance of the Python project
func NewPythonProject(cli *python.PythonCli, env *environment.Environment) FrameworkService {
	return &pythonProject{
		env: env,
		cli: cli,
	}
}

func (pp *pythonProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		// Python does not require compilation and will just package the raw source files
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			RequireBuild:   false,
		},
	}
}

// Gets the required external tools for the project
func (pp *pythonProject) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{pp.cli}
}

// Initializes the Python project
func (pp *pythonProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

// Restores the project dependencies using PIP requirements.txt
func (pp *pythonProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceRestoreResult, ServiceProgress]) {
			task.SetProgress(NewServiceProgress("Checking for Python virtual environment"))
			vEnvName := pp.getVenvName(serviceConfig)
			vEnvPath := path.Join(serviceConfig.Path(), vEnvName)

			_, err := os.Stat(vEnvPath)
			if err != nil {
				if os.IsNotExist(err) {
					task.SetProgress(NewServiceProgress("Creating Python virtual environment"))
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
					task.SetError(
						fmt.Errorf("python virtual environment for project '%s' is not accessible: %w", serviceConfig.Path(), err),
					)
					return
				}
			}

			task.SetProgress(NewServiceProgress("Installing Python PIP dependencies"))
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

// Build for Python apps performs a no-op and returns the service path with an optional output path when specified.
func (pp *pythonProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
			publishSource := serviceConfig.Path()

			if serviceConfig.OutputPath != "" {
				publishSource = filepath.Join(publishSource, serviceConfig.OutputPath)
			}

			task.SetResult(&ServiceBuildResult{
				Restore:         restoreOutput,
				BuildOutputPath: publishSource,
			})
		},
	)
}

func (pp *pythonProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			publishRoot, err := os.MkdirTemp("", "azd")
			if err != nil {
				task.SetError(fmt.Errorf("creating package directory for %s: %w", serviceConfig.Name, err))
				return
			}

			publishSource := buildOutput.BuildOutputPath
			if publishSource == "" {
				publishSource = filepath.Join(serviceConfig.Path(), serviceConfig.OutputPath)
			}

			task.SetProgress(NewServiceProgress("Copying deployment package"))
			if err := buildForZip(
				publishSource,
				publishRoot,
				buildForZipOptions{
					excludeConditions: []excludeDirEntryCondition{
						excludeVirtualEnv,
						excludePyCache,
					},
				}); err != nil {
				task.SetError(fmt.Errorf("publishing for %s: %w", serviceConfig.Name, err))
				return
			}

			task.SetResult(&ServicePackageResult{
				Build:       buildOutput,
				PackagePath: publishRoot,
			})
		},
	)
}

const cVenvConfigFileName = "pyvenv.cfg"

func isPythonVirtualEnv(path string) bool {
	// check if `pyvenv.cfg` is within the folder
	if _, err := os.Stat(filepath.Join(path, cVenvConfigFileName)); err == nil {
		return true
	}
	return false
}

func excludeVirtualEnv(path string, file os.FileInfo) bool {
	return file.IsDir() && isPythonVirtualEnv(path)
}

func excludePyCache(path string, file os.FileInfo) bool {
	return file.IsDir() && strings.ToLower(file.Name()) == "__pycache__"
}

func (pp *pythonProject) getVenvName(serviceConfig *ServiceConfig) string {
	trimmedPath := strings.TrimSpace(serviceConfig.Path())
	if len(trimmedPath) > 0 && trimmedPath[len(trimmedPath)-1] == os.PathSeparator {
		trimmedPath = trimmedPath[:len(trimmedPath)-1]
	}
	_, projectDir := filepath.Split(trimmedPath)
	return projectDir + "_env"
}
