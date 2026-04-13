// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeResources_CognitiveDeployments(t *testing.T) {
	tests := []struct {
		name      string
		resources []armTemplateResource
		wantCount int
		wantFirst cognitiveDeploymentInfo
	}{
		{
			name:      "no cognitive resources",
			resources: []armTemplateResource{{Type: "Microsoft.Storage/storageAccounts"}},
			wantCount: 0,
		},
		{
			name: "cognitive deployment with parent account",
			resources: []armTemplateResource{
				{
					Type:     "Microsoft.CognitiveServices/accounts",
					Name:     "my-ai-account",
					Location: "eastus",
				},
				{
					Type: "Microsoft.CognitiveServices/accounts/deployments",
					Name: "my-ai-account/gpt-4o-deployment",
					SKU:  mustArmField(armTemplateSKU{Name: "GlobalStandard", Capacity: new(10)}),
					Properties: mustJSON(map[string]any{
						"model": map[string]any{
							"name":    "gpt-4o",
							"format":  "OpenAI",
							"version": "2024-08-06",
						},
					}),
				},
			},
			wantCount: 1,
			wantFirst: cognitiveDeploymentInfo{
				AccountName:  "my-ai-account",
				Name:         "gpt-4o-deployment",
				ModelName:    "gpt-4o",
				ModelFormat:  "OpenAI",
				ModelVersion: "2024-08-06",
				SkuName:      "GlobalStandard",
				Capacity:     10,
				Location:     "eastus",
			},
		},
		{
			name: "deployment inherits location from parent account",
			resources: []armTemplateResource{
				{
					Type:     "Microsoft.CognitiveServices/accounts",
					Name:     "acct",
					Location: "westus2",
				},
				{
					Type: "Microsoft.CognitiveServices/accounts/deployments",
					Name: "acct/embed-deploy",
					SKU:  mustArmField(armTemplateSKU{Name: "Standard", Capacity: new(5)}),
					Properties: mustJSON(map[string]any{
						"model": map[string]any{
							"name":   "text-embedding-ada-002",
							"format": "OpenAI",
						},
					}),
				},
			},
			wantCount: 1,
			wantFirst: cognitiveDeploymentInfo{
				AccountName: "acct",
				Name:        "embed-deploy",
				ModelName:   "text-embedding-ada-002",
				ModelFormat: "OpenAI",
				SkuName:     "Standard",
				Capacity:    5,
				Location:    "westus2",
			},
		},
		{
			name: "deployment with no model name is excluded",
			resources: []armTemplateResource{
				{
					Type:       "Microsoft.CognitiveServices/accounts/deployments",
					Name:       "acct/dep",
					Properties: mustJSON(map[string]any{"model": map[string]any{}}),
				},
			},
			wantCount: 0,
		},
		{
			name: "multiple deployments",
			resources: []armTemplateResource{
				{
					Type:     "Microsoft.CognitiveServices/accounts",
					Name:     "acct",
					Location: "eastus",
				},
				{
					Type: "Microsoft.CognitiveServices/accounts/deployments",
					Name: "acct/dep-1",
					SKU:  mustArmField(armTemplateSKU{Name: "Standard", Capacity: new(10)}),
					Properties: mustJSON(map[string]any{
						"model": map[string]any{"name": "gpt-4o", "format": "OpenAI"},
					}),
				},
				{
					Type: "Microsoft.CognitiveServices/accounts/deployments",
					Name: "acct/dep-2",
					SKU:  mustArmField(armTemplateSKU{Name: "GlobalStandard", Capacity: new(20)}),
					Properties: mustJSON(map[string]any{
						"model": map[string]any{"name": "gpt-4o-mini", "format": "OpenAI"},
					}),
				},
			},
			wantCount: 2,
			wantFirst: cognitiveDeploymentInfo{
				AccountName: "acct",
				Name:        "dep-1",
				ModelName:   "gpt-4o",
				ModelFormat: "OpenAI",
				SkuName:     "Standard",
				Capacity:    10,
				Location:    "eastus",
			},
		},
		{
			name: "case insensitive resource type match",
			resources: []armTemplateResource{
				{
					Type:     "microsoft.cognitiveservices/accounts",
					Name:     "acct",
					Location: "eastus",
				},
				{
					Type: "microsoft.cognitiveservices/accounts/deployments",
					Name: "acct/dep",
					SKU:  mustArmField(armTemplateSKU{Name: "Standard", Capacity: new(1)}),
					Properties: mustJSON(map[string]any{
						"model": map[string]any{"name": "gpt-4o", "format": "OpenAI"},
					}),
				},
			},
			wantCount: 1,
			wantFirst: cognitiveDeploymentInfo{
				AccountName: "acct",
				Name:        "dep",
				ModelName:   "gpt-4o",
				ModelFormat: "OpenAI",
				SkuName:     "Standard",
				Capacity:    1,
				Location:    "eastus",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props := analyzeResources(tt.resources)
			require.Len(t, props.CognitiveDeployments, tt.wantCount)
			if tt.wantCount > 0 {
				require.Equal(t, tt.wantFirst, props.CognitiveDeployments[0])
			}
		})
	}
}

func TestResolveUsageName(t *testing.T) {
	catalog := []ai.AiModel{
		{
			Name: "gpt-4o",
			Versions: []ai.AiModelVersion{
				{
					Version: "2024-08-06",
					Skus: []ai.AiModelSku{
						{Name: "Standard", UsageName: "OpenAI.Standard.gpt-4o"},
						{Name: "GlobalStandard", UsageName: "OpenAI.GlobalStandard.gpt-4o"},
					},
				},
			},
		},
		{
			Name: "gpt-4.1-mini",
			Versions: []ai.AiModelVersion{
				{
					Version: "2025-04-14",
					Skus: []ai.AiModelSku{
						{Name: "GlobalStandard", UsageName: "OpenAI.GlobalStandard.gpt-4.1-mini"},
					},
				},
			},
		},
	}

	tests := []struct {
		name string
		dep  cognitiveDeploymentInfo
		want string
	}{
		{
			name: "matches model and sku",
			dep:  cognitiveDeploymentInfo{ModelName: "gpt-4o", SkuName: "Standard"},
			want: "OpenAI.Standard.gpt-4o",
		},
		{
			name: "matches global standard sku",
			dep:  cognitiveDeploymentInfo{ModelName: "gpt-4o", SkuName: "GlobalStandard"},
			want: "OpenAI.GlobalStandard.gpt-4o",
		},
		{
			name: "matches with version",
			dep:  cognitiveDeploymentInfo{ModelName: "gpt-4o", SkuName: "Standard", ModelVersion: "2024-08-06"},
			want: "OpenAI.Standard.gpt-4o",
		},
		{
			name: "version mismatch returns empty",
			dep:  cognitiveDeploymentInfo{ModelName: "gpt-4o", SkuName: "Standard", ModelVersion: "nonexistent"},
			want: "",
		},
		{
			name: "model not in catalog",
			dep:  cognitiveDeploymentInfo{ModelName: "unknown-model", SkuName: "Standard"},
			want: "",
		},
		{
			name: "sku not available for model",
			dep:  cognitiveDeploymentInfo{ModelName: "gpt-4.1-mini", SkuName: "Standard"},
			want: "",
		},
		{
			name: "gpt-4.1-mini resolves correctly",
			dep:  cognitiveDeploymentInfo{ModelName: "gpt-4.1-mini", SkuName: "GlobalStandard"},
			want: "OpenAI.GlobalStandard.gpt-4.1-mini",
		},
		{
			name: "case insensitive model match",
			dep:  cognitiveDeploymentInfo{ModelName: "GPT-4o", SkuName: "standard"},
			want: "OpenAI.Standard.gpt-4o",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveUsageName(catalog, tt.dep)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCheckAiModelQuota_NoDeployments(t *testing.T) {
	valCtx := &validationContext{
		Props: resourcesProperties{},
	}

	p := &BicepProvider{}
	results, err := p.checkAiModelQuota(t.Context(), valCtx)
	require.NoError(t, err)
	require.Nil(t, results)
}

// mustJSON marshals v to json.RawMessage, panicking on error. Test helper only.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// mustArmField creates an armField[T] from a value by marshaling to JSON. Test helper only.
func mustArmField[T any](v T) armField[T] {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var f armField[T]
	if err := f.UnmarshalJSON(b); err != nil {
		panic(err)
	}
	return f
}
