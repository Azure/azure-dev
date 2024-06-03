// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/swa"
)

type swaProject struct {
	env           *environment.Environment
	console       input.Console
	commandRunner exec.CommandRunner
	swa           swa.SwaCli
	framework     FrameworkService
}

// NewSwaProject creates a new instance of a Azd project that
// leverages swa cli for building
func NewSwaProject(
	env *environment.Environment,
	console input.Console,
	commandRunner exec.CommandRunner,
	swa swa.SwaCli,
	framework FrameworkService,
) CompositeFrameworkService {
	return &swaProject{
		env:           env,
		console:       console,
		commandRunner: commandRunner,
		swa:           swa,
		framework:     framework,
	}
}

// NewSwaProjectAsFrameworkService is the same as NewSwaProject().(FrameworkService) and exists to support our
// use of DI and ServiceLocators, where we sometimes need to resolve this type as a FrameworkService instance instead
// of a CompositeFrameworkService as [NewSwaProject] does.
func NewSwaProjectAsFrameworkService(
	env *environment.Environment,
	console input.Console,
	commandRunner exec.CommandRunner,
	swa swa.SwaCli,
	framework FrameworkService,
) FrameworkService {
	return NewSwaProject(env, console, commandRunner, swa, framework)
}

func (p *swaProject) Requirements() FrameworkRequirements {
	return FrameworkRequirements{
		Package: FrameworkPackageRequirements{
			RequireRestore: false,
			RequireBuild:   true,
		},
	}
}

// Gets the required external tools for the project
func (p *swaProject) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{p.swa}
}

// Initializes the swa project
func (p *swaProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return p.framework.Initialize(ctx, serviceConfig)
}

// Sets the inner framework service used for restore and build command
func (p *swaProject) SetSource(inner FrameworkService) {
	p.framework = inner
}

// Restores the dependencies for the swa project
func (p *swaProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	// When the program runs the restore actions for the underlying project (containerapp),
	// the dependencies are installed locally
	return p.framework.Restore(ctx, serviceConfig)
}

// Builds the swa project based on the swa-cli.config.json options specified within the Service path
func (p *swaProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	restoreOutput *ServiceRestoreResult,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {

			previewerWriter := p.console.ShowPreviewer(ctx,
				&input.ShowPreviewerOptions{
					Prefix:       "  ",
					MaxLineCount: 8,
					Title:        "Build SWA Project",
				})
			err := p.swa.Build(
				ctx,
				serviceConfig.Path(),
				previewerWriter,
			)
			p.console.StopPreviewer(ctx, false)

			if err != nil {
				task.SetError(err)
				return
			}

			task.SetResult(&ServiceBuildResult{
				Restore: restoreOutput,
			})
		},
	)
}

func (p *swaProject) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	buildOutput *ServiceBuildResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
			task.SetResult(&ServicePackageResult{
				Build: buildOutput,
			})
		},
	)
}
