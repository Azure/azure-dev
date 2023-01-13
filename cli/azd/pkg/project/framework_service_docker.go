// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"

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
	config    *ServiceConfig
	env       *environment.Environment
	docker    *docker.Docker
	framework FrameworkService
}

func (p *dockerProject) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{p.docker}
}

func (p *dockerProject) Package(ctx context.Context, progress chan<- string) (string, error) {
	dockerOptions := getDockerOptionsWithDefaults(p.config.Docker)

	log.Printf(
		"building image for service %s, cwd: %s, path: %s, context: %s)",
		p.config.Name,
		p.config.Path(),
		dockerOptions.Path,
		dockerOptions.Context,
	)

	// Build the container
	progress <- "Building docker image"
	imageId, err := p.docker.Build(ctx, p.config.Path(), dockerOptions.Path, dockerOptions.Platform, dockerOptions.Context)
	if err != nil {
		return "", fmt.Errorf("building container: %s at %s: %w", p.config.Name, dockerOptions.Context, err)
	}

	log.Printf("built image %s for %s", imageId, p.config.Name)
	return imageId, nil
}

func (p *dockerProject) InstallDependencies(ctx context.Context) error {
	// When the program runs the restore actions for the underlying project (containerapp),
	// the dependencies are installed locally
	return p.framework.InstallDependencies(ctx)
}

func (p *dockerProject) Initialize(ctx context.Context) error {
	return nil
}

func NewDockerProject(
	config *ServiceConfig,
	env *environment.Environment,
	docker *docker.Docker,
	framework FrameworkService,
) FrameworkService {
	return &dockerProject{
		config:    config,
		env:       env,
		docker:    docker,
		framework: framework,
	}
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
