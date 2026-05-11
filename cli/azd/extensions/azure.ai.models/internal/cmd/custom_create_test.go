// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"
)

func TestBuildDerivedModelURI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "short model name",
			input:    "FW-GPT-OSS-120B",
			expected: "azureml://registries/azureml-fireworks/models/FW-GPT-OSS-120B/versions/1",
		},
		{
			name:     "full azureml URI passthrough",
			input:    "azureml://registries/custom-reg/models/MyModel/versions/3",
			expected: "azureml://registries/custom-reg/models/MyModel/versions/3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildDerivedModelURI(tt.input)
			if got != tt.expected {
				t.Errorf("buildDerivedModelURI(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractBaseModelName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain name passthrough",
			input:    "FW-DeepSeek-V3.1",
			expected: "FW-DeepSeek-V3.1",
		},
		{
			name:     "extracts from azureml URI",
			input:    "azureml://registries/azureml-fireworks/models/FW-GPT-OSS-120B/versions/1",
			expected: "FW-GPT-OSS-120B",
		},
		{
			name:     "malformed URI returns as-is",
			input:    "azureml://registries/reg/nomodels",
			expected: "azureml://registries/reg/nomodels",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractBaseModelName(tt.input)
			if got != tt.expected {
				t.Errorf("extractBaseModelName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractVersionFromURI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "extracts version",
			input:    "azureml://registries/azureml-fireworks/models/FW-Qwen3-14B/versions/2",
			expected: "2",
		},
		{
			name:     "non-azureml returns empty",
			input:    "FW-GPT-OSS-120B",
			expected: "",
		},
		{
			name:     "no versions segment returns empty",
			input:    "azureml://registries/reg/models/MyModel",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractVersionFromURI(tt.input)
			if got != tt.expected {
				t.Errorf("extractVersionFromURI(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
