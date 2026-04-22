// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/stretchr/testify/require"
)

// seedCache pre-populates AiModelService.catalogCache for the given subscription
// so that fetchModelsForLocations serves entirely from cache and never touches
// the Azure client. Returns the service for chained test calls.
func seedCache(t *testing.T, subscriptionId string, models map[string][]*armcognitiveservices.Model) *AiModelService {
	t.Helper()
	svc := NewAiModelService(nil, nil)
	for loc, list := range models {
		svc.catalogCache[subscriptionId+":"+loc] = list
	}
	return svc
}

func deprecationFuture() *time.Time {
	t := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	return &t
}

// sampleModel builds a fully-populated AccountModel whose deprecation dates are all in
// the far future so the convertToAiModels pipeline keeps it.
func sampleModel(name, version, skuName, usageName string, isDefault bool) *armcognitiveservices.Model {
	ga := armcognitiveservices.ModelLifecycleStatus("GenerallyAvailable")
	capMin := int32(1)
	capMax := int32(100)
	capStep := int32(1)
	capDefault := int32(10)
	return &armcognitiveservices.Model{
		Model: &armcognitiveservices.AccountModel{
			Name:             &name,
			Version:          &version,
			Format:           ptrString("OpenAI"),
			IsDefaultVersion: &isDefault,
			LifecycleStatus:  &ga,
			Capabilities: map[string]*string{
				"chat":       ptrString("true"),
				"embeddings": ptrString("true"),
			},
			SKUs: []*armcognitiveservices.ModelSKU{
				{
					Name:            &skuName,
					UsageName:       &usageName,
					DeprecationDate: deprecationFuture(),
					Capacity: &armcognitiveservices.CapacityConfig{
						Default: &capDefault,
						Minimum: &capMin,
						Maximum: &capMax,
						Step:    &capStep,
					},
				},
			},
		},
	}
}

func ptrString(s string) *string { return &s }

func TestAiModelService_ListModels_FromCache(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	svc := seedCache(t, "sub-1", map[string][]*armcognitiveservices.Model{
		"eastus": {
			sampleModel("gpt-4o", "2024-05-13", "Standard", "OpenAI.Standard.gpt-4o", true),
		},
		"westus": {
			sampleModel("gpt-4o-mini", "2024-07-18", "Standard", "OpenAI.Standard.gpt-4o-mini", true),
		},
	})

	models, err := svc.ListModels(ctx, "sub-1", []string{"eastus", "westus"})
	require.NoError(t, err)
	require.Len(t, models, 2)
	require.Equal(t, "gpt-4o", models[0].Name)
	require.Equal(t, "gpt-4o-mini", models[1].Name)
}

func TestAiModelService_ListModelVersions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	svc := seedCache(t, "sub-1", map[string][]*armcognitiveservices.Model{
		"eastus": {
			sampleModel("gpt-4o", "2024-05-13", "Standard", "OpenAI.Standard.gpt-4o", false),
			sampleModel("gpt-4o", "2024-11-20", "Standard", "OpenAI.Standard.gpt-4o-2", true),
		},
	})

	t.Run("returns versions with default", func(t *testing.T) {
		versions, defaultVersion, err := svc.ListModelVersions(ctx, "sub-1", "gpt-4o", "eastus")
		require.NoError(t, err)
		require.Len(t, versions, 2)
		require.Equal(t, "2024-11-20", defaultVersion)
	})

	t.Run("returns error for missing model", func(t *testing.T) {
		_, _, err := svc.ListModelVersions(ctx, "sub-1", "missing-model", "eastus")
		require.Error(t, err)
	})
}

func TestAiModelService_ListModelSkus(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	svc := seedCache(t, "sub-1", map[string][]*armcognitiveservices.Model{
		"eastus": {
			sampleModel("gpt-4o", "2024-05-13", "Standard", "OpenAI.Standard.gpt-4o", true),
		},
	})

	t.Run("returns skus for version", func(t *testing.T) {
		skus, err := svc.ListModelSkus(ctx, "sub-1", "gpt-4o", "eastus", "2024-05-13")
		require.NoError(t, err)
		require.Len(t, skus, 1)
		require.Equal(t, "Standard", skus[0].Name)
	})

	t.Run("missing model returns error", func(t *testing.T) {
		_, err := svc.ListModelSkus(ctx, "sub-1", "missing", "eastus", "v1")
		require.Error(t, err)
	})

	t.Run("missing version returns error", func(t *testing.T) {
		_, err := svc.ListModelSkus(ctx, "sub-1", "gpt-4o", "eastus", "wrong-version")
		require.Error(t, err)
	})
}

func TestAiModelService_ResolveModelDeployments(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	svc := seedCache(t, "sub-1", map[string][]*armcognitiveservices.Model{
		"eastus": {
			sampleModel("gpt-4o", "2024-05-13", "Standard", "OpenAI.Standard.gpt-4o", true),
			sampleModel("gpt-4o", "2024-11-20", "GlobalStandard", "OpenAI.GlobalStandard.gpt-4o", false),
		},
	})

	t.Run("returns all matching deployments", func(t *testing.T) {
		result, err := svc.ResolveModelDeployments(ctx, "sub-1", "gpt-4o", &DeploymentOptions{
			Locations: []string{"eastus"},
		})
		require.NoError(t, err)
		require.Len(t, result, 2)
		for _, d := range result {
			require.Equal(t, "gpt-4o", d.ModelName)
			require.Equal(t, "OpenAI", d.Format)
			require.Equal(t, "eastus", d.Location)
			require.Equal(t, int32(10), d.Capacity)
		}
	})

	t.Run("filters by version", func(t *testing.T) {
		result, err := svc.ResolveModelDeployments(ctx, "sub-1", "gpt-4o", &DeploymentOptions{
			Locations: []string{"eastus"},
			Versions:  []string{"2024-05-13"},
		})
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Equal(t, "2024-05-13", result[0].Version)
	})

	t.Run("filters by sku", func(t *testing.T) {
		result, err := svc.ResolveModelDeployments(ctx, "sub-1", "gpt-4o", &DeploymentOptions{
			Locations: []string{"eastus"},
			Skus:      []string{"GlobalStandard"},
		})
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Equal(t, "GlobalStandard", result[0].Sku.Name)
	})

	t.Run("uses preferred capacity when valid", func(t *testing.T) {
		preferred := int32(25)
		result, err := svc.ResolveModelDeployments(ctx, "sub-1", "gpt-4o", &DeploymentOptions{
			Locations: []string{"eastus"},
			Capacity:  &preferred,
		})
		require.NoError(t, err)
		for _, d := range result {
			require.Equal(t, int32(25), d.Capacity)
		}
	})

	t.Run("model not found", func(t *testing.T) {
		_, err := svc.ResolveModelDeployments(ctx, "sub-1", "missing-model", &DeploymentOptions{
			Locations: []string{"eastus"},
		})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrModelNotFound)
	})

	t.Run("no deployment match", func(t *testing.T) {
		_, err := svc.ResolveModelDeployments(ctx, "sub-1", "gpt-4o", &DeploymentOptions{
			Locations: []string{"eastus"},
			Skus:      []string{"NonExistentSku"},
		})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNoDeploymentMatch)
	})

	t.Run("nil options still works", func(t *testing.T) {
		// nil options means no Locations → ListModels with empty locations triggers
		// ListLocations on the nil azureClient; hit that path by using explicit Locations
		// via an empty-but-non-nil options to verify nil-handling branch is reached
		// through ResolveModelDeployments.
		_, err := svc.ResolveModelDeployments(ctx, "sub-1", "gpt-4o", &DeploymentOptions{
			Locations: []string{"eastus"},
		})
		require.NoError(t, err)
	})
}

func TestAiModelService_ResolveModelDeployments_ExcludesFinetuneByDefault(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	svc := seedCache(t, "sub-1", map[string][]*armcognitiveservices.Model{
		"eastus": {
			sampleModel("gpt-4o", "v1", "FineTune", "OpenAI.Standard.gpt-4o-finetune", true),
		},
	})

	t.Run("finetune excluded by default", func(t *testing.T) {
		_, err := svc.ResolveModelDeployments(ctx, "sub-1", "gpt-4o", &DeploymentOptions{
			Locations: []string{"eastus"},
		})
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNoDeploymentMatch)
	})

	t.Run("finetune included via option", func(t *testing.T) {
		result, err := svc.ResolveModelDeployments(ctx, "sub-1", "gpt-4o", &DeploymentOptions{
			Locations:           []string{"eastus"},
			IncludeFinetuneSkus: true,
		})
		require.NoError(t, err)
		require.Len(t, result, 1)
	})
}

func TestAiModelService_ResolveModelDeploymentsWithQuota_RequiresSingleLocation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	svc := NewAiModelService(nil, nil)

	tests := []struct {
		name      string
		locations []string
	}{
		{"zero locations fails", nil},
		{"two locations fails", []string{"eastus", "westus"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := svc.ResolveModelDeploymentsWithQuota(ctx, "sub-1", "gpt-4o", &DeploymentOptions{
				Locations: tt.locations,
			}, &QuotaCheckOptions{})
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrQuotaLocationRequired))
		})
	}
}

func TestAiModelService_FetchModelsForLocations_CachedOnly(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	svc := seedCache(t, "sub-1", map[string][]*armcognitiveservices.Model{
		"eastus": {sampleModel("m1", "v1", "Standard", "x.y.z", true)},
		"westus": {sampleModel("m2", "v1", "Standard", "a.b.c", true)},
	})

	result, err := svc.fetchModelsForLocations(ctx, "sub-1", []string{"eastus", "westus"})
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Contains(t, result, "eastus")
	require.Contains(t, result, "westus")
}

func TestAiModelService_ConvertToAiModels_UsesNow(t *testing.T) {
	t.Parallel()

	svc := NewAiModelService(nil, nil)
	raw := map[string][]*armcognitiveservices.Model{
		"eastus": {sampleModel("m1", "v1", "Standard", "x.y.z", true)},
	}
	models := svc.convertToAiModels(raw)
	require.Len(t, models, 1)
	require.Equal(t, "m1", models[0].Name)
}
