// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"maps"
	"slices"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SetLayer creates or replaces a project layer.
func (s *projectService) SetLayer(
	ctx context.Context,
	req *azdext.SetLayerRequest,
) (*azdext.LayerResponse, error) {
	if req.GetLayer() == nil || req.GetLayer().GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "layer name cannot be empty")
	}
	rpcLayer := req.GetLayer()

	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()

	projectFilePath, projectConfig, err := s.loadProjectForMutation(ctx)
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
	if err := s.saveProjectMutation(ctx, projectFilePath, projectConfig); err != nil {
		return nil, err
	}

	return s.layerConfigResponse(ctx, layerConfig, false)
}

// GetLayer gets one persisted v2 project layer.
func (s *projectService) GetLayer(
	ctx context.Context,
	req *azdext.GetLayerRequest,
) (*azdext.LayerResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	projectConfig, err := s.lazyProjectConfig.GetValue()
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
func (s *projectService) ListLayers(
	ctx context.Context,
	_ *azdext.EmptyRequest,
) (*azdext.ListLayersResponse, error) {
	projectConfig, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, err
	}
	if projectConfig.Format() != project.ProjectFormatLayersV2 {
		return nil, status.Error(codes.FailedPrecondition,
			"ListLayers requires a project that uses the top-level layers format")
	}

	response := &azdext.ListLayersResponse{Layers: make([]*azdext.Layer, len(projectConfig.Layers))}
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
func (s *projectService) RemoveLayer(
	ctx context.Context,
	req *azdext.RemoveLayerRequest,
) (*azdext.RemoveLayerResponse, error) {
	if req == nil || req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "layer name cannot be empty")
	}
	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()

	projectFilePath, projectConfig, err := s.loadProjectForMutation(ctx)
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

	if err := s.saveProjectMutation(ctx, projectFilePath, projectConfig); err != nil {
		return nil, err
	}
	return &azdext.RemoveLayerResponse{RemovedServices: removedServices}, nil
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

func (s *projectService) layerConfigResponse(
	ctx context.Context,
	layer *project.LayerConfig,
	envsubst bool,
) (*azdext.LayerResponse, error) {
	mapped, err := s.layerConfigToProto(ctx, layer, envsubst)
	if err != nil {
		return nil, err
	}
	return &azdext.LayerResponse{Layer: mapped}, nil
}

func (s *projectService) layerConfigToProto(
	ctx context.Context,
	layer *project.LayerConfig,
	envsubst bool,
) (*azdext.Layer, error) {
	var mapped *azdext.Layer
	if err := s.newMapper(ctx, envsubst).Convert(layer, &mapped); err != nil {
		return nil, err
	}
	return mapped, nil
}

func (s *projectService) newMapper(ctx context.Context, envsubst bool) *mapper.Mapper {
	serviceMapper := mapper.WithContext(ctx).WithEnvSubst(envsubst)
	if envsubst {
		serviceMapper = serviceMapper.WithResolver(s.envResolver())
	}
	return serviceMapper
}
