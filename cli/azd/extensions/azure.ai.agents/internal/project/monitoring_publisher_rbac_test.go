// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	publisherTestComponent = "/subscriptions/sub-123/resourceGroups/rg/providers/microsoft.insights/components/appi"
	publisherTestPrincipal = "11111111-2222-3333-4444-555555555555"
)

func publisherTestEnv() map[string]string {
	return map[string]string{
		"AZD_FOUNDRY_MONITORING_MODE":            "create",
		"AZD_MONITORING_PRIVATE_LINK_SCOPE_ID":   "/subscriptions/sub-123/resourceGroups/rg/providers/microsoft.insights/privateLinkScopes/ampls",
		"AZURE_APPLICATION_INSIGHTS_RESOURCE_ID": publisherTestComponent,
	}
}

func neverAssign(t *testing.T) monitoringRoleAssigner {
	return func(context.Context, string, string, string, string, armauthorization.PrincipalType) (bool, error) {
		t.Fatal("role must not be assigned")
		return false, nil
	}
}

func TestMonitoringPublisherRoleID(t *testing.T) {
	assert.Equal(t, "3913510d-42f4-4e42-8a64-420c390055eb", roleMonitoringMetricsPublisher)
}

func TestGrantHostedAgentMonitoringPublisherAssignsRole(t *testing.T) {
	calls := 0
	assign := func(
		_ context.Context, principalID, roleID, _, scope string, principalType armauthorization.PrincipalType,
	) (bool, error) {
		calls++
		assert.Equal(t, publisherTestPrincipal, principalID)
		assert.Equal(t, roleMonitoringMetricsPublisher, roleID)
		assert.Equal(t, publisherTestComponent, scope)
		assert.Equal(t, armauthorization.PrincipalTypeServicePrincipal, principalType)
		return true, nil
	}
	touches := 0
	touch := func(_ context.Context, componentID string) error {
		touches++
		assert.Equal(t, publisherTestComponent, componentID)
		return nil
	}

	warning := grantHostedAgentMonitoringPublisher(
		t.Context(), assign, touch, publisherTestEnv(), " "+publisherTestPrincipal+" ",
	)

	assert.Empty(t, warning)
	assert.Equal(t, 1, calls)
	assert.Equal(t, 1, touches)
}

func TestGrantHostedAgentMonitoringPublisherSkips(t *testing.T) {
	without := func(key string) map[string]string {
		env := publisherTestEnv()
		delete(env, key)
		return env
	}
	tests := map[string]struct {
		env       map[string]string
		principal string
	}{
		"no monitoring mode":        {env: without("AZD_FOUNDRY_MONITORING_MODE"), principal: publisherTestPrincipal},
		"no component":              {env: without("AZURE_APPLICATION_INSIGHTS_RESOURCE_ID"), principal: publisherTestPrincipal},
		"public network (no scope)": {env: without("AZD_MONITORING_PRIVATE_LINK_SCOPE_ID"), principal: publisherTestPrincipal},
		"no principal":              {env: publisherTestEnv(), principal: " "},
		"user supplied component": {
			env: func() map[string]string {
				env := publisherTestEnv()
				env["AZD_FOUNDRY_MONITORING_MODE"] = "reuse-connect"
				return env
			}(),
			principal: publisherTestPrincipal,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			warning := grantHostedAgentMonitoringPublisher(t.Context(), neverAssign(t), nil, tt.env, tt.principal)
			assert.Empty(t, warning)
		})
	}
}

func TestGrantHostedAgentMonitoringPublisherHonorsSkipRoleAssignments(t *testing.T) {
	t.Run("environment value", func(t *testing.T) {
		env := publisherTestEnv()
		env["AZD_AGENT_SKIP_ROLE_ASSIGNMENTS"] = "true"

		warning := grantHostedAgentMonitoringPublisher(t.Context(), neverAssign(t), nil, env, publisherTestPrincipal)

		assert.Contains(t, warning, "AZD_AGENT_SKIP_ROLE_ASSIGNMENTS")
		assert.Contains(t, warning, "az role assignment create")
		assert.Contains(t, warning, publisherTestPrincipal)
	})
	t.Run("process fallback", func(t *testing.T) {
		t.Setenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS", "1")

		warning := grantHostedAgentMonitoringPublisher(
			t.Context(), neverAssign(t), nil, publisherTestEnv(), publisherTestPrincipal,
		)

		assert.Contains(t, warning, "AZD_AGENT_SKIP_ROLE_ASSIGNMENTS")
	})
	t.Run("environment value wins over process", func(t *testing.T) {
		t.Setenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS", "true")
		env := publisherTestEnv()
		env["AZD_AGENT_SKIP_ROLE_ASSIGNMENTS"] = "false"
		calls := 0
		assign := func(context.Context, string, string, string, string, armauthorization.PrincipalType) (bool, error) {
			calls++
			return false, nil
		}

		warning := grantHostedAgentMonitoringPublisher(t.Context(), assign, nil, env, publisherTestPrincipal)

		assert.Empty(t, warning)
		assert.Equal(t, 1, calls)
	})
}

func TestGrantHostedAgentMonitoringPublisherTouchesOnlyNewAssignments(t *testing.T) {
	assign := func(context.Context, string, string, string, string, armauthorization.PrincipalType) (bool, error) {
		return false, nil
	}
	touch := func(context.Context, string) error {
		t.Fatal("component must not be touched when the role already existed")
		return nil
	}

	warning := grantHostedAgentMonitoringPublisher(t.Context(), assign, touch, publisherTestEnv(), publisherTestPrincipal)

	assert.Empty(t, warning)
}

func TestGrantHostedAgentMonitoringPublisherTouchFailureReturnsWarning(t *testing.T) {
	assign := func(context.Context, string, string, string, string, armauthorization.PrincipalType) (bool, error) {
		return true, nil
	}
	touch := func(context.Context, string) error { return errors.New("denied") }

	warning := grantHostedAgentMonitoringPublisher(t.Context(), assign, touch, publisherTestEnv(), publisherTestPrincipal)

	assert.Contains(t, warning, "could not refresh")
	assert.Contains(t, warning, "denied")
}

func TestGrantHostedAgentMonitoringPublisherRetriesPrincipalNotFound(t *testing.T) {
	previous := monitoringGrantRetryDelay
	monitoringGrantRetryDelay = time.Millisecond
	t.Cleanup(func() { monitoringGrantRetryDelay = previous })

	calls := 0
	assign := func(context.Context, string, string, string, string, armauthorization.PrincipalType) (bool, error) {
		calls++
		if calls < 3 {
			return false, errors.New("PrincipalNotFound: principal does not exist")
		}
		return true, nil
	}

	warning := grantHostedAgentMonitoringPublisher(t.Context(), assign, nil, publisherTestEnv(), publisherTestPrincipal)

	assert.Empty(t, warning)
	assert.Equal(t, 3, calls)
}

func TestGrantHostedAgentMonitoringPublisherFailureReturnsCommand(t *testing.T) {
	calls := 0
	assign := func(context.Context, string, string, string, string, armauthorization.PrincipalType) (bool, error) {
		calls++
		return false, errors.New("AuthorizationFailed")
	}

	warning := grantHostedAgentMonitoringPublisher(t.Context(), assign, nil, publisherTestEnv(), publisherTestPrincipal)

	assert.Equal(t, 1, calls)
	assert.Contains(t, warning, "AuthorizationFailed")
	assert.Contains(t, warning, "az role assignment create --assignee-object-id "+publisherTestPrincipal)
	assert.Contains(t, warning, "--scope "+publisherTestComponent)
}

func TestAppendDeployWarning(t *testing.T) {
	t.Run("appends to last artifact note", func(t *testing.T) {
		result := &azdext.ServiceDeployResult{Artifacts: []*azdext.Artifact{
			{Metadata: map[string]string{"note": "first"}},
			{Metadata: map[string]string{"note": "invoke"}},
		}}

		appendDeployWarning(result, "role missing")

		assert.Equal(t, "first", result.Artifacts[0].Metadata["note"])
		assert.Equal(t, "invoke\n\nWarning: role missing", result.Artifacts[1].Metadata["note"])
	})
	t.Run("creates note and metadata", func(t *testing.T) {
		result := &azdext.ServiceDeployResult{Artifacts: []*azdext.Artifact{{}}}

		appendDeployWarning(result, "role missing")

		require.Equal(t, "Warning: role missing", result.Artifacts[0].Metadata["note"])
	})
	t.Run("empty warning or no artifacts is a no-op", func(t *testing.T) {
		result := &azdext.ServiceDeployResult{Artifacts: []*azdext.Artifact{{}}}
		appendDeployWarning(result, " ")
		assert.Nil(t, result.Artifacts[0].Metadata)
		assert.NotPanics(t, func() { appendDeployWarning(&azdext.ServiceDeployResult{}, "x") })
		assert.NotPanics(t, func() { appendDeployWarning(nil, "x") })
	})
}
