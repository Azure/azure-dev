// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundrypolicy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProjectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
	"Microsoft.CognitiveServices/accounts/account/projects/project"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestPipeline(fn roundTripFunc) runtime.Pipeline {
	return runtime.NewPipeline(
		"test",
		"v0.0.0",
		runtime.PipelineOptions{},
		&policy.ClientOptions{
			Transport: &http.Client{Transport: fn},
		},
	)
}

func createAgentAssignment(modify func(*PolicyAssignment)) PolicyAssignment {
	a := PolicyAssignment{
		AssignmentID:          "/subscriptions/sub/providers/microsoft.authorization/policyassignments/assignment",
		AssignmentDisplayName: "Agent kind policy",
		DefinitionDisplayName: "Microsoft Foundry agents should not use denied agent kinds",
		AssignmentParameters: map[string]any{
			"effect":           map[string]any{"value": "Deny"},
			"deniedAgentKinds": map[string]any{"value": []any{"prompt"}},
		},
		EnforcementMode: "Default",
	}
	if modify != nil {
		modify(&a)
	}
	return a
}

func createImageAssignment(
	kind string,
	patterns []string,
	modify func(*PolicyAssignment),
) PolicyAssignment {
	denied := kind == "denied"
	defID := AllowedContainerImagesPolicyID
	paramName := "allowedContainerImages"
	if denied {
		defID = DeniedContainerImagesPolicyID
		paramName = "deniedContainerImages"
	}
	a := PolicyAssignment{
		AssignmentID:          "/subscriptions/sub/providers/microsoft.authorization/policyassignments/" + kind,
		AssignmentDisplayName: kind + " images",
		PolicyDefinitionID:    defID,
		AssignmentParameters: map[string]any{
			"effect":  map[string]any{"value": "Deny"},
			paramName: map[string]any{"value": patterns},
		},
		EnforcementMode: "Default",
	}
	if modify != nil {
		modify(&a)
	}
	return a
}

func TestBuildResourceGraphQueries(t *testing.T) {
	t.Parallel()

	mgQuery := BuildManagementGroupQuery("sub'id")
	assignQuery := BuildPolicyAssignmentsQuery()
	exemptQuery := BuildPolicyExemptionsQuery([]string{
		"/subscriptions/sub/policyAssignments/assignment'id",
	})

	for _, q := range []string{mgQuery, assignQuery, exemptQuery} {
		assert.NotContains(t, strings.ToLower(q), "toscalar")
	}
	assert.Contains(t, mgQuery, "sub''id")
	assert.Contains(t, assignQuery, "b6ca2367-8597-4a3f-ab19-48f58d6d9278")
	assert.Contains(t, assignQuery, "85455646-5d02-433e-a42d-265151140427")
	assert.Contains(t, assignQuery, "215c2c77-3afb-4f55-be2f-709b60dc8d53")
	assert.Contains(t, assignQuery, "d0d9ac6c-628b-46c1-a177-eb22b12ce588")
	assert.Contains(t, assignQuery, "7740c22b-07ef-4bac-9f5a-e38d188f80b7")
	assert.Contains(t, assignQuery, "e7352ad2-6c86-4a07-9573-47f67b557ba8")
	assert.Contains(t, assignQuery, "aafe3651-cb78-4f68-9f81-e7e41509110f")
	assert.NotContains(t, strings.ToLower(assignQuery), "microsoft.authorization/policysetdefinitions")
	assert.Contains(t, assignQuery, "assignmentOverrides = properties.overrides")
	assert.Contains(t, assignQuery, "resourceSelectors = properties.resourceSelectors")
	assert.Contains(t, exemptQuery, "assignment''id")
}

func TestAgentKindPolicyEvaluation(t *testing.T) {
	t.Parallel()

	t.Run("blocks denied agent kinds and localized display names", func(t *testing.T) {
		t.Parallel()
		assert.True(t, IsAgentKindBlocked(createAgentAssignment(nil), testProjectID, "prompt"))
		assert.True(t, IsAgentKindBlocked(createAgentAssignment(func(a *PolicyAssignment) {
			a.DefinitionDisplayName = "[Preview]: Microsoft Foundry agents should not use denied agent kinds"
		}), testProjectID, "prompt"))
		assert.True(t, IsAgentKindBlocked(createAgentAssignment(func(a *PolicyAssignment) {
			a.DefinitionDisplayName = "Localized policy name"
			a.PolicyDefinitionID = DeniedAgentKindsPolicyID
		}), testProjectID, "prompt"))
		// prompt-voice normalizes to voice
		assert.True(t, IsAgentKindBlocked(createAgentAssignment(func(a *PolicyAssignment) {
			a.AssignmentParameters["deniedAgentKinds"] = map[string]any{"value": []any{"voice"}}
		}), testProjectID, "prompt-voice"))
	})

	t.Run("blocks agent kinds omitted from allowedAgentKinds", func(t *testing.T) {
		t.Parallel()
		allowedOnlyHosted := createAgentAssignment(func(a *PolicyAssignment) {
			a.DefinitionDisplayName = "Microsoft Foundry agents should only use allowed agent kinds"
			a.AssignmentParameters = map[string]any{
				"effect":            map[string]any{"value": "Deny"},
				"allowedAgentKinds": map[string]any{"value": []any{"hosted"}},
			}
		})
		assert.True(t, IsAgentKindBlocked(allowedOnlyHosted, testProjectID, "prompt"))
		assert.False(t, IsAgentKindBlocked(allowedOnlyHosted, testProjectID, "hosted"))
	})

	t.Run("ignores non-Deny, DoNotEnforce, notScopes, exemptions, overrides, and selectors", func(t *testing.T) {
		t.Parallel()
		cases := []func(*PolicyAssignment){
			func(a *PolicyAssignment) {
				a.AssignmentParameters["effect"] = map[string]any{"value": "Audit"}
			},
			func(a *PolicyAssignment) {
				a.AssignmentParameters["effect"] = map[string]any{"value": "Disabled"}
			},
			func(a *PolicyAssignment) {
				a.EnforcementMode = "DoNotEnforce"
			},
			func(a *PolicyAssignment) {
				a.NotScopes = []string{"/subscriptions/sub/resourceGroups/rg"}
			},
			func(a *PolicyAssignment) {
				a.IsExempt = true
			},
			func(a *PolicyAssignment) {
				a.AssignmentOverrides = []any{map[string]any{"kind": "policyEffect", "value": "Deny"}}
			},
			func(a *PolicyAssignment) {
				a.ResourceSelectors = []any{map[string]any{"name": "eastus-only"}}
			},
		}
		for _, mod := range cases {
			assert.False(t, IsAgentKindBlocked(createAgentAssignment(mod), testProjectID, "prompt"))
		}
	})

	t.Run("falls back to definition defaults", func(t *testing.T) {
		t.Parallel()
		a := createAgentAssignment(func(a *PolicyAssignment) {
			a.AssignmentParameters = map[string]any{}
			a.DefinitionParameters = map[string]any{
				"effect":           map[string]any{"defaultValue": "Deny"},
				"deniedAgentKinds": map[string]any{"defaultValue": []any{"prompt"}},
			}
		})
		assert.True(t, IsAgentKindBlocked(a, testProjectID, "prompt"))
	})

	t.Run("orders blocking assignments deterministically", func(t *testing.T) {
		t.Parallel()
		inv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				createAgentAssignment(func(a *PolicyAssignment) { a.AssignmentID = "/subscriptions/sub/z" }),
				createAgentAssignment(func(a *PolicyAssignment) { a.AssignmentID = "/subscriptions/sub/a" }),
			},
		}
		res := EvaluateAgentKindPolicy(inv, "prompt")
		require.Equal(t, OutcomeBlocked, res.Outcome)
		require.Len(t, res.Violations, 2)
		assert.Equal(t, "/subscriptions/sub/a", res.Violations[0].AssignmentID)
		assert.Equal(t, "/subscriptions/sub/z", res.Violations[1].AssignmentID)
	})
}

func TestContainerImagePolicyEvaluation(t *testing.T) {
	t.Parallel()

	t.Run("matches Azure Policy like patterns case-insensitively", func(t *testing.T) {
		t.Parallel()
		assert.True(t, MatchesAzurePolicyLike("REGISTRY.EXAMPLE.COM/Team/App:V1", "registry.example.com/*:v*"))
		assert.True(t, MatchesAzurePolicyLike("image:v?", "image:v?"))
		assert.True(t, MatchesAzurePolicyLike("image:v#", "image:v#"))
		assert.False(t, MatchesAzurePolicyLike("image:v7", "image:v?"))
		assert.False(t, MatchesAzurePolicyLike("image:vx", "image:v#"))
		assert.False(t, MatchesAzurePolicyLike("registryXexample.com/team/app:v1", "registry.example.com/*:v*"))
	})

	t.Run("evaluates denied and allowed policies against a full image", func(t *testing.T) {
		t.Parallel()
		img := ContainerImageCandidate{
			Type:  CandidateTypeImage,
			Value: "REGISTRY.EXAMPLE.COM/team/app:V1",
		}
		assert.Equal(t, OutcomeBlocked, EvaluateContainerImagePolicyAssignment(
			createImageAssignment("denied", []string{"registry.example.com/team/*:v*"}, nil),
			testProjectID,
			img,
		))
		assert.Equal(t, OutcomeAllowed, EvaluateContainerImagePolicyAssignment(
			createImageAssignment("allowed", []string{"registry.example.com/team/*"}, nil),
			testProjectID,
			img,
		))
		assert.Equal(t, OutcomeBlocked, EvaluateContainerImagePolicyAssignment(
			createImageAssignment("allowed", []string{"registry.example.com/other/*"}, nil),
			testProjectID,
			img,
		))
	})

	t.Run("uses only registry-wide patterns for Dockerfile registry preflight", func(t *testing.T) {
		t.Parallel()
		reg, ok := GetRegistryWidePatternRegistry("registry.example.com/*")
		assert.True(t, ok)
		assert.Equal(t, "registry.example.com", reg)

		for _, nonWide := range []string{
			"registry.example.com/team/*",
			"https://registry.example.com/*",
			"registry.example.com/*/",
		} {
			_, ok := GetRegistryWidePatternRegistry(nonWide)
			assert.False(t, ok, "pattern %q should not be treated as registry-wide", nonWide)
		}

		candidate := ContainerImageCandidate{
			Type:  CandidateTypeRegistry,
			Value: "https://REGISTRY.EXAMPLE.COM/",
		}
		assert.Equal(t, OutcomeBlocked, EvaluateContainerImagePolicyAssignment(
			createImageAssignment("denied", []string{"registry.example.com/*"}, nil),
			testProjectID,
			candidate,
		))
		assert.Equal(t, OutcomeIndeterminate, EvaluateContainerImagePolicyAssignment(
			createImageAssignment("denied", []string{"registry.example.com/team/*"}, nil),
			testProjectID,
			candidate,
		))
		assert.Equal(t, OutcomeAllowed, EvaluateContainerImagePolicyAssignment(
			createImageAssignment("allowed", []string{"registry.example.com/*"}, nil),
			testProjectID,
			candidate,
		))
		assert.Equal(t, OutcomeIndeterminate, EvaluateContainerImagePolicyAssignment(
			createImageAssignment("allowed", []string{"registry.example.com/team/*"}, nil),
			testProjectID,
			candidate,
		))
		assert.Equal(t, OutcomeBlocked, EvaluateContainerImagePolicyAssignment(
			createImageAssignment("allowed", []string{"approved.example.com/*"}, nil),
			testProjectID,
			candidate,
		))
	})
}

func TestToolAndModelPolicies(t *testing.T) {
	t.Parallel()

	t.Run("evaluates tool policy from inventory", func(t *testing.T) {
		t.Parallel()
		inv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/deny-tools",
					AssignmentDisplayName: "Deny web search",
					DefinitionDisplayName: "[Preview]: Microsoft Foundry agents should not use denied tools",
					AssignmentParameters: map[string]any{
						"effect":      map[string]any{"value": "Deny"},
						"deniedTools": map[string]any{"value": []any{"builtin:web_search"}},
					},
					EnforcementMode: "Default",
				},
			},
		}
		res := EvaluateToolPolicy(inv, []ToolCandidate{
			{Type: "code_interpreter"},
			{Type: "web_search"},
		})
		require.Equal(t, OutcomeBlocked, res.Outcome)
		require.Len(t, res.Violations, 1)
		assert.Equal(t, TargetTool, res.Violations[0].Target)
		assert.Equal(t, "web_search", res.Violations[0].TargetValue)
	})

	t.Run("evaluates live built-in Denied Tools (7740c22b) and Approved Tools (e7352ad2) policies", func(t *testing.T) {
		t.Parallel()
		deniedInv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/deny-live-tools",
					AssignmentDisplayName: "Deny Code Interpreter and Untrusted MCP",
					PolicyDefinitionID:    DeniedToolsPolicyID,
					DefinitionDisplayName: "[Preview]: Microsoft Foundry agents and toolboxes should not use denied tools",
					AssignmentParameters: map[string]any{
						"effect":              map[string]any{"value": "Deny"},
						"deniedToolTypes":     map[string]any{"value": []any{"code_interpreter"}},
						"deniedMcpEndpoints":  map[string]any{"value": []any{"https://untrusted.example.com/*"}},
						"deniedMcpConnectors": map[string]any{"value": []any{"blocked-connector"}},
					},
					EnforcementMode: "Default",
				},
			},
		}

		resDenied := EvaluateToolPolicy(deniedInv, []ToolCandidate{
			{Type: "web_search"},
			{Type: "code_interpreter"},
			{Type: "mcp", Name: "bad-url", ServerURL: "https://untrusted.example.com/mcp"},
			{Type: "mcp", Name: "bad-conn", ConnectorID: "BLOCKED-CONNECTOR"},
			{Type: "mcp", Name: "good-mcp", ServerURL: "https://trusted.example.com/mcp"},
		})
		require.Equal(t, OutcomeBlocked, resDenied.Outcome)
		require.Len(t, resDenied.Violations, 3)

		approvedInv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/approve-live-tools",
					AssignmentDisplayName: "Approved Tools Only",
					PolicyDefinitionID:    AllowedToolsPolicyID,
					DefinitionDisplayName: "[Preview]: Microsoft Foundry agents and toolboxes should only use approved tools",
					AssignmentParameters: map[string]any{
						"effect":                map[string]any{"value": "Deny"},
						"approvedToolTypes":     map[string]any{"value": []any{"web_search"}},
						"approvedMcpEndpoints":  map[string]any{"value": []any{"https://approved.example.com/*"}},
						"approvedMcpConnectors": map[string]any{"value": []any{"approved-connector"}},
					},
					EnforcementMode: "Default",
				},
			},
		}

		// Any of approvedToolTypes, approvedMcpEndpoints, or approvedMcpConnectors approves the tool.
		resApproved := EvaluateToolPolicy(approvedInv, []ToolCandidate{
			{Type: "web_search"},
			{Type: "mcp", Name: "by-endpoint", ServerURL: "https://approved.example.com/v1/mcp"},
			{Type: "mcp", Name: "by-connector", ConnectorID: "approved-connector"},
		})
		assert.Equal(t, OutcomeAllowed, resApproved.Outcome)
		assert.Empty(t, resApproved.Violations)

		// Unapproved tool type and unapproved MCP server (with known ServerURL and ConnectorID) are blocked.
		resUnapproved := EvaluateToolPolicy(approvedInv, []ToolCandidate{
			{Type: "code_interpreter"},
			{Type: "mcp", Name: "unapproved-mcp", ServerURL: "https://other.example.com/mcp", ConnectorID: "other-connector"},
		})
		require.Equal(t, OutcomeBlocked, resUnapproved.Outcome)
		require.Len(t, resUnapproved.Violations, 2)

		// Exact field & operator isolation:
		// 1. MCP tool named "code_interpreter" is NOT blocked by deniedToolTypes: ["code_interpreter"]
		// 2. code_interpreter tool named "web_search" is NOT approved by approvedToolTypes: ["web_search"]
		// 3. Literal "*" in deniedToolTypes is not treated as a wildcard
		// 4. ConnectionID is not matched against deniedMcpConnectors or approvedMcpConnectors
		wildcardTypeInv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/deny-wildcard-type",
					AssignmentDisplayName: "Deny Literal Wildcard",
					PolicyDefinitionID:    DeniedToolsPolicyID,
					AssignmentParameters: map[string]any{
						"effect":              map[string]any{"value": "Deny"},
						"deniedToolTypes":     map[string]any{"value": []any{"code_*", "code_interpreter"}},
						"deniedMcpConnectors": map[string]any{"value": []any{"conn-id-only"}},
					},
					EnforcementMode: "Default",
				},
			},
		}
		assert.Equal(t, OutcomeAllowed, EvaluateToolPolicyAssignment(
			wildcardTypeInv.Assignments[0],
			testProjectID,
			ToolCandidate{
				Type:         "mcp",
				Name:         "code_interpreter",
				ConnectionID: "conn-id-only",
				ServerURL:    "https://trusted.example.com/mcp",
				ConnectorID:  "safe-connector",
			},
		))
		assert.Equal(t, OutcomeAllowed, EvaluateToolPolicyAssignment(
			wildcardTypeInv.Assignments[0],
			testProjectID,
			ToolCandidate{Type: "code_runner"},
		))
		assert.Equal(t, OutcomeBlocked, EvaluateToolPolicyAssignment(
			approvedInv.Assignments[0],
			testProjectID,
			ToolCandidate{Type: "code_interpreter", Name: "web_search"},
		))

		// Unresolved MCP toolbox/connection (empty ServerURL & ConnectorID) under approvedMcpEndpoints
		// returns OutcomeIndeterminate rather than falsely blocking.
		resIndeterminateMCP := EvaluateToolPolicy(approvedInv, []ToolCandidate{
			{Type: "mcp", Name: "toolbox:my-tools", ConnectionID: "approved-connector"},
		})
		assert.Equal(t, OutcomeIndeterminate, resIndeterminateMCP.Outcome)
		assert.Empty(t, resIndeterminateMCP.Violations)
		assert.Contains(t, resIndeterminateMCP.IncompleteReason, "MCP tool endpoint or connector metadata")
	})

	t.Run("evaluates live built-in Approved Models policy (aafe3651) with authoritative Publisher and AssetID and defers when unknown", func(t *testing.T) {
		t.Parallel()
		inv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/approved-models",
					AssignmentDisplayName: "Foundry Approved Models",
					PolicyDefinitionID:    AllowedModelsPolicyID,
					DefinitionDisplayName: "Foundry model deployments should only use approved models",
					AssignmentParameters: map[string]any{
						"effect":            map[string]any{"value": "Deny"},
						"allowedPublishers": map[string]any{"value": []any{"Anthropic"}},
						"allowedAssetIds": map[string]any{"value": []any{
							"azureml://registries/azure-openai/models/gpt-4.1/versions/2025-04-14",
							"azureml://registries/azure-openai/models/gpt-5/",
						}},
					},
					EnforcementMode: "Default",
				},
			},
		}

		// Approved via authoritative AssetID
		resAsset := EvaluateModelInventoryPolicy(inv, []ModelCandidate{{
			DeploymentName: "chat",
			Name:           "gpt-4.1",
			Format:         "OpenAI",
			Version:        "2025-04-14",
			Publisher:      "OpenAI",
			AssetID:        "azureml://registries/azure-openai/models/gpt-4.1/versions/2025-04-14",
		}})
		assert.Equal(t, OutcomeAllowed, resAsset.Outcome)

		// Approved via authoritative Publisher
		resPublisher := EvaluateModelInventoryPolicy(inv, []ModelCandidate{{
			DeploymentName: "claude",
			Name:           "claude-3-7-sonnet",
			Publisher:      "Anthropic",
		}})
		assert.Equal(t, OutcomeAllowed, resPublisher.Outcome)

		// Blocked when both Publisher and AssetID are known and neither matches (e.g. different version or registry)
		resBlocked := EvaluateModelInventoryPolicy(inv, []ModelCandidate{{
			DeploymentName: "chat-old-ver",
			Name:           "gpt-4.1",
			Format:         "OpenAI",
			Version:        "2024-01-01",
			Publisher:      "OpenAI",
			AssetID:        "azureml://registries/azure-openai/models/gpt-4.1/versions/2024-01-01",
		}})
		assert.Equal(t, OutcomeBlocked, resBlocked.Outcome)
		require.Len(t, resBlocked.Violations, 1)

		// When Publisher and AssetID are not known locally (only Format/Name/Version), returns OutcomeIndeterminate
		// and EvaluateCandidate defers to authoritative ARM evaluateDeploymentPolicies!
		resIndeterminate := EvaluateModelInventoryPolicy(inv, []ModelCandidate{{
			DeploymentName: "chat",
			Name:           "gpt-4.1",
			Format:         "OpenAI",
			Version:        "2025-04-14",
		}})
		assert.Equal(t, OutcomeIndeterminate, resIndeterminate.Outcome)
		assert.Empty(t, resIndeterminate.Violations)

		armCompliantEval := NewARMDeploymentPolicyEvaluator(newTestPipeline(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"results":{"chat":{"evaluationOutcome":"Compliant"}}}`)),
				Request:    req,
			}, nil
		}), "https://management.azure.com")
		resDeferredAllowed := EvaluateCandidate(
			t.Context(),
			inv,
			Candidate{
				ProjectResourceID: testProjectID,
				Models: []ModelCandidate{{
					DeploymentName: "chat",
					Name:           "gpt-4.1",
					Format:         "OpenAI",
					Version:        "2025-04-14",
				}},
			},
			armCompliantEval,
		)
		assert.Equal(t, OutcomeAllowed, resDeferredAllowed.Outcome)
		assert.Empty(t, resDeferredAllowed.Violations)

		// Preserve literal allowedAssetIds entries without trimming:
		// 1. allowedAssetIds: [""] matches any known non-empty assetID via contains.
		emptyAssetEntryInv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/empty-asset-entry",
					AssignmentDisplayName: "Empty Asset Entry",
					PolicyDefinitionID:    AllowedModelsPolicyID,
					AssignmentParameters: map[string]any{
						"effect":          map[string]any{"value": "Deny"},
						"allowedAssetIds": map[string]any{"value": []any{""}},
					},
					EnforcementMode: "Default",
				},
			},
		}
		resEmptyEntry := EvaluateModelInventoryPolicy(emptyAssetEntryInv, []ModelCandidate{{
			DeploymentName: "chat",
			Name:           "gpt-4.1",
			Publisher:      "OpenAI",
			AssetID:        "azureml://registries/azure-openai/models/gpt-4.1/versions/2025-04-14",
		}})
		assert.Equal(t, OutcomeAllowed, resEmptyEntry.Outcome)

		// 2. allowedAssetIds entry with leading/trailing whitespace is matched literally (not trimmed).
		paddedAssetEntryInv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/padded-asset-entry",
					AssignmentDisplayName: "Padded Asset Entry",
					PolicyDefinitionID:    AllowedModelsPolicyID,
					AssignmentParameters: map[string]any{
						"effect":            map[string]any{"value": "Deny"},
						"allowedPublishers": map[string]any{"value": []any{"Anthropic"}},
						"allowedAssetIds":   map[string]any{"value": []any{" /models/gpt-4.1/ "}},
					},
					EnforcementMode: "Default",
				},
			},
		}
		resPaddedEntry := EvaluateModelInventoryPolicy(paddedAssetEntryInv, []ModelCandidate{{
			DeploymentName: "chat",
			Name:           "gpt-4.1",
			Publisher:      "OpenAI",
			AssetID:        "azureml://registries/azure-openai/models/gpt-4.1/versions/2025-04-14",
		}})
		assert.Equal(t, OutcomeBlocked, resPaddedEntry.Outcome)
		require.Len(t, resPaddedEntry.Violations, 1)
	})

	t.Run("evaluates model deployment policy via ARM evaluateDeploymentPolicies", func(t *testing.T) {
		t.Parallel()
		pipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/deployments/my-gpt4") {
				body := `{"properties":{"model":{"format":"OpenAI","name":"gpt-4o","version":"2024-08-06"}}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			}
			if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/evaluateDeploymentPolicies") {
				body := `{
					"results": {
						"my-gpt4": {
							"evaluationOutcome": "NonCompliant",
							"nonCompliantAssignments": [
								{
									"assignmentId": "/subscriptions/sub/providers/Microsoft.Authorization/policyAssignments/block-gpt4o",
									"effect": "deny"
								}
							]
						}
					}
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(bytes.NewReader(nil)),
				Request:    req,
			}, nil
		})

		eval := NewARMDeploymentPolicyEvaluator(pipeline, "https://management.azure.com")
		res := EvaluateCandidate(
			t.Context(),
			Inventory{ProjectID: testProjectID},
			Candidate{
				ProjectResourceID: testProjectID,
				Models:            []ModelCandidate{{DeploymentName: "my-gpt4"}},
			},
			eval,
		)
		require.Equal(t, OutcomeBlocked, res.Outcome)
		require.Len(t, res.Violations, 1)
		assert.Equal(t, TargetModel, res.Violations[0].Target)
		assert.Equal(t, "gpt-4o (version 2024-08-06)", res.Violations[0].TargetValue)
		assert.Equal(t, "block-gpt4o", res.Violations[0].DisplayName)
	})
}

func TestBuildPolicyAssignmentPortalURL(t *testing.T) {
	t.Parallel()

	foundryProjectID := "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/resourceGroups/rg with spaces/" +
		"providers/Microsoft.CognitiveServices/accounts/account/projects/project"
	assignmentID := "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/resourceGroups/rg with spaces/" +
		"providers/Microsoft.Authorization/policyAssignments/assignment"

	portalURL := BuildPolicyAssignmentPortalURL(foundryProjectID, assignmentID)
	parsedURL, err := url.Parse(portalURL)
	require.NoError(t, err)

	segments := strings.Split(parsedURL.EscapedPath(), "/")
	assignmentKey := segments[len(segments)-1]

	assert.Equal(
		t,
		"/nextgen/r/F1arwDVUQ0GNakZnSWLqGQ%2Crg%20with%20spaces%2C%2Caccount%2Cproject/"+
			"manage/govern/assignments/"+assignmentKey,
		parsedURL.EscapedPath(),
	)

	decoded, err := base64.RawURLEncoding.DecodeString(assignmentKey)
	require.NoError(t, err)
	assert.Equal(t, assignmentID, string(decoded))

	q := parsedURL.Query()
	assert.Equal(t, "1756abc0-3554-4341-8d6a-46674962ea19", q.Get("subscriptionId"))
	assert.Equal(
		t,
		"/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/resourceGroups/rg with spaces",
		q.Get("resourceGroupId"),
	)
	assert.Equal(t, foundryProjectID, q.Get("projectId"))
	assert.Equal(t, "govern_ignite", q.Get("flight"))

	assert.Equal(
		t,
		"https://ai.azure.com/?flight=govern_ignite",
		BuildPolicyAssignmentPortalURL("invalid-project-id", assignmentID),
	)
}

func TestLoadInventoryAndResourceGraphPagination(t *testing.T) {
	t.Parallel()

	t.Run("queries ancestors, assignments, and applicable exemptions in order", func(t *testing.T) {
		t.Parallel()
		mgScope := "/providers/Microsoft.Management/managementGroups/parent"
		mgAssignment := createAgentAssignment(func(a *PolicyAssignment) {
			a.AssignmentID = mgScope + "/providers/Microsoft.Authorization/policyAssignments/mg"
			a.AssignmentScope = mgScope
		})
		rgAssignment := createAgentAssignment(func(a *PolicyAssignment) {
			a.AssignmentID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Authorization/policyAssignments/rg"
			a.AssignmentScope = "/subscriptions/sub/resourceGroups/rg"
		})
		otherAssignment := createAgentAssignment(func(a *PolicyAssignment) {
			a.AssignmentID = "/subscriptions/sub/resourceGroups/other/providers/Microsoft.Authorization/policyAssignments/other"
			a.AssignmentScope = "/subscriptions/sub/resourceGroups/other"
		})

		var requests []ResourceGraphRequest
		queryFn := func(_ context.Context, req ResourceGraphRequest) ([]json.RawMessage, error) {
			requests = append(requests, req)
			switch len(requests) {
			case 1:
				b, _ := json.Marshal([]map[string]any{{"scope": mgScope}})
				var rows []json.RawMessage
				_ = json.Unmarshal(b, &rows)
				return rows, nil
			case 2:
				b, _ := json.Marshal([]PolicyAssignment{otherAssignment, rgAssignment, mgAssignment})
				var rows []json.RawMessage
				_ = json.Unmarshal(b, &rows)
				return rows, nil
			default:
				b, _ := json.Marshal([]map[string]any{{
					"exemptionId":        mgScope + "/providers/Microsoft.Authorization/policyExemptions/ex",
					"policyAssignmentId": mgAssignment.AssignmentID,
					"expiresOn":          "2999-01-01T00:00:00Z",
				}})
				var rows []json.RawMessage
				_ = json.Unmarshal(b, &rows)
				return rows, nil
			}
		}

		inv, err := LoadInventory(t.Context(), testProjectID, "sub", queryFn)
		require.NoError(t, err)
		require.Len(t, requests, 3)
		assert.Nil(t, requests[0].Options)
		assert.Equal(t, "AtScopeAboveAndBelow", requests[1].Options.AuthorizationScopeFilter)
		assert.Equal(t, "AtScopeAboveAndBelow", requests[2].Options.AuthorizationScopeFilter)
		assert.NotContains(t, requests[2].Query, strings.ToLower(otherAssignment.AssignmentID))

		res := EvaluateAgentKindPolicy(inv, "prompt")
		require.Equal(t, OutcomeBlocked, res.Outcome)
		require.Len(t, res.Violations, 1)
		assert.Equal(t, strings.ToLower(rgAssignment.AssignmentID), res.Violations[0].AssignmentID)
	})

	t.Run("follows Resource Graph continuation tokens and rejects truncated responses without token", func(t *testing.T) {
		t.Parallel()
		var callCount int
		pipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
			callCount++
			var body string
			if callCount == 1 {
				body = `{"data":[{"id":"first"}],"resultTruncated":true,"$skipToken":"page2"}`
			} else {
				body = `{"data":[{"id":"second"}],"resultTruncated":false}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		})

		queryFn := NewResourceGraphQueryFunc(pipeline, "https://management.azure.com", nil)
		rows, err := queryFn(t.Context(), ResourceGraphRequest{
			Subscriptions: []string{"sub"},
			Query:         "resources",
		})
		require.NoError(t, err)
		require.Len(t, rows, 2)

		var logged []string
		truncatedPipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"partial"}],"resultTruncated":true}`)),
				Request:    req,
			}, nil
		})
		truncQueryFn := NewResourceGraphQueryFunc(truncatedPipeline, "https://management.azure.com", func(msg string) {
			logged = append(logged, msg)
		})
		_, err = truncQueryFn(t.Context(), ResourceGraphRequest{Subscriptions: []string{"sub"}, Query: "resources"})
		require.EqualError(t, err, "unable to evaluate Azure Policy for the selected project")
		require.Len(t, logged, 1)
		assert.Contains(t, logged[0], "truncated")
	})
}

func TestInventoryCache(t *testing.T) {
	t.Parallel()

	t.Run("deduplicates concurrent requests and honors TTL", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1000, 0)
		cache := NewInventoryCache(5*time.Minute, func() time.Time { return now }, nil, 5*time.Minute, 5*time.Second)

		var fetchCount atomic.Int32
		inv := Inventory{ProjectID: testProjectID, Assignments: []PolicyAssignment{createAgentAssignment(nil)}}
		fetch := func(_ context.Context) (Inventory, error) {
			fetchCount.Add(1)
			return inv, nil
		}

		got1, err := cache.Get(t.Context(), testProjectID, fetch)
		require.NoError(t, err)
		now = now.Add(5*time.Minute - time.Millisecond)
		got2, err := cache.Get(t.Context(), strings.ToUpper(testProjectID), fetch)
		require.NoError(t, err)
		assert.Equal(t, int32(1), fetchCount.Load())
		assert.Equal(t, got1, got2)
	})

	t.Run("times out stalled request and does not cache failed first fetch", func(t *testing.T) {
		t.Parallel()
		var warnings []string
		cache := NewInventoryCache(
			time.Minute,
			time.Now,
			func(msg string) { warnings = append(warnings, msg) },
			time.Minute,
			20*time.Millisecond,
		)

		_, err := cache.Get(t.Context(), testProjectID, func(ctx context.Context) (Inventory, error) {
			<-ctx.Done()
			return Inventory{}, ctx.Err()
		})
		require.ErrorContains(t, err, "timed out")
		require.Len(t, warnings, 1)

		expected := Inventory{ProjectID: testProjectID}
		got, err := cache.Get(t.Context(), testProjectID, func(_ context.Context) (Inventory, error) {
			return expected, nil
		})
		require.NoError(t, err)
		assert.Equal(t, expected, got)
	})

	t.Run("retains stale inventory when refresh fails", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1000, 0)
		var warnings []string
		cache := NewInventoryCache(
			5*time.Minute,
			func() time.Time { return now },
			func(msg string) { warnings = append(warnings, msg) },
			5*time.Minute,
			time.Second,
		)

		initial := Inventory{ProjectID: testProjectID}
		_, err := cache.Get(t.Context(), testProjectID, func(_ context.Context) (Inventory, error) {
			return initial, nil
		})
		require.NoError(t, err)

		now = now.Add(5 * time.Minute)
		res, err := cache.GetFresh(t.Context(), testProjectID, func(_ context.Context) (Inventory, error) {
			return Inventory{}, errors.New("Policy service unavailable")
		})
		require.NoError(t, err)
		assert.Equal(t, FreshnessStale, res.Freshness)
		assert.Equal(t, initial, res.Inventory)
	})

	t.Run("deduplicates true concurrent Get calls with barrier", func(t *testing.T) {
		t.Parallel()
		cache := NewInventoryCache(5*time.Minute, time.Now, nil, 5*time.Minute, 5*time.Second)

		var fetchCount atomic.Int32
		fetchStarted := make(chan struct{})
		releaseFetch := make(chan struct{})
		expected := Inventory{ProjectID: testProjectID, Assignments: []PolicyAssignment{createAgentAssignment(nil)}}

		fetch := func(_ context.Context) (Inventory, error) {
			if fetchCount.Add(1) == 1 {
				close(fetchStarted)
			}
			<-releaseFetch
			return expected, nil
		}

		const workers = 4
		var wg sync.WaitGroup
		results := make([]Inventory, workers)
		errs := make([]error, workers)

		for i := range workers {
			wg.Go(func() {
				results[i], errs[i] = cache.Get(t.Context(), testProjectID, fetch)
			})
		}

		<-fetchStarted
		close(releaseFetch)
		wg.Wait()

		assert.Equal(t, int32(1), fetchCount.Load())
		for i := range workers {
			require.NoError(t, errs[i])
			assert.Equal(t, expected, results[i])
		}
	})

	t.Run("cancelled waiter returns immediately without breaking other concurrent waiters", func(t *testing.T) {
		t.Parallel()
		cache := NewInventoryCache(5*time.Minute, time.Now, nil, 5*time.Minute, 5*time.Second)

		fetchStarted := make(chan struct{})
		releaseFetch := make(chan struct{})
		expected := Inventory{ProjectID: testProjectID, Assignments: []PolicyAssignment{createAgentAssignment(nil)}}

		fetch := func(ctx context.Context) (Inventory, error) {
			close(fetchStarted)
			select {
			case <-releaseFetch:
				return expected, nil
			case <-ctx.Done():
				return Inventory{}, ctx.Err()
			}
		}

		ctx1, cancel1 := context.WithCancel(t.Context())
		defer cancel1()
		ctx2, cancel2 := context.WithCancel(t.Context())

		errCh1 := make(chan error, 1)
		resCh1 := make(chan Inventory, 1)
		errCh2 := make(chan error, 1)

		go func() {
			inv, err := cache.Get(ctx1, testProjectID, fetch)
			resCh1 <- inv
			errCh1 <- err
		}()

		<-fetchStarted

		go func() {
			_, err := cache.Get(ctx2, testProjectID, fetch)
			errCh2 <- err
		}()

		// Cancel waiter 2 while fetch is still in flight; it must return context.Canceled immediately.
		cancel2()
		err2 := <-errCh2
		require.ErrorIs(t, err2, context.Canceled)

		// Now release the shared fetch; caller 1 must still succeed.
		close(releaseFetch)
		require.NoError(t, <-errCh1)
		assert.Equal(t, expected, <-resCh1)
	})
}

func TestARMDeploymentPolicyEffectsAndEnrichment(t *testing.T) {
	t.Parallel()

	newEvaluatorWithResponse := func(resultsJSON string) *ARMDeploymentPolicyEvaluator {
		pipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/evaluateDeploymentPolicies") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(resultsJSON)),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(bytes.NewReader(nil)),
				Request:    req,
			}, nil
		})
		return NewARMDeploymentPolicyEvaluator(pipeline, "https://management.azure.com")
	}

	modelCand := []ModelCandidate{{
		DeploymentName: "my-gpt4",
		Name:           "gpt-4o",
		Format:         "OpenAI",
		Version:        "2024-08-06",
	}}

	t.Run("ignores audit-only and missing/non-exact effects even when NonCompliant", func(t *testing.T) {
		t.Parallel()
		eval := newEvaluatorWithResponse(`{
			"results": {
				"my-gpt4": {
					"evaluationOutcome": "NonCompliant",
					"nonCompliantAssignments": [
						{"assignmentId": "/subscriptions/sub/policyAssignments/audit-1", "effect": "Audit"},
						{"assignmentId": "/subscriptions/sub/policyAssignments/empty-1", "effect": ""},
						{"assignmentId": "/subscriptions/sub/policyAssignments/prefix-1", "effect": "denyAction"}
					]
				}
			}
		}`)
		evalRes, err := eval.EvaluateModelDeployments(t.Context(), testProjectID, modelCand)
		require.NoError(t, err)
		assert.Empty(t, evalRes.Violations)
	})

	t.Run("emits violations only for Deny assignments in mixed and multi-deny responses and enriches display name", func(t *testing.T) {
		t.Parallel()
		eval := newEvaluatorWithResponse(`{
			"results": {
				"my-gpt4": {
					"evaluationOutcome": "NonCompliant",
					"errorMessage": "Deployment 'my-gpt4' is non-compliant with policy '/subscriptions/sub/policyAssignments/audit-only'",
					"nonCompliantAssignments": [
						{"assignmentId": "/subscriptions/sub/policyAssignments/audit-only", "effect": "Audit"},
						{"assignmentId": "/subscriptions/sub/policyAssignments/deny-one", "effect": "Deny"},
						{"assignmentId": "/subscriptions/sub/policyAssignments/deny-two", "effect": "deny"}
					]
				}
			}
		}`)

		inv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/deny-one",
					AssignmentDisplayName: "Human Readable Deny One",
				},
			},
		}
		res := EvaluateCandidate(
			t.Context(),
			inv,
			Candidate{
				ProjectResourceID: testProjectID,
				Models:            modelCand,
			},
			eval,
		)
		require.Equal(t, OutcomeBlocked, res.Outcome)
		require.Len(t, res.Violations, 2)
		assert.Equal(t, "/subscriptions/sub/policyassignments/deny-one", res.Violations[0].AssignmentID)
		assert.Equal(t, "Human Readable Deny One", res.Violations[0].DisplayName)
		assert.Contains(t, res.Violations[0].Message, "Human Readable Deny One")
		assert.NotContains(t, res.Violations[0].Message, "audit-only")
		assert.Equal(t, "/subscriptions/sub/policyassignments/deny-two", res.Violations[1].AssignmentID)
		assert.Equal(t, "deny-two", res.Violations[1].DisplayName)
		assert.NotContains(t, res.Violations[1].Message, "audit-only")
	})

	t.Run("preserves both violations when one assignment denies two different models", func(t *testing.T) {
		t.Parallel()
		eval := newEvaluatorWithResponse(`{
			"results": {
				"chat-dep": {
					"evaluationOutcome": "NonCompliant",
					"nonCompliantAssignments": [
						{"assignmentId": "/subscriptions/sub/policyAssignments/approve-models", "effect": "Deny"}
					]
				},
				"embed-dep": {
					"evaluationOutcome": "NonCompliant",
					"nonCompliantAssignments": [
						{"assignmentId": "/subscriptions/sub/policyAssignments/approve-models", "effect": "Deny"}
					]
				}
			}
		}`)

		res := EvaluateCandidate(
			t.Context(),
			Inventory{ProjectID: testProjectID},
			Candidate{
				ProjectResourceID: testProjectID,
				Models: []ModelCandidate{
					{DeploymentName: "chat-dep", Name: "gpt-35-turbo", Format: "OpenAI", Version: "0613"},
					{DeploymentName: "embed-dep", Name: "text-embedding-ada-002", Format: "OpenAI", Version: "2"},
				},
			},
			eval,
		)
		require.Equal(t, OutcomeBlocked, res.Outcome)
		require.Len(t, res.Violations, 2)
		assert.Equal(t, "gpt-35-turbo (version 0613)", res.Violations[0].TargetValue)
		assert.Equal(t, "text-embedding-ada-002 (version 2)", res.Violations[1].TargetValue)
	})

	t.Run("preserves ARM violations for two versions of the same model while deduplicating against inventory violations", func(t *testing.T) {
		t.Parallel()
		eval := newEvaluatorWithResponse(`{
			"results": {
				"chat-v1": {
					"evaluationOutcome": "NonCompliant",
					"nonCompliantAssignments": [
						{"assignmentId": "/subscriptions/sub/policyAssignments/approve-models", "effect": "Deny"}
					]
				},
				"chat-v2": {
					"evaluationOutcome": "NonCompliant",
					"nonCompliantAssignments": [
						{"assignmentId": "/subscriptions/sub/policyAssignments/approve-models", "effect": "Deny"}
					]
				}
			}
		}`)

		models := []ModelCandidate{
			{DeploymentName: "chat-v1", Name: "gpt-4o", Format: "OpenAI", Version: "2024-05-13"},
			{DeploymentName: "chat-v2", Name: "gpt-4o", Format: "OpenAI", Version: "2024-08-06"},
		}

		// When inventory has no violation for approve-models, both version-specific ARM violations are preserved.
		resBothARM := EvaluateCandidate(
			t.Context(),
			Inventory{ProjectID: testProjectID},
			Candidate{ProjectResourceID: testProjectID, Models: models},
			eval,
		)
		require.Equal(t, OutcomeBlocked, resBothARM.Outcome)
		require.Len(t, resBothARM.Violations, 2)
		assert.Equal(t, "gpt-4o (version 2024-05-13)", resBothARM.Violations[0].TargetValue)
		assert.Equal(t, "gpt-4o (version 2024-08-06)", resBothARM.Violations[1].TargetValue)

		// When inventory already reported approve-models for gpt-4o, ARM violations for the same assignment & model deduplicate.
		invWithDeny := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/approve-models",
					AssignmentDisplayName: "Deny GPT-4o",
					DefinitionDisplayName: "[Preview]: Microsoft Foundry agents should not use denied models",
					AssignmentParameters: map[string]any{
						"effect":       map[string]any{"value": "Deny"},
						"deniedModels": map[string]any{"value": []any{"gpt-4o"}},
					},
					EnforcementMode: "Default",
				},
			},
		}
		resDeduped := EvaluateCandidate(
			t.Context(),
			invWithDeny,
			Candidate{ProjectResourceID: testProjectID, Models: models[:1]},
			eval,
		)
		require.Equal(t, OutcomeBlocked, resDeduped.Outcome)
		require.Len(t, resDeduped.Violations, 1)
		assert.Equal(t, "gpt-4o", resDeduped.Violations[0].TargetValue)
	})
}

func TestModelCandidateMergingAndAllowedModelsAliasHandling(t *testing.T) {
	t.Parallel()

	t.Run("merges sparse candidate preceding complete candidate without ARM GET", func(t *testing.T) {
		t.Parallel()
		var getCalls int
		var postBody evaluateDeploymentPoliciesRequest
		pipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				getCalls++
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(bytes.NewReader(nil)),
					Request:    req,
				}, nil
			}
			if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/evaluateDeploymentPolicies") {
				_ = json.NewDecoder(req.Body).Decode(&postBody)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"results":{"my-dep":{"evaluationOutcome":"Compliant"}}}`)),
					Request:    req,
				}, nil
			}
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil)), Request: req}, nil
		})

		eval := NewARMDeploymentPolicyEvaluator(pipeline, "https://management.azure.com")
		evalRes, err := eval.EvaluateModelDeployments(t.Context(), testProjectID, []ModelCandidate{
			{DeploymentName: "my-dep"},
			{DeploymentName: "my-dep", Name: "gpt-4o", Format: "OpenAI", Version: "2024-08-06"},
		})
		require.NoError(t, err)
		assert.Empty(t, evalRes.Violations)
		assert.Empty(t, evalRes.IncompleteReason)
		assert.Equal(t, 0, getCalls, "expected merged candidate to avoid ARM GET for deployment metadata")
		require.Len(t, postBody.Deployments, 1)
		assert.Equal(t, "my-dep", postBody.Deployments[0].Name)
		assert.Equal(t, "gpt-4o", postBody.Deployments[0].Properties.Model.Name)
		assert.Equal(t, "OpenAI", postBody.Deployments[0].Properties.Model.Format)
		assert.Equal(t, "2024-08-06", postBody.Deployments[0].Properties.Model.Version)
	})

	t.Run("allowedModels does not falsely block deployment-only alias and marks indeterminate when unresolved", func(t *testing.T) {
		t.Parallel()
		inv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/allow-gpt4o",
					AssignmentDisplayName: "Allow only GPT-4o",
					DefinitionDisplayName: "[Preview]: Microsoft Foundry agents should only use allowed models",
					AssignmentParameters: map[string]any{
						"effect":        map[string]any{"value": "Deny"},
						"allowedModels": map[string]any{"value": []any{"gpt-4o"}},
					},
					EnforcementMode: "Default",
				},
			},
		}

		// Sparse candidate with only deployment alias "production-chat" must NOT be blocked by allowedModels.
		resAliasOnly := EvaluateModelInventoryPolicy(inv, []ModelCandidate{
			{DeploymentName: "production-chat"},
		})
		assert.Equal(t, OutcomeIndeterminate, resAliasOnly.Outcome)
		assert.Empty(t, resAliasOnly.Violations)

		// Enriched candidate with DeploymentName "production-chat" and underlying Name "gpt-4o" is allowed.
		resAllowed := EvaluateModelInventoryPolicy(inv, []ModelCandidate{
			{DeploymentName: "production-chat"},
			{DeploymentName: "production-chat", Name: "gpt-4o", Format: "OpenAI", Version: "2024-08-06"},
		})
		assert.Equal(t, OutcomeAllowed, resAllowed.Outcome)
		assert.Empty(t, resAllowed.Violations)

		// Enriched candidate with underlying Name "gpt-35-turbo" is blocked.
		resBlocked := EvaluateModelInventoryPolicy(inv, []ModelCandidate{
			{DeploymentName: "production-chat", Name: "gpt-35-turbo"},
		})
		assert.Equal(t, OutcomeBlocked, resBlocked.Outcome)
		require.Len(t, resBlocked.Violations, 1)
	})

	t.Run("EvaluateCandidate preserves OutcomeIndeterminate when ARM model evaluation fails", func(t *testing.T) {
		t.Parallel()
		failingEval := NewARMDeploymentPolicyEvaluator(newTestPipeline(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("server error")),
				Request:    req,
			}, nil
		}), "https://management.azure.com")

		res := EvaluateCandidate(
			t.Context(),
			Inventory{ProjectID: testProjectID},
			Candidate{
				ProjectResourceID: testProjectID,
				Models: []ModelCandidate{{
					DeploymentName: "my-dep",
					Name:           "gpt-4o",
					Format:         "OpenAI",
					Version:        "2024-08-06",
				}},
			},
			failingEval,
		)
		assert.Equal(t, OutcomeIndeterminate, res.Outcome)
		assert.Contains(t, res.IncompleteReason, "model deployment policy evaluation failed")
	})

	t.Run("re-evaluates deniedModels and allowedModels inventory rules with ARM-resolved model names", func(t *testing.T) {
		t.Parallel()
		pipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/deployments/production-chat") {
				body := `{"properties":{"model":{"format":"OpenAI","name":"gpt-4o","version":"2024-08-06"}}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			}
			if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/evaluateDeploymentPolicies") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"results":{"production-chat":{"evaluationOutcome":"Compliant"}}}`)),
					Request:    req,
				}, nil
			}
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil)), Request: req}, nil
		})
		eval := NewARMDeploymentPolicyEvaluator(pipeline, "https://management.azure.com")

		deniedInv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/deny-gpt4o",
					AssignmentDisplayName: "Deny GPT-4o",
					DefinitionDisplayName: "[Preview]: Microsoft Foundry agents should not use denied models",
					AssignmentParameters: map[string]any{
						"effect":       map[string]any{"value": "Deny"},
						"deniedModels": map[string]any{"value": []any{"gpt-4o"}},
					},
					EnforcementMode: "Default",
				},
			},
		}

		// Without ARM resolution, deployment alias "production-chat" alone is indeterminate against deniedModels.
		noARMDenied := EvaluateModelInventoryPolicy(deniedInv, []ModelCandidate{{DeploymentName: "production-chat"}})
		assert.Equal(t, OutcomeIndeterminate, noARMDenied.Outcome)

		// With ARM resolution of "production-chat" -> "gpt-4o", inventory deniedModels blocks it.
		resDenied := EvaluateCandidate(
			t.Context(),
			deniedInv,
			Candidate{
				ProjectResourceID: testProjectID,
				Models:            []ModelCandidate{{DeploymentName: "production-chat"}},
			},
			eval,
		)
		assert.Equal(t, OutcomeBlocked, resDenied.Outcome)
		require.Len(t, resDenied.Violations, 1)
		assert.Equal(t, "Deny GPT-4o", resDenied.Violations[0].DisplayName)

		allowedInv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/allow-gpt4o",
					AssignmentDisplayName: "Allow GPT-4o",
					DefinitionDisplayName: "[Preview]: Microsoft Foundry agents should only use allowed models",
					AssignmentParameters: map[string]any{
						"effect":        map[string]any{"value": "Deny"},
						"allowedModels": map[string]any{"value": []any{"gpt-4o"}},
					},
					EnforcementMode: "Default",
				},
			},
		}

		// With ARM resolution of "production-chat" -> "gpt-4o", inventory allowedModels is satisfied.
		resAllowed := EvaluateCandidate(
			t.Context(),
			allowedInv,
			Candidate{
				ProjectResourceID: testProjectID,
				Models:            []ModelCandidate{{DeploymentName: "production-chat"}},
			},
			eval,
		)
		assert.Equal(t, OutcomeAllowed, resAllowed.Outcome)
		assert.Empty(t, resAllowed.Violations)

		// False-block prevention: partial local candidate with conflicting Name ("gpt-35-turbo", no format/version)
		// is overridden by authoritative ARM GET metadata ("gpt-4o") and allowed by allowedModels: ["gpt-4o"].
		resOverrideAllowed := EvaluateCandidate(
			t.Context(),
			allowedInv,
			Candidate{
				ProjectResourceID: testProjectID,
				Models:            []ModelCandidate{{DeploymentName: "production-chat", Name: "gpt-35-turbo"}},
			},
			eval,
		)
		assert.Equal(t, OutcomeAllowed, resOverrideAllowed.Outcome)
		assert.Empty(t, resOverrideAllowed.Violations)

		// Missed-denial prevention: partial local candidate with Name "gpt-4o" (no format/version)
		// is overridden when ARM GET resolves "gpt-35-turbo", which is blocked by deniedModels: ["gpt-35-turbo"].
		evalResolves35 := NewARMDeploymentPolicyEvaluator(newTestPipeline(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/deployments/production-chat") {
				body := `{"properties":{"model":{"format":"OpenAI","name":"gpt-35-turbo","version":"0613"}}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			}
			if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/evaluateDeploymentPolicies") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"results":{"production-chat":{"evaluationOutcome":"Compliant"}}}`)),
					Request:    req,
				}, nil
			}
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil)), Request: req}, nil
		}), "https://management.azure.com")

		denied35Inv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          "/subscriptions/sub/policyassignments/deny-gpt35",
					AssignmentDisplayName: "Deny GPT-3.5 Turbo",
					DefinitionDisplayName: "[Preview]: Microsoft Foundry agents should not use denied models",
					AssignmentParameters: map[string]any{
						"effect":       map[string]any{"value": "Deny"},
						"deniedModels": map[string]any{"value": []any{"gpt-35-turbo"}},
					},
					EnforcementMode: "Default",
				},
			},
		}
		resOverrideBlocked := EvaluateCandidate(
			t.Context(),
			denied35Inv,
			Candidate{
				ProjectResourceID: testProjectID,
				Models:            []ModelCandidate{{DeploymentName: "production-chat", Name: "gpt-4o"}},
			},
			evalResolves35,
		)
		assert.Equal(t, OutcomeBlocked, resOverrideBlocked.Outcome)
		require.Len(t, resOverrideBlocked.Violations, 1)
		assert.Equal(t, "Deny GPT-3.5 Turbo", resOverrideBlocked.Violations[0].DisplayName)
	})

	t.Run("handles metadata GET failure, partial GET failure with Deny block, missing results entry, and Compliant outcome", func(t *testing.T) {
		t.Parallel()

		// 1. Metadata GET failure -> OutcomeIndeterminate
		getFailEval := NewARMDeploymentPolicyEvaluator(newTestPipeline(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("not found")),
				Request:    req,
			}, nil
		}), "https://management.azure.com")
		resGetFail := EvaluateCandidate(
			t.Context(),
			Inventory{ProjectID: testProjectID},
			Candidate{
				ProjectResourceID: testProjectID,
				Models:            []ModelCandidate{{DeploymentName: "missing-dep"}},
			},
			getFailEval,
		)
		assert.Equal(t, OutcomeIndeterminate, resGetFail.Outcome)
		assert.Contains(t, resGetFail.IncompleteReason, "could not resolve model metadata")

		// 2. Partial failure: 1 model fails GET, 1 model blocked by Deny -> OutcomeBlocked with violation preserved
		partialEval := NewARMDeploymentPolicyEvaluator(newTestPipeline(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("not found")),
					Request:    req,
				}, nil
			}
			if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/evaluateDeploymentPolicies") {
				body := `{
					"results": {
						"known-dep": {
							"evaluationOutcome": "NonCompliant",
							"nonCompliantAssignments": [
								{"assignmentId": "/subscriptions/sub/policyAssignments/deny-known", "effect": "Deny"}
							]
						}
					}
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			}
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil)), Request: req}, nil
		}), "https://management.azure.com")
		resPartial := EvaluateCandidate(
			t.Context(),
			Inventory{ProjectID: testProjectID},
			Candidate{
				ProjectResourceID: testProjectID,
				Models: []ModelCandidate{
					{DeploymentName: "missing-dep"},
					{DeploymentName: "known-dep", Name: "gpt-4o", Format: "OpenAI", Version: "2024-08-06"},
				},
			},
			partialEval,
		)
		assert.Equal(t, OutcomeBlocked, resPartial.Outcome)
		require.Len(t, resPartial.Violations, 1)
		assert.Equal(t, "/subscriptions/sub/policyassignments/deny-known", resPartial.Violations[0].AssignmentID)

		// 3. Missing deployment entry in results -> OutcomeIndeterminate
		missingResEval := NewARMDeploymentPolicyEvaluator(newTestPipeline(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"results":{}}`)),
				Request:    req,
			}, nil
		}), "https://management.azure.com")
		resMissing := EvaluateCandidate(
			t.Context(),
			Inventory{ProjectID: testProjectID},
			Candidate{
				ProjectResourceID: testProjectID,
				Models: []ModelCandidate{
					{DeploymentName: "known-dep", Name: "gpt-4o", Format: "OpenAI", Version: "2024-08-06"},
				},
			},
			missingResEval,
		)
		assert.Equal(t, OutcomeIndeterminate, resMissing.Outcome)
		assert.Contains(t, resMissing.IncompleteReason, "omitted deployment")

		// 4. Complete "Compliant" response -> OutcomeAllowed
		compliantEval := NewARMDeploymentPolicyEvaluator(newTestPipeline(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"results":{"known-dep":{"evaluationOutcome":"Compliant"}}}`)),
				Request:    req,
			}, nil
		}), "https://management.azure.com")
		resCompliant := EvaluateCandidate(
			t.Context(),
			Inventory{ProjectID: testProjectID},
			Candidate{
				ProjectResourceID: testProjectID,
				Models: []ModelCandidate{
					{DeploymentName: "known-dep", Name: "gpt-4o", Format: "OpenAI", Version: "2024-08-06"},
				},
			},
			compliantEval,
		)
		assert.Equal(t, OutcomeAllowed, resCompliant.Outcome)
		assert.Empty(t, resCompliant.Violations)
		assert.Empty(t, resCompliant.IncompleteReason)
	})
}

func TestResourceGraphJSONAndDataEnvelopeValidation(t *testing.T) {
	t.Parallel()

	runQueryWithBody := func(body string) ([]json.RawMessage, error, []string) {
		var logs []string
		pipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		})
		q := NewResourceGraphQueryFunc(pipeline, "https://management.azure.com", func(msg string) {
			logs = append(logs, msg)
		})
		rows, err := q(t.Context(), ResourceGraphRequest{Subscriptions: []string{"sub"}, Query: "resources"})
		return rows, err, logs
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"data":[invalid`},
		{name: "null top-level body", body: `null`},
		{name: "missing data field", body: `{}`},
		{name: "null data field", body: `{"data":null}`},
		{name: "null data.rows field", body: `{"data":{"rows":null}}`},
		{name: "number data shape", body: `{"data":123}`},
		{name: "string data shape", body: `{"data":"unexpected"}`},
		{name: "object data without rows", body: `{"data":{"columns":[]}}`},
		{name: "object data with non-array rows", body: `{"data":{"rows":"bad"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err, logs := runQueryWithBody(tc.body)
			require.Error(t, err)
			assert.Nil(t, rows)
			assert.NotEmpty(t, logs)
		})
	}

	t.Run("valid empty array and object-with-rows shapes succeed", func(t *testing.T) {
		rows, err, logs := runQueryWithBody(`{"data":[]}`)
		require.NoError(t, err)
		assert.Empty(t, rows)
		assert.Empty(t, logs)

		rowsEmptyObj, errEmptyObj, logsEmptyObj := runQueryWithBody(`{"data":{"rows":[]}}`)
		require.NoError(t, errEmptyObj)
		assert.Empty(t, rowsEmptyObj)
		assert.Empty(t, logsEmptyObj)

		rows2, err2, logs2 := runQueryWithBody(`{"data":{"rows":[{"id":"one"}]}}`)
		require.NoError(t, err2)
		require.Len(t, rows2, 1)
		assert.Empty(t, logs2)
	})
}
