// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

type dotnetProject struct {
	config    *ServiceConfig
	env       *environment.Environment
	dotnetCli dotnet.DotNetCli
}

func (dp *dotnetProject) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{dp.dotnetCli}
}

func (dp *dotnetProject) Package(ctx context.Context, progress chan<- string) (string, error) {
	publishRoot, err := os.MkdirTemp("", "azd")
	if err != nil {
		return "", fmt.Errorf("creating package directory for %s: %w", dp.config.Name, err)
	}

	progress <- "Creating deployment package"
	if err := dp.dotnetCli.Publish(ctx, dp.config.Path(), publishRoot); err != nil {
		return "", err
	}

	if dp.config.OutputPath != "" {
		publishRoot = filepath.Join(publishRoot, dp.config.OutputPath)
	}

	return publishRoot, nil
}

func (dp *dotnetProject) InstallDependencies(ctx context.Context) error {
	if err := dp.dotnetCli.Restore(ctx, dp.config.Path()); err != nil {
		return err
	}
	return nil
}

func (dp *dotnetProject) setUserSecretsFromOutputs(ctx context.Context, args ServiceLifecycleEventArgs) error {
	bicepOutputArgs := args.Args["bicepOutput"]
	if bicepOutputArgs == nil {
		log.Println("no bicep outputs set as secrets to dotnet project, map args.Args doesn't contain key \"bicepOutput\"")
		return nil
	}

	bicepOutput, ok := bicepOutputArgs.(map[string]provisioning.OutputParameter)
	if !ok {
		return fmt.Errorf("fail on interface conversion: no type in map")
	}

	for key, val := range bicepOutput {
		if err := dp.dotnetCli.SetSecret(ctx, normalizeDotNetSecret(key), fmt.Sprint(val.Value), dp.config.Path()); err != nil {
			return err
		}
	}
	return nil
}

func (dp *dotnetProject) Initialize(ctx context.Context) error {
	if err := dp.dotnetCli.InitializeSecret(ctx, dp.config.Path()); err != nil {
		return err
	}
	handler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		return dp.setUserSecretsFromOutputs(ctx, args)
	}
	if err := dp.config.AddHandler(EnvironmentUpdated, handler); err != nil {
		return err
	}

	return nil
}

func normalizeDotNetSecret(key string) string {
	// dotnet recognizes "__" as the hierarchy key separator for environment variables, but for user secrets, it has to be
	// ":".
	return strings.ReplaceAll(key, "__", ":")
}

func NewDotNetProject(
	commandRunner exec.CommandRunner, config *ServiceConfig, env *environment.Environment,
) FrameworkService {
	return &dotnetProject{
		config:    config,
		env:       env,
		dotnetCli: dotnet.NewDotNetCli(commandRunner),
	}
}
