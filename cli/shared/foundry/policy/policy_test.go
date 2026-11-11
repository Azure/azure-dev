// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package policy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azpolicy "github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/shared/foundry/ferrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSubscriptionID = "1756abc0-3554-4341-8d6a-46674962ea19"
	testProjectID      = "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestPipeline(fn roundTripFunc) runtime.Pipeline {
	return runtime.NewPipeline(
		"test",
		"v0.0.0",
		runtime.PipelineOptions{},
		&azpolicy.ClientOptions{
			Transport: &http.Client{Transport: fn},
			Retry:     azpolicy.RetryOptions{MaxRetries: -1},
		},
	)
}

func validSubAssignmentID(name string) string {
	return "/subscriptions/" + testSubscriptionID +
		"/providers/Microsoft.Authorization/policyAssignments/" + name
}

func createAgentAssignment(modify func(*PolicyAssignment)) PolicyAssignment {
	a := PolicyAssignment{
		AssignmentID:          strings.ToLower(validSubAssignmentID("assignment")),
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
		AssignmentID:          strings.ToLower(validSubAssignmentID(kind)),
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

func TestShouldSkipPolicyChecks(t *testing.T) {
	t.Setenv(SkipPolicyChecksEnvKey, "true")
	t.Setenv(LegacySkipPolicyChecksEnvKey, "true")

	assert.False(t, ShouldSkipPolicyChecks(map[string]string{
		SkipPolicyChecksEnvKey: "false",
	}), "explicit env map must take precedence over os.Getenv")

	assert.True(t, ShouldSkipPolicyChecks(map[string]string{
		SkipPolicyChecksEnvKey: "1",
	}))

	assert.True(t, ShouldSkipPolicyChecks(map[string]string{
		LegacySkipPolicyChecksEnvKey: "true",
	}))

	assert.True(t, ShouldSkipPolicyChecks(nil))

	t.Setenv(SkipPolicyChecksEnvKey, "")
	assert.True(t, ShouldSkipPolicyChecks(nil), "falls back to legacy env var when primary is unset")

	t.Setenv(LegacySkipPolicyChecksEnvKey, "false")
	assert.False(t, ShouldSkipPolicyChecks(nil))

	t.Setenv(LegacySkipPolicyChecksEnvKey, "not-a-bool")
	assert.False(t, ShouldSkipPolicyChecks(map[string]string{
		SkipPolicyChecksEnvKey: "invalid-bool",
	}))
}

func TestParseFoundryProjectIDAndIsValidPolicyAssignmentID(t *testing.T) {
	t.Parallel()

	parsed, ok := ParseFoundryProjectID(testProjectID + "/")
	require.True(t, ok)
	assert.Equal(t, testSubscriptionID, parsed.SubscriptionID)
	assert.Equal(t, "rg", parsed.ResourceGroup)
	assert.Equal(t, "account", parsed.ProjectResourceName)
	assert.Equal(t, "project", parsed.ProjectName)

	invalidProjectIDs := []struct {
		name string
		id   string
	}{
		{name: "empty", id: ""},
		{name: "missing leading slash", id: strings.TrimPrefix(testProjectID, "/")},
		{name: "prefixed @attacker", id: "@attacker.example" + testProjectID},
		{name: "prefixed URL", id: "https://attacker.example" + testProjectID},
		{
			name: "non-UUID subscription",
			id: "/subscriptions/not-a-uuid/resourceGroups/rg/providers/" +
				"Microsoft.CognitiveServices/accounts/acc/projects/proj",
		},
		{
			name: "double slash empty segment",
			id: "/subscriptions/" + testSubscriptionID +
				"//resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acc/projects/proj",
		},
		{
			name: "dot segment in resourceGroup",
			id: "/subscriptions/" + testSubscriptionID +
				"/resourceGroups/./providers/Microsoft.CognitiveServices/accounts/acc/projects/proj",
		},
		{
			name: "dot-dot segment in project",
			id: "/subscriptions/" + testSubscriptionID +
				"/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acc/projects/..",
		},
		{
			name: "dot-dot segment in provider",
			id: "/subscriptions/" + testSubscriptionID +
				"/resourceGroups/rg/providers/../accounts/acc/projects/proj",
		},
		{
			name: "wrong resource provider namespace",
			id: "/subscriptions/" + testSubscriptionID +
				"/resourceGroups/rg/providers/Microsoft.Other/accounts/acc/projects/proj",
		},
		{
			name: "extra trailing segment",
			id:   testProjectID + "/extra",
		},
		{
			name: "trailing double slash //",
			id:   testProjectID + "//",
		},
		{
			name: "trailing triple slash ///",
			id:   testProjectID + "///",
		},
	}

	for _, tc := range invalidProjectIDs {
		t.Run("invalid project ID/"+tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ParseFoundryProjectID(tc.id)
			assert.False(t, ok)
			assert.Nil(t, got)
			assert.Equal(
				t,
				defaultFoundryGovernPortalURL,
				BuildPolicyAssignmentPortalURL(tc.id, validSubAssignmentID("valid")),
			)
		})
	}

	validAssignments := []struct {
		name string
		id   string
	}{
		{
			name: "management group scope",
			id: "/providers/Microsoft.Management/managementGroups/corp-mg/" +
				"providers/Microsoft.Authorization/policyAssignments/mg-policy",
		},
		{
			name: "subscription scope",
			id:   validSubAssignmentID("sub-policy"),
		},
		{
			name: "resource group scope",
			id: "/subscriptions/" + testSubscriptionID +
				"/resourceGroups/rg/providers/Microsoft.Authorization/policyAssignments/rg-policy",
		},
		{
			name: "account scope",
			id: "/subscriptions/" + testSubscriptionID +
				"/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/" +
				"providers/Microsoft.Authorization/policyAssignments/account-policy",
		},
		{
			name: "project scope",
			id:   testProjectID + "/providers/Microsoft.Authorization/policyAssignments/project-policy",
		},
		{
			name: "project scope with single trailing slash",
			id:   testProjectID + "/providers/Microsoft.Authorization/policyAssignments/project-policy/",
		},
	}

	for _, tc := range validAssignments {
		t.Run("valid assignment ID/"+tc.name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, IsValidPolicyAssignmentID(tc.id))
			portalURL := BuildPolicyAssignmentPortalURL(testProjectID, tc.id)
			assert.Contains(t, portalURL, "/manage/govern/assignments/")
		})
	}

	invalidAssignments := []struct {
		name string
		id   string
	}{
		{name: "empty", id: ""},
		{name: "missing leading slash", id: strings.TrimPrefix(validSubAssignmentID("p"), "/")},
		{
			name: "non-UUID subscription",
			id:   "/subscriptions/not-a-uuid/providers/Microsoft.Authorization/policyAssignments/deny",
		},
		{
			name: "malformed intermediate scope",
			id: "/subscriptions/" + testSubscriptionID +
				"/badSegment/foo/providers/Microsoft.Authorization/policyAssignments/deny",
		},
		{
			name: "malformed segment between resourceGroups and providers",
			id: "/subscriptions/" + testSubscriptionID + "/resourceGroups/rg/badSegment/foo/" +
				"providers/Microsoft.CognitiveServices/accounts/account/" +
				"providers/Microsoft.Authorization/policyAssignments/deny",
		},
		{
			name: "double slash //",
			id: "/subscriptions/" + testSubscriptionID + "//resourceGroups/rg/" +
				"providers/Microsoft.Authorization/policyAssignments/deny",
		},
		{
			name: "dot segment in assignment name",
			id:   "/subscriptions/" + testSubscriptionID + "/providers/Microsoft.Authorization/policyAssignments/..",
		},
		{
			name: "dot segment in management group",
			id: "/providers/Microsoft.Management/managementGroups/./" +
				"providers/Microsoft.Authorization/policyAssignments/deny",
		},
		{
			name: "forbidden @ character",
			id:   validSubAssignmentID("deny@evil"),
		},
		{
			name: "forbidden ? query character",
			id:   validSubAssignmentID("deny?foo=bar"),
		},
		{
			name: "forbidden # fragment character",
			id:   validSubAssignmentID("deny#frag"),
		},
		{
			name: "forbidden whitespace character",
			id:   validSubAssignmentID("deny policy"),
		},
		{
			name: "trailing double slash //",
			id:   validSubAssignmentID("deny") + "//",
		},
		{
			name: "trailing triple slash ///",
			id:   validSubAssignmentID("deny") + "///",
		},
	}

	for _, tc := range invalidAssignments {
		t.Run("invalid assignment ID/"+tc.name, func(t *testing.T) {
			t.Parallel()
			assert.False(t, IsValidPolicyAssignmentID(tc.id))
			assert.Equal(
				t,
				defaultFoundryGovernPortalURL,
				BuildPolicyAssignmentPortalURL(testProjectID, tc.id),
			)
		})
	}
}

func TestBuildPolicyAssignmentPortalURL(t *testing.T) {
	t.Parallel()

	assignmentID := "/subscriptions/" + testSubscriptionID + "/resourceGroups/rg/" +
		"providers/Microsoft.Authorization/policyAssignments/assignment"

	portalURL := BuildPolicyAssignmentPortalURL(testProjectID, assignmentID)
	parsedURL, err := url.Parse(portalURL)
	require.NoError(t, err)

	segments := strings.Split(parsedURL.EscapedPath(), "/")
	assignmentKey := segments[len(segments)-1]

	assert.Equal(
		t,
		"/nextgen/r/F1arwDVUQ0GNakZnSWLqGQ%2Crg%2C%2Caccount%2Cproject/"+
			"manage/govern/assignments/"+assignmentKey,
		parsedURL.EscapedPath(),
	)

	decoded, err := base64.RawURLEncoding.DecodeString(assignmentKey)
	require.NoError(t, err)
	assert.Equal(t, assignmentID, string(decoded))

	q := parsedURL.Query()
	assert.Equal(t, testSubscriptionID, q.Get("subscriptionId"))
	assert.Equal(
		t,
		"/subscriptions/"+testSubscriptionID+"/resourceGroups/rg",
		q.Get("resourceGroupId"),
	)
	assert.Equal(t, testProjectID, q.Get("projectId"))
	assert.Equal(t, "govern_ignite", q.Get("flight"))

	assert.Equal(
		t,
		"bm90LWEtaGV4LWd1aWQ",
		EncodeSubscriptionIDForPortal("not-a-hex-guid"),
	)
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
	assert.Contains(t, assignQuery, "8791d062-ba96-4c34-b604-8538f7e30ca0")
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
				a.NotScopes = []string{"/subscriptions/" + testSubscriptionID + "/resourceGroups/rg"}
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
				createAgentAssignment(func(a *PolicyAssignment) {
					a.AssignmentID = strings.ToLower(validSubAssignmentID("z"))
				}),
				createAgentAssignment(func(a *PolicyAssignment) {
					a.AssignmentID = strings.ToLower(validSubAssignmentID("a"))
				}),
			},
		}
		res := EvaluateAgentKindPolicy(inv, "prompt")
		require.Equal(t, OutcomeBlocked, res.Outcome)
		require.Len(t, res.Violations, 2)
		assert.Equal(t, strings.ToLower(validSubAssignmentID("a")), res.Violations[0].AssignmentID)
		assert.Equal(t, strings.ToLower(validSubAssignmentID("z")), res.Violations[1].AssignmentID)
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
					AssignmentID:          strings.ToLower(validSubAssignmentID("deny-tools")),
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
					AssignmentID:          strings.ToLower(validSubAssignmentID("deny-live-tools")),
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
					AssignmentID:          strings.ToLower(validSubAssignmentID("approve-live-tools")),
					AssignmentDisplayName: "Approved Tools Only",
					PolicyDefinitionID:    AllowedToolsPolicyID,
					DefinitionDisplayName: "[Preview]: Microsoft Foundry agents and " +
						"toolboxes should only use approved tools",
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

		resApproved := EvaluateToolPolicy(approvedInv, []ToolCandidate{
			{Type: "web_search"},
			{Type: "mcp", Name: "by-endpoint", ServerURL: "https://approved.example.com/v1/mcp"},
			{Type: "mcp", Name: "by-connector", ConnectorID: "approved-connector"},
		})
		assert.Equal(t, OutcomeAllowed, resApproved.Outcome)
		assert.Empty(t, resApproved.Violations)

		resUnapproved := EvaluateToolPolicy(approvedInv, []ToolCandidate{
			{Type: "code_interpreter"},
			{
				Type:        "mcp",
				Name:        "unapproved-mcp",
				ServerURL:   "https://other.example.com/mcp",
				ConnectorID: "other-connector",
			},
		})
		require.Equal(t, OutcomeBlocked, resUnapproved.Outcome)
		require.Len(t, resUnapproved.Violations, 2)

		wildcardTypeInv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          strings.ToLower(validSubAssignmentID("deny-wildcard-type")),
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

		resIndeterminateMCP := EvaluateToolPolicy(approvedInv, []ToolCandidate{
			{Type: "mcp", Name: "toolbox:my-tools", ConnectionID: "approved-connector"},
		})
		assert.Equal(t, OutcomeIndeterminate, resIndeterminateMCP.Outcome)
		assert.Empty(t, resIndeterminateMCP.Violations)
		assert.Contains(t, resIndeterminateMCP.IncompleteReason, "MCP tool endpoint or connector metadata")
	})

	t.Run("evaluates live built-in Approved Models (aafe3651) and Denied Models (8791d062) policies", func(t *testing.T) {
		t.Parallel()
		inv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          strings.ToLower(validSubAssignmentID("approved-models")),
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

		resAsset := EvaluateModelInventoryPolicy(inv, []ModelCandidate{{
			DeploymentName: "chat",
			Name:           "gpt-4.1",
			Format:         "OpenAI",
			Version:        "2025-04-14",
			Publisher:      "OpenAI",
			AssetID:        "azureml://registries/azure-openai/models/gpt-4.1/versions/2025-04-14",
		}})
		assert.Equal(t, OutcomeAllowed, resAsset.Outcome)

		resPublisher := EvaluateModelInventoryPolicy(inv, []ModelCandidate{{
			DeploymentName: "claude",
			Name:           "claude-3-7-sonnet",
			Publisher:      "Anthropic",
		}})
		assert.Equal(t, OutcomeAllowed, resPublisher.Outcome)

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
	})
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
			a.AssignmentID = "/subscriptions/" + testSubscriptionID +
				"/resourceGroups/rg/providers/Microsoft.Authorization/policyAssignments/rg"
			a.AssignmentScope = "/subscriptions/" + testSubscriptionID + "/resourceGroups/rg"
		})
		otherAssignment := createAgentAssignment(func(a *PolicyAssignment) {
			a.AssignmentID = "/subscriptions/" + testSubscriptionID +
				"/resourceGroups/other/providers/Microsoft.Authorization/policyAssignments/other"
			a.AssignmentScope = "/subscriptions/" + testSubscriptionID + "/resourceGroups/other"
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

		inv, err := LoadInventory(t.Context(), testProjectID, testSubscriptionID, queryFn)
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

	t.Run("follows continuation tokens, detects loops, and rejects truncated responses without token", func(t *testing.T) {
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
			Subscriptions: []string{testSubscriptionID},
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
		_, err = truncQueryFn(
			t.Context(),
			ResourceGraphRequest{Subscriptions: []string{testSubscriptionID}, Query: "resources"},
		)
		require.EqualError(t, err, "unable to evaluate Azure Policy for the selected project")
		require.Len(t, logged, 1)
		assert.Contains(t, logged[0], "truncated")

		var loopLogs []string
		loopPipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"first"}],"skipToken":"same-token"}`)),
				Request:    req,
			}, nil
		})
		loopQueryFn := NewResourceGraphQueryFunc(loopPipeline, "https://management.azure.com", func(msg string) {
			loopLogs = append(loopLogs, msg)
		})
		_, err = loopQueryFn(
			t.Context(),
			ResourceGraphRequest{Subscriptions: []string{testSubscriptionID}, Query: "resources"},
		)
		require.EqualError(t, err, "unable to evaluate Azure Policy for the selected project")
		require.Len(t, loopLogs, 1)
		assert.Contains(t, loopLogs[0], "repeated a continuation token")
	})

	t.Run("validates tabular vs array data shapes and rejects malformed envelopes", func(t *testing.T) {
		t.Parallel()
		runQueryWithBody := func(body string) ([]json.RawMessage, []string, error) {
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
			rows, err := q(
				t.Context(),
				ResourceGraphRequest{Subscriptions: []string{testSubscriptionID}, Query: "resources"},
			)
			return rows, logs, err
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
			{name: "object data without rows", body: `{"data":{"columns":[]}}`},
		} {
			rows, logs, err := runQueryWithBody(tc.body)
			require.Error(t, err, tc.name)
			assert.Nil(t, rows)
			assert.NotEmpty(t, logs)
		}

		rowsArr, _, errArr := runQueryWithBody(`{"data":[{"id":"a"}]}`)
		require.NoError(t, errArr)
		require.Len(t, rowsArr, 1)

		rowsTab, _, errTab := runQueryWithBody(`{"data":{"rows":[{"id":"b"}]}}`)
		require.NoError(t, errTab)
		require.Len(t, rowsTab, 1)
	})
}

func TestVerifyPolicyAssignmentsReadAccess(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		statusCode     int
		wantErrContain string
	}{
		{name: "200 OK succeeds", statusCode: http.StatusOK},
		{
			name:           "401 Unauthorized returns authentication error",
			statusCode:     http.StatusUnauthorized,
			wantErrContain: "authentication failed when verifying Azure Policy access (HTTP 401)",
		},
		{
			name:           "403 Forbidden returns permission error",
			statusCode:     http.StatusForbidden,
			wantErrContain: "Microsoft.Authorization/policyAssignments/read",
		},
		{
			name:           "500 Internal Server Error returns status error",
			statusCode:     http.StatusInternalServerError,
			wantErrContain: "HTTP 500",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, http.MethodGet, req.Method)
				assert.Contains(
					t,
					req.URL.Path,
					"/providers/Microsoft.Authorization/policyAssignments",
				)
				assert.Equal(t, "2023-04-01", req.URL.Query().Get("api-version"))
				assert.Equal(t, "atScope()", req.URL.Query().Get("$filter"))
				assert.Equal(t, "1", req.URL.Query().Get("$top"))
				return &http.Response{
					StatusCode: tc.statusCode,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Request:    req,
				}, nil
			})
			err := VerifyPolicyAssignmentsReadAccess(
				t.Context(),
				pipeline,
				"https://management.azure.com",
				testProjectID,
			)
			if tc.wantErrContain == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErrContain)
			}
		})
	}

	t.Run("rejects invalid or prefixed project ID without invoking HTTP transport", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		pipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
			callCount++
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Request:    req,
			}, nil
		})
		prefixedID := "@attacker.example" + testProjectID
		err := VerifyPolicyAssignmentsReadAccess(t.Context(), pipeline, "https://management.azure.com", prefixedID)
		require.ErrorContains(t, err, "invalid Foundry project resource ID")
		assert.Equal(t, 0, callCount)
	})
}

func TestInventoryCache(t *testing.T) {
	t.Parallel()

	t.Run("deduplicates concurrent requests, honors TTL, and supports stale-while-revalidate", func(t *testing.T) {
		t.Parallel()
		now := time.Unix(1000, 0)
		var mu sync.Mutex
		getNow := func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		}
		advanceNow := func(d time.Duration) {
			mu.Lock()
			now = now.Add(d)
			mu.Unlock()
		}

		cache := NewInventoryCache(5*time.Minute, getNow, nil, 5*time.Minute, 5*time.Second)

		var fetchCount atomic.Int32
		inv := Inventory{ProjectID: testProjectID, Assignments: []PolicyAssignment{createAgentAssignment(nil)}}
		fetch := func(_ context.Context) (Inventory, error) {
			fetchCount.Add(1)
			return inv, nil
		}

		got1, err := cache.Get(t.Context(), testProjectID, fetch)
		require.NoError(t, err)
		advanceNow(5*time.Minute - time.Millisecond)
		got2, err := cache.Get(t.Context(), strings.ToUpper(testProjectID), fetch)
		require.NoError(t, err)
		assert.Equal(t, int32(1), fetchCount.Load())
		assert.Equal(t, got1, got2)

		// Advance past TTL: GetWithFreshness returns Stale immediately and refreshes in background
		advanceNow(2 * time.Millisecond)
		refreshDone := make(chan struct{})
		staleRes, err := cache.GetWithFreshness(t.Context(), testProjectID, func(_ context.Context) (Inventory, error) {
			fetchCount.Add(1)
			close(refreshDone)
			return inv, nil
		})
		require.NoError(t, err)
		assert.Equal(t, FreshnessStale, staleRes.Freshness)
		<-refreshDone
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
			return Inventory{}, errors.New("policy service unavailable")
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
}

func TestARMDeploymentPolicyEvaluator(t *testing.T) {
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

	t.Run("ignores audit-only, missing effects, and Compliant outcome", func(t *testing.T) {
		t.Parallel()
		eval := newEvaluatorWithResponse(fmt.Sprintf(`{
			"results": {
				"my-gpt4": {
					"evaluationOutcome": "NonCompliant",
					"nonCompliantAssignments": [
						{"assignmentId": %q, "effect": "Audit"},
						{"assignmentId": %q, "effect": ""},
						{"assignmentId": %q, "effect": "denyAction"}
					]
				}
			}
		}`, validSubAssignmentID("audit-1"), validSubAssignmentID("empty-1"), validSubAssignmentID("prefix-1")))
		evalRes, err := eval.EvaluateModelDeployments(t.Context(), testProjectID, modelCand)
		require.NoError(t, err)
		assert.Empty(t, evalRes.Violations)

		evalCompliant := newEvaluatorWithResponse(fmt.Sprintf(`{
			"results": {
				"my-gpt4": {
					"evaluationOutcome": "Compliant",
					"nonCompliantAssignments": [
						{"assignmentId": %q, "effect": "Deny"}
					]
				}
			}
		}`, validSubAssignmentID("deny-1")))
		evalResComp, err := evalCompliant.EvaluateModelDeployments(t.Context(), testProjectID, modelCand)
		require.NoError(t, err)
		assert.Empty(t, evalResComp.Violations)
	})

	t.Run("emits violations only for Deny assignments and falls back on invalid assignmentID", func(t *testing.T) {
		t.Parallel()
		denyOneID := validSubAssignmentID("deny-one")
		denyTwoID := validSubAssignmentID("deny-two")
		badID := "/subscriptions/not-a-uuid/providers/Microsoft.Authorization/policyAssignments/bad"
		eval := newEvaluatorWithResponse(fmt.Sprintf(`{
			"results": {
				"my-gpt4": {
					"evaluationOutcome": "NonCompliant",
					"errorMessage": "Disallowed with secret=do-not-leak",
					"nonCompliantAssignments": [
						{"assignmentId": %q, "effect": "Audit"},
						{"assignmentId": %q, "effect": "Deny"},
						{"assignmentId": %q, "effect": "deny"},
						{"assignmentId": %q, "effect": "Deny"}
					]
				}
			}
		}`, validSubAssignmentID("audit-only"), denyOneID, denyTwoID, badID))

		inv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          strings.ToLower(denyOneID),
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
		require.Len(t, res.Violations, 3)
		assert.Equal(t, strings.ToLower(denyOneID), res.Violations[0].AssignmentID)
		assert.Equal(t, "Human Readable Deny One", res.Violations[0].DisplayName)
		assert.Contains(t, res.Violations[0].Message, "Human Readable Deny One")
		assert.NotContains(t, res.Violations[0].Message, "do-not-leak")
		assert.Equal(t, strings.ToLower(denyTwoID), res.Violations[1].AssignmentID)
		assert.Equal(t, "deny-two", res.Violations[1].DisplayName)
		assert.Empty(t, res.Violations[2].AssignmentID)
		assert.Equal(t, "Azure Policy assignment", res.Violations[2].DisplayName)
		assert.Equal(t, defaultFoundryGovernPortalURL, res.Violations[2].PortalURL)
	})

	t.Run("resolves missing metadata via GET deployment", func(t *testing.T) {
		t.Parallel()
		var getCalls int
		denyID := validSubAssignmentID("block-gpt4o")
		pipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/deployments/my-gpt4") {
				getCalls++
				body := `{"properties":{"model":{"format":"OpenAI","name":"gpt-4o","version":"2024-08-06"}}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			}
			if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/evaluateDeploymentPolicies") {
				body := fmt.Sprintf(`{
					"results": {
						"my-gpt4": {
							"evaluationOutcome": "NonCompliant",
							"nonCompliantAssignments": [
								{"assignmentId": %q, "effect": "deny"}
							]
						}
					}
				}`, denyID)
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
		assert.Equal(t, 1, getCalls)
		require.Len(t, res.Violations, 1)
		assert.Equal(t, "gpt-4o (version 2024-08-06)", res.Violations[0].TargetValue)
		assert.Equal(t, "block-gpt4o", res.Violations[0].DisplayName)
		assert.Equal(t, "my-gpt4", res.Violations[0].DeploymentName)
		assert.Equal(t, "OpenAI", res.Violations[0].ModelFormat)
		assert.Equal(t, "gpt-4o", res.Violations[0].ModelName)
		assert.Equal(t, "2024-08-06", res.Violations[0].ModelVersion)
	})

	t.Run("supports authoritative GET resolution and RequireMatchingDeploymentModel", func(t *testing.T) {
		t.Parallel()
		var postBody evaluateDeploymentPoliciesRequest
		pipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet &&
				(strings.Contains(req.URL.Path, "/deployments/mismatched-chat") ||
					strings.Contains(req.URL.Path, "/deployments/matching-chat")) {
				body := `{"properties":{"model":{"format":"OpenAI","name":"gpt-4o","version":"2024-08-06"}}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			}
			if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/evaluateDeploymentPolicies") {
				_ = json.NewDecoder(req.Body).Decode(&postBody)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(
						`{"results":{"mismatched-chat":{"evaluationOutcome":"Compliant"},` +
							`"matching-chat":{"evaluationOutcome":"Compliant"}}}`,
					)),
					Request: req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(bytes.NewReader(nil)),
				Request:    req,
			}, nil
		})

		// 1. Default (RequireMatchingDeploymentModel = false): GET deployment is authoritative
		defaultEval := NewARMDeploymentPolicyEvaluator(pipeline, "https://management.azure.com")
		defaultRes, err := defaultEval.EvaluateModelDeployments(t.Context(), testProjectID, []ModelCandidate{
			{DeploymentName: "mismatched-chat", Name: "gpt-4.1"},
		})
		require.NoError(t, err)
		assert.Empty(t, defaultRes.IncompleteReason)
		require.Len(t, defaultRes.ResolvedModels, 1)
		assert.Equal(t, "gpt-4o", defaultRes.ResolvedModels[0].Name)
		assert.Equal(t, "OpenAI", defaultRes.ResolvedModels[0].Format)
		assert.Equal(t, "2024-08-06", defaultRes.ResolvedModels[0].Version)

		// 2. WithRequireMatchingDeploymentModel(true): rejects mismatched live model name
		matchingEval := NewARMDeploymentPolicyEvaluator(pipeline, "https://management.azure.com").
			WithRequireMatchingDeploymentModel(true)
		evalRes, err := matchingEval.EvaluateModelDeployments(t.Context(), testProjectID, []ModelCandidate{
			{DeploymentName: "mismatched-chat", Name: "gpt-4.1"},
			{DeploymentName: "matching-chat", Name: "gpt-4o"},
		})
		require.NoError(t, err)
		assert.Contains(t, evalRes.IncompleteReason, `could not resolve model metadata for deployment "mismatched-chat"`)
		require.Len(t, postBody.Deployments, 1)
		assert.Equal(t, "matching-chat", postBody.Deployments[0].Name)
		assert.Equal(t, "gpt-4o", postBody.Deployments[0].Properties.Model.Name)
		assert.Equal(t, "OpenAI", postBody.Deployments[0].Properties.Model.Format)
		assert.Equal(t, "2024-08-06", postBody.Deployments[0].Properties.Model.Version)
		require.Len(t, evalRes.ResolvedModels, 2)
		assert.Equal(t, "gpt-4.1", evalRes.ResolvedModels[0].Name)
		assert.Empty(t, evalRes.ResolvedModels[0].Version)
		assert.Equal(t, "gpt-4o", evalRes.ResolvedModels[1].Name)
	})

	t.Run("batches >100 deployments and preserves Deny across partial batch failure", func(t *testing.T) {
		t.Parallel()
		denyID := validSubAssignmentID("approved-models-only")
		var batchCalls atomic.Int32
		pipeline := newTestPipeline(func(req *http.Request) (*http.Response, error) {
			call := batchCalls.Add(1)
			if call == 1 {
				body := fmt.Sprintf(`{
					"results": {
						"dep-0": {
							"evaluationOutcome": "NonCompliant",
							"nonCompliantAssignments": [
								{"assignmentId": %q, "effect": "Deny"}
							]
						}
					}
				}`, denyID)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("server error")),
				Request:    req,
			}, nil
		})

		eval := NewARMDeploymentPolicyEvaluator(pipeline, "https://management.azure.com")
		models := make([]ModelCandidate, 0, 101)
		for i := range 101 {
			models = append(models, ModelCandidate{
				DeploymentName: fmt.Sprintf("dep-%d", i),
				Name:           "gpt-4o",
				Format:         "OpenAI",
				Version:        "2024-08-06",
			})
		}

		evalRes, err := eval.EvaluateModelDeployments(t.Context(), testProjectID, models)
		require.Error(t, err)
		assert.Equal(t, int32(2), batchCalls.Load())
		require.Len(t, evalRes.Violations, 1)
		assert.Equal(t, "approved-models-only", evalRes.Violations[0].DisplayName)

		batchCalls.Store(0)
		candRes := EvaluateCandidate(
			t.Context(),
			Inventory{ProjectID: testProjectID},
			Candidate{ProjectResourceID: testProjectID, Models: models},
			eval,
		)
		assert.Equal(t, OutcomeBlocked, candRes.Outcome)
		require.Len(t, candRes.Violations, 1)
		assert.Equal(t, "approved-models-only", candRes.Violations[0].DisplayName)
	})
}

func TestClassifyDataPlanePolicyErrorAndViolationsToError(t *testing.T) {
	t.Parallel()

	t.Run("classifies runtime 403 policy denial with credential-safe synthesized message", func(t *testing.T) {
		t.Parallel()
		validAssignment := validSubAssignmentID("restrict-foundry-tools")
		body := fmt.Sprintf(`{"error":{"code":"agent_policy_denied",`+
			`"message":"Updating resource is noncompliant with policy %s:\n`+
			`MCP endpoint https://secret.example.com/mcp?api_key=SUPER_SECRET_TOKEN is not allowed (`+
			`Count of Microsoft.CognitiveServices.Data/accounts/projects/toolboxes/tools[*])",`+
			`"additionalInfo":{"policy_assignment_id":%q,"policy_definition_id":%q}}}`,
			validAssignment,
			validAssignment,
			DeniedToolsPolicyID,
		)
		respErr := &azcore.ResponseError{
			StatusCode: http.StatusForbidden,
			ErrorCode:  "agent_policy_denied",
			RawResponse: &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(body)),
			},
		}

		violations, ok := ClassifyDataPlanePolicyError(respErr, testProjectID)
		require.True(t, ok)
		require.Len(t, violations, 1)
		assert.Equal(t, TargetTool, violations[0].Target)
		assert.Equal(t, strings.ToLower(validAssignment), violations[0].AssignmentID)
		assert.Equal(t, "restrict-foundry-tools", violations[0].DisplayName)
		assert.NotContains(t, violations[0].Message, "SUPER_SECRET_TOKEN")
		assert.NotContains(t, violations[0].Message, "secret.example.com")
		assert.Contains(t, violations[0].PortalURL, "/manage/govern/assignments/")

		ferr := ViolationsToError(`toolbox "research"`, ferrors.CodeToolBlockedByPolicy, violations)
		require.NotNil(t, ferr)
		assert.Equal(t, ferrors.CodeToolBlockedByPolicy, ferr.Code)
		assert.Equal(t, ferrors.CategoryValidation, ferr.Category)
		assert.Contains(t, ferr.Message, `toolbox "research" is blocked by Azure Policy`)
		assert.Contains(t, ferr.Suggestion, "View policy (restrict-foundry-tools):")
		assert.NotContains(t, ferr.Message, "SUPER_SECRET_TOKEN")
		assert.NotContains(t, ferr.Suggestion, "SUPER_SECRET_TOKEN")
	})

	t.Run("rejects invalid assignment IDs in ClassifyDataPlanePolicyError", func(t *testing.T) {
		t.Parallel()
		// 1. "forbidden" with invalid assignment ID returns (_, false)
		badForbiddenBody := `{"error":{"code":"forbidden",` +
			`"message":"Operation is noncompliant with policy ` +
			`/subscriptions/not-a-uuid/providers/Microsoft.Authorization/policyAssignments/bad: denied"}}`
		badForbiddenErr := &azcore.ResponseError{
			StatusCode: http.StatusForbidden,
			ErrorCode:  "forbidden",
			RawResponse: &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(badForbiddenBody)),
			},
		}
		violations, ok := ClassifyDataPlanePolicyError(badForbiddenErr, testProjectID)
		assert.False(t, ok)
		assert.Nil(t, violations)

		// 2. "agent_policy_denied" with invalid assignment ID sanitizes AssignmentID/DisplayName
		// and falls back to Govern URL.
		badAgentDeniedBody := `{"error":{"code":"agent_policy_denied",` +
			`"message":"Denied by policy",` +
			`"additionalInfo":{"policyAssignmentId":"https://attacker.example/evil"}}}`
		badAgentDeniedErr := &azcore.ResponseError{
			StatusCode: http.StatusForbidden,
			ErrorCode:  "agent_policy_denied",
			RawResponse: &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(badAgentDeniedBody)),
			},
		}
		violations2, ok2 := ClassifyDataPlanePolicyError(badAgentDeniedErr, testProjectID)
		require.True(t, ok2)
		require.Len(t, violations2, 1)
		assert.Empty(t, violations2[0].AssignmentID)
		assert.Equal(t, "Azure Policy assignment", violations2[0].DisplayName)
		assert.Equal(t, defaultFoundryGovernPortalURL, violations2[0].PortalURL)
	})

	t.Run("ViolationsToError maps targets and deduplicates messages and links", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, ViolationsToError("agent", "", nil))

		vModel := []Violation{
			{
				Target:      TargetModel,
				TargetValue: "gpt-4o",
				DisplayName: "Model Policy",
				PortalURL:   "https://ai.azure.com/link1",
				Message:     `The "Model Policy" policy doesn't allow model "gpt-4o" in this project.`,
			},
			{
				Target:      TargetModel,
				TargetValue: "gpt-4o",
				DisplayName: "Model Policy",
				PortalURL:   "https://ai.azure.com/link1",
				Message:     `The "Model Policy" policy doesn't allow model "gpt-4o" in this project.`,
			},
		}
		errModel := ViolationsToError("", "", vModel)
		require.NotNil(t, errModel)
		assert.Equal(t, ferrors.CodeModelBlockedByPolicy, errModel.Code)
		assert.Equal(t, 1, strings.Count(errModel.Message, `doesn't allow model "gpt-4o"`))
		assert.Equal(t, 1, strings.Count(errModel.Suggestion, "https://ai.azure.com/link1"))

		// Mixed targets fall back to defaultCode (or CodeAgentBlockedByPolicy when empty)
		errMixed := ViolationsToError("service \"chat\"", ferrors.CodeToolboxBlockedByPolicy, []Violation{
			{Target: TargetAgentKind, DisplayName: "A"},
			{Target: TargetTool, DisplayName: "B"},
		})
		require.NotNil(t, errMixed)
		assert.Equal(t, ferrors.CodeToolboxBlockedByPolicy, errMixed.Code)
	})
}

func TestNewARMPipelineDefaults(t *testing.T) {
	t.Parallel()
	p := NewARMPipeline(nil, "", "")
	assert.NotNil(t, p)
}

func TestAdditionalPolicyCoverage(t *testing.T) {
	t.Parallel()

	t.Run("DeniedModelsPolicyID with deniedPublishers, deniedAssetIds, and deniedModels", func(t *testing.T) {
		t.Parallel()
		inv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          strings.ToLower(validSubAssignmentID("deny-models-builtin")),
					AssignmentDisplayName: "Deny Publisher and Asset",
					PolicyDefinitionID:    DeniedModelsPolicyID,
					AssignmentParameters: map[string]any{
						"effect":           map[string]any{"value": "Deny"},
						"deniedPublishers": map[string]any{"value": []any{"BlockedPub"}},
						"deniedAssetIds":   map[string]any{"value": []any{"models/blocked-model"}},
					},
					EnforcementMode: "Default",
				},
			},
		}

		assert.True(t, IsModelBlockedByInventory(inv.Assignments[0], testProjectID, ModelCandidate{
			Name:      "any",
			Publisher: "blockedpub",
		}))
		assert.True(t, IsModelBlockedByInventory(inv.Assignments[0], testProjectID, ModelCandidate{
			Name:      "any",
			Publisher: "SafePub",
			AssetID:   "azureml://registries/r/models/blocked-model/versions/1",
		}))
		assert.Equal(t, OutcomeIndeterminate, EvaluateModelInventoryAssignment(
			inv.Assignments[0],
			testProjectID,
			ModelCandidate{Name: "unknown-meta"},
		))
		assert.Equal(t, OutcomeAllowed, EvaluateModelInventoryAssignment(
			inv.Assignments[0],
			testProjectID,
			ModelCandidate{Name: "ok", Publisher: "SafePub", AssetID: "models/allowed"},
		))

		legacyDenyInv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				{
					AssignmentID:          strings.ToLower(validSubAssignmentID("deny-legacy-model")),
					AssignmentDisplayName: "Deny GPT-35",
					DefinitionDisplayName: "[Preview]: Microsoft Foundry agents should not use denied models",
					AssignmentParameters: map[string]any{
						"effect":       map[string]any{"value": "Deny"},
						"deniedModels": map[string]any{"value": []any{"gpt-35*"}},
					},
					EnforcementMode: "Default",
				},
			},
		}
		violations := EvaluateInventoryModelPolicy(legacyDenyInv, []ModelCandidate{
			{DeploymentName: "chat", Name: "gpt-35-turbo"},
		})
		require.Len(t, violations, 1)
		assert.Equal(t, "gpt-35-turbo", violations[0].TargetValue)
	})

	t.Run("EvaluateCandidate covers agent kind, container image/registry, tools, and models", func(t *testing.T) {
		t.Parallel()
		inv := Inventory{
			ProjectID: testProjectID,
			Assignments: []PolicyAssignment{
				createAgentAssignment(nil),
				createImageAssignment("denied", []string{"registry.example.com/team/*"}, nil),
			},
		}
		res := EvaluateCandidate(
			t.Context(),
			inv,
			Candidate{
				ProjectResourceID: testProjectID,
				AgentKind:         "prompt",
				ContainerRegistry: "registry.example.com",
				Tools:             []ToolCandidate{{Type: "code_interpreter", Name: "ci"}},
				Models:            []ModelCandidate{{DeploymentName: "chat", Name: "gpt-4o"}},
			},
			nil,
		)
		assert.Equal(t, OutcomeBlocked, res.Outcome)
		require.Len(t, res.Violations, 1)
		assert.Contains(t, res.IncompleteReason, "registry endpoint alone")

		// Full image blocked
		resImg := EvaluateCandidate(
			t.Context(),
			inv,
			Candidate{
				ProjectResourceID: testProjectID,
				ContainerImage:    "registry.example.com/team/app:v1",
			},
			nil,
		)
		assert.Equal(t, OutcomeBlocked, resImg.Outcome)
		require.Len(t, resImg.Violations, 1)
		assert.Equal(t, TargetContainerImage, resImg.Violations[0].Target)
	})

	t.Run("ClassifyDataPlanePolicyError regex extraction and target inference", func(t *testing.T) {
		t.Parallel()
		validAssignment := validSubAssignmentID("regex-extracted")
		for _, tc := range []struct {
			defID      string
			msg        string
			wantTarget TargetKind
			wantCode   string
		}{
			{
				defID:      DeniedAgentKindsPolicyID,
				msg:        "noncompliant with policy " + validAssignment + ": agent blocked",
				wantTarget: TargetAgentKind,
				wantCode:   ferrors.CodeAgentBlockedByPolicy,
			},
			{
				defID:      DeniedContainerImagesPolicyID,
				msg:        "noncompliant with policy " + validAssignment + ": image blocked",
				wantTarget: TargetContainerImage,
				wantCode:   ferrors.CodeContainerImageBlockedByPolicy,
			},
			{
				defID:      AllowedModelsPolicyID,
				msg:        "noncompliant with policy " + validAssignment + ": model blocked",
				wantTarget: TargetModel,
				wantCode:   ferrors.CodeModelBlockedByPolicy,
			},
		} {
			body := fmt.Sprintf(
				`{"error":{"code":"forbidden","message":%q,"additionalInfo":{"policyDefinitionId":%q}}}`,
				tc.msg,
				tc.defID,
			)
			respErr := &azcore.ResponseError{
				StatusCode: http.StatusForbidden,
				ErrorCode:  "forbidden",
				RawResponse: &http.Response{
					StatusCode: http.StatusForbidden,
					Body:       io.NopCloser(strings.NewReader(body)),
				},
			}
			v, ok := ClassifyDataPlanePolicyError(respErr, testProjectID)
			require.True(t, ok)
			require.Len(t, v, 1)
			assert.Equal(t, tc.wantTarget, v[0].Target)
			ferr := ViolationsToError("resource", "", v)
			require.NotNil(t, ferr)
			assert.Equal(t, tc.wantCode, ferr.Code)
		}
	})
}
