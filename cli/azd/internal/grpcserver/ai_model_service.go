// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
)

type aiModelService struct {
	azdext.UnimplementedAiModelServiceServer
	modelService *ai.AiModelService
}

// NewAiModelService creates a new AI model gRPC service.
func NewAiModelService(
	modelService *ai.AiModelService,
) azdext.AiModelServiceServer {
	return &aiModelService{
		modelService: modelService,
	}
}

// --- Primitives ---

func (s *aiModelService) ListModels(
	ctx context.Context, req *azdext.ListModelsRequest,
) (*azdext.ListModelsResponse, error) {
	subscriptionId, err := requireSubscriptionID(req.AzureContext)
	if err != nil {
		return nil, err
	}

	var filterOpts *ai.FilterOptions
	if req.Filter != nil {
		filterOpts = protoToFilterOptions(req.Filter)
	}

	// Always fetch canonical model data across subscription locations.
	// Location filters are applied only as inclusion filters below.
	models, err := s.modelService.ListModels(ctx, subscriptionId, nil)
	if err != nil {
		return nil, fmt.Errorf("listing models: %w", err)
	}

	if filterOpts != nil {
		models = ai.FilterModels(models, filterOpts)
	}

	protoModels := make([]*azdext.AiModel, len(models))
	for i := range models {
		if err := mapper.Convert(&models[i], &protoModels[i]); err != nil {
			return nil, fmt.Errorf("converting model to proto: %w", err)
		}
	}

	return &azdext.ListModelsResponse{Models: protoModels}, nil
}

func (s *aiModelService) ResolveModelDeployments(
	ctx context.Context, req *azdext.ResolveModelDeploymentsRequest,
) (*azdext.ResolveModelDeploymentsResponse, error) {
	subscriptionId, err := requireSubscriptionID(req.AzureContext)
	if err != nil {
		return nil, err
	}

	options := protoToDeploymentOptions(req.Options)
	if options == nil {
		options = &ai.DeploymentOptions{}
	}

	var deployments []ai.AiModelDeployment
	if req.Quota != nil {
		quotaOpts := protoToQuotaCheckOptions(req.Quota)
		deployments, err = s.modelService.ResolveModelDeploymentsWithQuota(
			ctx, subscriptionId, req.ModelName, options, quotaOpts)
	} else {
		deployments, err = s.modelService.ResolveModelDeployments(
			ctx, subscriptionId, req.ModelName, options)
	}
	if err != nil {
		return nil, mapAiResolveError(err, req.ModelName)
	}

	protoDeployments := make([]*azdext.AiModelDeployment, len(deployments))
	for i := range deployments {
		if err := mapper.Convert(&deployments[i], &protoDeployments[i]); err != nil {
			return nil, fmt.Errorf("converting deployment to proto: %w", err)
		}
	}

	return &azdext.ResolveModelDeploymentsResponse{
		Deployments: protoDeployments,
	}, nil
}

func (s *aiModelService) ListUsages(
	ctx context.Context, req *azdext.ListUsagesRequest,
) (*azdext.ListUsagesResponse, error) {
	subscriptionId, err := requireSubscriptionID(req.AzureContext)
	if err != nil {
		return nil, err
	}
	if req.Location == "" {
		return nil, aiStatusError(
			codes.InvalidArgument,
			azdext.AiErrorReasonLocationRequired,
			"location is required for listing usages",
			nil,
		)
	}

	usages, err := s.modelService.ListUsages(ctx, subscriptionId, req.Location)
	if err != nil {
		return nil, fmt.Errorf("listing usages: %w", err)
	}

	protoUsages := make([]*azdext.AiModelUsage, len(usages))
	for i := range usages {
		if err := mapper.Convert(&usages[i], &protoUsages[i]); err != nil {
			return nil, fmt.Errorf("converting usage to proto: %w", err)
		}
	}

	return &azdext.ListUsagesResponse{Usages: protoUsages}, nil
}

func (s *aiModelService) ListLocationsWithQuota(
	ctx context.Context, req *azdext.ListLocationsWithQuotaRequest,
) (*azdext.ListLocationsWithQuotaResponse, error) {
	subscriptionId, err := requireSubscriptionID(req.AzureContext)
	if err != nil {
		return nil, err
	}

	requirements := make([]ai.QuotaRequirement, len(req.Requirements))
	for i, r := range req.Requirements {
		requirements[i] = ai.QuotaRequirement{
			UsageName:   r.UsageName,
			MinCapacity: r.MinCapacity,
		}
	}

	locations, err := s.modelService.ListLocationsWithQuota(
		ctx, subscriptionId, req.AllowedLocations, requirements)
	if err != nil {
		return nil, fmt.Errorf("listing locations with quota: %w", err)
	}

	protoLocations := make([]*azdext.Location, len(locations))
	for i, loc := range locations {
		protoLocations[i] = &azdext.Location{Name: loc}
	}

	return &azdext.ListLocationsWithQuotaResponse{Locations: protoLocations}, nil
}

func (s *aiModelService) ListModelLocationsWithQuota(
	ctx context.Context, req *azdext.ListModelLocationsWithQuotaRequest,
) (*azdext.ListModelLocationsWithQuotaResponse, error) {
	subscriptionId, err := requireSubscriptionID(req.AzureContext)
	if err != nil {
		return nil, err
	}
	if req.ModelName == "" {
		return nil, fmt.Errorf("model_name is required")
	}

	minRemaining := float64(1)
	if req.Quota != nil && req.Quota.MinRemainingCapacity > 0 {
		minRemaining = req.Quota.MinRemainingCapacity
	}

	locations, err := s.modelService.ListModelLocationsWithQuota(
		ctx, subscriptionId, req.ModelName, req.AllowedLocations, minRemaining)
	if err != nil {
		return nil, mapAiResolveError(err, req.ModelName)
	}

	protoLocations := make([]*azdext.ModelLocationQuota, len(locations))
	for i, loc := range locations {
		protoLocations[i] = &azdext.ModelLocationQuota{
			Location:          &azdext.Location{Name: loc.Location},
			MaxRemainingQuota: loc.MaxRemainingQuota,
		}
	}

	return &azdext.ListModelLocationsWithQuotaResponse{Locations: protoLocations}, nil
}

func requireSubscriptionID(azureContext *azdext.AzureContext) (string, error) {
	if azureContext == nil || azureContext.Scope == nil || azureContext.Scope.SubscriptionId == "" {
		return "", aiStatusError(
			codes.InvalidArgument,
			azdext.AiErrorReasonMissingSubscription,
			"azure_context.scope.subscription_id is required",
			nil,
		)
	}

	return azureContext.Scope.SubscriptionId, nil
}

func protoToFilterOptions(f *azdext.AiModelFilterOptions) *ai.FilterOptions {
	if f == nil {
		return nil
	}
	return &ai.FilterOptions{
		Locations:         f.Locations,
		Capabilities:      f.Capabilities,
		Formats:           f.Formats,
		Statuses:          f.Statuses,
		ExcludeModelNames: f.ExcludeModelNames,
	}
}

func protoToDeploymentOptions(o *azdext.AiModelDeploymentOptions) *ai.DeploymentOptions {
	if o == nil {
		return nil
	}
	opts := &ai.DeploymentOptions{
		Locations: o.Locations,
		Versions:  o.Versions,
		Skus:      o.Skus,
	}
	if o.Capacity != nil {
		cap := *o.Capacity
		opts.Capacity = &cap
	}
	return opts
}

func protoToQuotaCheckOptions(q *azdext.QuotaCheckOptions) *ai.QuotaCheckOptions {
	if q == nil {
		return nil
	}
	return &ai.QuotaCheckOptions{
		MinRemainingCapacity: q.MinRemainingCapacity,
	}
}
