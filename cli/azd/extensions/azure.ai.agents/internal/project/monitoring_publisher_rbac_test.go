// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/v4"
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
	touch := func(_ context.Context, componentID string, created bool) error {
		touches++
		assert.True(t, created)
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

func TestGrantHostedAgentMonitoringPublisherPassesCreatedFlagToToucher(t *testing.T) {
	assign := func(context.Context, string, string, string, string, armauthorization.PrincipalType) (bool, error) {
		return false, nil
	}
	var got []bool
	touch := func(_ context.Context, _ string, created bool) error {
		got = append(got, created)
		return nil
	}

	warning := grantHostedAgentMonitoringPublisher(t.Context(), assign, touch, publisherTestEnv(), publisherTestPrincipal)

	assert.Empty(t, warning)
	assert.Equal(t, []bool{false}, got)
}

func TestGrantHostedAgentMonitoringPublisherTouchFailureReturnsWarning(t *testing.T) {
	assign := func(context.Context, string, string, string, string, armauthorization.PrincipalType) (bool, error) {
		return true, nil
	}
	touch := func(context.Context, string, bool) error { return errors.New("denied") }

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

func TestMonitoringConstantsArePinned(t *testing.T) {
	// These must match Azure's role definition and the azure.ai.projects provisioning outputs.
	assert.Equal(t, "3913510d-42f4-4e42-8a64-420c390055eb", roleMonitoringMetricsPublisher)
	assert.Equal(t, "Monitoring Metrics Publisher", roleMonitoringMetricsPublisherName)
	assert.Equal(t, "azd-monitoring-role-sync", monitoringRoleSyncTag)
	assert.Equal(t, "AZD_FOUNDRY_MONITORING_MODE", envFoundryMonitoringMode)
	assert.Equal(t, "AZD_MONITORING_PRIVATE_LINK_SCOPE_ID", envMonitoringPrivateLinkScope)
	assert.Equal(t, "AZURE_APPLICATION_INSIGHTS_RESOURCE_ID", envApplicationInsightsResource)
	assert.Equal(t, "AZD_AGENT_SKIP_ROLE_ASSIGNMENTS", envSkipRoleAssignments)
	assert.Equal(t, "create", monitoringModeCreate)
	assert.Equal(t, 4, monitoringGrantAttempts)
	assert.Equal(t, 15*time.Second, defaultMonitoringGrantRetryDelay)
}

func TestMonitoringGrantCommand(t *testing.T) {
	cmd := monitoringGrantCommand(publisherTestPrincipal, publisherTestComponent)
	assert.Equal(t,
		"az role assignment create --assignee-object-id "+publisherTestPrincipal+
			" --assignee-principal-type ServicePrincipal --role \"Monitoring Metrics Publisher\" --scope "+
			publisherTestComponent,
		cmd)
}

func TestAppendDeployWarning(t *testing.T) {
	t.Run("appends to last artifact note", func(t *testing.T) {
		result := &azdext.ServiceDeployResult{Artifacts: []*azdext.Artifact{
			{Metadata: map[string]string{"note": "first"}},
			{Metadata: map[string]string{"note": "invoke"}},
		}}

		appendDeployWarning(result, publisherTestComponent, "role missing")

		assert.Equal(t, "first", result.Artifacts[0].Metadata["note"])
		assert.Equal(t, "invoke\n\nWarning: role missing", result.Artifacts[1].Metadata["note"])
	})
	t.Run("creates note and metadata", func(t *testing.T) {
		result := &azdext.ServiceDeployResult{Artifacts: []*azdext.Artifact{{}}}

		appendDeployWarning(result, publisherTestComponent, "role missing")

		require.Equal(t, "Warning: role missing", result.Artifacts[0].Metadata["note"])
	})
	t.Run("empty warning is a no-op", func(t *testing.T) {
		result := &azdext.ServiceDeployResult{Artifacts: []*azdext.Artifact{{}}}
		appendDeployWarning(result, publisherTestComponent, " ")
		assert.Nil(t, result.Artifacts[0].Metadata)
		assert.NotPanics(t, func() { appendDeployWarning(nil, publisherTestComponent, "x") })
	})
	t.Run("no artifacts gets a fallback artifact carrying the warning", func(t *testing.T) {
		result := &azdext.ServiceDeployResult{}

		appendDeployWarning(result, publisherTestComponent, "role missing")

		require.Len(t, result.Artifacts, 1)
		assert.Equal(t, publisherTestComponent, result.Artifacts[0].Location)
		assert.Equal(t, "Warning: role missing", result.Artifacts[0].Metadata["note"])
	})
	t.Run("no artifacts and no component is a no-op", func(t *testing.T) {
		result := &azdext.ServiceDeployResult{}
		appendDeployWarning(result, "", "role missing")
		assert.Empty(t, result.Artifacts)
	})
}

type fakeTagsClient struct {
	existing  map[string]*string
	getErr    error
	updateErr error
	gets      int
	updates   []armresources.TagsPatchResource
}

func (f *fakeTagsClient) GetAtScope(
	_ context.Context, _ string, _ *armresources.TagsClientGetAtScopeOptions,
) (armresources.TagsClientGetAtScopeResponse, error) {
	f.gets++
	if f.getErr != nil {
		return armresources.TagsClientGetAtScopeResponse{}, f.getErr
	}
	return armresources.TagsClientGetAtScopeResponse{
		TagsResource: armresources.TagsResource{Properties: &armresources.Tags{Tags: f.existing}},
	}, nil
}

func (f *fakeTagsClient) UpdateAtScope(
	_ context.Context, _ string, p armresources.TagsPatchResource, _ *armresources.TagsClientBeginUpdateAtScopeOptions,
) (armresources.TagsClientUpdateAtScopeResponse, error) {
	if f.updateErr != nil {
		return armresources.TagsClientUpdateAtScopeResponse{}, f.updateErr
	}
	f.updates = append(f.updates, p)
	return armresources.TagsClientUpdateAtScopeResponse{}, nil
}

func TestRefreshMonitoringRoleSyncTag(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	synced := map[string]*string{monitoringRoleSyncTag: new("earlier")}

	t.Run("created assignment writes tag without reading", func(t *testing.T) {
		c := &fakeTagsClient{existing: synced}
		require.NoError(t, refreshMonitoringRoleSyncTag(t.Context(), c, publisherTestComponent, true, now))
		assert.Equal(t, 0, c.gets)
		require.Len(t, c.updates, 1)
		assert.Equal(t, "2026-01-02T03:04:05Z", *c.updates[0].Properties.Tags[monitoringRoleSyncTag])
		assert.Equal(t, armresources.TagsPatchOperationMerge, *c.updates[0].Operation)
	})
	t.Run("existing assignment with tag present is a no-op", func(t *testing.T) {
		c := &fakeTagsClient{existing: synced}
		require.NoError(t, refreshMonitoringRoleSyncTag(t.Context(), c, publisherTestComponent, false, now))
		assert.Empty(t, c.updates)
	})
	t.Run("existing assignment with tag missing re-applies it", func(t *testing.T) {
		c := &fakeTagsClient{existing: map[string]*string{"other": new("x")}}
		require.NoError(t, refreshMonitoringRoleSyncTag(t.Context(), c, publisherTestComponent, false, now))
		assert.Len(t, c.updates, 1)
	})
	t.Run("existing assignment with no tags re-applies it", func(t *testing.T) {
		c := &fakeTagsClient{}
		require.NoError(t, refreshMonitoringRoleSyncTag(t.Context(), c, publisherTestComponent, false, now))
		assert.Len(t, c.updates, 1)
	})
	t.Run("read failure is returned without writing", func(t *testing.T) {
		c := &fakeTagsClient{getErr: errors.New("read denied")}
		err := refreshMonitoringRoleSyncTag(t.Context(), c, publisherTestComponent, false, now)
		require.ErrorContains(t, err, "read denied")
		assert.Empty(t, c.updates)
	})
	t.Run("update failure is returned", func(t *testing.T) {
		c := &fakeTagsClient{updateErr: errors.New("write denied")}
		err := refreshMonitoringRoleSyncTag(t.Context(), c, publisherTestComponent, true, now)
		require.ErrorContains(t, err, "write denied")
	})
}

func TestMonitoringTagUpdateWaitsForCompletion(t *testing.T) {
	for _, status := range []string{"Succeeded", "Failed"} {
		t.Run(status, func(t *testing.T) {
			var methods []string
			transport := storageTestTransport(func(request *http.Request) (*http.Response, error) {
				methods = append(methods, request.Method)
				header := http.Header{"Content-Type": {"application/json"}}
				code := http.StatusOK
				body := `{"status":"` + status + `"}`
				if request.Method == http.MethodPatch {
					require.Equal(t, publisherTestComponent+"/providers/Microsoft.Resources/tags/default", request.URL.Path)
					var patch armresources.TagsPatchResource
					require.NoError(t, json.NewDecoder(request.Body).Decode(&patch))
					require.Equal(t, new(armresources.TagsPatchOperationMerge), patch.Operation)
					require.Contains(t, patch.Properties.Tags, monitoringRoleSyncTag)
					code = http.StatusAccepted
					header.Set("Azure-AsyncOperation", "https://management.azure.com/operations/tag-update")
					header.Set("Location", "https://management.azure.com/operations/tag-update/result")
					body = `{}`
				} else if request.URL.Path == "/operations/tag-update" {
					if status == "Failed" {
						body = `{"status":"Failed","error":{"code":"AuthorizationFailed","message":"denied"}}`
					}
				} else {
					require.Equal(t, publisherTestComponent+"/providers/Microsoft.Resources/tags/default", request.URL.Path)
					body = `{"properties":{"tags":{}}}`
				}
				return &http.Response{
					StatusCode: code, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request,
				}, nil
			})
			client, err := armresources.NewTagsClient("sub-123", storageTestCredential{}, &arm.ClientOptions{
				ClientOptions: azcore.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}},
			})
			require.NoError(t, err)

			err = refreshMonitoringRoleSyncTag(
				t.Context(), pollingMonitoringTagsClient{client}, publisherTestComponent, true, time.Now(),
			)

			require.GreaterOrEqual(t, len(methods), 2)
			require.Equal(t, []string{http.MethodPatch, http.MethodGet}, methods[:2])
			if status == "Failed" {
				require.ErrorContains(t, err, "AuthorizationFailed")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
