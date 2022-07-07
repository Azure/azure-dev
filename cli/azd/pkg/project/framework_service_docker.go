// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type dockerProject struct {
	config    *ServiceConfig
	env       *environment.Environment
	docker    *tools.Docker
	framework FrameworkService
}

func (p *dockerProject) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{p.docker}
}

func (p *dockerProject) Package(ctx context.Context, progress chan<- string) (string, error) {
	log.Printf("building image for service %s (path: %s)", p.config.Name, p.config.Path())

	// Build the container
	progress <- "Building docker image"
	imageId, err := p.docker.Build(ctx, "./Dockerfile", p.config.Path())
	if err != nil {
		return "", fmt.Errorf("building container: %s at %s: %w", p.config.Name, p.config.Path(), err)
	}

	log.Printf("built image %s for %s", imageId, p.config.Name)
	return imageId, nil
}

func (p *dockerProject) InstallDependencies(ctx context.Context) error {
	// When the program runs the restore actions for the underlying project (containerapp),
	// the dependencies are installed locally
	return p.framework.InstallDependencies(ctx)
}

func NewDockerProject(config *ServiceConfig, env *environment.Environment, docker *tools.Docker, framework FrameworkService) FrameworkService {
	return &dockerProject{
		config:    config,
		env:       env,
		docker:    docker,
		framework: framework,
	}
}
