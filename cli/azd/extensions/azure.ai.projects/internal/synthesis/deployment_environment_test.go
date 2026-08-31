// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
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
