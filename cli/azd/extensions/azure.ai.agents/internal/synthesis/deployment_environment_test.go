// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSynthesizeDeploymentEnvironmentReferences(t *testing.T) {
	testSynthesizeDeploymentEnvironmentReferences(t)
}

func testSynthesizeDeploymentEnvironmentReferences(t *testing.T) {
	raw := []byte(`services:
  project:
    host: azure.ai.project
    deployments:
      - name: ${DEPLOYMENT_NAME}
        model:
          name: ${MODEL_NAME}
          format: ${MODEL_FORMAT}
          version: ${MODEL_VERSION}
        sku:
          name: ${SKU_NAME}
          capacity: ${MODEL_SKU_CAPACITY}
`)
	env := map[string]string{
		"DEPLOYMENT_NAME":    "chat",
		"MODEL_NAME":         "gpt",
		"MODEL_FORMAT":       "OpenAI",
		"MODEL_VERSION":      "1",
		"SKU_NAME":           "GlobalStandard",
		"MODEL_SKU_CAPACITY": "50",
	}

	result, err := Synthesize(Input{RawAzureYAML: raw, ServiceName: "project", Env: env})
	require.NoError(t, err)
	deployments := result.Parameters["deployments"].([]Deployment)
	require.Len(t, deployments, 1)
	assert.Equal(t, "chat", deployments[0].Name)
	assert.Equal(t, "gpt", deployments[0].Model.Name)
	assert.Equal(t, 50, deployments[0].Sku.Capacity)

	preserved, err := Synthesize(Input{
		RawAzureYAML: raw, ServiceName: "project", PreserveVarRefs: true,
	})
	require.NoError(t, err)
	deployments = preserved.Parameters["deployments"].([]Deployment)
	assert.Equal(t, "${DEPLOYMENT_NAME}", deployments[0].Name)
	assert.Equal(t, "${MODEL_SKU_CAPACITY}", deployments[0].Sku.Capacity)
}

func TestResolveDeploymentsRejectsMalformedCanonicalTuple(t *testing.T) {
	tests := []struct {
		name       string
		deployment Deployment
		message    string
	}{
		{
			name: "partial",
			deployment: Deployment{
				Name: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
				Model: DeploymentModel{
					Name:    "gpt",
					Format:  "OpenAI",
					Version: "1",
				},
				Sku: DeploymentSku{Name: "Standard", Capacity: 10},
			},
			message: "complete canonical environment tuple",
		},
		{
			name: "mixed custom",
			deployment: Deployment{
				Name: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
				Model: DeploymentModel{
					Name:    "${CUSTOM_MODEL_NAME}",
					Format:  "${AZURE_AI_MODEL_FORMAT}",
					Version: "${AZURE_AI_MODEL_VERSION}",
				},
				Sku: DeploymentSku{
					Name:     "${AZURE_AI_MODEL_SKU_NAME}",
					Capacity: "${AZURE_AI_MODEL_SKU_CAPACITY}",
				},
			},
			message: "complete canonical environment tuple",
		},
		{
			name: "mismatched suffix",
			deployment: Deployment{
				Name: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
				Model: DeploymentModel{
					Name:    "${AZURE_AI_MODEL_NAME_2}",
					Format:  "${AZURE_AI_MODEL_FORMAT}",
					Version: "${AZURE_AI_MODEL_VERSION}",
				},
				Sku: DeploymentSku{
					Name:     "${AZURE_AI_MODEL_SKU_NAME}",
					Capacity: "${AZURE_AI_MODEL_SKU_CAPACITY}",
				},
			},
			message: "mismatched suffixes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveDeployments(
				[]Deployment{tt.deployment},
				nil,
				true,
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestResolveDeploymentsParsesLiteralCapacityWhenPreservingReferences(
	t *testing.T,
) {
	deployments, err := ResolveDeployments(
		[]Deployment{{
			Name:  "chat",
			Model: DeploymentModel{Name: "gpt", Format: "OpenAI", Version: "1"},
			Sku:   DeploymentSku{Name: "Standard", Capacity: "10"},
		}},
		nil,
		true,
	)

	require.NoError(t, err)
	assert.Equal(t, 10, deployments[0].Sku.Capacity)
}

func TestResolveDeploymentsRejectsNonPositiveCapacity(t *testing.T) {
	for _, capacity := range []any{0, -1, "0", "-1"} {
		t.Run(fmt.Sprintf("%v", capacity), func(t *testing.T) {
			_, err := ResolveDeployments(
				[]Deployment{{
					Name:  "chat",
					Model: DeploymentModel{Name: "gpt", Format: "OpenAI", Version: "1"},
					Sku:   DeploymentSku{Name: "Standard", Capacity: capacity},
				}},
				nil,
				true,
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "positive integer")
		})
	}
}
