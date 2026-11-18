// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"net/http"
	"testing"

	"azure.ai.projects/synthesis"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerraformMonitoringExportDiscovery(t *testing.T) {
	t.Parallel()
	for _, tt := range []*struct {
		name        string
		mode        string
		component   string
		fixture     monitoringCloudFixture
		environment map[string]string
		wantMode    string
		wantErr     string
	}{
		{name: "fresh creation", mode: "create", wantMode: "create"},
		{
			name: "missing connection cross-subscription reuse", mode: "reuse-connect",
			component: monitoringTestComponent, wantMode: "reuse-connect",
		},
		{
			name: "preserve selected environment reference", mode: "reuse-connect", component: "${MONITORING_TEST_ID}",
			environment: map[string]string{"MONITORING_TEST_ID": monitoringTestComponent}, wantMode: "reuse-connect",
		},
		{
			name: "compatible managed identity with disabled local auth", mode: "create", wantMode: "already-connected",
			fixture: monitoringCloudFixture{
				projectConnections: []any{monitoringTestConnection("external", monitoringTestComponent, "ManagedIdentity", false)},
				componentPolicy:    map[string]any{"DisableLocalAuth": true},
			},
		},
		{
			name: "compatible shared Entra monitoring", mode: "reuse-connect", component: "${MONITORING_TEST_ID}",
			environment: map[string]string{"MONITORING_TEST_ID": monitoringTestComponent}, wantMode: "already-connected",
			fixture: monitoringCloudFixture{
				accountConnections: []any{monitoringTestConnection("inherited", monitoringTestComponent, "AAD", true)},
			},
		},
		{
			name: "sibling monitoring is not inherited", mode: "create", wantMode: "create",
			fixture: monitoringCloudFixture{
				accountConnections: []any{monitoringTestConnection("azd-appinsights-sibling", monitoringTestComponent, "ApiKey", false)},
			},
		},
		{
			name: "conflicting explicit destination", mode: "reuse-connect", component: monitoringTestComponent + "-different",
			fixture: monitoringCloudFixture{
				projectConnections: []any{monitoringTestConnection("external", monitoringTestComponent, "ApiKey", false)},
			},
			wantErr: "conflicts",
		},
		{
			name: "reserved account name", mode: "create",
			fixture: monitoringCloudFixture{
				accountConnections: []any{monitoringTestConnection(monitoringTestConnectionName, monitoringTestComponent, "ApiKey", false)},
			},
			wantErr: "protected",
		},
		{name: "project missing", mode: "create", fixture: monitoringCloudFixture{projectStatus: 404}, wantErr: "does not exist"},
		{
			name: "project forbidden", mode: "create",
			fixture: monitoringCloudFixture{projectStatus: http.StatusForbidden}, wantErr: "HTTP 403",
		},
		{
			name: "failed discovery", mode: "create",
			fixture: monitoringCloudFixture{listStatus: http.StatusServiceUnavailable}, wantErr: "HTTP 503",
		},
		{
			name: "incomplete project list", mode: "create",
			fixture: monitoringCloudFixture{missingProjectList: true}, wantErr: "incomplete list",
		},
		{
			name: "incomplete account list", mode: "create",
			fixture: monitoringCloudFixture{missingAccountList: true}, wantErr: "incomplete list",
		},
		{
			name: "slot lookup forbidden", mode: "create",
			fixture: monitoringCloudFixture{slotStatus: http.StatusForbidden}, wantErr: "HTTP 403",
		},
		{
			name: "disabled component local auth", mode: "reuse-connect", component: monitoringTestComponent,
			fixture: monitoringCloudFixture{componentPolicy: map[string]any{"DisableLocalAuth": true}},
			wantErr: "disabled local authentication",
		},
		{
			name: "restricted component ingestion", mode: "reuse-connect", component: monitoringTestComponent,
			fixture: monitoringCloudFixture{componentPolicy: map[string]any{"publicNetworkAccessForIngestion": "Disabled"}},
			wantErr: "restricted public ingestion",
		},
		{
			name: "disabled workspace local auth", mode: "reuse-connect", component: monitoringTestComponent,
			fixture: monitoringCloudFixture{workspacePolicy: map[string]any{
				"features": map[string]any{"disableLocalAuth": true},
			}},
			wantErr: "disabled workspace local authentication",
		},
		{
			name: "malformed workspace auth policy", mode: "reuse-connect", component: monitoringTestComponent,
			fixture: monitoringCloudFixture{workspacePolicy: map[string]any{"features": "unavailable"}},
			wantErr: "authentication policy is unavailable",
		},
		{
			name: "workspace read forbidden", mode: "reuse-connect", component: monitoringTestComponent,
			fixture: monitoringCloudFixture{workspaceStatus: http.StatusForbidden}, wantErr: "HTTP 403",
		},
		{
			name: "declared connection missing", mode: "already-connected", component: monitoringTestComponent,
			wantErr: "was not found",
		},
		{
			name: "stale selected project", mode: "create",
			environment: map[string]string{"AZURE_AI_PROJECT_ID": monitoringTestProject + "-different"},
			wantErr:     "different Foundry project",
		},
		{
			name: "native resources are not implicitly imported", mode: "create",
			environment: map[string]string{
				"AZD_FOUNDRY_MONITORING_MODE": "create", "AZURE_APPLICATION_INSIGHTS_RESOURCE_ID": monitoringTestComponent,
			},
			wantErr: "cannot adopt",
		},
		{
			name: "explicit empty reference is not create", mode: "reuse-connect", component: "${MONITORING_TEST_ID}",
			environment: map[string]string{"MONITORING_TEST_ID": ""}, wantErr: "nonempty",
		},
		{name: "disabled monitoring needs no reads", mode: "none", wantMode: "none"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			provider := newMonitoringCloudFixture(t, &tt.fixture)
			parameters := map[string]any{
				"monitoringMode": tt.mode, "applicationInsightsResourceId": tt.component,
			}
			resolver := synthesis.MonitoringResolver{
				Credential: provider.credential, ClientOptions: provider.armClientOptions,
			}
			err := resolver.ResolveTerraformExport(
				t.Context(), parameters, monitoringTestProject, tt.environment)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.NotContains(t, err.Error(), "synthetic-secret-must-not-leak")
				assert.Equal(t, tt.mode, parameters["monitoringMode"], "failed export must not select a different graph")
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantMode, parameters["monitoringMode"])
				if tt.component != "" {
					assert.Equal(t, tt.component, parameters["applicationInsightsResourceId"], "preserve authored references")
				} else if tt.wantMode == "already-connected" {
					assert.Equal(t, monitoringTestComponent, parameters["applicationInsightsResourceId"])
				}
			}
			assert.Empty(t, tt.fixture.writes)
			assert.Empty(t, tt.fixture.deletions)
			for _, request := range tt.fixture.requests {
				assert.Contains(t, request, "GET ")
			}
			if tt.mode == "none" {
				assert.Empty(t, tt.fixture.requests)
			}
		})
	}
}

func TestTerraformMonitoringExportCancellation(t *testing.T) {
	t.Parallel()
	provider := newMonitoringCloudFixture(t, &monitoringCloudFixture{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	resolver := synthesis.MonitoringResolver{Credential: provider.credential, ClientOptions: provider.armClientOptions}
	err := resolver.ResolveTerraformExport(ctx, map[string]any{
		"monitoringMode": "create", "applicationInsightsResourceId": "",
	}, monitoringTestProject, nil)
	assert.ErrorIs(t, err, context.Canceled)
}
