// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/pkg/foundrypolicy"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
)

func TestCheckPolicy(t *testing.T) {
	t.Parallel()

	validProjectID := "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/" +
		"resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"

	passingPriors := []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "remote.auth", Status: StatusPass},
	}

	t.Run("passes when all services comply with policy", func(t *testing.T) {
		t.Parallel()
		check := newCheckPolicy(Dependencies{
			AzdClient: &azdext.AzdClient{},
			readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
				return validProjectID, nil
			},
			probeProjectPolicy: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.ProjectPolicyResult, error) {
				return &project.ProjectPolicyResult{
					ProjectScope: validProjectID,
					AccountName:  "acct",
					ProjectName:  "proj",
					Services: []project.ServicePolicyFinding{
						{ServiceName: "assistant", AgentKind: "prompt"},
					},
				}, nil
			},
		})

		got := check.Fn(t.Context(), Options{}, passingPriors)
		assert.Equal(t, StatusPass, got.Status)
		assert.Contains(t, got.Message, "comply with active Foundry Azure Policy")
	})

	t.Run("fails and redacts scope, links, and embedded message/targetValue tokens by default", func(t *testing.T) {
		t.Parallel()
		deepLink := "https://ai.azure.com/nextgen/r/route/manage/govern/assignments/key?subscriptionId=1756abc0-3554-4341-8d6a-46674962ea19"
		rawMsg := "Deployment blocked by /subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/deny-hosted " +
			"under /providers/Microsoft.Management/managementGroups/corp-mg (see https://ai.azure.com/nextgen/r/secret/manage/govern)."
		rawTargetVal := "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/deployments/gpt-4o"

		check := newCheckPolicy(Dependencies{
			AzdClient: &azdext.AzdClient{},
			readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
				return validProjectID, nil
			},
			probeProjectPolicy: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.ProjectPolicyResult, error) {
				return &project.ProjectPolicyResult{
					ProjectScope: validProjectID,
					AccountName:  "acct",
					ProjectName:  "proj",
					Services: []project.ServicePolicyFinding{
						{
							ServiceName: "hosted-svc",
							AgentKind:   "hosted",
							Outcome:     foundrypolicy.OutcomeBlocked,
							Violations: []foundrypolicy.Violation{
								{
									Target:       foundrypolicy.TargetAgentKind,
									TargetValue:  rawTargetVal,
									AssignmentID: "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/deny-hosted",
									DisplayName:  "Block Hosted Agents",
									PortalURL:    deepLink,
									Message:      rawMsg,
								},
							},
						},
					},
				}, nil
			},
		})

		redactedRes := check.Fn(t.Context(), Options{Unredacted: false}, passingPriors)
		assert.Equal(t, StatusFail, redactedRes.Status)
		assert.NotContains(t, redactedRes.Message, "1756abc0-3554-4341-8d6a-46674962ea19")
		assert.NotContains(t, redactedRes.Message, "/providers/Microsoft.Management/managementGroups/corp-mg")
		assert.NotContains(t, redactedRes.Message, "https://ai.azure.com/nextgen/r/secret")
		assert.Contains(t, redactedRes.Message, redactedPlaceholder)
		assert.Contains(t, redactedRes.Suggestion, "--unredacted")
		assert.Equal(t, []string{foundryGovernPortalFallback}, redactedRes.Links)
		assert.Equal(t, redactedPlaceholder, redactedRes.Details["projectScope"])
		svcDetails := redactedRes.Details["services"].([]map[string]any)
		violDetails := svcDetails[0]["violations"].([]map[string]any)
		assert.Equal(t, redactedPlaceholder, violDetails[0]["targetValue"])
		assert.NotContains(t, violDetails[0]["message"].(string), "1756abc0-3554-4341-8d6a-46674962ea19")

		unredactedRes := check.Fn(t.Context(), Options{Unredacted: true}, passingPriors)
		assert.Equal(t, StatusFail, unredactedRes.Status)
		assert.Contains(t, unredactedRes.Message, rawMsg)
		assert.NotContains(t, unredactedRes.Suggestion, "--unredacted")
		assert.Equal(t, []string{deepLink}, unredactedRes.Links)
		assert.Equal(t, validProjectID, unredactedRes.Details["projectScope"])
		unredactedSvcDetails := unredactedRes.Details["services"].([]map[string]any)
		unredactedViolDetails := unredactedSvcDetails[0]["violations"].([]map[string]any)
		assert.Equal(t, rawTargetVal, unredactedViolDetails[0]["targetValue"])
	})

	t.Run("skips when a service has OutcomeIndeterminate from registry-only image policy or ARM model evaluation error", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name   string
			reason string
		}{
			{
				name:   "registry-only image policy with repository pattern",
				reason: "container image policy uses repository/tag patterns that cannot be verified from registry endpoint alone",
			},
			{
				name:   "ARM model deployment policy evaluation error",
				reason: "model deployment policy evaluation failed: evaluateDeploymentPolicies returned HTTP 500",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				check := newCheckPolicy(Dependencies{
					AzdClient: &azdext.AzdClient{},
					readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
						return validProjectID, nil
					},
					probeProjectPolicy: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.ProjectPolicyResult, error) {
						return &project.ProjectPolicyResult{
							ProjectScope: validProjectID,
							AccountName:  "acct",
							ProjectName:  "proj",
							Services: []project.ServicePolicyFinding{
								{
									ServiceName:      "my-agent",
									AgentKind:        "hosted",
									Outcome:          foundrypolicy.OutcomeIndeterminate,
									IncompleteReason: tc.reason,
								},
							},
						}, nil
					},
				})

				got := check.Fn(t.Context(), Options{}, passingPriors)
				assert.Equal(t, StatusSkip, got.Status)
				assert.Contains(t, got.Message, "Azure Policy compliance could not be fully verified")
				assert.NotContains(t, got.Message, "offline")
				assert.Contains(t, got.Message, tc.reason)
			})
		}
	})

	t.Run("skips when probe fails or upstream auth fails", func(t *testing.T) {
		t.Parallel()
		check := newCheckPolicy(Dependencies{
			AzdClient: &azdext.AzdClient{},
			readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
				return validProjectID, nil
			},
			probeProjectPolicy: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.ProjectPolicyResult, error) {
				return nil, errors.New("Resource Graph unavailable")
			},
		})

		got := check.Fn(t.Context(), Options{}, passingPriors)
		assert.Equal(t, StatusSkip, got.Status)
		assert.Contains(t, got.Message, "could not evaluate Foundry Azure Policy")

		blockedPrior := []Result{
			{ID: "local.environment-selected", Status: StatusPass},
			{ID: "remote.auth", Status: StatusFail},
		}
		gotBlocked := check.Fn(t.Context(), Options{}, blockedPrior)
		assert.Equal(t, StatusSkip, gotBlocked.Status)
		assert.Contains(t, gotBlocked.Message, "remote.auth")
	})
}
