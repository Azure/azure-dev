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

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
)

const (
	defaultDotNetBuildConfiguration string = "Release"
)

type dotnetProject struct {
	env       *environment.Environment
	dotnetCli dotnet.DotNetCli
}

// NewDotNetProject creates a new instance of a dotnet project
func NewDotNetProject(
	dotNetCli dotnet.DotNetCli,
	env *environment.Environment,
) FrameworkService {
	return &dotnetProject{
		env:       env,
		dotnetCli: dotNetCli,
	}
}

func (dp *dotnetProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		// dotnet will automatically restore & build the project if needed
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			RequireBuild:   false,
		},
	}
}

// Gets the required external tools for the project
func (dp *dotnetProject) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{dp.dotnetCli}
}

// Initializes the docker project
func (dp *dotnetProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	if err := dp.dotnetCli.InitializeSecret(ctx, serviceConfig.Path()); err != nil {
		return err
	}
	handler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		return dp.setUserSecretsFromOutputs(ctx, serviceConfig, args)
	}
	if err := serviceConfig.AddHandler(ServiceEventEnvUpdated, handler); err != nil {
		return err
	}

	return nil
}

// Restores the dependencies for the project
func (dp *dotnetProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceRestoreResult, ServiceProgress]) {
			task.SetProgress(NewServiceProgress("Restoring .NET project dependencies"))
			if err := dp.dotnetCli.Restore(ctx, serviceConfig.Path()); err != nil {
				task.SetError(err)
				return
			}

			task.SetResult(&ServiceRestoreResult{})
		},
	)
}

// Builds the dotnet project using the dotnet CLI
func (dp *dotnetProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
			task.SetProgress(NewServiceProgress("Building .NET project"))
			if err := dp.dotnetCli.Build(ctx, serviceConfig.Path(), defaultDotNetBuildConfiguration, ""); err != nil {
				task.SetError(err)
				return
			}

			defaultOutputDir := filepath.Join("./bin", defaultDotNetBuildConfiguration)

			// Attempt to find the default build output location
			buildOutputDir := serviceConfig.Path()
			_, err := os.Stat(filepath.Join(buildOutputDir, defaultOutputDir))
			if err == nil {
				buildOutputDir = filepath.Join(buildOutputDir, defaultOutputDir)
			}

			// By default dotnet build will create a sub folder for the project framework version, etc. net6.0
			// If we have a single folder under build configuration assume this location as build output result
			subDirs, err := os.ReadDir(buildOutputDir)
			if err == nil {
				if len(subDirs) == 1 {
					buildOutputDir = filepath.Join(buildOutputDir, subDirs[0].Name())
				}
			}

			task.SetResult(&ServiceBuildResult{
				Restore:         restoreOutput,
				BuildOutputPath: buildOutputDir,
			})
		},
	)
}

func (dp *dotnetProject) Package(
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

			task.SetProgress(NewServiceProgress("Publishing .NET project"))
			if err := dp.dotnetCli.Publish(ctx, serviceConfig.Path(), defaultDotNetBuildConfiguration, publishRoot); err != nil {
				task.SetError(err)
				return
			}

			if serviceConfig.OutputPath != "" {
				publishRoot = filepath.Join(publishRoot, serviceConfig.OutputPath)
			}

			task.SetResult(&ServicePackageResult{
				Build:       buildOutput,
				PackagePath: publishRoot,
			})
		},
	)
}

func (dp *dotnetProject) setUserSecretsFromOutputs(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	args ServiceLifecycleEventArgs,
) error {
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
		if err := dp.dotnetCli.SetSecret(
			ctx,
			normalizeDotNetSecret(key),
			fmt.Sprint(val.Value),
			serviceConfig.Path(),
		); err != nil {
			return err
		}
	}
	return nil
}

func normalizeDotNetSecret(key string) string {
	// dotnet recognizes "__" as the hierarchy key separator for environment variables, but for user secrets, it has to be
	// ":".
	return strings.ReplaceAll(key, "__", ":")
}
