// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/foundrypolicy"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPolicyProjectID = "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/" +
	"resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"

type fakePolicyTokenCredential struct{}

func (fakePolicyTokenCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token"}, nil
}

func TestExtractServicePolicyCandidate(t *testing.T) {
	t.Parallel()

	t.Run("prompt agent with model, tools, and memory", func(t *testing.T) {
		t.Parallel()
		svc := inlineAgentService(t, map[string]any{
			"kind":         "prompt",
			"name":         "my-prompt",
			"model":        "gpt-4.1",
			"instructions": "Help users.",
			"tools": []any{
				map[string]any{"type": "web_search"},
				map[string]any{"type": "mcp", "name": "github", "project_connection_id": "gh-conn"},
			},
			"memory": map[string]any{
				"chatModel":      "gpt-4.1",
				"embeddingModel": "text-embedding-3-small",
			},
		})
		cand := ExtractServicePolicyCandidate(svc, t.TempDir(), map[string]string{
			"AZURE_AI_PROJECT_ID": testPolicyProjectID,
		})
		assert.Equal(t, testPolicyProjectID, cand.ProjectResourceID)
		assert.Equal(t, "prompt", cand.AgentKind)
		require.Len(t, cand.Models, 1)
		assert.Equal(t, "gpt-4.1", cand.Models[0].Name)
		require.Len(t, cand.Tools, 3)
		assert.Equal(t, "web_search", cand.Tools[0].Type)
		assert.Equal(t, "mcp", cand.Tools[1].Type)
		assert.Equal(t, "gh-conn", cand.Tools[1].ConnectionID)
		assert.Equal(t, "memory_search", cand.Tools[2].Type)
	})

	t.Run("hosted container agent with Dockerfile uses registry candidate", func(t *testing.T) {
		t.Parallel()
		svc := inlineAgentService(t, map[string]any{
			"kind": "hosted",
			"name": "my-hosted",
		})
		cand := ExtractServicePolicyCandidate(svc, t.TempDir(), map[string]string{
			"AZURE_AI_PROJECT_ID":               testPolicyProjectID,
			"AZURE_CONTAINER_REGISTRY_ENDPOINT": "myacr.azurecr.io",
		})
		assert.Equal(t, "hosted", cand.AgentKind)
		assert.Empty(t, cand.ContainerImage)
		assert.Equal(t, "myacr.azurecr.io", cand.ContainerRegistry)
	})

	t.Run("hosted container agent with imagePassthrough uses full image candidate", func(t *testing.T) {
		t.Parallel()
		svc := inlineAgentService(t, map[string]any{
			"kind": "hosted",
			"name": "my-hosted",
		})
		svc.Image = "myacr.azurecr.io/team/agent:v1"
		svc.Docker = &azdext.DockerProjectOptions{ImagePassthrough: true}

		cand := ExtractServicePolicyCandidate(svc, t.TempDir(), map[string]string{
			"AZURE_AI_PROJECT_ID":               testPolicyProjectID,
			"AZURE_CONTAINER_REGISTRY_ENDPOINT": "myacr.azurecr.io",
		})
		assert.Equal(t, "hosted", cand.AgentKind)
		assert.Equal(t, "myacr.azurecr.io/team/agent:v1", cand.ContainerImage)
		assert.Empty(t, cand.ContainerRegistry)
	})

	t.Run("hosted code agent skips container image and registry candidates", func(t *testing.T) {
		t.Parallel()
		svc := inlineAgentService(t, map[string]any{
			"kind": "hosted",
			"name": "my-code-agent",
			"codeConfiguration": map[string]any{
				"runtime":    "python_3_13",
				"entryPoint": "app.py",
			},
		})
		cand := ExtractServicePolicyCandidate(svc, t.TempDir(), map[string]string{
			"AZURE_AI_PROJECT_ID":               testPolicyProjectID,
			"AZURE_CONTAINER_REGISTRY_ENDPOINT": "myacr.azurecr.io",
		})
		assert.Equal(t, "hosted", cand.AgentKind)
		assert.Empty(t, cand.ContainerImage)
		assert.Empty(t, cand.ContainerRegistry)
	})
}

func TestCheckHostedContainerImagePolicyAndViolationsError(t *testing.T) {
	t.Parallel()

	t.Run("blocks disallowed container image and returns structured validation error", func(t *testing.T) {
		t.Parallel()
		ops := policyPreflightOperations{
			loadInventory: func(_ context.Context, _ azcore.TokenCredential, projectID string) (foundrypolicy.Inventory, error) {
				return foundrypolicy.Inventory{
					ProjectID: projectID,
					Assignments: []foundrypolicy.PolicyAssignment{
						{
							AssignmentID:          "/subscriptions/sub/providers/microsoft.authorization/policyassignments/deny-img",
							AssignmentDisplayName: "Block untrusted registries",
							PolicyDefinitionID:    foundrypolicy.DeniedContainerImagesPolicyID,
							AssignmentParameters: map[string]any{
								"effect":                map[string]any{"value": "Deny"},
								"deniedContainerImages": map[string]any{"value": []any{"untrusted.example.com/*"}},
							},
							EnforcementMode: "Default",
						},
					},
				}, nil
			},
		}

		err := checkHostedContainerImagePolicy(
			t.Context(),
			testPolicyProjectID,
			fakePolicyTokenCredential{},
			"my-hosted",
			"untrusted.example.com/team/agent:v1",
			ops,
		)
		require.Error(t, err)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		assert.Equal(t, exterrors.CodeContainerImageBlockedByPolicy, localErr.Code)
		assert.Contains(t, localErr.Message, "Block untrusted registries")
		assert.Contains(t, localErr.Suggestion, "https://ai.azure.com/nextgen/r/")
	})

	t.Run("fails open when policy inventory query fails", func(t *testing.T) {
		t.Parallel()
		ops := policyPreflightOperations{
			loadInventory: func(_ context.Context, _ azcore.TokenCredential, _ string) (foundrypolicy.Inventory, error) {
				return foundrypolicy.Inventory{}, errors.New("Resource Graph unavailable")
			},
		}

		err := checkHostedContainerImagePolicy(
			t.Context(),
			testPolicyProjectID,
			fakePolicyTokenCredential{},
			"my-hosted",
			"untrusted.example.com/team/agent:v1",
			ops,
		)
		require.NoError(t, err)
	})

	t.Run("selects specific and combined error codes across target kinds", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, exterrors.CodeAgentKindBlockedByPolicy, selectPolicyErrorCode([]foundrypolicy.Violation{
			{Target: foundrypolicy.TargetAgentKind},
		}))
		assert.Equal(t, exterrors.CodeModelBlockedByPolicy, selectPolicyErrorCode([]foundrypolicy.Violation{
			{Target: foundrypolicy.TargetModel},
		}))
		assert.Equal(t, exterrors.CodeToolBlockedByPolicy, selectPolicyErrorCode([]foundrypolicy.Violation{
			{Target: foundrypolicy.TargetTool},
		}))
		assert.Equal(t, exterrors.CodeAgentPolicyBlocked, selectPolicyErrorCode([]foundrypolicy.Violation{
			{Target: foundrypolicy.TargetAgentKind},
			{Target: foundrypolicy.TargetModel},
		}))
	})
}
