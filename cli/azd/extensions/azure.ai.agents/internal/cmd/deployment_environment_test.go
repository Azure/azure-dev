// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
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

func TestPersistDeploymentEnvironmentReservesExistingCanonicalIndices(
	t *testing.T,
) {
	deployments := []project.Deployment{
		{
			Name:  "chat",
			Model: project.DeploymentModel{Name: "gpt", Format: "OpenAI", Version: "1"},
			Sku:   project.DeploymentSku{Name: "Standard", Capacity: 10},
		},
		{
			Name: "${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
			Model: project.DeploymentModel{
				Name:    "${AZURE_AI_MODEL_NAME_2}",
				Format:  "${AZURE_AI_MODEL_FORMAT_2}",
				Version: "${AZURE_AI_MODEL_VERSION_2}",
			},
			Sku: project.DeploymentSku{
				Name:     "${AZURE_AI_MODEL_SKU_NAME_2}",
				Capacity: "${AZURE_AI_MODEL_SKU_CAPACITY_2}",
			},
		},
	}
	values := map[string]string{}
	setEnv := func(_ context.Context, key, value string) error {
		values[key] = value
		return nil
	}

	references, err := persistDeploymentEnvironment(
		t.Context(),
		setEnv,
		deployments,
	)

	require.NoError(t, err)
	assert.Equal(t, "chat", values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
	assert.NotContains(t, values, "AZURE_AI_MODEL_DEPLOYMENT_NAME_2")
	assert.Equal(t, deployments[1], references[1])
	assert.Equal(t, "${AZURE_AI_MODEL_DEPLOYMENT_NAME}", references[0].Name)
}

func TestPersistDeploymentEnvironmentRejectsNonPositiveCapacity(t *testing.T) {
	for _, capacity := range []any{0, -1, "0", "-1"} {
		t.Run(fmt.Sprintf("%v", capacity), func(t *testing.T) {
			_, err := persistDeploymentEnvironment(
				t.Context(),
				func(_ context.Context, _, _ string) error {
					return nil
				},
				[]project.Deployment{{
					Name:  "chat",
					Model: project.DeploymentModel{Name: "gpt"},
					Sku:   project.DeploymentSku{Capacity: capacity},
				}},
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "positive integer")
		})
	}
}

func TestPersistAdoptedDeploymentEnvironmentPreservesRetainedLiteral(t *testing.T) {
	t.Parallel()

	entry := foundryDeploymentEntry{
		Deployment: project.Deployment{
			Name: "chat",
			Model: project.DeploymentModel{
				Name:    "gpt-4.1",
				Format:  "OpenAI",
				Version: "2025-04-14",
			},
			Sku: project.DeploymentSku{
				Name:     "GlobalStandard",
				Capacity: 10,
			},
		},
		preserveManifest: true,
	}
	setEnv := func(_ context.Context, _, _ string) error {
		t.Error("retained literal deployments must not write environment values")
		return nil
	}

	got, err := persistAdoptedDeploymentEnvironment(
		t.Context(),
		setEnv,
		[]foundryDeploymentEntry{entry},
		nil,
	)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, entry.Deployment, got[0].Deployment)
	assert.True(t, got[0].preserveManifest)
}

func TestPersistAdoptedDeploymentEnvironmentTracksManagedDeployment(
	t *testing.T,
) {
	t.Parallel()

	managed := project.Deployment{
		Name: "chat",
		Model: project.DeploymentModel{
			Name:    "gpt-4.1",
			Format:  "OpenAI",
			Version: "2025-04-14",
		},
		Sku: project.DeploymentSku{
			Name:     "GlobalStandard",
			Capacity: 10,
		},
	}
	values := map[string]string{}
	setEnv := func(_ context.Context, key, value string) error {
		values[key] = value
		return nil
	}

	got, err := persistAdoptedDeploymentEnvironment(
		t.Context(),
		setEnv,
		[]foundryDeploymentEntry{{
			Deployment:       managed,
			referenceIndex:   0,
			preserveManifest: false,
		}},
		[]project.Deployment{managed},
	)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "${AZURE_AI_MODEL_DEPLOYMENT_NAME}", got[0].Deployment.Name)
	assert.Equal(t, "${AZURE_AI_MODEL_SKU_CAPACITY}", got[0].Deployment.Sku.Capacity)
	assert.Equal(t, "chat", values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
	assert.Equal(t, "10", values["AZURE_AI_MODEL_SKU_CAPACITY"])
}

func TestPersistAdoptedDeploymentEnvironmentPersistsExistingSelection(
	t *testing.T,
) {
	t.Parallel()

	existing := project.Deployment{
		Name: "existing-chat",
		Model: project.DeploymentModel{
			Name:    "gpt-4.1",
			Format:  "OpenAI",
			Version: "2025-04-14",
		},
		Sku: project.DeploymentSku{
			Name:     "GlobalStandard",
			Capacity: 50,
		},
	}
	values := map[string]string{}
	setEnv := func(_ context.Context, key, value string) error {
		values[key] = value
		return nil
	}

	got, err := persistAdoptedDeploymentEnvironment(
		t.Context(),
		setEnv,
		nil,
		[]project.Deployment{existing},
	)

	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, "existing-chat", values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
	assert.Equal(t, "gpt-4.1", values["AZURE_AI_MODEL_NAME"])
	assert.Equal(t, "50", values["AZURE_AI_MODEL_SKU_CAPACITY"])
}

func TestPersistAdoptedDeploymentEnvironmentReservesReorderedSuffix(
	t *testing.T,
) {
	t.Parallel()

	existingReference := project.Deployment{
		Name: "${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
		Model: project.DeploymentModel{
			Name:    "${AZURE_AI_MODEL_NAME_2}",
			Format:  "${AZURE_AI_MODEL_FORMAT_2}",
			Version: "${AZURE_AI_MODEL_VERSION_2}",
		},
		Sku: project.DeploymentSku{
			Name:     "${AZURE_AI_MODEL_SKU_NAME_2}",
			Capacity: "${AZURE_AI_MODEL_SKU_CAPACITY_2}",
		},
	}
	newDeployment := project.Deployment{
		Name:  "new-chat",
		Model: project.DeploymentModel{Name: "gpt-4.1"},
		Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
	}
	values := map[string]string{}
	setEnv := func(_ context.Context, key, value string) error {
		values[key] = value
		return nil
	}

	got, err := persistAdoptedDeploymentEnvironment(
		t.Context(),
		setEnv,
		[]foundryDeploymentEntry{
			{referenceIndex: 0},
			{referenceIndex: 1},
		},
		[]project.Deployment{existingReference, newDeployment},
	)

	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
		got[0].Deployment.Name,
	)
	assert.Equal(t, "${AZURE_AI_MODEL_DEPLOYMENT_NAME}", got[1].Deployment.Name)
	assert.Equal(t, "new-chat", values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
	assert.NotContains(t, values, "AZURE_AI_MODEL_DEPLOYMENT_NAME_2")
}
