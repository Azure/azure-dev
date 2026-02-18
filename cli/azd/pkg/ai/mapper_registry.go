// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func init() {
	registerAiModelMappings()
}

// registerAiModelMappings registers all AI model type conversions with the mapper.
func registerAiModelMappings() {
	// AiModel -> proto AiModel
	mapper.MustRegister(func(_ context.Context, src *AiModel) (*azdext.AiModel, error) {
		versions := make([]*azdext.AiModelVersion, len(src.Versions))
		for i, v := range src.Versions {
			proto, err := aiModelVersionToProto(&v)
			if err != nil {
				return nil, err
			}
			versions[i] = proto
		}

		return &azdext.AiModel{
			Name:            src.Name,
			Format:          src.Format,
			LifecycleStatus: src.LifecycleStatus,
			Capabilities:    src.Capabilities,
			Versions:        versions,
			Locations:       src.Locations,
		}, nil
	})

	// proto AiModel -> AiModel
	mapper.MustRegister(func(_ context.Context, src *azdext.AiModel) (*AiModel, error) {
		versions := make([]AiModelVersion, len(src.Versions))
		for i, v := range src.Versions {
			versions[i] = protoToAiModelVersion(v)
		}

		return &AiModel{
			Name:            src.Name,
			Format:          src.Format,
			LifecycleStatus: src.LifecycleStatus,
			Capabilities:    src.Capabilities,
			Versions:        versions,
			Locations:       src.Locations,
		}, nil
	})

	// AiModelSku -> proto AiModelSku
	mapper.MustRegister(func(_ context.Context, src *AiModelSku) (*azdext.AiModelSku, error) {
		return aiModelSkuToProto(src), nil
	})

	// proto AiModelSku -> AiModelSku
	mapper.MustRegister(func(_ context.Context, src *azdext.AiModelSku) (*AiModelSku, error) {
		return protoToAiModelSku(src), nil
	})

	// AiModelDeployment -> proto AiModelDeployment
	mapper.MustRegister(func(_ context.Context, src *AiModelDeployment) (*azdext.AiModelDeployment, error) {
		return &azdext.AiModelDeployment{
			ModelName:      src.ModelName,
			Format:         src.Format,
			Version:        src.Version,
			Location:       src.Location,
			Sku:            aiModelSkuToProto(&src.Sku),
			Capacity:       src.Capacity,
			RemainingQuota: src.RemainingQuota,
		}, nil
	})

	// proto AiModelDeployment -> AiModelDeployment
	mapper.MustRegister(func(_ context.Context, src *azdext.AiModelDeployment) (*AiModelDeployment, error) {
		var sku AiModelSku
		if src.Sku != nil {
			sku = *protoToAiModelSku(src.Sku)
		}
		return &AiModelDeployment{
			ModelName:      src.ModelName,
			Format:         src.Format,
			Version:        src.Version,
			Location:       src.Location,
			Sku:            sku,
			Capacity:       src.Capacity,
			RemainingQuota: src.RemainingQuota,
		}, nil
	})

	// AiModelUsage -> proto AiModelUsage
	mapper.MustRegister(func(_ context.Context, src *AiModelUsage) (*azdext.AiModelUsage, error) {
		return &azdext.AiModelUsage{
			Name:         src.Name,
			CurrentValue: src.CurrentValue,
			Limit:        src.Limit,
		}, nil
	})

	// proto AiModelUsage -> AiModelUsage
	mapper.MustRegister(func(_ context.Context, src *azdext.AiModelUsage) (*AiModelUsage, error) {
		return &AiModelUsage{
			Name:         src.Name,
			CurrentValue: src.CurrentValue,
			Limit:        src.Limit,
		}, nil
	})
}

func aiModelVersionToProto(src *AiModelVersion) (*azdext.AiModelVersion, error) {
	skus := make([]*azdext.AiModelSku, len(src.Skus))
	for i, s := range src.Skus {
		skus[i] = aiModelSkuToProto(&s)
	}

	return &azdext.AiModelVersion{
		Version:   src.Version,
		IsDefault: src.IsDefault,
		Skus:      skus,
	}, nil
}

func protoToAiModelVersion(src *azdext.AiModelVersion) AiModelVersion {
	skus := make([]AiModelSku, len(src.Skus))
	for i, s := range src.Skus {
		skus[i] = *protoToAiModelSku(s)
	}

	return AiModelVersion{
		Version:   src.Version,
		IsDefault: src.IsDefault,
		Skus:      skus,
	}
}

func aiModelSkuToProto(src *AiModelSku) *azdext.AiModelSku {
	return &azdext.AiModelSku{
		Name:            src.Name,
		UsageName:       src.UsageName,
		DefaultCapacity: src.DefaultCapacity,
		MinCapacity:     src.MinCapacity,
		MaxCapacity:     src.MaxCapacity,
		CapacityStep:    src.CapacityStep,
	}
}

func protoToAiModelSku(src *azdext.AiModelSku) *AiModelSku {
	return &AiModelSku{
		Name:            src.Name,
		UsageName:       src.UsageName,
		DefaultCapacity: src.DefaultCapacity,
		MinCapacity:     src.MinCapacity,
		MaxCapacity:     src.MaxCapacity,
		CapacityStep:    src.CapacityStep,
	}
}
