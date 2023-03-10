// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

type DockerProjectOptions struct {
	Path     string           `json:"path"`
	Context  string           `json:"context"`
	Platform string           `json:"platform"`
	Tag      ExpandableString `json:"tag"`
}

type dockerProject struct {
	env       *environment.Environment
	docker    docker.Docker
	framework FrameworkService
}

func NewDockerProject(
	config *ServiceConfig,
	env *environment.Environment,
	docker docker.Docker,
	framework FrameworkService,
) FrameworkService {
	return &dockerProject{
		env:       env,
		docker:    docker,
		framework: framework,
	}
}

func (p *dockerProject) RequiredExternalTools(context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{p.docker}
}

func (p *dockerProject) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

func (p *dockerProject) Restore(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress] {
	// When the program runs the restore actions for the underlying project (containerapp),
	// the dependencies are installed locally
	return p.framework.Restore(ctx, serviceConfig)
}

func (p *dockerProject) Build(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress] {
	return async.RunTaskWithProgress(
		func(task *async.TaskContextWithProgress[*ServiceBuildResult, ServiceProgress]) {
			dockerOptions := getDockerOptionsWithDefaults(serviceConfig.Docker)

			log.Printf(
				"building image for service %s, cwd: %s, path: %s, context: %s)",
				serviceConfig.Name,
				serviceConfig.Path(),
				dockerOptions.Path,
				dockerOptions.Context,
			)

			// Build the container
			task.SetProgress(NewServiceProgress("Building docker image"))
			imageId, err := p.docker.Build(
				ctx,
				serviceConfig.Path(),
				dockerOptions.Path,
				dockerOptions.Platform,
				dockerOptions.Context,
			)
			if err != nil {
				task.SetError(fmt.Errorf("building container: %s at %s: %w", serviceConfig.Name, dockerOptions.Context, err))
				return
			}

			log.Printf("built image %s for %s", imageId, serviceConfig.Name)
			task.SetResult(&ServiceBuildResult{
				BuildOutputPath: imageId,
			})
		},
	)
}

func getDockerOptionsWithDefaults(options DockerProjectOptions) DockerProjectOptions {
	if options.Path == "" {
		options.Path = "./Dockerfile"
	}

	if options.Platform == "" {
		options.Platform = "amd64"
	}

	if options.Context == "" {
		options.Context = "."
	}

	return options
}
