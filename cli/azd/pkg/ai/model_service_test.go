// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/stretchr/testify/require"
)

func TestFilterModels(t *testing.T) {
	models := []AiModel{
		{
			Name:         "gpt-4o",
			Format:       "OpenAI",
			Capabilities: []string{"chat", "completion"},
			Locations:    []string{"eastus", "westus"},
			Versions: []AiModelVersion{
				{Version: "2024-05-13", LifecycleStatus: "stable"},
				{Version: "2024-11-20", IsDefault: true, LifecycleStatus: "stable"},
			},
		},
		{
			Name:         "gpt-4o-mini",
			Format:       "OpenAI",
			Capabilities: []string{"chat"},
			Locations:    []string{"eastus"},
			Versions: []AiModelVersion{
				{Version: "2024-07-18", IsDefault: true, LifecycleStatus: "preview"},
			},
		},
		{
			Name:         "text-embedding-ada-002",
			Format:       "OpenAI",
			Capabilities: []string{"embeddings"},
			Locations:    []string{"westus"},
			Versions: []AiModelVersion{
				{Version: "2", IsDefault: true, LifecycleStatus: "stable"},
			},
		},
	}

	tests := []struct {
		name     string
		options  *FilterOptions
		expected []string // expected model names
	}{
		{
			name:     "nil options returns all",
			options:  nil,
			expected: []string{"gpt-4o", "gpt-4o-mini", "text-embedding-ada-002"},
		},
		{
			name:     "filter by capability - chat",
			options:  &FilterOptions{Capabilities: []string{"chat"}},
			expected: []string{"gpt-4o", "gpt-4o-mini"},
		},
		{
			name:     "filter by capability - embeddings",
			options:  &FilterOptions{Capabilities: []string{"embeddings"}},
			expected: []string{"text-embedding-ada-002"},
		},
		{
			name:     "filter by format",
			options:  &FilterOptions{Formats: []string{"OpenAI"}},
			expected: []string{"gpt-4o", "gpt-4o-mini", "text-embedding-ada-002"},
		},
		{
			name:     "filter by status",
			options:  &FilterOptions{Statuses: []string{"stable"}},
			expected: []string{"gpt-4o", "text-embedding-ada-002"},
		},
		{
			name:     "filter by location",
			options:  &FilterOptions{Locations: []string{"eastus"}},
			expected: []string{"gpt-4o", "gpt-4o-mini"},
		},
		{
			name:     "exclude model names",
			options:  &FilterOptions{ExcludeModelNames: []string{"gpt-4o"}},
			expected: []string{"gpt-4o-mini", "text-embedding-ada-002"},
		},
		{
			name: "combined filters",
			options: &FilterOptions{
				Capabilities: []string{"chat"},
				Statuses:     []string{"stable"},
			},
			expected: []string{"gpt-4o"},
		},
		{
			name: "no matches",
			options: &FilterOptions{
				Capabilities: []string{"image-generation"},
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterModels(models, tt.options)
			names := make([]string, len(result))
			for i, m := range result {
				names[i] = m.Name
			}
			require.Equal(t, tt.expected, names)
		})
	}
}

func TestFilterModels_FiltersVersionsByStatus(t *testing.T) {
	t.Parallel()

	models := []AiModel{
		{
			Name:      "gpt-4o",
			Format:    "OpenAI",
			Locations: []string{"eastus", "westus"},
			Versions: []AiModelVersion{
				{Version: "2024-08-06", LifecycleStatus: "Deprecating"},
				{Version: "2024-11-20", IsDefault: true, LifecycleStatus: "GenerallyAvailable"},
			},
		},
	}

	filtered := FilterModels(models, &FilterOptions{Statuses: []string{"Deprecating"}})
	require.Len(t, filtered, 1)
	require.Len(t, filtered[0].Versions, 1)
	require.Equal(t, "2024-08-06", filtered[0].Versions[0].Version)
	require.Equal(t, "Deprecating", filtered[0].Versions[0].LifecycleStatus)
}

func TestConvertToAiModels_FiltersDeprecatedVersionsAndSkus(t *testing.T) {
	t.Parallel()

	svc := NewAiModelService(nil, nil)
	now := time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC)

	rawModels := map[string][]*armcognitiveservices.Model{
		"northcentralus": {
			{
				Model: &armcognitiveservices.AccountModel{
					Name:            new("gpt-35-turbo"),
					Version:         new("0613"),
					LifecycleStatus: new(armcognitiveservices.ModelLifecycleStatus("Deprecated")),
					Deprecation: &armcognitiveservices.ModelDeprecationInfo{
						Inference: new("2025-04-30T00:00:00Z"),
					},
					SKUs: []*armcognitiveservices.ModelSKU{
						{
							Name:            new("Standard"),
							UsageName:       new("OpenAI.Standard.gpt-35-turbo"),
							DeprecationDate: new(time.Date(2025, 2, 13, 0, 0, 0, 0, time.UTC)),
						},
					},
				},
			},
			{
				Model: &armcognitiveservices.AccountModel{
					Name:            new("gpt-4o"),
					Version:         new("2024-08-06"),
					LifecycleStatus: new(armcognitiveservices.ModelLifecycleStatus("Deprecating")),
					Deprecation: &armcognitiveservices.ModelDeprecationInfo{
						Inference: new("2026-10-01T00:00:00Z"),
					},
					SKUs: []*armcognitiveservices.ModelSKU{
						{
							Name:            new("Standard"),
							UsageName:       new("OpenAI.Standard.gpt-4o"),
							DeprecationDate: new(time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)),
						},
						{
							Name:            new("GlobalStandard"),
							UsageName:       new("OpenAI.GlobalStandard.gpt-4o"),
							DeprecationDate: new(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
						},
					},
				},
			},
			{
				Model: &armcognitiveservices.AccountModel{
					Name:            new("all-expired"),
					Version:         new("1"),
					LifecycleStatus: new(armcognitiveservices.ModelLifecycleStatus("GenerallyAvailable")),
					SKUs: []*armcognitiveservices.ModelSKU{
						{
							Name:            new("Standard"),
							UsageName:       new("Custom.Standard.all-expired"),
							DeprecationDate: new(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
						},
					},
				},
			},
			{
				Model: &armcognitiveservices.AccountModel{
					Name:            new("gpt-4.1-mini"),
					Version:         new("2025-04-14"),
					LifecycleStatus: new(armcognitiveservices.ModelLifecycleStatus("GenerallyAvailable")),
					SKUs: []*armcognitiveservices.ModelSKU{
						{
							Name:            new("GlobalStandard"),
							UsageName:       new("OpenAI.GlobalStandard.gpt-4.1-mini"),
							DeprecationDate: new(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)),
						},
					},
				},
			},
		},
	}

	models := svc.convertToAiModelsAt(rawModels, now, nil)
	require.Len(t, models, 2)

	require.Equal(t, "gpt-4.1-mini", models[0].Name)
	require.Equal(t, "gpt-4o", models[1].Name)

	require.Len(t, models[1].Versions, 1)
	require.Equal(t, "2024-08-06", models[1].Versions[0].Version)
	require.Equal(t, "Deprecating", models[1].Versions[0].LifecycleStatus)
	require.Len(t, models[1].Versions[0].Skus, 1)
	require.Equal(t, "GlobalStandard", models[1].Versions[0].Skus[0].Name)
	require.Equal(t, "OpenAI.GlobalStandard.gpt-4o", models[1].Versions[0].Skus[0].UsageName)
}

func TestConvertToAiModels_PreservesVersionLifecycleStatus(t *testing.T) {
	t.Parallel()

	svc := NewAiModelService(nil, nil)
	now := time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC)

	rawModels := map[string][]*armcognitiveservices.Model{
		"northcentralus": {
			{
				Model: &armcognitiveservices.AccountModel{
					Name:            new("gpt-4o"),
					Version:         new("2024-08-06"),
					LifecycleStatus: new(armcognitiveservices.ModelLifecycleStatus("Deprecating")),
					SKUs: []*armcognitiveservices.ModelSKU{
						{
							Name:            new("GlobalStandard"),
							UsageName:       new("OpenAI.GlobalStandard.gpt-4o"),
							DeprecationDate: new(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
						},
					},
				},
			},
			{
				Model: &armcognitiveservices.AccountModel{
					Name:             new("gpt-4o"),
					Version:          new("2024-11-20"),
					IsDefaultVersion: new(true),
					LifecycleStatus:  new(armcognitiveservices.ModelLifecycleStatus("GenerallyAvailable")),
					SKUs: []*armcognitiveservices.ModelSKU{
						{
							Name:            new("GlobalStandard"),
							UsageName:       new("OpenAI.GlobalStandard.gpt-4o"),
							DeprecationDate: new(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
						},
					},
				},
			},
		},
	}

	models := svc.convertToAiModelsAt(rawModels, now, nil)
	require.Len(t, models, 1)
	require.Equal(t, "gpt-4o", models[0].Name)
	require.Empty(t, models[0].LifecycleStatus)
	require.Len(t, models[0].Versions, 2)

	versionStatuses := map[string]string{}
	for _, version := range models[0].Versions {
		versionStatuses[version.Version] = version.LifecycleStatus
	}

	require.Equal(t, map[string]string{
		"2024-08-06": "Deprecating",
		"2024-11-20": "GenerallyAvailable",
	}, versionStatuses)
}

func TestConvertToAiModels_FiltersStatusesBeforeAggregation(t *testing.T) {
	t.Parallel()

	svc := NewAiModelService(nil, nil)
	now := time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC)

	rawModels := map[string][]*armcognitiveservices.Model{
		"eastus": {
			{
				Model: &armcognitiveservices.AccountModel{
					Name:            new("gpt-4o"),
					Version:         new("2024-08-06"),
					LifecycleStatus: new(armcognitiveservices.ModelLifecycleStatus("Deprecating")),
					SKUs: []*armcognitiveservices.ModelSKU{
						{
							Name:            new("GlobalStandard"),
							UsageName:       new("OpenAI.GlobalStandard.gpt-4o"),
							DeprecationDate: new(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
						},
					},
				},
			},
		},
		"westus": {
			{
				Model: &armcognitiveservices.AccountModel{
					Name:             new("gpt-4o"),
					Version:          new("2024-11-20"),
					IsDefaultVersion: new(true),
					LifecycleStatus:  new(armcognitiveservices.ModelLifecycleStatus("GenerallyAvailable")),
					SKUs: []*armcognitiveservices.ModelSKU{
						{
							Name:            new("GlobalStandard"),
							UsageName:       new("OpenAI.GlobalStandard.gpt-4o"),
							DeprecationDate: new(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
						},
					},
				},
			},
		},
	}

	models := svc.convertToAiModelsAt(rawModels, now, []string{"GenerallyAvailable"})
	require.Len(t, models, 1)
	require.Equal(t, "gpt-4o", models[0].Name)
	require.Empty(t, models[0].LifecycleStatus)
	require.Equal(t, []string{"westus"}, models[0].Locations)
	require.Len(t, models[0].Versions, 1)
	require.Equal(t, "2024-11-20", models[0].Versions[0].Version)
	require.Equal(t, "GenerallyAvailable", models[0].Versions[0].LifecycleStatus)
}

func TestConvertToAiModels_ExcludesLocationsWithOnlyDeprecatedEntries(t *testing.T) {
	t.Parallel()

	svc := NewAiModelService(nil, nil)
	now := time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC)

	rawModels := map[string][]*armcognitiveservices.Model{
		"eastus": {
			{
				Model: &armcognitiveservices.AccountModel{
					Name:            new("gpt-4o"),
					Version:         new("2024-08-06"),
					LifecycleStatus: new(armcognitiveservices.ModelLifecycleStatus("GenerallyAvailable")),
					SKUs: []*armcognitiveservices.ModelSKU{
						{
							Name:            new("GlobalStandard"),
							UsageName:       new("OpenAI.GlobalStandard.gpt-4o"),
							DeprecationDate: new(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
						},
					},
				},
			},
		},
		"westus": {
			{
				Model: &armcognitiveservices.AccountModel{
					Name:            new("gpt-4o"),
					Version:         new("2024-08-06"),
					LifecycleStatus: new(armcognitiveservices.ModelLifecycleStatus("GenerallyAvailable")),
					SKUs: []*armcognitiveservices.ModelSKU{
						{
							Name:            new("GlobalStandard"),
							UsageName:       new("OpenAI.GlobalStandard.gpt-4o"),
							DeprecationDate: new(time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)),
						},
					},
				},
			},
		},
	}

	models := svc.convertToAiModelsAt(rawModels, now, nil)
	require.Len(t, models, 1)
	require.Equal(t, "gpt-4o", models[0].Name)
	require.Empty(t, models[0].LifecycleStatus)
	require.Equal(t, []string{"eastus"}, models[0].Locations)
	require.Len(t, models[0].Versions, 1)
	require.Equal(t, "GenerallyAvailable", models[0].Versions[0].LifecycleStatus)
	require.Len(t, models[0].Versions[0].Skus, 1)
}

func TestFilterModelsByQuota(t *testing.T) {
	models := []AiModel{
		{
			Name: "gpt-4o",
			Versions: []AiModelVersion{
				{
					Version: "2024-05-13",
					Skus: []AiModelSku{
						{Name: "Standard", UsageName: "OpenAI.Standard.gpt-4o"},
						{Name: "GlobalStandard", UsageName: "OpenAI.GlobalStandard.gpt-4o"},
					},
				},
			},
		},
		{
			Name: "gpt-4o-mini",
			Versions: []AiModelVersion{
				{
					Version: "2024-07-18",
					Skus: []AiModelSku{
						{Name: "Standard", UsageName: "OpenAI.Standard.gpt-4o-mini"},
					},
				},
			},
		},
		{
			Name: "text-embedding-ada-002",
			Versions: []AiModelVersion{
				{
					Version: "2",
					Skus: []AiModelSku{
						{Name: "Standard", UsageName: "OpenAI.Standard.text-embedding-ada-002"},
					},
				},
			},
		},
	}

	tests := []struct {
		name         string
		usages       []AiModelUsage
		minRemaining float64
		expected     []string
	}{
		{
			name: "all models have quota",
			usages: []AiModelUsage{
				{Name: "OpenAI.Standard.gpt-4o", CurrentValue: 10, Limit: 100},
				{Name: "OpenAI.GlobalStandard.gpt-4o", CurrentValue: 0, Limit: 200},
				{Name: "OpenAI.Standard.gpt-4o-mini", CurrentValue: 50, Limit: 200},
				{Name: "OpenAI.Standard.text-embedding-ada-002", CurrentValue: 0, Limit: 50},
			},
			minRemaining: 1,
			expected:     []string{"gpt-4o", "gpt-4o-mini", "text-embedding-ada-002"},
		},
		{
			name: "one model exhausted",
			usages: []AiModelUsage{
				{Name: "OpenAI.Standard.gpt-4o", CurrentValue: 100, Limit: 100},
				{Name: "OpenAI.GlobalStandard.gpt-4o", CurrentValue: 200, Limit: 200},
				{Name: "OpenAI.Standard.gpt-4o-mini", CurrentValue: 50, Limit: 200},
				{Name: "OpenAI.Standard.text-embedding-ada-002", CurrentValue: 0, Limit: 50},
			},
			minRemaining: 1,
			expected:     []string{"gpt-4o-mini", "text-embedding-ada-002"},
		},
		{
			name: "model with one SKU above threshold keeps model",
			usages: []AiModelUsage{
				{Name: "OpenAI.Standard.gpt-4o", CurrentValue: 100, Limit: 100},      // exhausted
				{Name: "OpenAI.GlobalStandard.gpt-4o", CurrentValue: 0, Limit: 200},  // 200 remaining
				{Name: "OpenAI.Standard.gpt-4o-mini", CurrentValue: 190, Limit: 200}, // 10 remaining
				{Name: "OpenAI.Standard.text-embedding-ada-002", CurrentValue: 0, Limit: 50},
			},
			minRemaining: 50,
			expected:     []string{"gpt-4o", "text-embedding-ada-002"},
		},
		{
			name: "min remaining 0 means any remaining > 0",
			usages: []AiModelUsage{
				{Name: "OpenAI.Standard.gpt-4o", CurrentValue: 100, Limit: 100},       // 0 remaining
				{Name: "OpenAI.GlobalStandard.gpt-4o", CurrentValue: 200, Limit: 200}, // 0 remaining
				{Name: "OpenAI.Standard.gpt-4o-mini", CurrentValue: 199, Limit: 200},  // 1 remaining
			},
			minRemaining: 0,
			expected:     []string{"gpt-4o-mini"},
		},
		{
			name: "model with no matching usage excluded (conservative)",
			usages: []AiModelUsage{
				{Name: "OpenAI.Standard.gpt-4o", CurrentValue: 10, Limit: 100},
			},
			minRemaining: 1,
			expected:     []string{"gpt-4o"},
		},
		{
			name:         "empty usages excludes all models",
			usages:       []AiModelUsage{},
			minRemaining: 1,
			expected:     []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterModelsByQuota(models, tt.usages, tt.minRemaining)
			names := make([]string, len(result))
			for i, m := range result {
				names[i] = m.Name
			}
			require.Equal(t, tt.expected, names)
		})
	}
}

func TestResolveCapacity(t *testing.T) {
	tests := []struct {
		name      string
		sku       AiModelSku
		preferred *int32
		expected  int32
	}{
		{
			name: "preferred capacity used when valid",
			sku: AiModelSku{
				DefaultCapacity: 10,
				MinCapacity:     1,
				MaxCapacity:     100,
				CapacityStep:    1,
			},
			preferred: new(int32(50)),
			expected:  50,
		},
		{
			name: "preferred capacity out of range uses default",
			sku: AiModelSku{
				DefaultCapacity: 10,
				MinCapacity:     1,
				MaxCapacity:     100,
				CapacityStep:    1,
			},
			preferred: new(int32(200)),
			expected:  10,
		},
		{
			name: "preferred capacity below min uses default",
			sku: AiModelSku{
				DefaultCapacity: 10,
				MinCapacity:     5,
				MaxCapacity:     100,
				CapacityStep:    1,
			},
			preferred: new(int32(2)),
			expected:  10,
		},
		{
			name: "preferred capacity aligned to step",
			sku: AiModelSku{
				DefaultCapacity: 10,
				MinCapacity:     0,
				MaxCapacity:     100,
				CapacityStep:    10,
			},
			preferred: new(int32(15)),
			expected:  10,
		},
		{
			name: "preferred capacity aligned relative to minimum",
			sku: AiModelSku{
				DefaultCapacity: 7,
				MinCapacity:     7,
				MaxCapacity:     100,
				CapacityStep:    5,
			},
			preferred: new(int32(12)),
			expected:  12,
		},
		{
			name: "no preferred uses default",
			sku: AiModelSku{
				DefaultCapacity: 25,
				MinCapacity:     1,
				MaxCapacity:     100,
				CapacityStep:    1,
			},
			preferred: nil,
			expected:  25,
		},
		{
			name: "no preferred no default returns 0",
			sku: AiModelSku{
				DefaultCapacity: 0,
				MinCapacity:     1,
				MaxCapacity:     100,
				CapacityStep:    1,
			},
			preferred: nil,
			expected:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolveCapacity(tt.sku, tt.preferred)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestResolveCapacityWithQuota(t *testing.T) {
	t.Run("uses default when it fits in remaining quota", func(t *testing.T) {
		capacity, ok := ResolveCapacityWithQuota(AiModelSku{
			DefaultCapacity: 25,
			MinCapacity:     1,
			MaxCapacity:     100,
			CapacityStep:    1,
		}, nil, 50)

		require.True(t, ok)
		require.Equal(t, int32(25), capacity)
	})

	t.Run("falls back below default when no preferred capacity is set", func(t *testing.T) {
		capacity, ok := ResolveCapacityWithQuota(AiModelSku{
			DefaultCapacity: 5000,
			MinCapacity:     0,
			MaxCapacity:     5000,
			CapacityStep:    0,
		}, nil, 1000)

		require.True(t, ok)
		require.Equal(t, int32(1000), capacity)
	})

	t.Run("respects min and step when falling back", func(t *testing.T) {
		capacity, ok := ResolveCapacityWithQuota(AiModelSku{
			DefaultCapacity: 3000,
			MinCapacity:     100,
			MaxCapacity:     3000,
			CapacityStep:    100,
		}, nil, 950)

		require.True(t, ok)
		require.Equal(t, int32(900), capacity)
	})

	t.Run("fails when explicit preferred capacity does not fit", func(t *testing.T) {
		capacity, ok := ResolveCapacityWithQuota(AiModelSku{
			DefaultCapacity: 5000,
			MinCapacity:     0,
			MaxCapacity:     5000,
			CapacityStep:    0,
		}, new(int32(5000)), 1000)

		require.False(t, ok)
		require.Equal(t, int32(5000), capacity)
	})

	t.Run("fails when remaining quota is below effective minimum", func(t *testing.T) {
		capacity, ok := ResolveCapacityWithQuota(AiModelSku{
			DefaultCapacity: 0,
			MinCapacity:     100,
			MaxCapacity:     3000,
			CapacityStep:    100,
		}, nil, 50)

		require.False(t, ok)
		require.Equal(t, int32(0), capacity)
	})

	t.Run("falls back using step alignment relative to minimum", func(t *testing.T) {
		capacity, ok := ResolveCapacityWithQuota(AiModelSku{
			DefaultCapacity: 27,
			MinCapacity:     7,
			MaxCapacity:     100,
			CapacityStep:    5,
		}, nil, 20)

		require.True(t, ok)
		require.Equal(t, int32(17), capacity)
	})
}

func TestMaxModelRemainingQuota(t *testing.T) {
	model := AiModel{
		Name: "gpt-4o",
		Versions: []AiModelVersion{
			{
				Version: "v1",
				Skus: []AiModelSku{
					{Name: "Standard", UsageName: "OpenAI.Standard.gpt-4o"},
					{Name: "GlobalStandard", UsageName: "OpenAI.GlobalStandard.gpt-4o"},
				},
			},
		},
	}

	t.Run("returns highest remaining across model skus", func(t *testing.T) {
		usageMap := map[string]AiModelUsage{
			"OpenAI.Standard.gpt-4o":       {Name: "OpenAI.Standard.gpt-4o", CurrentValue: 50, Limit: 100},
			"OpenAI.GlobalStandard.gpt-4o": {Name: "OpenAI.GlobalStandard.gpt-4o", CurrentValue: 10, Limit: 200},
		}

		maxRemaining, found := maxModelRemainingQuota(model, usageMap)
		require.True(t, found)
		require.Equal(t, float64(190), maxRemaining)
	})

	t.Run("returns not found when no model usage entries exist", func(t *testing.T) {
		usageMap := map[string]AiModelUsage{
			"OpenAI.Standard.other": {Name: "OpenAI.Standard.other", CurrentValue: 1, Limit: 10},
		}

		maxRemaining, found := maxModelRemainingQuota(model, usageMap)
		require.False(t, found)
		require.Equal(t, float64(0), maxRemaining)
	})
}

func TestModelLocations(t *testing.T) {
	models := []AiModel{
		{Name: "a", Locations: []string{"westus", "eastus"}},
		{Name: "b", Locations: []string{"eastus", "centralus"}},
	}

	locations := modelLocations(models)

	require.Equal(t, []string{"centralus", "eastus", "westus"}, locations)
}

func TestFilterModelsByAnyLocationQuota(t *testing.T) {
	models := []AiModel{
		{
			Name:      "model-a",
			Locations: []string{"eastus", "westus"},
			Versions: []AiModelVersion{
				{
					Version: "1",
					Skus: []AiModelSku{
						{Name: "Standard", UsageName: "a_usage"},
					},
				},
			},
		},
		{
			Name:      "model-b",
			Locations: []string{"westus"},
			Versions: []AiModelVersion{
				{
					Version: "1",
					Skus: []AiModelSku{
						{Name: "Standard", UsageName: "b_usage"},
					},
				},
			},
		},
		{
			Name:      "model-c",
			Locations: []string{"eastus"},
			Versions: []AiModelVersion{
				{
					Version: "1",
					Skus: []AiModelSku{
						{Name: "Standard", UsageName: "c_usage"},
					},
				},
			},
		},
	}

	usagesByLocation := map[string][]AiModelUsage{
		"eastus": {
			{Name: "a_usage", CurrentValue: 9, Limit: 10}, // remaining: 1
			{Name: "c_usage", CurrentValue: 5, Limit: 5},  // remaining: 0
		},
		"westus": {
			{Name: "b_usage", CurrentValue: 8, Limit: 10}, // remaining: 2
		},
	}

	filtered := filterModelsByAnyLocationQuota(models, usagesByLocation, 1)
	filteredNames := make([]string, 0, len(filtered))
	for _, model := range filtered {
		filteredNames = append(filteredNames, model.Name)
	}

	require.Equal(t, []string{"model-a", "model-b"}, filteredNames)
}

func TestIsFinetuneUsageName(t *testing.T) {
	tests := []struct {
		usageName string
		expected  bool
	}{
		{"OpenAI.Standard.gpt-4o", false},
		{"OpenAI.GlobalStandard.gpt-4o", false},
		{"OpenAI.Standard.gpt-4o-finetune", true},
		{"OpenAI.GlobalStandard.gpt-4o-FINEtune", true},
		{"OpenAI.ProvisionedManaged", false},
		{"OpenAI.ProvisionedManaged-finetune", true},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.usageName, func(t *testing.T) {
			require.Equal(t, tt.expected, IsFinetuneUsageName(tt.usageName))
		})
	}
}
