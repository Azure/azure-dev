// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type projectService struct {
	azdext.UnimplementedProjectServiceServer

	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]
	lazyEnvManager *lazy.Lazy[environment.Manager]
}

func NewProjectService(
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnvManager *lazy.Lazy[environment.Manager],
) azdext.ProjectServiceServer {
	return &projectService{
		lazyAzdContext: lazyAzdContext,
		lazyEnvManager: lazyEnvManager,
	}
}

// toDockerOptions converts a project.DockerProjectOptions to azdext.DockerProjectOptions.
// Returns nil if no docker options are configured.
func toDockerOptions(docker *project.DockerProjectOptions, envKeyMapper func(string) string) *azdext.DockerProjectOptions {
	// Check if any docker configuration is present
	if docker.Path == "" &&
		docker.Context == "" &&
		docker.Platform == "" &&
		docker.Target == "" &&
		docker.Registry.Empty() &&
		docker.Image.Empty() &&
		docker.Tag.Empty() &&
		!docker.RemoteBuild &&
		len(docker.BuildArgs) == 0 {
		return nil
	}

	options := &azdext.DockerProjectOptions{
		Path:        docker.Path,
		Context:     docker.Context,
		Platform:    docker.Platform,
		Target:      docker.Target,
		Registry:    docker.Registry.MustEnvsubst(envKeyMapper),
		Image:       docker.Image.MustEnvsubst(envKeyMapper),
		Tag:         docker.Tag.MustEnvsubst(envKeyMapper),
		RemoteBuild: docker.RemoteBuild,
	}

	// Convert build args with env substitution
	if len(docker.BuildArgs) > 0 {
		options.BuildArgs = make([]string, len(docker.BuildArgs))
		for i, arg := range docker.BuildArgs {
			options.BuildArgs[i] = arg.MustEnvsubst(envKeyMapper)
		}
	}

	return options
}

func (s *projectService) Get(ctx context.Context, req *azdext.EmptyRequest) (*azdext.GetProjectResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := project.Load(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	envKeyMapper := func(env string) string {
		return ""
	}

	defaultEnvironment, err := azdContext.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}

	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	if defaultEnvironment != "" {
		env, err := envManager.Get(ctx, defaultEnvironment)
		if err == nil && env != nil {
			envKeyMapper = env.Getenv
		}
	}

	project := &azdext.ProjectConfig{
		Name:              projectConfig.Name,
		ResourceGroupName: projectConfig.ResourceGroupName.MustEnvsubst(envKeyMapper),
		Path:              projectConfig.Path,
		Infra: &azdext.InfraOptions{
			Provider: string(projectConfig.Infra.Provider),
			Path:     projectConfig.Infra.Path,
			Module:   projectConfig.Infra.Module,
		},
		Services: map[string]*azdext.ServiceConfig{},
	}

	if projectConfig.Metadata != nil {
		project.Metadata = &azdext.ProjectMetadata{
			Template: projectConfig.Metadata.Template,
		}
	}

	for name, service := range projectConfig.Services {
		protoService := &azdext.ServiceConfig{
			Name:              service.Name,
			ResourceGroupName: service.ResourceGroupName.MustEnvsubst(envKeyMapper),
			ResourceName:      service.ResourceName.MustEnvsubst(envKeyMapper),
			ApiVersion:        service.ApiVersion,
			RelativePath:      service.RelativePath,
			Host:              string(service.Host),
			Language:          string(service.Language),
			OutputPath:        service.OutputPath,
			Image:             service.Image.MustEnvsubst(envKeyMapper),
		}

		// Populate docker options with env substitution
		if dockerOptions := toDockerOptions(&service.Docker, envKeyMapper); dockerOptions != nil {
			protoService.Docker = dockerOptions
		}

		project.Services[name] = protoService
	}

	return &azdext.GetProjectResponse{
		Project: project,
	}, nil
}

func (s *projectService) AddService(ctx context.Context, req *azdext.AddServiceRequest) (*azdext.EmptyResponse, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}

	projectConfig, err := project.Load(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}

	serviceConfig := &project.ServiceConfig{
		Project:      projectConfig,
		Name:         req.Service.Name,
		RelativePath: req.Service.RelativePath,
		Language:     project.ServiceLanguageKind(req.Service.Language),
		Host:         project.ServiceTargetKind(req.Service.Host),
	}

	if projectConfig.Services == nil {
		projectConfig.Services = map[string]*project.ServiceConfig{}
	}

	projectConfig.Services[req.Service.Name] = serviceConfig
	if err := project.Save(ctx, projectConfig, azdContext.ProjectPath()); err != nil {
		return nil, err
	}

	return &azdext.EmptyResponse{}, nil
}
