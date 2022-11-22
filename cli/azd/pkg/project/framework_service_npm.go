// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/npm"
	"github.com/otiai10/copy"
)

type npmProject struct {
	config *ServiceConfig
	env    *environment.Environment
	cli    npm.NpmCli
}

func (np *npmProject) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{np.cli}
}

func (np *npmProject) Package(ctx context.Context, progress chan<- string) (string, error) {
	publishRoot, err := os.MkdirTemp("", "azd")
	if err != nil {
		return "", fmt.Errorf("creating package directory for %s: %w", np.config.Name, err)
	}

	// Run NPM install
	progress <- "Installing dependencies"
	if err := np.cli.Install(ctx, np.config.Path(), false); err != nil {
		return "", err
	}

	// Run Build, injecting env.
	envs := make([]string, 0, len(np.env.Values)+1)
	for k, v := range np.env.Values {
		envs = append(envs, fmt.Sprintf("%s=%s", k, v))
	}
	envs = append(envs, "NODE_ENV=production")

	progress <- "Building service"
	if err := np.cli.Build(ctx, np.config.Path(), envs); err != nil {
		return "", err
	}

	// Copy directory rooted by dist to publish root.
	publishSource := np.config.Path()

	if np.config.OutputPath != "" {
		publishSource = filepath.Join(publishSource, np.config.OutputPath)
	}

	progress <- "Copying deployment package"
	if err := copy.Copy(
		publishSource,
		publishRoot,
		skipPatterns(
			filepath.Join(publishSource, "node_modules"), filepath.Join(publishSource, ".azure"))); err != nil {
		return "", fmt.Errorf("publishing for %s: %w", np.config.Name, err)
	}

	return publishRoot, nil
}

func (np *npmProject) InstallDependencies(ctx context.Context) error {
	if err := np.cli.Install(ctx, np.config.Path(), false); err != nil {
		return err
	}
	return nil
}

func (np *npmProject) Initialize(ctx context.Context) error {
	return nil
}

func NewNpmProject(commandRunner exec.CommandRunner, config *ServiceConfig, env *environment.Environment) FrameworkService {
	return &npmProject{
		config: config,
		env:    env,
		cli:    npm.NewNpmCli(commandRunner),
	}
}
