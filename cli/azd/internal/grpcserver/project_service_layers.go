// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"
	"maps"
	"slices"

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
	_ BetaProjectServiceSetLayerOverride            = (*betaProjectService)(nil)
	_ BetaProjectServiceGetLayerOverride            = (*betaProjectService)(nil)
	_ BetaProjectServiceListLayersOverride          = (*betaProjectService)(nil)
	_ BetaProjectServiceRemoveLayerOverride         = (*betaProjectService)(nil)
	_ BetaProjectServiceGetResolvedServicesOverride = (*betaProjectService)(nil)
)

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

// SetLayer creates or replaces a project layer.
func (s *betaProjectService) SetLayer(
	ctx context.Context,
	req *v1beta.SetLayerRequest,
) (*v1beta.LayerResponse, error) {
	if req.GetLayer() == nil || req.GetLayer().GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "layer name cannot be empty")
	}
	rpcLayer := req.GetLayer()

	s.service.configMutationMu.Lock()
	defer s.service.configMutationMu.Unlock()

	projectFilePath, projectConfig, err := s.service.loadProjectForMutation(ctx)
	if err != nil {
		return nil, err
	}
	if projectConfig.Format() != project.ProjectFormatLayersV2 {
		return nil, status.Error(codes.FailedPrecondition,
			"SetLayer requires a project that uses the top-level layers format")
	}

	layerIndex := slices.IndexFunc(projectConfig.Layers, func(layer *project.LayerConfig) bool {
		return layer != nil && layer.Name == rpcLayer.GetName()
	})

	for _, protoInfra := range rpcLayer.GetInfra() {
		if protoInfra.GetName() == "" {
			return nil, status.Error(codes.InvalidArgument, "infrastructure entry name cannot be empty")
		}
	}
	for _, serviceName := range slices.Sorted(maps.Keys(rpcLayer.GetServices())) {
		if serviceName == "" {
			return nil, status.Error(codes.InvalidArgument, "service name cannot be empty")
		}
		if rpcLayer.GetServices()[serviceName] == nil {
			return nil, status.Errorf(codes.InvalidArgument, "service %q has an empty definition", serviceName)
		}
	}

	var layerConfig *project.LayerConfig
	if err := s.newMapper(ctx, false).Convert(rpcLayer, &layerConfig); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "converting layer: %v", err)
	}

	for _, serviceConfig := range layerConfig.Services {
		serviceConfig.Project = projectConfig
	}

	if layerIndex >= 0 {
		projectConfig.Layers[layerIndex] = layerConfig
	} else {
		projectConfig.Layers = append(projectConfig.Layers, layerConfig)
	}

	if err := projectConfig.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.service.saveProjectMutation(ctx, projectFilePath, projectConfig); err != nil {
		return nil, err
	}

	return s.layerConfigResponse(ctx, layerConfig, false)
}

// GetLayer gets one persisted v2 project layer.
func (s *betaProjectService) GetLayer(
	ctx context.Context,
	req *v1beta.GetLayerRequest,
) (*v1beta.LayerResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	projectConfig, err := s.service.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}
	if projectConfig.Format() != project.ProjectFormatLayersV2 {
		return nil, status.Error(codes.FailedPrecondition,
			"GetLayer requires a project that uses the top-level layers format")
	}
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "layer name cannot be empty")
	}
	layerIndex := slices.IndexFunc(projectConfig.Layers, func(layer *project.LayerConfig) bool {
		return layer != nil && layer.Name == req.GetName()
	})
	if layerIndex < 0 {
		return nil, status.Errorf(codes.NotFound, "layer %q not found", req.GetName())
	}
	return s.layerConfigResponse(ctx, projectConfig.Layers[layerIndex], req.GetEnvsubst())
}

// ListLayers lists all persisted v2 project layers.
func (s *betaProjectService) ListLayers(
	ctx context.Context,
	_ *v1beta.EmptyRequest,
) (*v1beta.ListLayersResponse, error) {
	projectConfig, err := s.service.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}
	if projectConfig.Format() != project.ProjectFormatLayersV2 {
		return nil, status.Error(codes.FailedPrecondition,
			"ListLayers requires a project that uses the top-level layers format")
	}

	response := &v1beta.ListLayersResponse{Layers: make([]*v1beta.Layer, len(projectConfig.Layers))}
	for i, layer := range projectConfig.Layers {
		mapped, err := s.layerConfigToProto(ctx, layer, false)
		if err != nil {
			return nil, err
		}
		response.Layers[i] = mapped
	}
	return response, nil
}

// RemoveLayer removes a project layer and its contents.
func (s *betaProjectService) RemoveLayer(
	ctx context.Context,
	req *v1beta.RemoveLayerRequest,
) (*v1beta.RemoveLayerResponse, error) {
	if req == nil || req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "layer name cannot be empty")
	}
	s.service.configMutationMu.Lock()
	defer s.service.configMutationMu.Unlock()

	projectFilePath, projectConfig, err := s.service.loadProjectForMutation(ctx)
	if err != nil {
		return nil, err
	}
	if projectConfig.Format() != project.ProjectFormatLayersV2 {
		return nil, status.Error(codes.FailedPrecondition,
			"RemoveLayer requires a project that uses the top-level layers format")
	}
	targetIndex := slices.IndexFunc(projectConfig.Layers, func(layer *project.LayerConfig) bool {
		return layer != nil && layer.Name == req.GetName()
	})
	if targetIndex < 0 {
		return nil, status.Errorf(codes.NotFound, "layer %q not found", req.GetName())
	}
	target := projectConfig.Layers[targetIndex]
	removedServices := slices.Sorted(maps.Keys(target.Services))
	projectConfig.Layers = slices.Delete(projectConfig.Layers, targetIndex, targetIndex+1)

	if err := s.service.saveProjectMutation(ctx, projectFilePath, projectConfig); err != nil {
		return nil, err
	}
	return &v1beta.RemoveLayerResponse{RemovedServices: removedServices}, nil
}

func (s *betaProjectService) layerConfigResponse(
	ctx context.Context,
	layer *project.LayerConfig,
	envsubst bool,
) (*v1beta.LayerResponse, error) {
	mapped, err := s.layerConfigToProto(ctx, layer, envsubst)
	if err != nil {
		return nil, err
	}
	return &v1beta.LayerResponse{Layer: mapped}, nil
}

func (s *betaProjectService) layerConfigToProto(
	ctx context.Context,
	layer *project.LayerConfig,
	envsubst bool,
) (*v1beta.Layer, error) {
	var mapped *v1beta.Layer
	if err := s.newMapper(ctx, envsubst).Convert(layer, &mapped); err != nil {
		return nil, err
	}
	return mapped, nil
}

func (s *betaProjectService) newMapper(ctx context.Context, envsubst bool) *mapper.Mapper {
	serviceMapper := mapper.WithContext(ctx).WithEnvSubst(envsubst)
	if envsubst {
		serviceMapper = serviceMapper.WithResolver(s.service.envResolver())
	}
	return serviceMapper
}

func (s *projectService) loadProjectForMutation(
	ctx context.Context,
) (string, *project.ProjectConfig, error) {
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return "", nil, err
	}
	projectFilePath := azdContext.ProjectPath()
	projectConfig, err := project.Load(ctx, projectFilePath)
	if err != nil {
		return "", nil, err
	}
	return projectFilePath, projectConfig, nil
}

func (s *projectService) saveProjectMutation(
	ctx context.Context,
	projectFilePath string,
	projectConfig *project.ProjectConfig,
) error {
	if err := project.Save(ctx, projectConfig, projectFilePath); err != nil {
		return err
	}
	return s.reloadAndCacheProjectConfig(ctx, projectFilePath)
}
