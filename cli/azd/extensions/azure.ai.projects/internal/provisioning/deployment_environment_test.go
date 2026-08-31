// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"azure.ai.projects/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const deploymentReferenceYAML = `services:
  project:
    host: azure.ai.project
    deployments:
      - name: ${AZURE_AI_MODEL_DEPLOYMENT_NAME}
        model:
          name: ${AZURE_AI_MODEL_NAME}
          format: ${AZURE_AI_MODEL_FORMAT}
          version: ${AZURE_AI_MODEL_VERSION}
        sku:
          name: ${AZURE_AI_MODEL_SKU_NAME}
          capacity: ${AZURE_AI_MODEL_SKU_CAPACITY}
`

func TestReconcileDeploymentEnvironmentPromptsWithHostedAgentFilters(t *testing.T) {
	env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{}}
	prompt := &resolveEnvStubPromptServer{
		model: &azdext.AiModel{Name: "gpt-5-mini", Format: "OpenAI"},
		deployment: &azdext.AiModelDeployment{
			ModelName: "gpt-5-mini",
			Format:    "OpenAI",
			Version:   "2025-08-07",
			Sku:       &azdext.AiModelSku{Name: "GlobalStandard"},
			Capacity:  50,
		},
	}
	client := newResolveEnvTestClient(t, env, prompt)
	provider := &FoundryProvisioningProvider{
		azdClient:   client,
		projectPath: t.TempDir(),
		envName:     "test",
		subID:       "sub",
		location:    "eastus2",
	}

	require.NoError(t, provider.reconcileDeploymentEnvironment(
		t.Context(), []byte(deploymentReferenceYAML), "project",
	))

	assert.Equal(t, "gpt-5-mini", env.set["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
	assert.Equal(t, "gpt-5-mini", env.set["AZURE_AI_MODEL_NAME"])
	assert.Equal(t, "OpenAI", env.set["AZURE_AI_MODEL_FORMAT"])
	assert.Equal(t, "2025-08-07", env.set["AZURE_AI_MODEL_VERSION"])
	assert.Equal(t, "GlobalStandard", env.set["AZURE_AI_MODEL_SKU_NAME"])
	assert.Equal(t, "50", env.set["AZURE_AI_MODEL_SKU_CAPACITY"])
	require.Len(t, prompt.modelRequests, 1)
	assert.Equal(t, []string{"eastus2"}, prompt.modelRequests[0].GetFilter().GetLocations())
	assert.Equal(t, []string{agentsV2ModelCapability}, prompt.modelRequests[0].GetFilter().GetCapabilities())
	assert.EqualValues(t, 1, prompt.modelRequests[0].GetQuota().GetMinRemainingCapacity())
	require.Len(t, prompt.deployRequests, 1)
	assert.Equal(t, []string{"eastus2"}, prompt.deployRequests[0].GetOptions().GetLocations())
	assert.EqualValues(t, 1, prompt.deployRequests[0].GetQuota().GetMinRemainingCapacity())
}

func TestReconcileDeploymentEnvironmentNoPromptErrorNamesMissingValues(t *testing.T) {
	env := &resolveEnvStubEnvServer{envName: "ci", get: map[string]string{}}
	prompt := &resolveEnvStubPromptServer{
		modelErr: status.Error(codes.FailedPrecondition, "prompt required"),
	}
	client := newResolveEnvTestClient(t, env, prompt)
	provider := &FoundryProvisioningProvider{
		azdClient:   client,
		projectPath: t.TempDir(),
		envName:     "ci",
		subID:       "sub",
		location:    "eastus2",
	}

	err := provider.reconcileDeploymentEnvironment(
		t.Context(), []byte(deploymentReferenceYAML), "project",
	)
	require.Error(t, err)
	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Equal(t, exterrors.CodeMissingModelDeployment, local.Code)
	assert.Contains(t, local.Message, "AZURE_AI_MODEL_DEPLOYMENT_NAME")
	assert.Contains(t, local.Message, "AZURE_AI_MODEL_SKU_CAPACITY")
	assert.Empty(t, env.set)
}

func TestReconcileDeploymentEnvironmentLeavesStaticManifestUnchanged(t *testing.T) {
	env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{}}
	prompt := &resolveEnvStubPromptServer{}
	client := newResolveEnvTestClient(t, env, prompt)
	provider := &FoundryProvisioningProvider{
		azdClient:   client,
		projectPath: t.TempDir(),
		envName:     "test",
	}
	raw := []byte(`services:
  project:
    host: azure.ai.project
    deployments:
      - name: gpt
        model: {name: gpt, format: OpenAI, version: "1"}
        sku: {name: Standard, capacity: 10}
`)

	require.NoError(t, provider.reconcileDeploymentEnvironment(t.Context(), raw, "project"))
	assert.Empty(t, prompt.modelRequests)
	assert.Empty(t, env.set)
}
