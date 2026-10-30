// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	monitoringTestProject = "/subscriptions/sub/resourceGroups/project-rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	monitoringTestComponent = "/subscriptions/other-sub/resourceGroups/monitoring-rg/providers/" +
		"Microsoft.Insights/components/component"
	monitoringTestWorkspace = "/subscriptions/other-sub/resourceGroups/monitoring-rg/providers/" +
		"Microsoft.OperationalInsights/workspaces/workspace"
	monitoringTestConnectionName = "azd-appinsights-b35f1607b6f65"
)

type monitoringTestCredential struct{}

func (monitoringTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "synthetic-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type monitoringCloudFixture struct {
	projectConnections     []any
	accountConnections     []any
	missingProjectList     bool
	missingAccountList     bool
	componentPolicy        map[string]any
	workspacePolicy        map[string]any
	projectStatus          int
	accountStatus          int
	listStatus             int
	slotStatus             int
	workspaceStatus        int
	groupStatus            int
	plannedStatus          int
	previous               map[string]any
	mu                     sync.Mutex
	writes                 []map[string]any
	plans                  []map[string]any
	requests               []string
	plannedChanges         []any
	slotConnection         any
	legacySlotConnection   any
	groupResources         []any
	ownedComponent         map[string]any
	resourceListStatus     int
	connectionDeleteStatus int
	deletions              []string
	deploymentStatus       int
	deploymentOutputs      map[string]any
	deploymentResources    []any
}

func newMonitoringCloudFixture(t *testing.T, fixture *monitoringCloudFixture) *FoundryProvisioningProvider {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.requests = append(fixture.requests, request.Method+" "+request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		reply := func(status int, value any) {
			writer.WriteHeader(status)
			require.NoError(t, json.NewEncoder(writer).Encode(value))
		}
		failure := func(status int) {
			reply(status, map[string]any{"error": map[string]any{
				"code": "SyntheticReadFailure", "message": "synthetic-secret-must-not-leak",
			}})
		}
		switch {
		case strings.EqualFold(request.URL.Path, "/subscriptions/sub/resourceGroups/adjunct-rg"):
			if request.Method == http.MethodDelete {
				fixture.deletions = append(fixture.deletions, request.URL.Path)
				if fixture.groupStatus != 0 {
					failure(fixture.groupStatus)
					return
				}
				reply(http.StatusOK, map[string]any{})
				return
			}
			require.Equal(t, http.MethodGet, request.Method)
			if fixture.groupStatus != 0 {
				reply(fixture.groupStatus, map[string]any{"error": map[string]any{
					"code": "SyntheticReadFailure", "message": "resource group lookup failed",
				}})
				return
			}
			reply(http.StatusOK, map[string]any{
				"id": request.URL.Path, "tags": map[string]any{"azd-env-name": "test"},
			})
		case strings.EqualFold(request.URL.Path, "/subscriptions/sub/resourceGroups/adjunct-rg/resources"):
			if fixture.resourceListStatus != 0 {
				failure(fixture.resourceListStatus)
				return
			}
			resources := []any{}
			filter := request.URL.Query().Get("$filter")
			for _, resource := range fixture.groupResources {
				value, ok := resource.(map[string]any)
				require.True(t, ok)
				resourceType, ok := value["type"].(string)
				require.True(t, ok)
				if strings.Contains(filter, resourceType) {
					resources = append(resources, resource)
				}
			}
			reply(http.StatusOK, map[string]any{"value": resources})
		case strings.EqualFold(request.URL.Path, "/subscriptions/sub/resourceGroups/adjunct-rg/providers/"+
			"Microsoft.CognitiveServices/accounts"):
			if fixture.groupStatus != 0 {
				failure(fixture.groupStatus)
				return
			}
			reply(http.StatusOK, map[string]any{"value": []any{}})
		case fixture.ownedComponent != nil && request.URL.Path == fixture.ownedComponent["id"]:
			reply(http.StatusOK, fixture.ownedComponent)
		case strings.EqualFold(request.URL.Path, "/subscriptions/sub/resourceGroups/adjunct-rg/providers/"+
			"Microsoft.Insights/components/owned"):
			failure(http.StatusNotFound)
		case strings.Contains(request.URL.Path, "/providers/Microsoft.Resources/deployments/"):
			if request.Method == http.MethodGet {
				if fixture.previous != nil {
					reply(http.StatusOK, map[string]any{"properties": fixture.previous})
					return
				}
				failure(http.StatusNotFound)
				return
			}
			if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/exportTemplate") {
				reply(http.StatusOK, map[string]any{"template": fixture.previous["template"]})
				return
			}
			var body map[string]any
			require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
			if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/whatIf") {
				fixture.plans = append(fixture.plans, body)
				reply(http.StatusOK, map[string]any{
					"status": "Succeeded", "properties": map[string]any{"changes": fixture.plannedChanges},
				})
				return
			}
			require.Equal(t, http.MethodPut, request.Method)
			fixture.writes = append(fixture.writes, body)
			if fixture.deploymentStatus != 0 {
				failure(fixture.deploymentStatus)
				return
			}
			reply(http.StatusOK, map[string]any{
				"id": request.URL.Path,
				"properties": map[string]any{
					"provisioningState": "Succeeded",
					"outputs":           fixture.deploymentOutputs, "outputResources": fixture.deploymentResources,
				},
			})
		case request.URL.Path == monitoringTestProject:
			if fixture.projectStatus != 0 {
				failure(fixture.projectStatus)
				return
			}
			reply(http.StatusOK, map[string]any{"id": request.URL.Path, "properties": map[string]any{}})
		case request.URL.Path == monitoringTestProject+"/connections":
			if fixture.listStatus != 0 {
				failure(fixture.listStatus)
				return
			}
			if fixture.missingProjectList {
				reply(http.StatusOK, map[string]any{})
				return
			}
			connections := fixture.projectConnections
			if connections == nil {
				connections = []any{}
			}
			reply(http.StatusOK, map[string]any{"value": connections})
		case strings.HasSuffix(request.URL.Path, "/projects/project/connections/azd-appinsights-project"):
			if fixture.legacySlotConnection != nil {
				reply(http.StatusOK, fixture.legacySlotConnection)
				return
			}
			failure(http.StatusNotFound)
		case strings.HasSuffix(request.URL.Path, "/projects/project/connections/"+monitoringTestConnectionName):
			if request.Method == http.MethodDelete {
				fixture.deletions = append(fixture.deletions, request.URL.Path)
				if fixture.connectionDeleteStatus != 0 {
					failure(fixture.connectionDeleteStatus)
					return
				}
				writer.WriteHeader(http.StatusNoContent)
				return
			}
			if fixture.slotConnection != nil {
				reply(http.StatusOK, fixture.slotConnection)
				return
			}
			status := http.StatusNotFound
			if fixture.slotStatus != 0 {
				status = fixture.slotStatus
			}
			failure(status)
		case strings.HasSuffix(request.URL.Path, "/accounts/account/connections"):
			if fixture.missingAccountList {
				reply(http.StatusOK, map[string]any{})
				return
			}
			connections := fixture.accountConnections
			if connections == nil {
				connections = []any{}
			}
			reply(http.StatusOK, map[string]any{"value": connections})
		case strings.HasSuffix(request.URL.Path, "/accounts/account"):
			if fixture.accountStatus != 0 {
				failure(fixture.accountStatus)
				return
			}
			reply(http.StatusOK, map[string]any{"id": request.URL.Path, "properties": map[string]any{
				"disableLocalAuth": true, "publicNetworkAccess": "Enabled",
			}})
		case strings.HasSuffix(request.URL.Path, "/components/new"), strings.HasSuffix(request.URL.Path, "/workspaces/new"):
			status := http.StatusNotFound
			if fixture.plannedStatus != 0 {
				status = fixture.plannedStatus
			}
			if status == http.StatusOK {
				reply(status, map[string]any{"id": request.URL.Path, "properties": map[string]any{
					"WorkspaceResourceId": strings.Replace(
						request.URL.Path, "/Microsoft.Insights/components/new", "/Microsoft.OperationalInsights/workspaces/new", 1),
					"ConnectionString": "synthetic-secret-must-not-leak",
				}})
				return
			}
			failure(status)
		case request.URL.Path == monitoringTestComponent:
			properties := map[string]any{
				"WorkspaceResourceId": monitoringTestWorkspace, "AppId": "synthetic-app-id",
				"ConnectionString": "synthetic-secret-must-not-leak",
			}
			maps.Copy(properties, fixture.componentPolicy)
			reply(http.StatusOK, map[string]any{"id": request.URL.Path, "properties": properties})
		case request.URL.Path == monitoringTestWorkspace:
			if fixture.workspaceStatus != 0 {
				failure(fixture.workspaceStatus)
				return
			}
			properties := map[string]any{}
			maps.Copy(properties, fixture.workspacePolicy)
			reply(http.StatusOK, map[string]any{"id": request.URL.Path, "properties": properties})
		default:
			t.Errorf("unexpected monitoring request %s %s", request.Method, request.URL.Path)
			failure(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	return &FoundryProvisioningProvider{
		projectPath: t.TempDir(), subID: "sub", envName: "test", location: "westus3",
		rgName: "adjunct-rg", foundryName: "project", credential: monitoringTestCredential{},
		armClientOptions: &arm.ClientOptions{ClientOptions: policy.ClientOptions{
			Transport: server.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
			Cloud: cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {Endpoint: server.URL, Audience: "https://management.azure.com/"},
			}},
		}},
	}
}

func monitoringTestConnection(name, target, auth string, shared bool) any {
	return map[string]any{
		"name": name, "properties": map[string]any{
			"category": "AppInsights", "authType": auth, "target": target, "isSharedToAll": shared,
		},
	}
}

func monitoringTestSource(mode string) *templateSource {
	return &templateSource{
		mode: templateModeEmbedded,
		parameters: map[string]any{
			"projectResourceId":             map[string]any{"value": monitoringTestProject},
			"monitoringMode":                map[string]any{"value": mode},
			"applicationInsightsResourceId": map[string]any{"value": monitoringTestComponent},
		},
		armTemplate: map[string]any{"$schema": "synthetic", "resources": []any{}},
	}
}

func TestMonitoringPreflightCloudPreservation(t *testing.T) {
	t.Parallel()
	for _, tt := range []*struct {
		name     string
		fixture  monitoringCloudFixture
		wantErr  string
		wantMode string
		wantName string
	}{
		{name: "missing connection uses cross-subscription BYO", wantMode: "reuse-connect"},
		{
			name: "MI connection survives disabled local auth",
			fixture: monitoringCloudFixture{
				projectConnections: []any{monitoringTestConnection("user-monitoring", monitoringTestComponent, "ManagedIdentity", false)},
				componentPolicy:    map[string]any{"DisableLocalAuth": true},
			},
			wantMode: "already-connected", wantName: "user-monitoring",
		},
		{
			name: "shared inherited connection is preserved",
			fixture: monitoringCloudFixture{
				accountConnections: []any{monitoringTestConnection("shared-monitoring", monitoringTestComponent, "AAD", true)},
			},
			wantMode: "already-connected", wantName: "shared-monitoring",
		},
		{
			name: "unshared sibling does not block missing connection",
			fixture: monitoringCloudFixture{
				accountConnections: []any{monitoringTestConnection("azd-appinsights-sibling", monitoringTestComponent, "ApiKey", false)},
			},
			wantMode: "reuse-connect",
		},
		{
			name: "shared same-name destination must not be hidden as a mirror",
			fixture: monitoringCloudFixture{
				projectConnections: []any{monitoringTestConnection("monitoring", monitoringTestComponent, "ApiKey", false)},
				accountConnections: []any{monitoringTestConnection("monitoring", monitoringTestComponent+"-other", "ApiKey", true)},
			},
			wantErr: "conflicting destinations",
		},
		{
			name: "actual account mirror is not a second connection",
			fixture: monitoringCloudFixture{
				projectConnections: []any{monitoringTestConnection("monitoring", monitoringTestComponent, "ApiKey", false)},
				accountConnections: []any{monitoringTestConnection("monitoring", monitoringTestComponent, "ApiKey", false)},
			},
			wantMode: "already-connected", wantName: "monitoring",
		},
		{
			name: "reserved account name is protected",
			fixture: monitoringCloudFixture{
				accountConnections: []any{monitoringTestConnection(monitoringTestConnectionName, monitoringTestComponent, "ApiKey", false)},
			},
			wantErr: "protected",
		},
		{
			name: "different selected component conflicts",
			fixture: monitoringCloudFixture{
				projectConnections: []any{monitoringTestConnection("user-monitoring", monitoringTestComponent+"-other", "ApiKey", false)},
			},
			wantErr: "conflicts",
		},
		{
			name:    "project read denied is not absence",
			fixture: monitoringCloudFixture{projectStatus: http.StatusForbidden}, wantErr: "HTTP 403",
		},
		{
			name:    "failed list is not absence",
			fixture: monitoringCloudFixture{listStatus: http.StatusServiceUnavailable}, wantErr: "HTTP 503",
		},
		{
			name:    "failed reserved-name lookup is not absence",
			fixture: monitoringCloudFixture{slotStatus: http.StatusForbidden}, wantErr: "HTTP 403",
		},
		{
			name:    "disabled local auth blocks a new ApiKey",
			fixture: monitoringCloudFixture{componentPolicy: map[string]any{"DisableLocalAuth": true}},
			wantErr: "disabled local authentication",
		},
		{
			name:    "restricted ingestion is not weakened",
			fixture: monitoringCloudFixture{componentPolicy: map[string]any{"publicNetworkAccessForIngestion": "Disabled"}},
			wantErr: "restricted public ingestion",
		},
		{
			name:    "workspace access is required",
			fixture: monitoringCloudFixture{workspaceStatus: http.StatusForbidden}, wantErr: "HTTP 403",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newMonitoringCloudFixture(t, &tt.fixture)
			client, err := p.deploymentsClient(t.Context())
			require.NoError(t, err)
			source := monitoringTestSource("reuse-connect")
			err = p.prepareMonitoring(t.Context(), source, client, func(string) {})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.NotContains(t, err.Error(), "synthetic-secret-must-not-leak")
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantMode, source.parameters["monitoringMode"].(map[string]any)["value"])
				if tt.wantName != "" {
					assert.Equal(t, tt.wantName, source.parameters["monitoringConnectionName"].(map[string]any)["value"])
					assert.Equal(t, false, source.parameters["monitoringCreateConnection"].(map[string]any)["value"])
				}
			}
			tt.fixture.mu.Lock()
			defer tt.fixture.mu.Unlock()
			assert.Empty(t, tt.fixture.writes, "preflight performs no deployment or connection writes")
		})
	}
}

func TestMonitoringPreflightRunsBeforeDeployment(t *testing.T) {
	t.Parallel()
	for _, preview := range []bool{false, true} {
		for _, disabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("preview=%t/disabled-auth=%t", preview, disabled), func(t *testing.T) {
				t.Parallel()
				fixture := monitoringCloudFixture{componentPolicy: map[string]any{"DisableLocalAuth": disabled}}
				p := newMonitoringCloudFixture(t, &fixture)
				p.onDiskSource = monitoringTestSource("reuse-connect")
				p.onDiskSource.mode = templateModeBicep
				var err error
				if preview {
					_, err = p.Preview(t.Context(), func(string) {})
				} else {
					_, err = p.Deploy(t.Context(), func(string) {})
				}
				fixture.mu.Lock()
				defer fixture.mu.Unlock()
				if disabled {
					require.ErrorContains(t, err, "disabled local authentication")
					assert.Empty(t, fixture.writes, "unsafe setup must fail before the root deployment or final What-If")
					return
				}
				require.NoError(t, err)
				submissions := fixture.writes
				if preview {
					assert.Empty(t, fixture.writes, "preview never submits a deployment")
					submissions = fixture.plans
				}
				require.Len(t, submissions, 1, "one ordinary root deployment or final What-If, not a second monitoring deployment")
				properties := submissions[0]["properties"].(map[string]any)
				parameters := properties["parameters"].(map[string]any)
				assert.Equal(t, "reuse-connect", parameters["monitoringMode"].(map[string]any)["value"])
			})
		}
	}
}

func TestMonitoringPreflightImplicitExistingProjectIsNotConsent(t *testing.T) {
	t.Parallel()
	fixture := monitoringCloudFixture{}
	p := newMonitoringCloudFixture(t, &fixture)
	p.synthResult = &synthesis.Result{MonitoringExplicit: false}
	source := monitoringTestSource("create")
	client, err := p.deploymentsClient(t.Context())
	require.NoError(t, err)
	require.NoError(t, p.prepareMonitoring(t.Context(), source, client, func(string) {}))
	assert.Equal(t, "none", source.parameters["monitoringMode"].(map[string]any)["value"])
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	assert.Empty(t, fixture.writes)
	assert.Len(t, fixture.requests, 3, "only deployment binding, What-If and project identity are read; no setup or discovery")
}

func TestMonitoringPreflightOwnedExistingProjectRetainsCreateGraph(t *testing.T) {
	t.Parallel()
	source := monitoringTestSource("create")
	for name, value := range map[string]string{
		"location": "westus3", "resourceGroupName": "monitoring-rg", "resourceTokenSalt": "test",
	} {
		source.parameters[name] = map[string]any{"value": value}
	}
	fixture := monitoringCloudFixture{
		projectConnections: []any{monitoringTestConnection(monitoringTestConnectionName, monitoringTestComponent, "ApiKey", false)},
		plannedChanges: []any{
			map[string]any{"resourceId": monitoringTestComponent, "changeType": "NoChange"},
			map[string]any{"resourceId": monitoringTestWorkspace, "changeType": "NoChange"},
		},
		previous: map[string]any{
			"provisioningState": "Succeeded", "parameters": source.parameters,
			"outputs": map[string]any{
				"AZURE_AI_PROJECT_ID":                       map[string]any{"type": "String", "value": monitoringTestProject},
				"AZURE_APPLICATION_INSIGHTS_RESOURCE_ID":    map[string]any{"type": "String", "value": monitoringTestComponent},
				"AZURE_LOG_ANALYTICS_WORKSPACE_RESOURCE_ID": map[string]any{"type": "String", "value": monitoringTestWorkspace},
			},
		},
	}
	p := newMonitoringCloudFixture(t, &fixture)
	p.subID = "other-sub"
	client, err := p.deploymentsClient(t.Context())
	require.NoError(t, err)
	require.NoError(t, p.prepareMonitoring(t.Context(), source, client, func(string) {}))
	assert.Equal(t, "create", source.parameters["monitoringMode"].(map[string]any)["value"],
		"observing the connection must not drop owned monitoring resources or the shared adjunct group")
	assert.Equal(t, false, source.parameters["monitoringCreateConnection"].(map[string]any)["value"])
}

func TestMonitoringPreflightDoesNotAdoptPlannedResource(t *testing.T) {
	t.Parallel()
	fixture := monitoringCloudFixture{plannedChanges: []any{
		map[string]any{"resourceId": monitoringTestComponent, "changeType": "Modify"},
		map[string]any{"resourceId": monitoringTestWorkspace, "changeType": "Modify"},
	}}
	p := newMonitoringCloudFixture(t, &fixture)
	p.subID = "other-sub"
	source := monitoringTestSource("create")
	source.parameters["resourceGroupName"] = map[string]any{"value": "monitoring-rg"}
	client, err := p.deploymentsClient(t.Context())
	require.NoError(t, err)
	require.ErrorContains(t, p.prepareMonitoring(t.Context(), source, client, func(string) {}), "not owned")
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	assert.Empty(t, fixture.writes)
}

func TestMonitoringPreflightFreshNativeTargetComesFromARM(t *testing.T) {
	t.Parallel()
	component := "/subscriptions/sub/resourceGroups/project-rg/providers/Microsoft.Insights/components/new"
	workspace := "/subscriptions/sub/resourceGroups/project-rg/providers/Microsoft.OperationalInsights/workspaces/new"
	fixture := monitoringCloudFixture{
		projectStatus: http.StatusNotFound, accountStatus: http.StatusNotFound,
		plannedChanges: []any{
			map[string]any{"resourceId": monitoringTestProject, "changeType": "Create"},
			map[string]any{"resourceId": component, "changeType": "Create"},
			map[string]any{"resourceId": workspace, "changeType": "Create"},
		},
	}
	p := newMonitoringCloudFixture(t, &fixture)
	p.synthResult = &synthesis.Result{}
	p.virtualEnv = map[string]string{"AZURE_AI_PROJECT_ID": monitoringTestProject + "-stale"}
	source := monitoringTestSource("create")
	delete(source.parameters, "projectResourceId")
	source.parameters["applicationInsightsResourceId"] = map[string]any{"value": ""}
	for name, value := range map[string]string{
		"location": "westus3", "resourceGroupName": "project-rg", "foundryProjectName": "project", "resourceTokenSalt": "test",
	} {
		source.parameters[name] = map[string]any{"value": value}
	}
	client, err := p.deploymentsClient(t.Context())
	require.NoError(t, err)
	require.NoError(t, p.prepareMonitoring(t.Context(), source, client, func(string) {}))
	assert.Equal(t, "create", source.parameters["monitoringMode"].(map[string]any)["value"])
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	assert.Empty(t, fixture.writes)
	assert.Len(t, fixture.plans, 1)
	assert.Contains(t, fixture.requests, "GET "+monitoringTestProject)
	for _, request := range fixture.requests {
		assert.NotContains(t, request, "-stale")
	}
}

func TestMonitoringPreflightFailedNativeAttemptCanRetryWithoutTrustingOutputs(t *testing.T) {
	t.Parallel()
	source := monitoringTestSource("create")
	delete(source.parameters, "projectResourceId")
	source.parameters["applicationInsightsResourceId"] = map[string]any{"value": ""}
	for name, value := range map[string]string{
		"location": "westus3", "resourceGroupName": "project-rg", "foundryProjectName": "project", "resourceTokenSalt": "test",
	} {
		source.parameters[name] = map[string]any{"value": value}
	}
	fixture := monitoringCloudFixture{
		plannedStatus: http.StatusOK,
		plannedChanges: []any{
			map[string]any{"resourceId": monitoringTestProject, "changeType": "NoChange"},
			map[string]any{
				"resourceId": "/subscriptions/sub/resourceGroups/project-rg/providers/Microsoft.Insights/components/new",
				"changeType": "Modify",
			},
			map[string]any{
				"resourceId": "/subscriptions/sub/resourceGroups/project-rg/providers/Microsoft.OperationalInsights/workspaces/new",
				"changeType": "NoChange",
			},
		},
		previous: map[string]any{
			"provisioningState": "Failed", "parameters": source.parameters, "template": source.armTemplate,
			"outputs": map[string]any{
				"AZURE_AI_PROJECT_ID": map[string]any{"type": "String", "value": monitoringTestProject + "-untrusted"},
			},
		},
	}
	p := newMonitoringCloudFixture(t, &fixture)
	p.synthResult = &synthesis.Result{}
	client, err := p.deploymentsClient(t.Context())
	require.NoError(t, err)
	require.NoError(t, p.prepareMonitoring(t.Context(), source, client, func(string) {}))
	assert.Equal(t, "create", source.parameters["monitoringMode"].(map[string]any)["value"])
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	assert.Empty(t, fixture.writes)
	for _, request := range fixture.requests {
		assert.NotContains(t, request, "-untrusted")
	}
}
