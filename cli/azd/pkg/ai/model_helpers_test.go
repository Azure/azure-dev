// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptrTo[T any](v T) *T { return &v }

func TestModelHasDefaultVersion(t *testing.T) {
	tests := []struct {
		name     string
		model    AiModel
		expected bool
	}{
		{
			name: "has default version",
			model: AiModel{
				Name: "gpt-4o",
				Versions: []AiModelVersion{
					{Version: "v1", IsDefault: false},
					{Version: "v2", IsDefault: true},
				},
			},
			expected: true,
		},
		{
			name: "no default version",
			model: AiModel{
				Name: "gpt-4o",
				Versions: []AiModelVersion{
					{Version: "v1", IsDefault: false},
					{Version: "v2", IsDefault: false},
				},
			},
			expected: false,
		},
		{
			name: "single default version",
			model: AiModel{
				Name: "gpt-4o-mini",
				Versions: []AiModelVersion{
					{Version: "2024-07-18", IsDefault: true},
				},
			},
			expected: true,
		},
		{
			name: "empty versions",
			model: AiModel{
				Name:     "empty-model",
				Versions: []AiModelVersion{},
			},
			expected: false,
		},
		{
			name: "nil versions",
			model: AiModel{
				Name:     "nil-versions",
				Versions: nil,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ModelHasDefaultVersion(tt.model)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertSku(t *testing.T) {
	tests := []struct {
		name     string
		input    *armcognitiveservices.ModelSKU
		expected AiModelSku
	}{
		{
			name: "fully populated",
			input: &armcognitiveservices.ModelSKU{
				Name:      ptrTo("GlobalStandard"),
				UsageName: ptrTo("OpenAI.GlobalStandard.gpt-4o"),
				Capacity: &armcognitiveservices.CapacityConfig{
					Default: ptrTo(int32(10)),
					Minimum: ptrTo(int32(1)),
					Maximum: ptrTo(int32(100)),
					Step:    ptrTo(int32(5)),
				},
			},
			expected: AiModelSku{
				Name:            "GlobalStandard",
				UsageName:       "OpenAI.GlobalStandard.gpt-4o",
				DefaultCapacity: 10,
				MinCapacity:     1,
				MaxCapacity:     100,
				CapacityStep:    5,
			},
		},
		{
			name: "nil capacity",
			input: &armcognitiveservices.ModelSKU{
				Name:      ptrTo("Standard"),
				UsageName: ptrTo("OpenAI.Standard.gpt-4o"),
				Capacity:  nil,
			},
			expected: AiModelSku{
				Name:            "Standard",
				UsageName:       "OpenAI.Standard.gpt-4o",
				DefaultCapacity: 0,
				MinCapacity:     0,
				MaxCapacity:     0,
				CapacityStep:    0,
			},
		},
		{
			name: "nil name and usage",
			input: &armcognitiveservices.ModelSKU{
				Name:      nil,
				UsageName: nil,
				Capacity: &armcognitiveservices.CapacityConfig{
					Default: ptrTo(int32(5)),
				},
			},
			expected: AiModelSku{
				Name:            "",
				UsageName:       "",
				DefaultCapacity: 5,
				MinCapacity:     0,
				MaxCapacity:     0,
				CapacityStep:    0,
			},
		},
		{
			name: "partial capacity fields",
			input: &armcognitiveservices.ModelSKU{
				Name:      ptrTo("ProvisionedManaged"),
				UsageName: ptrTo("OpenAI.ProvisionedManaged"),
				Capacity: &armcognitiveservices.CapacityConfig{
					Default: nil,
					Minimum: ptrTo(int32(10)),
					Maximum: nil,
					Step:    ptrTo(int32(10)),
				},
			},
			expected: AiModelSku{
				Name:            "ProvisionedManaged",
				UsageName:       "OpenAI.ProvisionedManaged",
				DefaultCapacity: 0,
				MinCapacity:     10,
				MaxCapacity:     0,
				CapacityStep:    10,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertSku(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSafeString(t *testing.T) {
	tests := []struct {
		name     string
		input    *string
		expected string
	}{
		{
			name:     "nil returns empty",
			input:    nil,
			expected: "",
		},
		{
			name:     "non-nil returns value",
			input:    ptrTo("hello"),
			expected: "hello",
		},
		{
			name:     "empty string returns empty",
			input:    ptrTo(""),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, safeString(tt.input))
		})
	}
}

func TestSafeFloat64(t *testing.T) {
	tests := []struct {
		name     string
		input    *float64
		expected float64
	}{
		{
			name:     "nil returns zero",
			input:    nil,
			expected: 0,
		},
		{
			name:     "non-nil returns value",
			input:    ptrTo(42.5),
			expected: 42.5,
		},
		{
			name:     "zero value returns zero",
			input:    ptrTo(0.0),
			expected: 0,
		},
		{
			name:     "negative value",
			input:    ptrTo(-1.5),
			expected: -1.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, safeFloat64(tt.input))
		})
	}
}

func TestModelHasQuota(t *testing.T) {
	model := AiModel{
		Name: "gpt-4o",
		Versions: []AiModelVersion{
			{
				Version: "2024-05-13",
				Skus: []AiModelSku{
					{
						Name:      "Standard",
						UsageName: "OpenAI.Standard.gpt-4o",
					},
					{
						Name:      "GlobalStandard",
						UsageName: "OpenAI.GlobalStandard.gpt-4o",
					},
				},
			},
		},
	}

	tests := []struct {
		name         string
		usageMap     map[string]AiModelUsage
		minRemaining float64
		expected     bool
	}{
		{
			name: "has sufficient quota",
			usageMap: map[string]AiModelUsage{
				"OpenAI.Standard.gpt-4o": {
					Name:         "OpenAI.Standard.gpt-4o",
					CurrentValue: 10,
					Limit:        100,
				},
			},
			minRemaining: 50,
			expected:     true,
		},
		{
			name: "all quota exhausted",
			usageMap: map[string]AiModelUsage{
				"OpenAI.Standard.gpt-4o": {
					Name:         "OpenAI.Standard.gpt-4o",
					CurrentValue: 100,
					Limit:        100,
				},
				"OpenAI.GlobalStandard.gpt-4o": {
					Name:         "OpenAI.GlobalStandard.gpt-4o",
					CurrentValue: 200,
					Limit:        200,
				},
			},
			minRemaining: 1,
			expected:     false,
		},
		{
			name: "one sku has quota the other exhausted",
			usageMap: map[string]AiModelUsage{
				"OpenAI.Standard.gpt-4o": {
					Name:         "OpenAI.Standard.gpt-4o",
					CurrentValue: 100,
					Limit:        100,
				},
				"OpenAI.GlobalStandard.gpt-4o": {
					Name:         "OpenAI.GlobalStandard.gpt-4o",
					CurrentValue: 0,
					Limit:        200,
				},
			},
			minRemaining: 100,
			expected:     true,
		},
		{
			name:         "no usage entries for model",
			usageMap:     map[string]AiModelUsage{},
			minRemaining: 1,
			expected:     false,
		},
		{
			name: "remaining exactly equals min",
			usageMap: map[string]AiModelUsage{
				"OpenAI.Standard.gpt-4o": {
					Name:         "OpenAI.Standard.gpt-4o",
					CurrentValue: 90,
					Limit:        100,
				},
			},
			minRemaining: 10,
			expected:     true,
		},
		{
			name: "remaining just below min",
			usageMap: map[string]AiModelUsage{
				"OpenAI.Standard.gpt-4o": {
					Name:         "OpenAI.Standard.gpt-4o",
					CurrentValue: 91,
					Limit:        100,
				},
			},
			minRemaining: 10,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := modelHasQuota(
				model, tt.usageMap, tt.minRemaining,
			)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestModelHasQuota_EmptyVersions(t *testing.T) {
	model := AiModel{
		Name:     "empty",
		Versions: []AiModelVersion{},
	}

	usageMap := map[string]AiModelUsage{
		"some.usage": {
			Name: "some.usage", CurrentValue: 0, Limit: 100,
		},
	}

	require.False(t, modelHasQuota(model, usageMap, 1))
}
