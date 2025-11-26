// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptions_GetWithDefaults(t *testing.T) {
	// Save original defaultOptions and restore after tests
	originalDefaults := defaultOptions
	t.Cleanup(func() {
		defaultOptions = originalDefaults
	})

	// Set test default options
	defaultOptions = Options{
		Provider: Bicep,
		Module:   "main",
		Path:     "infra",
	}

	tests := []struct {
		name           string
		baseOptions    Options
		otherOptions   []Options
		expectedResult Options
		expectError    bool
	}{
		{
			name: "merges base options with defaults",
			baseOptions: Options{
				Provider: Terraform,
				Path:     "custom-infra",
			},
			otherOptions: nil,
			expectedResult: Options{
				Provider: Terraform,
				Module:   "main",
				Path:     "custom-infra",
			},
			expectError: false,
		},
		{
			name:         "empty base options uses defaults",
			baseOptions:  Options{},
			otherOptions: nil,
			expectedResult: Options{
				Provider: Bicep,
				Module:   "main",
				Path:     "infra",
			},
			expectError: false,
		},
		{
			name: "merges multiple other options in order (first non-empty value wins)",
			baseOptions: Options{
				Provider: Terraform,
			},
			otherOptions: []Options{
				{
					Path:   "infra-override-1",
					Module: "module-1",
				},
				{
					Module: "module-2",
					Name:   "layer-1",
				},
			},
			expectedResult: Options{
				Provider: Terraform,
				Path:     "infra-override-1",
				Module:   "module-1", // First other option wins for Module
				Name:     "layer-1",
			},
			expectError: false,
		},
		{
			name: "base options take precedence over other options",
			baseOptions: Options{
				Provider: Pulumi,
				Path:     "base-path",
			},
			otherOptions: []Options{
				{
					Provider: Bicep,
					Path:     "other-path",
					Module:   "other-module",
				},
			},
			expectedResult: Options{
				Provider: Pulumi,
				Path:     "base-path",
				Module:   "other-module",
			},
			expectError: false,
		},
		{
			name: "defaults fill in missing fields",
			baseOptions: Options{
				Name: "custom-name",
			},
			otherOptions: []Options{
				{
					Module: "custom-module",
				},
			},
			expectedResult: Options{
				Provider: Bicep,
				Module:   "custom-module",
				Path:     "infra",
				Name:     "custom-name",
			},
			expectError: false,
		},
		{
			name: "merges deployment stacks",
			baseOptions: Options{
				Provider: Bicep,
				DeploymentStacks: map[string]any{
					"stack1": "value1",
				},
			},
			otherOptions: []Options{
				{
					DeploymentStacks: map[string]any{
						"stack2": "value2",
					},
				},
			},
			expectedResult: Options{
				Provider: Bicep,
				Module:   "main",
				Path:     "infra",
				DeploymentStacks: map[string]any{
					"stack1": "value1",
					"stack2": "value2",
				},
			},
			expectError: false,
		},
		{
			name: "preserves IgnoreDeploymentState flag",
			baseOptions: Options{
				Provider:              Terraform,
				IgnoreDeploymentState: true,
			},
			otherOptions: nil,
			expectedResult: Options{
				Provider:              Terraform,
				Module:                "main",
				Path:                  "infra",
				IgnoreDeploymentState: true,
			},
			expectError: false,
		},
		{
			name: "handles layers in base options",
			baseOptions: Options{
				Layers: []Options{
					{
						Provider: Bicep,
						Name:     "layer-1",
						Path:     "layer1-path",
					},
					{
						Provider: Terraform,
						Name:     "layer-2",
						Path:     "layer2-path",
					},
				},
			},
			otherOptions: nil,
			expectedResult: Options{
				Provider: Bicep,
				Module:   "main",
				Path:     "infra",
				Layers: []Options{
					{
						Provider: Bicep,
						Name:     "layer-1",
						Path:     "layer1-path",
					},
					{
						Provider: Terraform,
						Name:     "layer-2",
						Path:     "layer2-path",
					},
				},
			},
			expectError: false,
		},
		{
			name: "all fields specified overrides defaults completely",
			baseOptions: Options{
				Provider: Arm,
				Path:     "custom-path",
				Module:   "custom-module",
				Name:     "custom-name",
				DeploymentStacks: map[string]any{
					"key": "value",
				},
				IgnoreDeploymentState: true,
			},
			otherOptions: nil,
			expectedResult: Options{
				Provider: Arm,
				Path:     "custom-path",
				Module:   "custom-module",
				Name:     "custom-name",
				DeploymentStacks: map[string]any{
					"key": "value",
				},
				IgnoreDeploymentState: true,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.baseOptions.GetWithDefaults(tt.otherOptions...)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResult.Provider, result.Provider, "Provider mismatch")
				assert.Equal(t, tt.expectedResult.Path, result.Path, "Path mismatch")
				assert.Equal(t, tt.expectedResult.Module, result.Module, "Module mismatch")
				assert.Equal(t, tt.expectedResult.Name, result.Name, "Name mismatch")
				assert.Equal(
					t,
					tt.expectedResult.IgnoreDeploymentState,
					result.IgnoreDeploymentState,
					"IgnoreDeploymentState mismatch",
				)

				if tt.expectedResult.DeploymentStacks != nil {
					assert.Equal(
						t,
						tt.expectedResult.DeploymentStacks,
						result.DeploymentStacks,
						"DeploymentStacks mismatch",
					)
				}

				if tt.expectedResult.Layers != nil {
					require.Len(t, result.Layers, len(tt.expectedResult.Layers), "Layers length mismatch")
					for i, expectedLayer := range tt.expectedResult.Layers {
						assert.Equal(t, expectedLayer.Provider, result.Layers[i].Provider)
						assert.Equal(t, expectedLayer.Name, result.Layers[i].Name)
						assert.Equal(t, expectedLayer.Path, result.Layers[i].Path)
					}
				}
			}
		})
	}
}

func TestOptions_GetWithDefaults_MergePrecedence(t *testing.T) {
	// Save original defaultOptions and restore after tests
	originalDefaults := defaultOptions
	t.Cleanup(func() {
		defaultOptions = originalDefaults
	})

	// Set test default options
	defaultOptions = Options{
		Provider: Bicep,
		Module:   "default-module",
		Path:     "default-path",
	}

	t.Run("verifies merge precedence order: base > other > defaults", func(t *testing.T) {
		baseOptions := Options{
			Provider: Terraform,
		}

		otherOptions := []Options{
			{
				Module: "other-module",
			},
		}

		result, err := baseOptions.GetWithDefaults(otherOptions...)
		require.NoError(t, err)

		// Base option (Terraform) should win over default (Bicep)
		assert.Equal(t, Terraform, result.Provider)
		// Other option (other-module) should win over default (default-module)
		assert.Equal(t, "other-module", result.Module)
		// Default (default-path) should be used when not specified elsewhere
		assert.Equal(t, "default-path", result.Path)
	})
}

func TestOptions_GetWithDefaults_EmptyVariations(t *testing.T) {
	// Save original defaultOptions and restore after tests
	originalDefaults := defaultOptions
	t.Cleanup(func() {
		defaultOptions = originalDefaults
	})

	defaultOptions = Options{
		Provider: Bicep,
		Module:   "main",
		Path:     "infra",
	}

	t.Run("handles nil other options", func(t *testing.T) {
		baseOptions := Options{
			Provider: Terraform,
		}

		result, err := baseOptions.GetWithDefaults(nil...)
		require.NoError(t, err)
		assert.Equal(t, Terraform, result.Provider)
		assert.Equal(t, "main", result.Module)
		assert.Equal(t, "infra", result.Path)
	})

	t.Run("handles empty other options slice", func(t *testing.T) {
		baseOptions := Options{
			Provider: Pulumi,
		}

		result, err := baseOptions.GetWithDefaults([]Options{}...)
		require.NoError(t, err)
		assert.Equal(t, Pulumi, result.Provider)
		assert.Equal(t, "main", result.Module)
		assert.Equal(t, "infra", result.Path)
	})

	t.Run("handles empty options in other slice", func(t *testing.T) {
		baseOptions := Options{
			Provider: Arm,
		}

		otherOptions := []Options{
			{},
			{},
		}

		result, err := baseOptions.GetWithDefaults(otherOptions...)
		require.NoError(t, err)
		assert.Equal(t, Arm, result.Provider)
		assert.Equal(t, "main", result.Module)
		assert.Equal(t, "infra", result.Path)
	})
}
