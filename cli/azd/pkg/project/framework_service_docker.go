// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type DockerProjectOptions struct {
	Path     string `json:"path"`
	Context  string `json:"context"`
	Platform string `json:"platform"`
}

type dockerProject struct {
	config    *ServiceConfig
	env       *environment.Environment
	docker    *tools.Docker
	framework FrameworkService
	options   DockerProjectOptions
}

func (p *dockerProject) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{p.docker}
}

func (p *dockerProject) Package(ctx context.Context, progress chan<- string) (string, error) {
	log.Printf("building image for service %s, cwd: %s, path: %s, context: %s)", p.config.Name, p.config.Path(), p.options.Path, p.options.Context)

	// Build the container
	progress <- "Building docker image"
	imageId, err := p.docker.Build(ctx, p.config.Path(), p.options.Path, p.options.Platform, p.options.Context)
	if err != nil {
		return "", fmt.Errorf("building container: %s at %s: %w", p.config.Name, p.options.Context, err)
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
		options:   createDockerOptions(config),
	}
}

func createDockerOptions(config *ServiceConfig) DockerProjectOptions {
	dockerOptions := DockerProjectOptions{
		Path:     "./Dockerfile",
		Platform: "amd64",
		Context:  ".",
	}

	if len(config.Options) == 0 {
		return dockerOptions
	}

	dockerMap, ok := config.Options["docker"]
	if !ok {
		return dockerOptions
	}

	log.Printf("found custom docker options %s\n", dockerMap)

	jsonBytes, err := json.Marshal(dockerMap)
	if err != nil {
		log.Printf("error marshalling project options to JSON: %s", err.Error())
		return dockerOptions
	}

	if err := json.Unmarshal(jsonBytes, &dockerOptions); err != nil {
		log.Printf("error unmarshalling project to DockerProjectOptions: %s", err.Error())
		return dockerOptions
	}

	return dockerOptions
}
