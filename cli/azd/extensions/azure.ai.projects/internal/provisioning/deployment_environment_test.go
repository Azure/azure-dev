// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"azure.ai.projects/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
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

func TestReconcileDeploymentEnvironmentFallsBackFromPreferredCapacity(t *testing.T) {
	env := &resolveEnvStubEnvServer{envName: "test", get: map[string]string{}}
	prompt := &resolveEnvStubPromptServer{
		model: &azdext.AiModel{Name: "gpt-5-mini", Format: "OpenAI"},
		deployment: &azdext.AiModelDeployment{
			ModelName: "gpt-5-mini",
			Format:    "OpenAI",
			Version:   "2025-08-07",
			Sku:       &azdext.AiModelSku{Name: "GlobalStandard"},
			Capacity:  10,
		},
		deploymentErrs: []error{
			aiReasonError(t, codes.FailedPrecondition,
				azdext.AiErrorReasonNoValidSkus),
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

	require.Len(t, prompt.deployRequests, 2)
	assert.EqualValues(t, 50,
		prompt.deployRequests[0].GetOptions().GetCapacity())
	assert.Nil(t, prompt.deployRequests[1].GetOptions().Capacity)
	assert.Equal(t, "10", env.set["AZURE_AI_MODEL_SKU_CAPACITY"])
}

func TestReconcileDeploymentEnvironmentKeepsValidTuple(t *testing.T) {
	env := &resolveEnvStubEnvServer{
		envName: "test",
		get:     validDeploymentEnvironment(),
	}
	prompt := &resolveEnvStubPromptServer{}
	ai := &resolveEnvStubAiServer{
		deployments: []*azdext.AiModelDeployment{validDeployment()},
	}
	client := newResolveEnvTestClient(t, env, prompt, ai)
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

	require.Len(t, ai.requests, 1)
	assert.Equal(t, "gpt-5-mini", ai.requests[0].GetModelName())
	assert.Empty(t, prompt.modelRequests)
	assert.Empty(t, env.set)
}

func TestReconcileDeploymentEnvironmentRepromptsForStaleTuple(t *testing.T) {
	env := &resolveEnvStubEnvServer{
		envName: "test",
		get:     validDeploymentEnvironment(),
	}
	prompt := &resolveEnvStubPromptServer{
		modelErr: status.Error(codes.FailedPrecondition, "prompt required"),
	}
	ai := &resolveEnvStubAiServer{}
	client := newResolveEnvTestClient(t, env, prompt, ai)
	provider := &FoundryProvisioningProvider{
		azdClient:   client,
		projectPath: t.TempDir(),
		envName:     "test",
		subID:       "sub",
		location:    "eastus2",
	}

	err := provider.reconcileDeploymentEnvironment(
		t.Context(), []byte(deploymentReferenceYAML), "project",
	)

	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Contains(t, local.Message, "deployment 1")
	assert.Contains(t, local.Message, "are incompatible")
	assert.NotContains(t, local.Message, "are missing")
	assert.Empty(t, env.set)
}

func TestReconcileDeploymentEnvironmentTreatsNoMatchAsStale(t *testing.T) {
	for _, reason := range []string{
		azdext.AiErrorReasonModelNotFound,
		azdext.AiErrorReasonNoDeploymentMatch,
	} {
		t.Run(reason, func(t *testing.T) {
			env := &resolveEnvStubEnvServer{
				envName: "test",
				get:     validDeploymentEnvironment(),
			}
			prompt := &resolveEnvStubPromptServer{
				modelErr: status.Error(
					codes.FailedPrecondition, "prompt required"),
			}
			ai := &resolveEnvStubAiServer{
				err: aiReasonError(t, codes.NotFound, reason),
			}
			client := newResolveEnvTestClient(t, env, prompt, ai)
			provider := &FoundryProvisioningProvider{
				azdClient:   client,
				projectPath: t.TempDir(),
				envName:     "test",
				subID:       "sub",
				location:    "eastus2",
			}

			err := provider.reconcileDeploymentEnvironment(
				t.Context(), []byte(deploymentReferenceYAML), "project",
			)

			var local *azdext.LocalError
			require.ErrorAs(t, err, &local)
			assert.Contains(t, local.Message, "are incompatible")
			require.Len(t, prompt.modelRequests, 1)
		})
	}
}

func TestReconcileDeploymentEnvironmentPropagatesCatalogFailure(t *testing.T) {
	env := &resolveEnvStubEnvServer{
		envName: "test",
		get:     validDeploymentEnvironment(),
	}
	prompt := &resolveEnvStubPromptServer{}
	ai := &resolveEnvStubAiServer{
		err: status.Error(codes.Unavailable, "catalog unavailable"),
	}
	client := newResolveEnvTestClient(t, env, prompt, ai)
	provider := &FoundryProvisioningProvider{
		azdClient:   client,
		projectPath: t.TempDir(),
		envName:     "test",
		subID:       "sub",
		location:    "eastus2",
	}

	err := provider.reconcileDeploymentEnvironment(
		t.Context(), []byte(deploymentReferenceYAML), "project",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "validate model deployment 1")
	assert.Contains(t, err.Error(), "catalog unavailable")
	assert.Empty(t, prompt.modelRequests)
}

func TestReconcileDeploymentEnvironmentNoPromptErrorNamesMissingValues(t *testing.T) {
	env := &resolveEnvStubEnvServer{
		envName: "ci",
		get: map[string]string{
			"AZURE_AI_MODEL_NAME": "gpt-5-mini",
		},
	}
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
	assert.Contains(t, local.Message, "deployment 1")
	assert.Contains(t, local.Message, "are missing")
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

func TestReconcileDeploymentEnvironmentValidatesIndexedTuples(t *testing.T) {
	envValues := validDeploymentEnvironment()
	envValues["AZURE_AI_MODEL_DEPLOYMENT_NAME_2"] = "embed"
	envValues["AZURE_AI_MODEL_NAME_2"] = "text-embedding-3-small"
	envValues["AZURE_AI_MODEL_FORMAT_2"] = "OpenAI"
	envValues["AZURE_AI_MODEL_VERSION_2"] = "1"
	envValues["AZURE_AI_MODEL_SKU_NAME_2"] = "Standard"
	envValues["AZURE_AI_MODEL_SKU_CAPACITY_2"] = "10"
	env := &resolveEnvStubEnvServer{envName: "test", get: envValues}
	prompt := &resolveEnvStubPromptServer{}
	ai := &resolveEnvStubAiServer{
		deployments: []*azdext.AiModelDeployment{
			validDeployment(),
			{
				ModelName: "text-embedding-3-small",
				Format:    "OpenAI",
				Version:   "1",
				Sku:       &azdext.AiModelSku{Name: "Standard"},
				Capacity:  10,
			},
		},
	}
	client := newResolveEnvTestClient(t, env, prompt, ai)
	provider := &FoundryProvisioningProvider{
		azdClient:   client,
		projectPath: t.TempDir(),
		envName:     "test",
		subID:       "sub",
		location:    "eastus2",
	}
	raw := []byte(`services:
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
      - name: ${AZURE_AI_MODEL_DEPLOYMENT_NAME_2}
        model:
          name: ${AZURE_AI_MODEL_NAME_2}
          format: ${AZURE_AI_MODEL_FORMAT_2}
          version: ${AZURE_AI_MODEL_VERSION_2}
        sku:
          name: ${AZURE_AI_MODEL_SKU_NAME_2}
          capacity: ${AZURE_AI_MODEL_SKU_CAPACITY_2}
`)

	require.NoError(t, provider.reconcileDeploymentEnvironment(
		t.Context(), raw, "project",
	))

	require.Len(t, ai.requests, 2)
	assert.Equal(t, "gpt-5-mini", ai.requests[0].GetModelName())
	assert.Equal(t, "text-embedding-3-small",
		ai.requests[1].GetModelName())
	assert.Empty(t, prompt.modelRequests)
}

func validDeploymentEnvironment() map[string]string {
	return map[string]string{
		"AZURE_AI_MODEL_DEPLOYMENT_NAME": "chat",
		"AZURE_AI_MODEL_NAME":            "gpt-5-mini",
		"AZURE_AI_MODEL_FORMAT":          "OpenAI",
		"AZURE_AI_MODEL_VERSION":         "2025-08-07",
		"AZURE_AI_MODEL_SKU_NAME":        "GlobalStandard",
		"AZURE_AI_MODEL_SKU_CAPACITY":    "50",
	}
}

func validDeployment() *azdext.AiModelDeployment {
	return &azdext.AiModelDeployment{
		ModelName: "gpt-5-mini",
		Format:    "OpenAI",
		Version:   "2025-08-07",
		Sku:       &azdext.AiModelSku{Name: "GlobalStandard"},
		Capacity:  50,
	}
}

func aiReasonError(t *testing.T, code codes.Code, reason string) error {
	t.Helper()
	st, err := status.New(code, reason).WithDetails(&errdetails.ErrorInfo{
		Domain: azdext.AiErrorDomain,
		Reason: reason,
	})
	require.NoError(t, err)
	return st.Err()
}
