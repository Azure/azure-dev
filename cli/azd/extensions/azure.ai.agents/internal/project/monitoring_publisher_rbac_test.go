// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
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
		"AZURE_APPLICATION_INSIGHTS_RESOURCE_ID": publisherTestComponent,
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

	grantHostedAgentMonitoringPublisher(t.Context(), assign, publisherTestEnv(), " "+publisherTestPrincipal+" ")

	assert.Equal(t, 1, calls)
}

func TestGrantHostedAgentMonitoringPublisherSkips(t *testing.T) {
	tests := map[string]struct {
		env       map[string]string
		principal string
	}{
		"no monitoring mode": {
			env:       map[string]string{"AZURE_APPLICATION_INSIGHTS_RESOURCE_ID": publisherTestComponent},
			principal: publisherTestPrincipal,
		},
		"no component": {
			env:       map[string]string{"AZD_FOUNDRY_MONITORING_MODE": "create"},
			principal: publisherTestPrincipal,
		},
		"no principal": {env: publisherTestEnv(), principal: " "},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assign := func(
				context.Context, string, string, string, string, armauthorization.PrincipalType,
			) (bool, error) {
				t.Fatal("role must not be assigned")
				return false, nil
			}
			grantHostedAgentMonitoringPublisher(t.Context(), assign, tt.env, tt.principal)
		})
	}
}

func TestGrantHostedAgentMonitoringPublisherRetriesPrincipalNotFound(t *testing.T) {
	previous := monitoringGrantRetryDelay
	monitoringGrantRetryDelay = time.Millisecond
	t.Cleanup(func() { monitoringGrantRetryDelay = previous })

	calls := 0
	assign := func(
		context.Context, string, string, string, string, armauthorization.PrincipalType,
	) (bool, error) {
		calls++
		if calls < 3 {
			return false, errors.New("PrincipalNotFound: principal does not exist")
		}
		return true, nil
	}

	grantHostedAgentMonitoringPublisher(t.Context(), assign, publisherTestEnv(), publisherTestPrincipal)

	assert.Equal(t, 3, calls)
}

func TestGrantHostedAgentMonitoringPublisherDoesNotRetryOtherErrors(t *testing.T) {
	calls := 0
	assign := func(
		context.Context, string, string, string, string, armauthorization.PrincipalType,
	) (bool, error) {
		calls++
		return false, errors.New("AuthorizationFailed")
	}

	require.NotPanics(t, func() {
		grantHostedAgentMonitoringPublisher(t.Context(), assign, publisherTestEnv(), publisherTestPrincipal)
	})

	assert.Equal(t, 1, calls)
}
