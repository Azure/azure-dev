// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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

func TestPersistDeploymentEnvironmentReservesExistingCanonicalBase(t *testing.T) {
	existing := project.Deployment{
		Name: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		Model: project.DeploymentModel{
			Name:    "${AZURE_AI_MODEL_NAME}",
			Format:  "${AZURE_AI_MODEL_FORMAT}",
			Version: "${AZURE_AI_MODEL_VERSION}",
		},
		Sku: project.DeploymentSku{
			Name:     "${AZURE_AI_MODEL_SKU_NAME}",
			Capacity: "${AZURE_AI_MODEL_SKU_CAPACITY}",
		},
	}
	newDeployment := project.Deployment{
		Name:  "new-chat",
		Model: project.DeploymentModel{Name: "gpt", Format: "OpenAI", Version: "1"},
		Sku:   project.DeploymentSku{Name: "Standard", Capacity: 10},
	}
	values := map[string]string{}
	setEnv := func(_ context.Context, key, value string) error {
		values[key] = value
		return nil
	}

	references, err := persistDeploymentEnvironment(
		t.Context(),
		setEnv,
		[]project.Deployment{existing, newDeployment},
	)

	require.NoError(t, err)
	assert.NotContains(t, values, "AZURE_AI_MODEL_DEPLOYMENT_NAME")
	assert.Equal(t, "new-chat", values["AZURE_AI_MODEL_DEPLOYMENT_NAME_2"])
	assert.Equal(t, existing, references[0])
	assert.Equal(t, "${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}", references[1].Name)
}

func TestPersistProjectDeploymentConfigurationsReservesProjectTuples(
	t *testing.T,
) {
	t.Parallel()

	existing := project.Deployment{
		Name: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		Model: project.DeploymentModel{
			Name:    "${AZURE_AI_MODEL_NAME}",
			Format:  "${AZURE_AI_MODEL_FORMAT}",
			Version: "${AZURE_AI_MODEL_VERSION}",
		},
		Sku: project.DeploymentSku{
			Name:     "${AZURE_AI_MODEL_SKU_NAME}",
			Capacity: "${AZURE_AI_MODEL_SKU_CAPACITY}",
		},
	}
	existingValue, err := deploymentConfigValue(existing)
	require.NoError(t, err)
	server := &recordingProjectServer{
		projectPath: t.TempDir(),
		existing: map[string]*azdext.ServiceConfig{
			"foundry": {Name: "foundry", Host: AiProjectHost},
		},
		rawConfig: map[string]map[string]any{
			"foundry": {
				"deployments":          []any{existingValue},
				"deploymentReferences": []any{existingValue},
			},
		},
	}
	client := newProjectRecorderClient(
		t,
		server,
		&testEnvironmentServiceServer{
			values: map[string]map[string]string{
				"test": {
					"AZURE_AI_MODEL_DEPLOYMENT_NAME": "managed-chat",
					"AZURE_AI_MODEL_NAME":            "gpt-4o",
					"AZURE_AI_MODEL_FORMAT":          "OpenAI",
					"AZURE_AI_MODEL_VERSION":         "1",
					"AZURE_AI_MODEL_SKU_NAME":        "GlobalStandard",
					"AZURE_AI_MODEL_SKU_CAPACITY":    "10",
				},
			},
		},
	)
	values := map[string]string{}
	setEnv := func(_ context.Context, key, value string) error {
		values[key] = value
		return nil
	}
	newDeployment := project.Deployment{
		Name:  "new-chat",
		Model: project.DeploymentModel{Name: "gpt-4.1", Format: "OpenAI", Version: "1"},
		Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
	}

	configurations, err := persistProjectDeploymentConfigurations(
		t.Context(),
		client,
		"test",
		setEnv,
		[]project.Deployment{newDeployment},
		[]int{0},
		nil,
	)

	require.NoError(t, err)
	require.Len(t, configurations.managed, 1)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
		configurations.managed[0].Name,
	)
	assert.Empty(t, configurations.references)
	require.Len(t, configurations.allReferences, 1)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
		configurations.allReferences[0].Name,
	)
	assert.Equal(t, "new-chat", values["AZURE_AI_MODEL_DEPLOYMENT_NAME_2"])
	assert.NotContains(t, values, "AZURE_AI_MODEL_DEPLOYMENT_NAME")

	clear(values)
	configurations, err = persistProjectDeploymentConfigurations(
		t.Context(),
		client,
		"test",
		setEnv,
		[]project.Deployment{{
			Name:  "managed-chat",
			Model: project.DeploymentModel{Name: "gpt-4o", Format: "OpenAI", Version: "1"},
			Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
		}},
		[]int{0},
		nil,
	)

	require.NoError(t, err)
	require.Len(t, configurations.managed, 1)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		configurations.managed[0].Name,
	)
	assert.Empty(t, configurations.references)
	require.Len(t, configurations.allReferences, 1)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		configurations.allReferences[0].Name,
	)
	assert.Empty(t, values)
}

func TestPersistProjectDeploymentConfigurationsReusesExistingReference(
	t *testing.T,
) {
	t.Parallel()

	existingReference := project.Deployment{
		Name: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		Model: project.DeploymentModel{
			Name:    "${AZURE_AI_MODEL_NAME}",
			Format:  "${AZURE_AI_MODEL_FORMAT}",
			Version: "${AZURE_AI_MODEL_VERSION}",
		},
		Sku: project.DeploymentSku{
			Name:     "${AZURE_AI_MODEL_SKU_NAME}",
			Capacity: "${AZURE_AI_MODEL_SKU_CAPACITY}",
		},
	}
	existingValue, err := deploymentConfigValue(existingReference)
	require.NoError(t, err)
	server := &recordingProjectServer{
		projectPath: t.TempDir(),
		existing: map[string]*azdext.ServiceConfig{
			"foundry": {Name: "foundry", Host: AiProjectHost},
		},
		rawConfig: map[string]map[string]any{
			"foundry": {
				"deploymentReferences": []any{existingValue},
			},
		},
	}
	env := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"test": {
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-chat",
				"AZURE_AI_MODEL_NAME":            "gpt-4.1",
				"AZURE_AI_MODEL_FORMAT":          "OpenAI",
				"AZURE_AI_MODEL_VERSION":         "1",
				"AZURE_AI_MODEL_SKU_NAME":        "GlobalStandard",
				"AZURE_AI_MODEL_SKU_CAPACITY":    "10",
			},
		},
	}
	client := newProjectRecorderClient(t, server, env)
	values := map[string]string{}
	setEnv := func(_ context.Context, key, value string) error {
		values[key] = value
		return nil
	}

	configurations, err := persistProjectDeploymentConfigurations(
		t.Context(),
		client,
		"test",
		setEnv,
		[]project.Deployment{{
			Name:  "existing-chat",
			Model: project.DeploymentModel{Name: "gpt-4.1", Format: "OpenAI", Version: "1"},
			Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
		}},
		nil,
		nil,
	)

	require.NoError(t, err)
	assert.Empty(t, configurations.managed)
	require.Len(t, configurations.references, 1)
	assert.Equal(t, existingReference, configurations.references[0])
	require.Len(t, configurations.allReferences, 1)
	assert.Equal(t, existingReference, configurations.allReferences[0])
	assert.Empty(t, values)

	configurations, err = persistProjectDeploymentConfigurations(
		t.Context(),
		client,
		"test",
		setEnv,
		[]project.Deployment{{
			Name:  "existing-chat",
			Model: project.DeploymentModel{Name: "gpt-4.1", Format: "OpenAI", Version: "1"},
			Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
		}},
		[]int{0},
		nil,
	)

	require.NoError(t, err)
	require.Len(t, configurations.managed, 1)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
		configurations.managed[0].Name,
	)
	assert.Empty(t, configurations.references)
	require.Len(t, configurations.allReferences, 1)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
		configurations.allReferences[0].Name,
	)
	assert.Equal(t, "existing-chat", values["AZURE_AI_MODEL_DEPLOYMENT_NAME_2"])
}

func TestPersistDeploymentConfigurationsSeparatesOwnership(t *testing.T) {
	t.Parallel()

	configurations, err := persistDeploymentConfigurations(
		t.Context(),
		func(context.Context, string, string) error { return nil },
		[]project.Deployment{
			{
				Name:  "managed-chat",
				Model: project.DeploymentModel{Name: "gpt-4.1", Format: "OpenAI", Version: "1"},
				Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
			},
			{
				Name:  "existing-chat",
				Model: project.DeploymentModel{Name: "gpt-4.1", Format: "OpenAI", Version: "1"},
				Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
			},
		},
		[]int{0},
		nil,
		nil,
	)

	require.NoError(t, err)
	require.Len(t, configurations.managed, 1)
	require.Len(t, configurations.references, 1)
	require.Len(t, configurations.allReferences, 2)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		configurations.managed[0].Name,
	)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
		configurations.references[0].Name,
	)
}

func TestRewriteManifestDeploymentReferencesSwapsCanonicalSlots(t *testing.T) {
	t.Parallel()

	manifest, err := agent_yaml.InjectParameterValuesIntoManifest(
		processModelsTestManifest(),
		agent_yaml.ParameterValues{
			"primary":   "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
			"secondary": "${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
		},
	)
	require.NoError(t, err)

	err = rewriteManifestDeploymentReferences(
		manifest,
		[]project.Deployment{
			canonicalDeploymentReference(1),
			canonicalDeploymentReference(0),
		},
	)
	require.NoError(t, err)

	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
		environmentValue(manifest, "PRIMARY_DEPLOYMENT"),
	)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		environmentValue(manifest, "SECONDARY_DEPLOYMENT"),
	)
}

func TestRewriteContainerDeploymentReferencesUsesResolvedSlot(t *testing.T) {
	t.Parallel()

	environment := []agent_yaml.EnvironmentVariable{{
		Name:  "AZURE_AI_MODEL_DEPLOYMENT_NAME",
		Value: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
	}}
	definition := &agent_yaml.ContainerAgent{
		EnvironmentVariables: &environment,
	}

	err := rewriteContainerDeploymentReferences(
		definition,
		[]project.Deployment{canonicalDeploymentReference(1)},
	)

	require.NoError(t, err)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
		(*definition.EnvironmentVariables)[0].Value,
	)
}

func canonicalDeploymentReference(index int) project.Deployment {
	keys := deploymentKeys(index)
	return project.Deployment{
		Name: fmt.Sprintf("${%s}", keys.deploymentName),
		Model: project.DeploymentModel{
			Name:    fmt.Sprintf("${%s}", keys.modelName),
			Format:  fmt.Sprintf("${%s}", keys.modelFormat),
			Version: fmt.Sprintf("${%s}", keys.modelVersion),
		},
		Sku: project.DeploymentSku{
			Name:     fmt.Sprintf("${%s}", keys.skuName),
			Capacity: fmt.Sprintf("${%s}", keys.capacity),
		},
	}
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

func TestPersistAdoptedDeploymentConfigurationReservesManifestTuple(
	t *testing.T,
) {
	t.Parallel()

	manifestDeployment := project.Deployment{
		Name: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		Model: project.DeploymentModel{
			Name:    "${AZURE_AI_MODEL_NAME}",
			Format:  "${AZURE_AI_MODEL_FORMAT}",
			Version: "${AZURE_AI_MODEL_VERSION}",
		},
		Sku: project.DeploymentSku{
			Name:     "${AZURE_AI_MODEL_SKU_NAME}",
			Capacity: "${AZURE_AI_MODEL_SKU_CAPACITY}",
		},
	}
	existingDeployment := project.Deployment{
		Name:  "existing-chat",
		Model: project.DeploymentModel{Name: "gpt-4.1", Format: "OpenAI", Version: "1"},
		Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
	}
	values := map[string]string{}
	setEnv := func(_ context.Context, key, value string) error {
		values[key] = value
		return nil
	}

	kept, references, err := persistAdoptedDeploymentConfiguration(
		t.Context(),
		setEnv,
		[]foundryDeploymentEntry{{
			ServiceName:      "foundry",
			Deployment:       manifestDeployment,
			preserveManifest: true,
		}},
		[]foundryDeploymentReference{{
			ServiceName: "foundry",
			Deployment:  existingDeployment,
		}},
		nil,
	)

	require.NoError(t, err)
	require.Len(t, kept, 1)
	assert.Equal(t, manifestDeployment, kept[0].Deployment)
	require.Len(t, references, 1)
	assert.Equal(t, "foundry", references[0].ServiceName)
	assert.Equal(
		t,
		"${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}",
		references[0].Deployment.Name,
	)
	assert.Equal(
		t,
		"existing-chat",
		values["AZURE_AI_MODEL_DEPLOYMENT_NAME_2"],
	)
	assert.NotContains(t, values, "AZURE_AI_MODEL_DEPLOYMENT_NAME")
}
