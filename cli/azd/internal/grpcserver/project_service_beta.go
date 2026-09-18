// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type betaProjectService struct {
	service *projectService
}

var (
	_ BetaProjectServiceGetOverride                 = (*betaProjectService)(nil)
	_ BetaProjectServiceAddServiceOverride          = (*betaProjectService)(nil)
	_ BetaProjectServiceGetResolvedServicesOverride = (*betaProjectService)(nil)
)

// Get returns the beta project types so the additional fields in v1beta.ServiceConfig and
// v1beta.InfraOptions survive a read-modify-write round trip. Converting through v1 would drop those fields.
func (s *betaProjectService) Get(
	ctx context.Context,
	_ *v1beta.EmptyRequest,
) (*v1beta.GetProjectResponse, error) {
	projectConfig, err := s.service.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}
	if projectConfig.Format() == project.ProjectFormatLayersV2 {
		return nil, status.Error(codes.FailedPrecondition,
			"Get is not supported for top-level layers projects; use ListLayers or GetLayer instead")
	}

	var mapped *v1beta.ProjectConfig
	if err := mapper.WithResolver(s.service.envResolver()).Convert(projectConfig, &mapped); err != nil {
		return nil, fmt.Errorf("converting project config to beta proto: %w", err)
	}
	return &v1beta.GetProjectResponse{Project: mapped}, nil
}

// AddService reads the beta request directly so the additional fields in v1beta.ServiceConfig and
// v1beta.InfraOptions are preserved when the service is saved. Top-level layer projects use SetLayer instead.
func (s *betaProjectService) AddService(
	ctx context.Context,
	req *v1beta.AddServiceRequest,
) (*v1beta.EmptyResponse, error) {
	if req.GetService() == nil || req.GetService().GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "service name cannot be empty")
	}

	s.service.configMutationMu.Lock()
	defer s.service.configMutationMu.Unlock()

	azdContext, err := s.service.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}
	if err := s.service.reloadAndCacheProjectConfig(ctx, azdContext.ProjectPath()); err != nil {
		return nil, err
	}
	projectConfig, err := s.service.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}
	if projectConfig.Format() == project.ProjectFormatLayersV2 {
		return nil, status.Error(codes.FailedPrecondition,
			"AddService cannot modify a top-level layers project; use SetLayer instead")
	}

	var serviceConfig *project.ServiceConfig
	if err := mapper.Convert(req.GetService(), &serviceConfig); err != nil {
		return nil, fmt.Errorf("failed converting beta service configuration: %w", err)
	}
	if projectConfig.Services == nil {
		projectConfig.Services = map[string]*project.ServiceConfig{}
	}
	serviceName := req.GetService().GetName()
	if existingService, exists := projectConfig.Services[serviceName]; exists &&
		existingService.EventDispatcher != nil {
		serviceConfig.EventDispatcher = existingService.EventDispatcher
	} else {
		serviceConfig.EventDispatcher = ext.NewEventDispatcher[project.ServiceLifecycleEventArgs]()
	}
	if existingService, exists := projectConfig.Services[serviceName]; exists {
		preserveUnchangedEnvTemplates(existingService, serviceConfig, s.service.envResolver())
	}
	serviceConfig.Project = projectConfig
	serviceConfig.Name = serviceName
	projectConfig.Services[serviceName] = serviceConfig
	if err := project.Save(ctx, projectConfig, azdContext.ProjectPath()); err != nil {
		return nil, err
	}
	return &v1beta.EmptyResponse{}, nil
}

// GetResolvedServices returns v1beta.ServiceConfig values so their additional service and infrastructure
// fields are preserved instead of being lost through a conversion to v1.
func (s *betaProjectService) GetResolvedServices(
	ctx context.Context,
	_ *v1beta.EmptyRequest,
) (*v1beta.GetResolvedServicesResponse, error) {
	azdContext, err := s.service.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}
	projectConfig, err := project.Load(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, err
	}
	services, err := s.service.importManager.ServiceStable(ctx, projectConfig)
	if err != nil {
		return nil, fmt.Errorf("resolving services: %w", err)
	}

	mappedServices := make(map[string]*v1beta.ServiceConfig, len(services))
	for _, service := range services {
		var mapped *v1beta.ServiceConfig
		if err := mapper.WithResolver(s.service.envResolver()).Convert(service, &mapped); err != nil {
			return nil, fmt.Errorf("converting service config to beta proto: %w", err)
		}
		mappedServices[service.Name] = mapped
	}
	return &v1beta.GetResolvedServicesResponse{Services: mappedServices}, nil
}
