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

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/otiai10/copy"
)

type pythonProject struct {
	config *ServiceConfig
	env    *environment.Environment
	cli    *python.PythonCli
}

func (pp *pythonProject) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{pp.cli}
}

func (pp *pythonProject) Package(_ context.Context, progress chan<- string) (string, error) {
	publishRoot, err := os.MkdirTemp("", "azd")
	if err != nil {
		return "", fmt.Errorf("creating package directory for %s: %w", pp.config.Name, err)
	}

	publishSource := pp.config.Path()

	if pp.config.OutputPath != "" {
		publishSource = filepath.Join(publishSource, pp.config.OutputPath)
	}

	progress <- "Copying deployment package"
	if err := copy.Copy(
		publishSource,
		publishRoot,
		skipPatterns(
			filepath.Join(publishSource, "__pycache__"), filepath.Join(publishSource, ".azure"))); err != nil {
		return "", fmt.Errorf("publishing for %s: %w", pp.config.Name, err)
	}

	return publishRoot, nil
}

func (pp *pythonProject) InstallDependencies(ctx context.Context) error {
	vEnvName := pp.getVenvName()
	vEnvPath := path.Join(pp.config.Path(), vEnvName)

	_, err := os.Stat(vEnvPath)
	if err != nil {
		if os.IsNotExist(err) {
			err = pp.cli.CreateVirtualEnv(ctx, pp.config.Path(), vEnvName)
			if err != nil {
				return fmt.Errorf(
					"python virtual environment for project '%s' could not be created: %w",
					pp.config.Path(),
					err,
				)
			}
		} else {
			return fmt.Errorf("python virtual environment for project '%s' is not accessible: %w", pp.config.Path(), err)
		}
	}

	err = pp.cli.InstallRequirements(ctx, pp.config.Path(), vEnvName, "requirements.txt")
	if err != nil {
		return fmt.Errorf("requirements for project '%s' could not be installed: %w", pp.config.Path(), err)
	}

	return nil
}

func (pp *pythonProject) getVenvName() string {
	trimmedPath := strings.TrimSpace(pp.config.Path())
	if len(trimmedPath) > 0 && trimmedPath[len(trimmedPath)-1] == os.PathSeparator {
		trimmedPath = trimmedPath[:len(trimmedPath)-1]
	}
	_, projectDir := filepath.Split(trimmedPath)
	return projectDir + "_env"
}

func (pp *pythonProject) Config() *ServiceConfig {
	return pp.config
}

func (pp *pythonProject) Initialize(ctx context.Context) error {
	return nil
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

func NewPythonProject(
	commandRunner exec.CommandRunner, config *ServiceConfig, env *environment.Environment,
) FrameworkService {
	return &pythonProject{
		config: config,
		env:    env,
		cli:    python.NewPythonCli(commandRunner),
	}
}
