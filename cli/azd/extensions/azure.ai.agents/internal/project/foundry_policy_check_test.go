// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"testing"
	"time"

	"azure.ai.projects/synthesis"
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/foundrypolicy"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
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
				map[string]any{
					"type":                  "mcp",
					"name":                  "github",
					"project_connection_id": "gh-conn",
					"server_url":            "https://api.githubcopilot.com/mcp",
					"connector_id":          "github-connector",
				},
			},
			"memory": map[string]any{
				"store":          "conv-mem",
				"chatModel":      "gpt-4.1",
				"embeddingModel": "text-embedding-3-small",
			},
		})
		cand := ExtractServicePolicyCandidate(svc, t.TempDir(), map[string]string{
			"AZURE_AI_PROJECT_ID": testPolicyProjectID,
		})
		assert.Equal(t, testPolicyProjectID, cand.ProjectResourceID)
		assert.Equal(t, "prompt", cand.AgentKind)
		require.Len(t, cand.Models, 2)
		assert.Equal(t, "gpt-4.1", cand.Models[0].DeploymentName)
		assert.Empty(t, cand.Models[0].Name)
		assert.Equal(t, "text-embedding-3-small", cand.Models[1].DeploymentName)
		assert.Empty(t, cand.Models[1].Name)
		require.Len(t, cand.Tools, 3)
		assert.Equal(t, "web_search", cand.Tools[0].Type)
		assert.Equal(t, "mcp", cand.Tools[1].Type)
		assert.Equal(t, "gh-conn", cand.Tools[1].ConnectionID)
		assert.Equal(t, "https://api.githubcopilot.com/mcp", cand.Tools[1].ServerURL)
		assert.Equal(t, "github-connector", cand.Tools[1].ConnectorID)
		assert.Equal(t, "memory_search_preview", cand.Tools[2].Type)
	})

	t.Run("resolves ${VAR} model placeholders, AI_PROJECT_DEPLOYMENTS metadata, and nested tool connections", func(t *testing.T) {
		t.Parallel()
		svc := inlineAgentService(t, map[string]any{
			"kind":         "prompt",
			"name":         "my-prompt",
			"model":        "${CHAT_DEPLOYMENT}",
			"instructions": "Help users.",
			"tools": []any{
				map[string]any{
					"type": "azure_ai_search",
					"azure_ai_search": map[string]any{
						"indexes": []any{
							map[string]any{"project_connection_id": "${SEARCH_CONN}"},
						},
					},
				},
				map[string]any{
					"type": "bing_grounding",
					"bing_grounding": map[string]any{
						"search_configurations": []any{
							map[string]any{"project_connection_id": "${BING_CONN}"},
						},
					},
				},
				map[string]any{"type": "memory_search_preview"},
			},
			"memory": map[string]any{
				"store":          "conv-mem",
				"chatModel":      "${CHAT_DEPLOYMENT}",
				"embeddingModel": "${EMBED_DEPLOYMENT}",
			},
		})
		encodedDeps, err := EncodeProjectDeploymentsEnv([]Deployment{
			{
				Name: "production-chat",
				Model: DeploymentModel{
					Name:    "gpt-4o",
					Format:  "OpenAI",
					Version: "2024-08-06",
				},
			},
		})
		require.NoError(t, err)

		cand := ExtractServicePolicyCandidate(svc, t.TempDir(), map[string]string{
			"AZURE_AI_PROJECT_ID":    testPolicyProjectID,
			"CHAT_DEPLOYMENT":        "production-chat",
			"EMBED_DEPLOYMENT":       "prod-embed",
			"SEARCH_CONN":            "my-search-conn",
			"BING_CONN":              "my-bing-conn",
			"AI_PROJECT_DEPLOYMENTS": encodedDeps,
		})
		require.Len(t, cand.Models, 2)
		assert.Equal(t, "production-chat", cand.Models[0].DeploymentName)
		assert.Equal(t, "gpt-4o", cand.Models[0].Name)
		assert.Equal(t, "OpenAI", cand.Models[0].Format)
		assert.Equal(t, "2024-08-06", cand.Models[0].Version)
		assert.Equal(t, "prod-embed", cand.Models[1].DeploymentName)
		assert.Empty(t, cand.Models[1].Name)

		// memory_search_preview is deduplicated when already declared in tools
		require.Len(t, cand.Tools, 3)
		assert.Equal(t, "azure_ai_search", cand.Tools[0].Type)
		assert.Equal(t, "my-search-conn", cand.Tools[0].ConnectionID)
		assert.Equal(t, "bing_grounding", cand.Tools[1].Type)
		assert.Equal(t, "my-bing-conn", cand.Tools[1].ConnectionID)
		assert.Equal(t, "memory_search_preview", cand.Tools[2].Type)
	})

	t.Run("resolves sibling azure.ai.project deployment bindings and service-level tool env overrides", func(t *testing.T) {
		t.Parallel()
		svc := inlineAgentService(t, map[string]any{
			"kind":         "prompt",
			"name":         "my-prompt",
			"model":        "template-chat",
			"instructions": "Help users.",
			"tools": []any{
				map[string]any{
					"type": "azure_ai_search",
					"azure_ai_search": map[string]any{
						"indexes": []any{
							map[string]any{"project_connection_id": "${SEARCH_CONN}"},
						},
					},
				},
			},
		})
		svc.Uses = []string{"foundry-proj"}
		svc.Environment = map[string]string{
			"SEARCH_CONN": "service-override-conn",
		}

		projProps, err := structpb.NewStruct(map[string]any{
			"deployments": []any{
				map[string]any{
					"name": "template-chat",
					"model": map[string]any{
						"name":    "gpt-4.1",
						"format":  "OpenAI",
						"version": "2025-04-14",
					},
				},
			},
		})
		require.NoError(t, err)
		projSvc := &azdext.ServiceConfig{
			Name:                 "foundry-proj",
			Host:                 FoundryProjectHost,
			AdditionalProperties: projProps,
		}
		services := map[string]*azdext.ServiceConfig{
			svc.GetName():  svc,
			"foundry-proj": projSvc,
		}

		bindingsVal, err := synthesis.EncodeDeploymentBindings(map[string]string{
			"template-chat": "bound-prod-chat",
		})
		require.NoError(t, err)

		cand := ExtractServicePolicyCandidateWithServices(svc, services, t.TempDir(), map[string]string{
			"AZURE_AI_PROJECT_ID":              testPolicyProjectID,
			synthesis.DeploymentBindingsEnvKey: bindingsVal,
			"SEARCH_CONN":                      "project-level-conn",
		})
		require.Len(t, cand.Models, 1)
		assert.Equal(t, "bound-prod-chat", cand.Models[0].DeploymentName)
		require.Len(t, cand.Tools, 1)
		assert.Equal(t, "azure_ai_search", cand.Tools[0].Type)
		assert.Equal(t, "service-override-conn", cand.Tools[0].ConnectionID)
	})

	t.Run("enriches toolbox and connection-backed MCP tools with ServerURL when available", func(t *testing.T) {
		t.Parallel()
		svc := inlineAgentService(t, map[string]any{
			"kind":         "prompt",
			"name":         "my-prompt",
			"model":        "gpt-4.1",
			"instructions": "Help users.",
			"tools": []any{
				map[string]any{
					"type":                  "mcp",
					"name":                  "conn-mcp",
					"project_connection_id": "my-mcp-conn",
				},
			},
			"toolboxes": []any{
				map[string]any{"name": "web-search-tools"},
			},
			"toolConnections": []any{
				map[string]any{
					"name":        "my-mcp-conn",
					"category":    "CustomKeys",
					"target":      "${MCP_CONN_TARGET}",
					"authType":    "CustomKeys",
					"isShared":    true,
					"credentials": map[string]any{"keys": map[string]any{"k": "v"}},
				},
			},
		})

		cand := ExtractServicePolicyCandidate(svc, t.TempDir(), map[string]string{
			"AZURE_AI_PROJECT_ID":                   testPolicyProjectID,
			"TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT": "https://toolbox.example.com/mcp",
			"MCP_CONN_TARGET":                       "https://conn-target.example.com/mcp",
		})
		require.Len(t, cand.Tools, 2)
		assert.Equal(t, "mcp", cand.Tools[0].Type)
		assert.Equal(t, "conn-mcp", cand.Tools[0].Name)
		assert.Equal(t, "https://conn-target.example.com/mcp", cand.Tools[0].ServerURL)
		assert.Equal(t, "mcp", cand.Tools[1].Type)
		assert.Equal(t, "toolbox:web-search-tools", cand.Tools[1].Name)
		assert.Equal(t, "https://toolbox.example.com/mcp", cand.Tools[1].ServerURL)
	})

	t.Run("hosted container agent with Dockerfile uses registry candidate or docker.registry fallback", func(t *testing.T) {
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

		// Fallback to docker.registry when AZURE_CONTAINER_REGISTRY_ENDPOINT is unset
		svc.Docker = &azdext.DockerProjectOptions{Registry: "${CUSTOM_ACR}"}
		candFallback := ExtractServicePolicyCandidate(svc, t.TempDir(), map[string]string{
			"AZURE_AI_PROJECT_ID": testPolicyProjectID,
			"CUSTOM_ACR":          "fallbackacr.azurecr.io",
		})
		assert.Equal(t, "fallbackacr.azurecr.io", candFallback.ContainerRegistry)

		// When image != "" without imagePassthrough or AZD_AGENT_SKIP_ACR=true, neither
		// ContainerImage nor ContainerRegistry is pre-populated before interactive selection.
		svcAmbiguous := inlineAgentService(t, map[string]any{
			"kind":  "hosted",
			"name":  "my-hosted",
			"image": "prebuilt.example.com/team/agent:v1",
		})
		candAmbiguous := ExtractServicePolicyCandidate(svcAmbiguous, t.TempDir(), map[string]string{
			"AZURE_AI_PROJECT_ID":               testPolicyProjectID,
			"AZURE_CONTAINER_REGISTRY_ENDPOINT": "myacr.azurecr.io",
		})
		assert.Empty(t, candAmbiguous.ContainerImage)
		assert.Empty(t, candAmbiguous.ContainerRegistry)
		assert.Empty(t, candAmbiguous.Models)
	})

	t.Run("hosted agent excludes synthetic memory_search_preview and toolbox MCP wrapper tools and ignores unrelated AI_PROJECT_DEPLOYMENTS", func(t *testing.T) {
		t.Parallel()
		encodedDeps, err := EncodeProjectDeploymentsEnv([]Deployment{
			{
				Name: "unrelated-project-dep",
				Model: DeploymentModel{
					Name:    "gpt-4o",
					Format:  "OpenAI",
					Version: "2024-08-06",
				},
			},
			{
				Name: "chat",
				Model: DeploymentModel{
					Name:    "gpt-4.1",
					Format:  "OpenAI",
					Version: "2025-04-14",
				},
			},
		})
		require.NoError(t, err)

		// Hosted agent with no models does not inherit unrelated AI_PROJECT_DEPLOYMENTS
		svcNoModels := inlineAgentService(t, map[string]any{
			"kind": "hosted",
			"name": "hosted-no-models",
		})
		candNoModels := ExtractServicePolicyCandidate(svcNoModels, t.TempDir(), map[string]string{
			"AZURE_AI_PROJECT_ID":    testPolicyProjectID,
			"AI_PROJECT_DEPLOYMENTS": encodedDeps,
		})
		assert.Empty(t, candNoModels.Models)

		// Hosted agent with memoryStores, toolboxes, and static deployments:
		// - AI_PROJECT_DEPLOYMENTS takes precedence over static targetCfg.Deployments
		// - memoryStore models and explicit toolbox tools are extracted, but synthetic
		//   memory_search_preview and toolbox:<name> MCP wrapper tools are NOT added.
		svcHosted := inlineAgentService(t, map[string]any{
			"kind": "hosted",
			"name": "hosted-with-stores",
			"deployments": []any{
				map[string]any{
					"name": "chat",
					"model": map[string]any{
						"name":    "gpt-35-turbo",
						"format":  "OpenAI",
						"version": "0613",
					},
				},
			},
			"memoryStores": []any{
				map[string]any{
					"name":           "my-store",
					"chatModel":      "chat",
					"embeddingModel": "text-embedding-3-small",
				},
			},
			"toolboxes": []any{
				map[string]any{
					"name": "my-tb",
				},
			},
		})
		candHosted := ExtractServicePolicyCandidate(svcHosted, t.TempDir(), map[string]string{
			"AZURE_AI_PROJECT_ID":    testPolicyProjectID,
			"AI_PROJECT_DEPLOYMENTS": encodedDeps,
		})
		require.Len(t, candHosted.Models, 2)
		assert.Equal(t, "chat", candHosted.Models[0].DeploymentName)
		assert.Equal(t, "gpt-4.1", candHosted.Models[0].Name)
		assert.Equal(t, "2025-04-14", candHosted.Models[0].Version)
		assert.Equal(t, "text-embedding-3-small", candHosted.Models[1].DeploymentName)
		assert.Empty(t, candHosted.Tools)
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
			nil,
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
			nil,
			ops,
		)
		require.NoError(t, err)
	})

	t.Run("caches deterministic candidate evaluation per inventory snapshot and invalidates on inventory refresh", func(t *testing.T) {
		t.Parallel()
		invCalls := 0
		modelCalls := 0
		currentInv := foundrypolicy.Inventory{
			ProjectID: testPolicyProjectID,
			Assignments: []foundrypolicy.PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/deny-prompt",
					AssignmentDisplayName: "Deny Prompt Agents",
					PolicyDefinitionID:    foundrypolicy.DeniedAgentKindsPolicyID,
					AssignmentParameters: map[string]any{
						"effect":           map[string]any{"value": "Deny"},
						"deniedAgentKinds": map[string]any{"value": []any{"prompt"}},
					},
					EnforcementMode: "Default",
				},
			},
		}

		ops := policyPreflightOperations{
			evalCache: newCandidateEvaluationCache(0, nil),
			loadInventory: func(_ context.Context, _ azcore.TokenCredential, _ string) (foundrypolicy.Inventory, error) {
				invCalls++
				return currentInv, nil
			},
			evaluateModel: func(
				_ context.Context,
				_ azcore.TokenCredential,
				_ string,
				models []foundrypolicy.ModelCandidate,
			) (foundrypolicy.ModelPolicyEvaluation, error) {
				modelCalls++
				return foundrypolicy.ModelPolicyEvaluation{
					ResolvedModels: models,
				}, nil
			},
		}

		cand := foundrypolicy.Candidate{
			ProjectResourceID: testPolicyProjectID,
			ServiceName:       "dedup-cache-svc",
			AgentKind:         "prompt",
			Models: []foundrypolicy.ModelCandidate{{
				DeploymentName: "my-gpt4o",
				Name:           "gpt-4o",
				Format:         "OpenAI",
				Version:        "2024-08-06",
			}},
		}

		// First evaluation: blocked by deny-prompt assignment; evaluates ARM model once.
		res1, err1 := EvaluateCandidateWithCredential(t.Context(), fakePolicyTokenCredential{}, cand, ops)
		require.NoError(t, err1)
		assert.Equal(t, foundrypolicy.OutcomeBlocked, res1.Outcome)
		assert.Equal(t, 1, invCalls)
		assert.Equal(t, 1, modelCalls)

		// Second evaluation with the same inventory snapshot (predeploy after prepackage):
		// hits evalCache and skips duplicate ARM model evaluation.
		res2, err2 := EvaluateCandidateWithCredential(t.Context(), fakePolicyTokenCredential{}, cand, ops)
		require.NoError(t, err2)
		assert.Equal(t, foundrypolicy.OutcomeBlocked, res2.Outcome)
		assert.Equal(t, 2, invCalls)
		assert.Equal(t, 1, modelCalls)

		// When inventory refreshes and the Deny policy is removed, evalCache does NOT serve
		// the obsolete blocked verdict; it re-evaluates against the refreshed inventory.
		currentInv = foundrypolicy.Inventory{ProjectID: testPolicyProjectID}
		res3, err3 := EvaluateCandidateWithCredential(t.Context(), fakePolicyTokenCredential{}, cand, ops)
		require.NoError(t, err3)
		assert.Equal(t, foundrypolicy.OutcomeAllowed, res3.Outcome)
		assert.Empty(t, res3.Violations)
		assert.Equal(t, 3, invCalls)
		assert.Equal(t, 2, modelCalls)

		// 1. Toggling IsExempt from false -> true (Blocked -> Allowed) with all other fields unchanged.
		mgScope := "/providers/microsoft.management/managementgroups/corp-mg"
		baseAssignment := foundrypolicy.PolicyAssignment{
			AssignmentID:          mgScope + "/providers/microsoft.authorization/policyassignments/mg-deny-prompt",
			AssignmentScope:       mgScope,
			AssignmentDisplayName: "MG Deny Prompt Agents",
			PolicyDefinitionID:    foundrypolicy.DeniedAgentKindsPolicyID,
			AssignmentParameters: map[string]any{
				"effect":           map[string]any{"value": "Deny"},
				"deniedAgentKinds": map[string]any{"value": []any{"prompt"}},
			},
			EnforcementMode:       "Default",
			ManagementGroupScopes: []string{mgScope},
			IsExempt:              false,
		}
		currentInv = foundrypolicy.Inventory{
			ProjectID:   testPolicyProjectID,
			Assignments: []foundrypolicy.PolicyAssignment{baseAssignment},
		}
		resNotExempt, err := EvaluateCandidateWithCredential(t.Context(), fakePolicyTokenCredential{}, cand, ops)
		require.NoError(t, err)
		assert.Equal(t, foundrypolicy.OutcomeBlocked, resNotExempt.Outcome)
		assert.Equal(t, 4, invCalls)
		assert.Equal(t, 3, modelCalls)

		exemptAssignment := baseAssignment
		exemptAssignment.IsExempt = true
		currentInv = foundrypolicy.Inventory{
			ProjectID:   testPolicyProjectID,
			Assignments: []foundrypolicy.PolicyAssignment{exemptAssignment},
		}
		resExempt, err := EvaluateCandidateWithCredential(t.Context(), fakePolicyTokenCredential{}, cand, ops)
		require.NoError(t, err)
		assert.Equal(t, foundrypolicy.OutcomeAllowed, resExempt.Outcome)
		assert.Empty(t, resExempt.Violations)
		assert.Equal(t, 5, invCalls)
		assert.Equal(t, 4, modelCalls)

		// 2. Toggling IsExempt from true -> false (Allowed -> Blocked) with all other fields unchanged.
		ops.evalCache = newCandidateEvaluationCache(0, nil)
		resExemptFirst, err := EvaluateCandidateWithCredential(t.Context(), fakePolicyTokenCredential{}, cand, ops)
		require.NoError(t, err)
		assert.Equal(t, foundrypolicy.OutcomeAllowed, resExemptFirst.Outcome)

		currentInv = foundrypolicy.Inventory{
			ProjectID:   testPolicyProjectID,
			Assignments: []foundrypolicy.PolicyAssignment{baseAssignment},
		}
		resUnexempted, err := EvaluateCandidateWithCredential(t.Context(), fakePolicyTokenCredential{}, cand, ops)
		require.NoError(t, err)
		assert.Equal(t, foundrypolicy.OutcomeBlocked, resUnexempted.Outcome)
		require.Len(t, resUnexempted.Violations, 1)

		// 3. Changing ManagementGroupScopes on a management-group-scoped assignment so applicability changes.
		nonApplicableMGAssignment := baseAssignment
		nonApplicableMGAssignment.ManagementGroupScopes = []string{
			"/providers/microsoft.management/managementgroups/other-mg",
		}
		currentInv = foundrypolicy.Inventory{
			ProjectID:   testPolicyProjectID,
			Assignments: []foundrypolicy.PolicyAssignment{nonApplicableMGAssignment},
		}
		resOtherMG, err := EvaluateCandidateWithCredential(t.Context(), fakePolicyTokenCredential{}, cand, ops)
		require.NoError(t, err)
		assert.Equal(t, foundrypolicy.OutcomeAllowed, resOtherMG.Outcome)
		assert.Empty(t, resOtherMG.Violations)
	})

	t.Run("loadProjectPolicyInventoryFromCache fails open when cached inventory is stale and refresh fails", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1000, 0)
		cache := foundrypolicy.NewInventoryCache(
			5*time.Minute,
			func() time.Time { return now },
			func(string) {},
			5*time.Minute,
			time.Second,
		)

		denyInv := foundrypolicy.Inventory{
			ProjectID: testPolicyProjectID,
			Assignments: []foundrypolicy.PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/deny-prompt",
					AssignmentDisplayName: "Deny Prompt Agents",
					PolicyDefinitionID:    foundrypolicy.DeniedAgentKindsPolicyID,
					AssignmentParameters: map[string]any{
						"effect":           map[string]any{"value": "Deny"},
						"deniedAgentKinds": map[string]any{"value": []any{"prompt"}},
					},
					EnforcementMode: "Default",
				},
			},
		}

		// Initial fresh populate succeeds.
		inv1, err1 := loadProjectPolicyInventoryFromCache(
			t.Context(),
			cache,
			testPolicyProjectID,
			func(_ context.Context) (foundrypolicy.Inventory, error) {
				return denyInv, nil
			},
		)
		require.NoError(t, err1)
		require.Len(t, inv1.Assignments, 1)

		// Advance past TTL and fail refresh -> GetFresh returns FreshnessStale, which
		// loadProjectPolicyInventoryFromCache rejects with an error so preflight fails open.
		now = now.Add(6 * time.Minute)
		_, errStale := loadProjectPolicyInventoryFromCache(
			t.Context(),
			cache,
			testPolicyProjectID,
			func(_ context.Context) (foundrypolicy.Inventory, error) {
				return foundrypolicy.Inventory{}, errors.New("Resource Graph unavailable")
			},
		)
		require.ErrorContains(t, errStale, "stale")

		// Advance past retry throttle and succeed refresh -> returns updated FreshnessFresh inventory.
		now = now.Add(6 * time.Minute)
		updatedInv := foundrypolicy.Inventory{ProjectID: testPolicyProjectID}
		invFresh, errFresh := loadProjectPolicyInventoryFromCache(
			t.Context(),
			cache,
			testPolicyProjectID,
			func(_ context.Context) (foundrypolicy.Inventory, error) {
				return updatedInv, nil
			},
		)
		require.NoError(t, errFresh)
		assert.Empty(t, invFresh.Assignments)
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

func TestPolicyOptOutEnvVars(t *testing.T) {
	blockingOps := policyPreflightOperations{
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

	// 1. azdEnv primary key
	assert.True(t, shouldSkipPolicyChecks(map[string]string{
		"AZD_AI_AGENT_SKIP_POLICY_CHECKS": "true",
	}))
	require.NoError(t, checkHostedContainerImagePolicy(
		t.Context(),
		testPolicyProjectID,
		fakePolicyTokenCredential{},
		"my-hosted",
		"untrusted.example.com/team/agent:v1",
		map[string]string{"AZD_AI_AGENT_SKIP_POLICY_CHECKS": "true"},
		blockingOps,
	))

	// 2. azdEnv legacy fallback key
	assert.True(t, shouldSkipPolicyChecks(map[string]string{
		"AZD_AGENT_SKIP_POLICY_CHECKS": "1",
	}))
	require.NoError(t, checkHostedContainerImagePolicy(
		t.Context(),
		testPolicyProjectID,
		fakePolicyTokenCredential{},
		"my-hosted",
		"untrusted.example.com/team/agent:v1",
		map[string]string{"AZD_AGENT_SKIP_POLICY_CHECKS": "1"},
		blockingOps,
	))

	// 3. Process env primary key
	t.Setenv("AZD_AI_AGENT_SKIP_POLICY_CHECKS", "true")
	assert.True(t, shouldSkipPolicyChecks(nil))
	require.NoError(t, checkHostedContainerImagePolicy(
		t.Context(),
		testPolicyProjectID,
		fakePolicyTokenCredential{},
		"my-hosted",
		"untrusted.example.com/team/agent:v1",
		nil,
		blockingOps,
	))

	// 4. azdEnv legacy fallback key takes precedence over process env primary key = false
	t.Setenv("AZD_AI_AGENT_SKIP_POLICY_CHECKS", "false")
	assert.True(t, shouldSkipPolicyChecks(map[string]string{
		"AZD_AGENT_SKIP_POLICY_CHECKS": "true",
	}))
}

func TestEvaluateCandidateWithCredentialDeadlinePropagation(t *testing.T) {
	t.Parallel()

	cand := foundrypolicy.Candidate{
		ProjectResourceID: testPolicyProjectID,
		ServiceName:       "deadline-svc",
		AgentKind:         "prompt",
		Models: []foundrypolicy.ModelCandidate{{
			DeploymentName: "gpt-4o",
			Name:           "gpt-4o",
			Format:         "OpenAI",
			Version:        "2024-08-06",
		}},
	}

	t.Run("applies default 30s deadline when caller context has no deadline", func(t *testing.T) {
		t.Parallel()
		var invHasDeadline, modelHasDeadline bool
		var invRemaining, modelRemaining time.Duration

		ops := policyPreflightOperations{
			loadInventory: func(ctx context.Context, _ azcore.TokenCredential, projectID string) (foundrypolicy.Inventory, error) {
				dl, ok := ctx.Deadline()
				invHasDeadline = ok
				invRemaining = time.Until(dl)
				return foundrypolicy.Inventory{ProjectID: projectID}, nil
			},
			evaluateModel: func(
				ctx context.Context,
				_ azcore.TokenCredential,
				_ string,
				models []foundrypolicy.ModelCandidate,
			) (foundrypolicy.ModelPolicyEvaluation, error) {
				dl, ok := ctx.Deadline()
				modelHasDeadline = ok
				modelRemaining = time.Until(dl)
				return foundrypolicy.ModelPolicyEvaluation{ResolvedModels: models}, nil
			},
		}

		res, err := EvaluateCandidateWithCredential(context.Background(), fakePolicyTokenCredential{}, cand, ops)
		require.NoError(t, err)
		assert.Equal(t, foundrypolicy.OutcomeAllowed, res.Outcome)
		assert.True(t, invHasDeadline)
		assert.True(t, modelHasDeadline)
		assert.LessOrEqual(t, invRemaining, foundrypolicy.DefaultPolicyPreflightTimeout)
		assert.Greater(t, invRemaining, time.Second)
		assert.LessOrEqual(t, modelRemaining, foundrypolicy.DefaultPolicyPreflightTimeout)
	})

	t.Run("preserves shorter caller deadline and propagates cancellation across stages", func(t *testing.T) {
		t.Parallel()
		shortCtx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		callerDeadline, _ := shortCtx.Deadline()

		var invDeadline, modelDeadline time.Time
		ops := policyPreflightOperations{
			loadInventory: func(ctx context.Context, _ azcore.TokenCredential, projectID string) (foundrypolicy.Inventory, error) {
				invDeadline, _ = ctx.Deadline()
				return foundrypolicy.Inventory{ProjectID: projectID}, nil
			},
			evaluateModel: func(
				ctx context.Context,
				_ azcore.TokenCredential,
				_ string,
				_ []foundrypolicy.ModelCandidate,
			) (foundrypolicy.ModelPolicyEvaluation, error) {
				modelDeadline, _ = ctx.Deadline()
				<-ctx.Done()
				return foundrypolicy.ModelPolicyEvaluation{}, ctx.Err()
			},
		}

		res, err := EvaluateCandidateWithCredential(shortCtx, fakePolicyTokenCredential{}, cand, ops)
		require.NoError(t, err)
		assert.Equal(t, callerDeadline, invDeadline)
		assert.Equal(t, callerDeadline, modelDeadline)
		assert.Equal(t, foundrypolicy.OutcomeIndeterminate, res.Outcome)
		assert.Contains(t, res.IncompleteReason, context.DeadlineExceeded.Error())
	})
}
