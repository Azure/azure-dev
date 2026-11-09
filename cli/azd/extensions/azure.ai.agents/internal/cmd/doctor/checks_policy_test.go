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

	t.Run("fails and redacts scope/links by default when a policy blocks an agent service", func(t *testing.T) {
		t.Parallel()
		deepLink := "https://ai.azure.com/nextgen/r/route/manage/govern/assignments/key?subscriptionId=1756abc0-3554-4341-8d6a-46674962ea19"
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
							Violations: []foundrypolicy.Violation{
								{
									Target:       foundrypolicy.TargetAgentKind,
									TargetValue:  "hosted",
									AssignmentID: "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/providers/Microsoft.Authorization/policyAssignments/deny-hosted",
									DisplayName:  "Block Hosted Agents",
									PortalURL:    deepLink,
									Message:      `The "Block Hosted Agents" policy doesn't allow hosted agent creation in this project.`,
								},
							},
						},
					},
				}, nil
			},
		})

		redactedRes := check.Fn(t.Context(), Options{Unredacted: false}, passingPriors)
		assert.Equal(t, StatusFail, redactedRes.Status)
		assert.Contains(t, redactedRes.Message, "Block Hosted Agents")
		assert.Equal(t, []string{foundryGovernPortalFallback}, redactedRes.Links)
		assert.Equal(t, redactedPlaceholder, redactedRes.Details["projectScope"])

		unredactedRes := check.Fn(t.Context(), Options{Unredacted: true}, passingPriors)
		assert.Equal(t, StatusFail, unredactedRes.Status)
		assert.Equal(t, []string{deepLink}, unredactedRes.Links)
		assert.Equal(t, validProjectID, unredactedRes.Details["projectScope"])
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
