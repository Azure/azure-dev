// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilterModels(t *testing.T) {
	models := []AiModel{
		{
			Name:            "gpt-4o",
			Format:          "OpenAI",
			LifecycleStatus: "stable",
			Capabilities:    []string{"chat", "completion"},
			Locations:       []string{"eastus", "westus"},
		},
		{
			Name:            "gpt-4o-mini",
			Format:          "OpenAI",
			LifecycleStatus: "preview",
			Capabilities:    []string{"chat"},
			Locations:       []string{"eastus"},
		},
		{
			Name:            "text-embedding-ada-002",
			Format:          "OpenAI",
			LifecycleStatus: "stable",
			Capabilities:    []string{"embeddings"},
			Locations:       []string{"westus"},
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
			preferred: intPtr(50),
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
			preferred: intPtr(200),
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
			preferred: intPtr(2),
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
			preferred: intPtr(15),
			expected:  10,
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

func intPtr(v int32) *int32 {
	return &v
}
