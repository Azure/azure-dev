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
	cli *python.Cli
}

// NewPythonProject creates a new instance of the Python project
func NewPythonProject(cli *python.Cli, env *environment.Environment) FrameworkService {
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
func (pp *pythonProject) RequiredExternalTools(_ context.Context, _ *ServiceConfig) []tools.ExternalTool {
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
	progress *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	progress.SetProgress(NewServiceProgress("Checking for Python virtual environment"))
	vEnvName := pp.getVenvName(serviceConfig)
	vEnvPath := path.Join(serviceConfig.Path(), vEnvName)

	_, err := os.Stat(vEnvPath)
	if err != nil {
		if os.IsNotExist(err) {
			progress.SetProgress(NewServiceProgress("Creating Python virtual environment"))
			err = pp.cli.CreateVirtualEnv(ctx, serviceConfig.Path(), vEnvName)
			if err != nil {
				return nil, fmt.Errorf(
					"python virtual environment for project '%s' could not be created: %w",
					serviceConfig.Path(),
					err,
				)
			}
		} else {
			return nil, fmt.Errorf(
				"python virtual environment for project '%s' is not accessible: %w", serviceConfig.Path(), err)
		}
	}

	progress.SetProgress(NewServiceProgress("Installing Python PIP dependencies"))
	err = pp.cli.InstallRequirements(ctx, serviceConfig.Path(), vEnvName, "requirements.txt")
	if err != nil {
		return nil, fmt.Errorf("requirements for project '%s' could not be installed: %w", serviceConfig.Path(), err)
	}

	return &ServiceRestoreResult{}, nil
}

// Build for Python apps performs a no-op and returns the service path with an optional output path when specified.
func (pp *pythonProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
	progress *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	buildSource := serviceConfig.Path()

	if serviceConfig.OutputPath != "" {
		buildSource = filepath.Join(buildSource, serviceConfig.OutputPath)
	}

	return &ServiceBuildResult{
		Restore:         restoreOutput,
		BuildOutputPath: buildSource,
	}, nil
}

func (pp *pythonProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	packageDest, err := os.MkdirTemp("", "azd")
	if err != nil {
		return nil, fmt.Errorf("creating package directory for %s: %w", serviceConfig.Name, err)
	}

	packageSource := buildOutput.BuildOutputPath
	if packageSource == "" {
		packageSource = filepath.Join(serviceConfig.Path(), serviceConfig.OutputPath)
	}

	if entries, err := os.ReadDir(packageSource); err != nil || len(entries) == 0 {
		return nil, fmt.Errorf("package source '%s' is empty or does not exist", packageSource)
	}

	progress.SetProgress(NewServiceProgress("Copying deployment package"))
	if err := buildForZip(
		packageSource,
		packageDest,
		buildForZipOptions{}); err != nil {

		return nil, fmt.Errorf("packaging for %s: %w", serviceConfig.Name, err)
	}

	if err := validatePackageOutput(packageDest); err != nil {
		return nil, err
	}

	return &ServicePackageResult{
		Build:       buildOutput,
		PackagePath: packageDest,
	}, nil
}

func isPythonVirtualEnv(path string) bool {
	// check if `pyvenv.cfg` is within the folder
	if _, err := os.Stat(filepath.Join(path, "pyvenv.cfg")); err == nil {
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
