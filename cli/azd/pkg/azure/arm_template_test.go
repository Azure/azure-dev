// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHasParamReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"no references", "OpenAI.Standard.gpt-4o, 10", false},
		{"single reference", "$(p:modelName)", true},
		{"reference with text", "OpenAI.Standard.$(p:modelName)", true},
		{"multiple references", "$(p:modelName), $(p:capacity)", true},
		{"empty string", "", false},
		{"partial syntax no close", "$(p:modelName", false},
		{"partial syntax no prefix", "$(modelName)", false},
		{"env prefix not param", "$(env:VAR_NAME)", false},
		{"bicep interpolation not matched", "${modelName}", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, HasParamReferences(tt.input))
		})
	}
}

func TestExtractParamReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"no references", "OpenAI.Standard.gpt-4o, 10", nil},
		{"single reference", "$(p:modelName)", []string{"modelName"}},
		{"multiple references", "$(p:modelName), $(p:capacity)", []string{"modelName", "capacity"}},
		{"duplicate references", "$(p:modelName), $(p:modelName)", []string{"modelName"}},
		{"mixed text and refs", "prefix.$(p:model).suffix, $(p:cap)", []string{"model", "cap"}},
		{"empty string", "", nil},
		{"reference with spaces", "$(p: modelName )", []string{"modelName"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ExtractParamReferences(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestResolveParamReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		values    map[string]any
		expected  string
		expectErr bool
	}{
		{
			name:     "single reference",
			input:    "$(p:modelName)",
			values:   map[string]any{"modelName": "gpt-4o"},
			expected: "gpt-4o",
		},
		{
			name:     "multiple references",
			input:    "$(p:modelName), $(p:capacity)",
			values:   map[string]any{"modelName": "gpt-4o", "capacity": 10},
			expected: "gpt-4o, 10",
		},
		{
			name:     "no references passthrough",
			input:    "OpenAI.Standard.gpt-4o, 10",
			values:   map[string]any{},
			expected: "OpenAI.Standard.gpt-4o, 10",
		},
		{
			name:      "missing reference",
			input:     "$(p:modelName), $(p:capacity)",
			values:    map[string]any{"modelName": "gpt-4o"},
			expectErr: true,
		},
		{
			name:     "integer value",
			input:    "$(p:capacity)",
			values:   map[string]any{"capacity": 10},
			expected: "10",
		},
		{
			name:     "float value",
			input:    "$(p:capacity)",
			values:   map[string]any{"capacity": 10.5},
			expected: "10.5",
		},
		{
			name:      "empty values map",
			input:     "$(p:modelName)",
			values:    map[string]any{},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := ResolveParamReferences(tt.input, tt.values)
			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestAzdMetadataParamDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata AzdMetadata
		expected []string
	}{
		{
			name:     "no usage names",
			metadata: AzdMetadata{},
			expected: nil,
		},
		{
			name: "constant usage names",
			metadata: AzdMetadata{
				UsageName: usageName{"OpenAI.Standard.gpt-4o, 10"},
			},
			expected: nil,
		},
		{
			name: "single reference",
			metadata: AzdMetadata{
				UsageName: usageName{"$(p:modelName), $(p:capacity)"},
			},
			expected: []string{"modelName", "capacity"},
		},
		{
			name: "multiple usage names with refs",
			metadata: AzdMetadata{
				UsageName: usageName{
					"$(p:model1), $(p:cap1)",
					"$(p:model2), $(p:cap2)",
				},
			},
			expected: []string{"model1", "cap1", "model2", "cap2"},
		},
		{
			name: "mixed constant and reference",
			metadata: AzdMetadata{
				UsageName: usageName{
					"OpenAI.Standard.gpt-4o, 10",
					"$(p:modelName), $(p:capacity)",
				},
			},
			expected: []string{"modelName", "capacity"},
		},
		{
			name: "deduplicate across entries",
			metadata: AzdMetadata{
				UsageName: usageName{
					"$(p:modelName), $(p:capacity)",
					"$(p:modelName), $(p:capacity)",
				},
			},
			expected: []string{"modelName", "capacity"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.metadata.ParamDependencies()
			if tt.expected == nil {
				require.Nil(t, result)
			} else {
				require.Equal(t, tt.expected, result)
			}
		})
	}
}
