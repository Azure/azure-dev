// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"azureaiagent/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistDeploymentEnvironmentUsesCanonicalIndexedKeys(t *testing.T) {
	deployments := []project.Deployment{
		{
			Name:  "chat",
			Model: project.DeploymentModel{Name: "gpt", Format: "OpenAI", Version: "1"},
			Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 50},
		},
		{
			Name:  "embed",
			Model: project.DeploymentModel{Name: "embedding", Format: "OpenAI", Version: "2"},
			Sku:   project.DeploymentSku{Name: "Standard", Capacity: 10},
		},
		{
			Name:  "reasoning",
			Model: project.DeploymentModel{Name: "reasoning", Format: "OpenAI", Version: "3"},
			Sku:   project.DeploymentSku{Name: "DataZoneStandard", Capacity: 20},
		},
	}
	values := map[string]string{}
	setEnv := func(_ context.Context, key, value string) error {
		values[key] = value
		return nil
	}

	references, err := persistDeploymentEnvironment(t.Context(), setEnv, deployments)

	require.NoError(t, err)
	assert.Equal(t, "chat", values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
	assert.Equal(t, "embed", values["AZURE_AI_MODEL_DEPLOYMENT_NAME_2"])
	assert.Equal(t, "reasoning", values["AZURE_AI_MODEL_DEPLOYMENT_NAME_3"])
	assert.Equal(t, "20", values["AZURE_AI_MODEL_SKU_CAPACITY_3"])
	assert.Equal(t, "${AZURE_AI_MODEL_NAME}", references[0].Model.Name)
	assert.Equal(t, "${AZURE_AI_MODEL_SKU_NAME_2}", references[1].Sku.Name)
	assert.Equal(t, "${AZURE_AI_MODEL_SKU_CAPACITY_3}", references[2].Sku.Capacity)
}

func TestPersistDeploymentEnvironmentPreservesCustomReferences(t *testing.T) {
	deployment := project.Deployment{
		Name: "${CUSTOM_DEPLOYMENT_NAME}",
		Model: project.DeploymentModel{
			Name:    "gpt",
			Format:  "OpenAI",
			Version: "1",
		},
		Sku: project.DeploymentSku{Name: "Standard", Capacity: 10},
	}
	setEnv := func(_ context.Context, _, _ string) error {
		t.Fatal("custom references must not be rewritten")
		return nil
	}

	references, err := persistDeploymentEnvironment(
		t.Context(), setEnv, []project.Deployment{deployment})

	require.NoError(t, err)
	assert.Equal(t, deployment, references[0])
}
