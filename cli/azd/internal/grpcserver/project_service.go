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

	initialized bool
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
		project.Services[name] = &azdext.ServiceConfig{
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
