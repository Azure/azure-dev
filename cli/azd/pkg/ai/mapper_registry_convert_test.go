// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// These tests drive the package-level mapper.MustRegister closures registered in
// mapper_registry.go via init()->registerAiModelMappings(). Invoking mapper.Convert
// with each supported src/dst pair exercises the closure bodies (which would otherwise
// never run during unit tests that call the un-exported helper funcs directly).

func TestMapper_AiModel_RoundTrip(t *testing.T) {
	t.Parallel()

	src := &AiModel{
		Name:         "gpt-4o",
		Format:       "OpenAI",
		Capabilities: []string{"chat", "embeddings"},
		Locations:    []string{"eastus", "westus"},
		Versions: []AiModelVersion{
			{
				Version:         "2024-05-13",
				IsDefault:       true,
				LifecycleStatus: "GenerallyAvailable",
				Skus: []AiModelSku{
					{
						Name:            "Standard",
						UsageName:       "OpenAI.Standard.gpt-4o",
						DefaultCapacity: 10,
						MinCapacity:     1,
						MaxCapacity:     100,
						CapacityStep:    1,
					},
				},
			},
			{
				Version:         "2024-11-20",
				IsDefault:       false,
				LifecycleStatus: "Preview",
				Skus:            []AiModelSku{},
			},
		},
	}

	var proto *azdext.AiModel
	require.NoError(t, mapper.Convert(src, &proto))
	require.NotNil(t, proto)
	require.Equal(t, src.Name, proto.Name)
	require.Equal(t, src.Format, proto.Format)
	require.Equal(t, src.Capabilities, proto.Capabilities)
	require.Equal(t, src.Locations, proto.Locations)
	require.Len(t, proto.Versions, len(src.Versions))
	require.Equal(t, src.Versions[0].Version, proto.Versions[0].Version)
	require.Equal(t, src.Versions[0].IsDefault, proto.Versions[0].IsDefault)
	require.Equal(t, src.Versions[0].LifecycleStatus, proto.Versions[0].LifecycleStatus)
	require.Len(t, proto.Versions[0].Skus, 1)
	require.Equal(t, "OpenAI.Standard.gpt-4o", proto.Versions[0].Skus[0].UsageName)

	// Reverse direction
	var back *AiModel
	require.NoError(t, mapper.Convert(proto, &back))
	require.NotNil(t, back)
	require.Equal(t, src.Name, back.Name)
	require.Equal(t, src.Format, back.Format)
	require.Equal(t, src.Capabilities, back.Capabilities)
	require.Equal(t, src.Locations, back.Locations)
	require.Len(t, back.Versions, len(src.Versions))
	require.Equal(t, src.Versions[0].Skus[0], back.Versions[0].Skus[0])
}

func TestMapper_AiModelSku_RoundTrip(t *testing.T) {
	t.Parallel()

	src := &AiModelSku{
		Name:            "GlobalStandard",
		UsageName:       "OpenAI.GlobalStandard.gpt-4o",
		DefaultCapacity: 25,
		MinCapacity:     1,
		MaxCapacity:     1000,
		CapacityStep:    1,
	}

	var proto *azdext.AiModelSku
	require.NoError(t, mapper.Convert(src, &proto))
	require.NotNil(t, proto)
	require.Equal(t, src.Name, proto.Name)
	require.Equal(t, src.UsageName, proto.UsageName)
	require.Equal(t, src.DefaultCapacity, proto.DefaultCapacity)
	require.Equal(t, src.MinCapacity, proto.MinCapacity)
	require.Equal(t, src.MaxCapacity, proto.MaxCapacity)
	require.Equal(t, src.CapacityStep, proto.CapacityStep)

	var back *AiModelSku
	require.NoError(t, mapper.Convert(proto, &back))
	require.NotNil(t, back)
	require.Equal(t, *src, *back)
}

func TestMapper_AiModelDeployment_RoundTrip(t *testing.T) {
	t.Parallel()

	remaining := float64(42)
	src := &AiModelDeployment{
		ModelName: "gpt-4o",
		Format:    "OpenAI",
		Version:   "2024-05-13",
		Location:  "eastus",
		Sku: AiModelSku{
			Name:            "Standard",
			UsageName:       "OpenAI.Standard.gpt-4o",
			DefaultCapacity: 10,
			MinCapacity:     1,
			MaxCapacity:     100,
			CapacityStep:    1,
		},
		Capacity:       10,
		RemainingQuota: &remaining,
	}

	var proto *azdext.AiModelDeployment
	require.NoError(t, mapper.Convert(src, &proto))
	require.NotNil(t, proto)
	require.Equal(t, src.ModelName, proto.ModelName)
	require.Equal(t, src.Format, proto.Format)
	require.Equal(t, src.Version, proto.Version)
	require.Equal(t, src.Location, proto.Location)
	require.Equal(t, src.Capacity, proto.Capacity)
	require.Equal(t, remaining, *proto.RemainingQuota)
	require.NotNil(t, proto.Sku)
	require.Equal(t, src.Sku.Name, proto.Sku.Name)

	var back *AiModelDeployment
	require.NoError(t, mapper.Convert(proto, &back))
	require.NotNil(t, back)
	require.Equal(t, src.ModelName, back.ModelName)
	require.Equal(t, src.Sku, back.Sku)
	require.NotNil(t, back.RemainingQuota)
	require.Equal(t, remaining, *back.RemainingQuota)
}

func TestMapper_AiModelDeployment_NilSku(t *testing.T) {
	t.Parallel()

	proto := &azdext.AiModelDeployment{
		ModelName: "model-a",
		Format:    "OpenAI",
		Version:   "v1",
		Location:  "eastus",
		Capacity:  5,
		Sku:       nil,
	}

	var back *AiModelDeployment
	require.NoError(t, mapper.Convert(proto, &back))
	require.NotNil(t, back)
	require.Equal(t, "model-a", back.ModelName)
	require.Equal(t, AiModelSku{}, back.Sku)
}

func TestMapper_AiModelUsage_RoundTrip(t *testing.T) {
	t.Parallel()

	src := &AiModelUsage{
		Name:         "OpenAI.Standard.gpt-4o",
		CurrentValue: 10,
		Limit:        100,
	}

	var proto *azdext.AiModelUsage
	require.NoError(t, mapper.Convert(src, &proto))
	require.NotNil(t, proto)
	require.Equal(t, src.Name, proto.Name)
	require.Equal(t, src.CurrentValue, proto.CurrentValue)
	require.Equal(t, src.Limit, proto.Limit)

	var back *AiModelUsage
	require.NoError(t, mapper.Convert(proto, &back))
	require.NotNil(t, back)
	require.Equal(t, *src, *back)
}
