// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"azure.ai.projects/internal/provisioning"
	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandDeployment(t *testing.T) {
	t.Parallel()

	got, err := expandDeployment(synthesis.Deployment{
		Name: "${DEPLOYMENT_NAME}",
		Model: synthesis.DeploymentModel{
			Name:    "${MODEL_NAME}",
			Format:  "${MODEL_FORMAT}",
			Version: "${MODEL_VERSION}",
		},
		Sku: synthesis.DeploymentSku{Name: "${SKU_NAME}"},
	}, map[string]string{
		"DEPLOYMENT_NAME": "chat",
		"MODEL_NAME":      "gpt-4.1",
		"MODEL_FORMAT":    "OpenAI",
		"MODEL_VERSION":   "2025-04-14",
		"SKU_NAME":        "GlobalStandard",
	})

	require.NoError(t, err)
	require.Equal(t, synthesis.Deployment{
		Name: "chat",
		Model: synthesis.DeploymentModel{
			Name:    "gpt-4.1",
			Format:  "OpenAI",
			Version: "2025-04-14",
		},
		Sku: synthesis.DeploymentSku{Name: "GlobalStandard"},
	}, got)
}

func TestMatchingLiveDeploymentsExpandsDeploymentValues(t *testing.T) {
	t.Parallel()

	expanded, matches, err := matchingLiveDeployments(
		synthesis.Deployment{
			Name:  "${DEPLOYMENT_NAME}",
			Model: synthesis.DeploymentModel{Name: "${MODEL_NAME}"},
		},
		[]liveProjectDeployment{
			{
				Name:  "chat",
				Model: synthesis.DeploymentModel{Name: "gpt-4.1"},
			},
		},
		map[string]string{
			"DEPLOYMENT_NAME": "chat",
			"MODEL_NAME":      "gpt-4.1",
		},
	)

	require.NoError(t, err)
	require.Equal(t, "chat", expanded.Name)
	require.Equal(t, "gpt-4.1", expanded.Model.Name)
	require.Len(t, matches, 1)
	require.Equal(t, "chat", matches[0].Name)
}

func TestSelectReconciledDefaultPreservesLiveDefault(t *testing.T) {
	t.Parallel()

	got := selectReconciledDefault(
		"chat",
		[]liveProjectDeployment{
			{Name: "chat"},
		},
		[]synthesis.Deployment{
			{Name: "embedding"},
		},
	)

	assert.Equal(t, "chat", got)
}

func TestSelectReconciledDefaultFallsBackToReferencedDeployment(t *testing.T) {
	t.Parallel()

	got := selectReconciledDefault(
		"removed",
		[]liveProjectDeployment{
			{Name: "chat"},
		},
		[]synthesis.Deployment{
			{Name: "embedding"},
		},
	)

	assert.Equal(t, "embedding", got)
}

func TestProjectAddMutationIncludesDeploymentReconciliation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		mutation          string
		reconciledChanged bool
		want              string
	}{
		{
			name:              "deployment update changes unchanged project",
			mutation:          "unchanged",
			reconciledChanged: true,
			want:              "updated",
		},
		{
			name:              "unchanged deployment preserves unchanged project",
			mutation:          "unchanged",
			reconciledChanged: false,
			want:              "unchanged",
		},
		{
			name:              "created project remains created",
			mutation:          "created",
			reconciledChanged: true,
			want:              "created",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, projectAddMutation(
				tt.mutation, tt.reconciledChanged,
			))
		})
	}
}

func TestReconcileAdoptedDeploymentsEmitsPolicyWarningWhenModelDenied(t *testing.T) {
	const (
		projectID = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-foundry/" +
			"providers/Microsoft.CognitiveServices/accounts/my-account/projects/my-project"
		endpoint = "https://my-account.services.ai.azure.com/api/projects/my-project"
	)

	declared := synthesis.Deployment{
		Name:  "chat",
		Model: synthesis.DeploymentModel{Format: "OpenAI", Name: "gpt-4o", Version: "2024-08-06"},
		Sku:   synthesis.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
	}

	root := t.TempDir()
	client, projectServer, envServer, _, _ := newSelfInitializingDeploymentClient(t, root)
	projectServer.services["project"] = map[string]any{
		"host":        aiProjectHost,
		"endpoint":    endpoint,
		"deployments": []any{deploymentMap(declared)},
	}

	var warnBuf bytes.Buffer
	var evaluated bool
	prevEval := evaluateReconciledModelPolicies
	prevList := listLiveProjectDeployments
	listLiveProjectDeployments = func(
		_ context.Context,
		_ *resolvedProject,
		_ azcore.TokenCredential,
	) ([]liveProjectDeployment, error) {
		return []liveProjectDeployment{
			{
				Name:  "existing-chat",
				Model: declared.Model,
				Sku:   declared.Sku,
			},
		}, nil
	}
	evaluateReconciledModelPolicies = func(
		_ context.Context,
		_ azcore.TokenCredential,
		gotProjectID string,
		deployments []synthesis.Deployment,
	) ([]provisioning.ModelPolicyViolation, error) {
		evaluated = true
		assert.Equal(t, projectID, gotProjectID)
		require.NotEmpty(t, deployments)
		return []provisioning.ModelPolicyViolation{
			{
				DeploymentName: "existing-chat",
				ModelFormat:    "OpenAI",
				ModelName:      "gpt-4o",
				ModelVersion:   "2024-08-06",
				DisplayName:    "approved-models",
				PortalURL:      "https://ai.azure.com/?flight=govern_ignite",
			},
		}, errors.New("batch 2 failed")
	}
	t.Cleanup(func() {
		evaluateReconciledModelPolicies = prevEval
		listLiveProjectDeployments = prevList
	})

	target := &resolvedProject{
		Mode:              projectModeExistingID,
		ResourceId:        projectID,
		SubscriptionId:    "00000000-0000-0000-0000-000000000001",
		UserTenantId:      "tenant",
		ResourceGroupName: "rg-foundry",
		AccountName:       "my-account",
		ProjectName:       "my-project",
		Endpoint:          endpoint,
		Location:          "eastus",
	}

	defaultName, changed, _, err := reconcileAdoptedDeploymentsWithWriter(
		t.Context(),
		client,
		root,
		"test",
		target,
		"project",
		true,
		policyWarningWriterForOutput(&warnBuf, &projectAddFlags{output: "default"}),
	)
	require.NoError(t, err)
	assert.True(t, evaluated)
	assert.True(t, changed)
	assert.Equal(t, "existing-chat", defaultName)
	assert.Equal(t, "existing-chat", envServer.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])

	warningOutput := warnBuf.String()
	assert.Contains(
		t,
		warningOutput,
		"Warning: One or more model deployments do not comply with Azure Policy in the selected project:",
	)
	assert.Contains(
		t,
		warningOutput,
		`The "approved-models" policy doesn't allow model "gpt-4o (version 2024-08-06)" for deployment "existing-chat" in this project.`,
	)
	assert.Contains(t, warningOutput, "View policy: https://ai.azure.com/?flight=govern_ignite")

	for _, outputFormat := range []string{"json", "none"} {
		t.Run("suppresses warning when output is "+outputFormat, func(t *testing.T) {
			delete(envServer.values, projectDeploymentsEnvKey)
			evaluated = false

			var suppressedBuf bytes.Buffer
			writer := policyWarningWriterForOutput(&suppressedBuf, &projectAddFlags{output: outputFormat})
			assert.Nil(t, writer)

			gotDefault, gotChanged, _, err := reconcileAdoptedDeploymentsWithWriter(
				t.Context(),
				client,
				root,
				"test",
				target,
				"project",
				true,
				writer,
			)
			require.NoError(t, err)
			assert.True(t, evaluated)
			assert.True(t, gotChanged)
			assert.Equal(t, "existing-chat", gotDefault)
			assert.Empty(t, suppressedBuf.String())
		})
	}
}
