// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
)

type projectService struct {
	azdext.UnimplementedProjectServiceServer

	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]
	lazyEnvManager *lazy.Lazy[environment.Manager]
	ghCli          *github.Cli
}

func NewProjectService(
	lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnvManager *lazy.Lazy[environment.Manager],
	ghCli *github.Cli,
) azdext.ProjectServiceServer {
	return &projectService{
		lazyAzdContext: lazyAzdContext,
		lazyEnvManager: lazyEnvManager,
		ghCli:          ghCli,
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
		var protoService *azdext.ServiceConfig

		// Use mapper with environment variable resolver
		if err := mapper.WithResolver(envKeyMapper).Convert(service, &protoService); err != nil {
			return nil, fmt.Errorf("converting service config to proto: %w", err)
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

	serviceConfig := &project.ServiceConfig{}
	if err := mapper.Convert(req.Service, &serviceConfig); err != nil {
		return nil, fmt.Errorf("failed converting service configuration, %w", err)
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

func (
	s *projectService,
) ParseGitHubUrl(
	ctx context.Context,
	req *azdext.ParseGitHubUrlRequest,
) (*azdext.ParseGitHubUrlResponse, error) {
	urlInfo, err := templates.ParseGitHubUrl(ctx, req.Url, s.ghCli)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GitHub URL: %w", err)
	}

	return &azdext.ParseGitHubUrlResponse{
		Hostname: urlInfo.Hostname,
		RepoSlug: urlInfo.RepoSlug,
		Branch:   urlInfo.Branch,
		FilePath: urlInfo.FilePath,
	}, nil
}
