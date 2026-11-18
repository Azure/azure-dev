// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSub       = "00000000-0000-0000-0000-000000000001"
	testProjectID = "/subscriptions/" + testSub +
		"/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"
	testAccountID   = "/subscriptions/" + testSub + "/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct"
	testComponentID = "/subscriptions/" + testSub + "/resourceGroups/mon/providers/Microsoft.Insights/components/ai"
	testOtherID     = "/subscriptions/" + testSub + "/resourceGroups/mon/providers/Microsoft.Insights/components/other"
	testWorkspaceID = "/subscriptions/" + testSub +
		"/resourceGroups/mon/providers/Microsoft.OperationalInsights/workspaces/law"
)

type fakeReader struct {
	resources     map[string]*armresources.GenericResource
	resourceErr   error
	project       []MonitoringConnectionRecord
	projectErr    error
	account       []MonitoringConnectionRecord
	accountErr    error
	nameExists    bool
	nameExistsErr error
}

func (f *fakeReader) Resource(
	_ context.Context, id *arm.ResourceID, _ string,
) (*armresources.GenericResource, bool, error) {
	if f.resourceErr != nil {
		return nil, false, f.resourceErr
	}
	resource, ok := f.resources[strings.ToLower(id.String())]
	return resource, ok, nil
}

func (f *fakeReader) ProjectConnections(context.Context, *arm.ResourceID) ([]MonitoringConnectionRecord, error) {
	return f.project, f.projectErr
}

func (f *fakeReader) AccountConnections(context.Context, *arm.ResourceID) ([]MonitoringConnectionRecord, error) {
	return f.account, f.accountErr
}

func (f *fakeReader) ProjectConnectionExists(context.Context, *arm.ResourceID, string) (bool, error) {
	return f.nameExists, f.nameExistsErr
}

func armResource(id string, properties map[string]any) *armresources.GenericResource {
	return &armresources.GenericResource{ID: &id, Properties: properties}
}

func healthyReader() *fakeReader {
	return &fakeReader{resources: map[string]*armresources.GenericResource{
		strings.ToLower(testProjectID): armResource(testProjectID, map[string]any{}),
		strings.ToLower(testAccountID): armResource(testAccountID, map[string]any{}),
		strings.ToLower(testComponentID): armResource(testComponentID, map[string]any{
			"WorkspaceResourceId": testWorkspaceID, "ConnectionString": "InstrumentationKey=k",
		}),
		strings.ToLower(testWorkspaceID): armResource(testWorkspaceID, map[string]any{}),
	}}
}

func connRecord(name, category, target, auth string, shared *bool) MonitoringConnectionRecord {
	cat := armcognitiveservices.ConnectionCategory(category)
	properties := &armcognitiveservices.ConnectionPropertiesV2{Category: &cat, IsSharedToAll: shared}
	if target != "" {
		properties.Target = &target
	}
	if auth != "" {
		authType := armcognitiveservices.ConnectionAuthType(auth)
		properties.AuthType = &authType
	}
	return MonitoringConnectionRecord{Name: name, Properties: properties}
}

func boolPtr(value bool) *bool { return &value }

func TestMonitoringConnectionNameIsStableAndBounded(t *testing.T) {
	t.Parallel()
	name := MonitoringConnectionName("Proj")
	assert.Equal(t, name, MonitoringConnectionName("proj"))
	assert.NotEqual(t, name, MonitoringConnectionName("proj2"))
	assert.True(t, strings.HasPrefix(name, "azd-appinsights-"))
	assert.Len(t, name, len("azd-appinsights-")+13)
}

func TestMonitoringResourceID(t *testing.T) {
	t.Parallel()
	_, err := MonitoringResourceID(testComponentID, "Microsoft.Insights/components")
	require.NoError(t, err)
	for _, bad := range []string{"", "not-an-id", testAccountID} {
		_, err := MonitoringResourceID(bad, "Microsoft.Insights/components")
		assert.Error(t, err, bad)
	}
}

func TestMonitoringReadErrorSanitizes(t *testing.T) {
	t.Parallel()
	secret := errors.New("body contains InstrumentationKey=secret")
	assert.NotContains(t, MonitoringReadError("op", secret).Error(), "secret")
	assert.ErrorIs(t, MonitoringReadError("op", fmt.Errorf("wrap: %w", context.Canceled)), context.Canceled)
	assert.ErrorIs(t, MonitoringReadError("op", context.DeadlineExceeded), context.DeadlineExceeded)
	response := &azcore.ResponseError{StatusCode: 403}
	assert.Contains(t, MonitoringReadError("op", response).Error(), "HTTP 403")
	assert.True(t, monitoringNotFound(&azcore.ResponseError{StatusCode: 404}))
	assert.False(t, monitoringNotFound(response))
	assert.False(t, monitoringNotFound(secret))
}

func TestMonitoringPolicies(t *testing.T) {
	t.Parallel()
	_, err := MonitoringProperties(&armresources.GenericResource{})
	assert.Error(t, err)
	properties, err := MonitoringProperties(armResource("x", map[string]any{"a": 1}))
	require.NoError(t, err)
	assert.Equal(t, 1, properties["a"])

	for name, props := range map[string]map[string]any{
		"local auth disabled":      {"DisableLocalAuth": true},
		"local auth wrong type":    {"DisableLocalAuth": "yes"},
		"features wrong type":      {"features": "x"},
		"workspace local disabled": {"features": map[string]any{"disableLocalAuth": true}},
		"ingestion disabled":       {"publicNetworkAccessForIngestion": "Disabled"},
		"access wrong type":        {"publicNetworkAccess": 5},
		"acls wrong type":          {"networkAcls": "x"},
		"acl deny":                 {"networkAcls": map[string]any{"defaultAction": "Deny"}},
	} {
		assert.Error(t, MonitoringKeyPolicy(props), name)
	}
	assert.NoError(t, MonitoringKeyPolicy(map[string]any{
		"DisableLocalAuth": false, "publicNetworkAccessForIngestion": "enabled",
		"features":    map[string]any{"disableLocalAuth": false},
		"networkAcls": map[string]any{"defaultAction": "Allow"},
	}))
}

func TestValidateReusableAppInsightsConnection(t *testing.T) {
	t.Parallel()
	reserved := MonitoringConnectionName("proj")
	good := connRecord("ai", "AppInsights", testComponentID, "ApiKey", nil)
	isMonitoring, err := validateReusableAppInsightsConnection("ai", reserved, good.Properties)
	require.NoError(t, err)
	assert.True(t, isMonitoring)

	other := connRecord("storage", "AzureStorageAccount", "https://x", "AAD", nil)
	isMonitoring, err = validateReusableAppInsightsConnection("storage", reserved, other.Properties)
	require.NoError(t, err)
	assert.False(t, isMonitoring)

	_, err = validateReusableAppInsightsConnection("x", reserved, nil)
	assert.Error(t, err)
	_, err = validateReusableAppInsightsConnection(reserved, reserved, other.Properties)
	assert.ErrorContains(t, err, "already in use")
	missing := connRecord("ai", "AppInsights", "", "ApiKey", nil)
	_, err = validateReusableAppInsightsConnection("ai", reserved, missing.Properties)
	assert.ErrorContains(t, err, "lacks")
	badTarget := connRecord("ai", "AppInsights", testAccountID, "ApiKey", nil)
	_, err = validateReusableAppInsightsConnection("ai", reserved, badTarget.Properties)
	assert.Error(t, err)
	mismatch := connRecord("ai", "AppInsights", testComponentID, "ApiKey", nil)
	metadataID := testWorkspaceID
	mismatch.Properties.Metadata = map[string]*string{"ResourceId": &metadataID}
	_, err = validateReusableAppInsightsConnection("ai", reserved, mismatch.Properties)
	assert.ErrorContains(t, err, "disagree")
	badAuth := connRecord("ai", "AppInsights", testComponentID, "CustomKeys", nil)
	_, err = validateReusableAppInsightsConnection("ai", reserved, badAuth.Properties)
	assert.ErrorContains(t, err, "authentication")
}

func TestMonitoringResolverResourceIdentity(t *testing.T) {
	t.Parallel()
	componentID, err := arm.ParseResourceID(testComponentID)
	require.NoError(t, err)
	reader := healthyReader()
	resolver := &MonitoringResolver{Reader: reader}
	got, found, err := resolver.Resource(t.Context(), componentID, "v")
	require.NoError(t, err)
	assert.True(t, found)
	assert.NotNil(t, got)

	missing, err := arm.ParseResourceID(testOtherID)
	require.NoError(t, err)
	_, found, err = resolver.Resource(t.Context(), missing, "v")
	require.NoError(t, err)
	assert.False(t, found)

	wrong := "/subscriptions/other"
	reader.resources[strings.ToLower(testComponentID)] = &armresources.GenericResource{ID: &wrong}
	_, _, err = resolver.Resource(t.Context(), componentID, "v")
	assert.ErrorContains(t, err, "unexpected resource identity")

	reader.resourceErr = errors.New("boom")
	_, _, err = resolver.Resource(t.Context(), componentID, "v")
	assert.Error(t, err)
}

func TestMonitoringResolverReaderSelection(t *testing.T) {
	t.Parallel()
	assert.IsType(t, &armMonitoringReader{}, (&MonitoringResolver{}).reader())
	custom := &fakeReader{}
	assert.Same(t, custom, (&MonitoringResolver{Reader: custom}).reader())
}

func TestValidateComponent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*fakeReader)
		id      string
		key     bool
		wantErr string
	}{
		{name: "healthy with key", id: testComponentID, key: true},
		{name: "healthy without key", id: testComponentID},
		{name: "bad id", id: "nope", wantErr: "resource ID"},
		{
			name: "missing component", id: testComponentID, wantErr: "does not exist",
			mutate: func(f *fakeReader) { delete(f.resources, strings.ToLower(testComponentID)) },
		},
		{
			name: "no workspace", id: testComponentID, wantErr: "Log Analytics",
			mutate: func(f *fakeReader) {
				f.resources[strings.ToLower(testComponentID)] = armResource(testComponentID, map[string]any{})
			},
		},
		{
			name: "missing workspace", id: testComponentID, wantErr: "workspace does not exist",
			mutate: func(f *fakeReader) { delete(f.resources, strings.ToLower(testWorkspaceID)) },
		},
		{
			name: "local auth disabled", id: testComponentID, key: true, wantErr: "local authentication",
			mutate: func(f *fakeReader) {
				f.resources[strings.ToLower(testComponentID)] = armResource(testComponentID, map[string]any{
					"WorkspaceResourceId": testWorkspaceID, "ConnectionString": "k", "DisableLocalAuth": true,
				})
			},
		},
		{
			name: "no connection string", id: testComponentID, key: true, wantErr: "no connection string",
			mutate: func(f *fakeReader) {
				f.resources[strings.ToLower(testComponentID)] = armResource(testComponentID, map[string]any{
					"WorkspaceResourceId": testWorkspaceID,
				})
			},
		},
		{
			name: "workspace local auth disabled", id: testComponentID, key: true, wantErr: "workspace local",
			mutate: func(f *fakeReader) {
				f.resources[strings.ToLower(testWorkspaceID)] = armResource(testWorkspaceID, map[string]any{
					"features": map[string]any{"disableLocalAuth": true},
				})
			},
		},
		{
			name: "read failure", id: testComponentID, wantErr: "boom",
			mutate: func(f *fakeReader) { f.resourceErr = errors.New("boom") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := healthyReader()
			if tt.mutate != nil {
				tt.mutate(reader)
			}
			err := (&MonitoringResolver{Reader: reader}).ValidateComponent(t.Context(), tt.id, tt.key)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestDiscoverConnection(t *testing.T) {
	t.Parallel()
	project, err := arm.ParseResourceID(testProjectID)
	require.NoError(t, err)
	reserved := MonitoringConnectionName("proj")
	appInsights := func(name string, shared *bool) MonitoringConnectionRecord {
		return connRecord(name, "AppInsights", testComponentID, "ApiKey", shared)
	}
	storage := func(name string, shared *bool) MonitoringConnectionRecord {
		return connRecord(name, "AzureStorageAccount", "https://x", "AAD", shared)
	}
	tests := []struct {
		name          string
		reader        *fakeReader
		projectExists bool
		accountExists bool
		wantTarget    string
		wantErr       string
	}{
		{name: "none", reader: &fakeReader{}, projectExists: true, accountExists: true},
		{
			name:          "project connection",
			reader:        &fakeReader{project: []MonitoringConnectionRecord{appInsights("a", nil)}},
			projectExists: true, wantTarget: testComponentID,
		},
		{
			name:          "shared account connection",
			reader:        &fakeReader{account: []MonitoringConnectionRecord{appInsights("a", boolPtr(true))}},
			accountExists: true, wantTarget: testComponentID,
		},
		{
			name: "mirrored entry is not double counted",
			reader: &fakeReader{
				project: []MonitoringConnectionRecord{appInsights("a", nil)},
				account: []MonitoringConnectionRecord{appInsights("A", boolPtr(false))},
			},
			projectExists: true, accountExists: true, wantTarget: testComponentID,
		},
		{
			name: "mirrored entry with different target is ambiguous",
			reader: &fakeReader{
				project: []MonitoringConnectionRecord{appInsights("a", nil)},
				account: []MonitoringConnectionRecord{connRecord("a", "AppInsights", testOtherID, "ApiKey", boolPtr(false))},
			},
			projectExists: true, accountExists: true, wantErr: "ambiguous",
		},
		{
			name: "conflicting destinations",
			reader: &fakeReader{project: []MonitoringConnectionRecord{
				appInsights("a", nil), connRecord("b", "AppInsights", testOtherID, "ApiKey", nil),
			}},
			projectExists: true, wantErr: "conflicting",
		},
		{
			name:          "reserved name used by other category",
			reader:        &fakeReader{project: []MonitoringConnectionRecord{storage(reserved, nil)}},
			projectExists: true, wantErr: "already in use",
		},
		{
			name:          "reserved name exists without listing",
			reader:        &fakeReader{nameExists: true},
			projectExists: true, wantErr: "already in use",
		},
		{
			name:          "name check failure",
			reader:        &fakeReader{nameExistsErr: errors.New("boom")},
			projectExists: true, wantErr: "boom",
		},
		{
			name:          "unshared sibling connection ignored",
			reader:        &fakeReader{account: []MonitoringConnectionRecord{storage("sibling", boolPtr(false))}},
			accountExists: true,
		},
		{
			name:          "reserved name protected at account",
			reader:        &fakeReader{account: []MonitoringConnectionRecord{storage(reserved, boolPtr(false))}},
			accountExists: true, wantErr: "protected",
		},
		{
			name:          "restricted account sharing",
			reader:        &fakeReader{account: []MonitoringConnectionRecord{appInsights("a", nil)}},
			accountExists: true, wantErr: "restricted",
		},
		{
			name:          "account connection without properties",
			reader:        &fakeReader{account: []MonitoringConnectionRecord{{Name: "x"}}},
			accountExists: true, wantErr: "unavailable",
		},
		{
			name: "project list failure", reader: &fakeReader{projectErr: errors.New("boom")},
			projectExists: true, wantErr: "boom",
		},
		{
			name: "account list failure", reader: &fakeReader{accountErr: errors.New("boom")},
			accountExists: true, wantErr: "boom",
		},
		{name: "absent scopes are not read", reader: &fakeReader{projectErr: errors.New("boom"), accountErr: errors.New("boom")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (&MonitoringResolver{Reader: tt.reader}).DiscoverConnection(
				t.Context(), project, tt.projectExists, tt.accountExists)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.wantTarget == "" {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantTarget, got.Target)
		})
	}
}

func TestResolveTerraformExport(t *testing.T) {
	t.Parallel()
	reuse := func() map[string]any {
		return map[string]any{"monitoringMode": "reuse-connect", "applicationInsightsResourceId": testComponentID}
	}
	connected := func(f *fakeReader) {
		f.project = []MonitoringConnectionRecord{connRecord("a", "AppInsights", testComponentID, "AAD", nil)}
	}
	tests := []struct {
		name       string
		params     map[string]any
		environ    map[string]string
		project    string
		mutate     func(*fakeReader)
		wantErr    string
		wantMode   string
		wantTarget string
	}{
		{name: "none is a no-op", params: map[string]any{"monitoringMode": "none"}, project: testProjectID, wantMode: "none"},
		{name: "invalid mode", params: map[string]any{"monitoringMode": "bogus"}, project: testProjectID, wantErr: "invalid monitoringMode"},
		{name: "bad project id", params: reuse(), project: "x", wantErr: "ARM resource ID"},
		{
			name: "different saved project", params: reuse(), project: testProjectID,
			environ: map[string]string{"AZURE_AI_PROJECT_ID": testProjectID + "2"}, wantErr: "different Foundry project",
		},
		{
			name:   "previously created monitoring cannot be adopted",
			params: map[string]any{"monitoringMode": "create", "applicationInsightsResourceId": ""},
			environ: map[string]string{
				"AZD_FOUNDRY_MONITORING_MODE": "create", "AZURE_APPLICATION_INSIGHTS_RESOURCE_ID": testComponentID,
			},
			project: testProjectID, wantErr: "cannot adopt",
		},
		{
			name: "component must be a string", params: map[string]any{"monitoringMode": "reuse-connect"},
			project: testProjectID, wantErr: "component resource ID string",
		},
		{
			name:    "environment reference resolves",
			params:  map[string]any{"monitoringMode": "reuse-connect", "applicationInsightsResourceId": "${AI_ID}"},
			environ: map[string]string{"AI_ID": testComponentID}, project: testProjectID, wantMode: "reuse-connect",
		},
		{
			name:    "unresolved reference",
			params:  map[string]any{"monitoringMode": "reuse-connect", "applicationInsightsResourceId": "${MISSING_ID}"},
			project: testProjectID, wantErr: "MISSING_ID",
		},
		{
			name: "project missing", params: reuse(), project: testProjectID, wantErr: "project does not exist",
			mutate: func(f *fakeReader) { delete(f.resources, strings.ToLower(testProjectID)) },
		},
		{
			name: "account missing", params: reuse(), project: testProjectID, wantErr: "account does not exist",
			mutate: func(f *fakeReader) { delete(f.resources, strings.ToLower(testAccountID)) },
		},
		{
			name: "existing connection upgrades to already-connected", params: reuse(), project: testProjectID,
			mutate: connected, wantMode: "already-connected", wantTarget: testComponentID,
		},
		{
			name:    "existing connection fills missing component",
			params:  map[string]any{"monitoringMode": "already-connected", "applicationInsightsResourceId": ""},
			project: testProjectID, mutate: connected, wantMode: "already-connected", wantTarget: testComponentID,
		},
		{
			name: "component conflicts with effective connection", params: reuse(), project: testProjectID,
			wantErr: "conflicts",
			mutate: func(f *fakeReader) {
				f.project = []MonitoringConnectionRecord{connRecord("a", "AppInsights", testOtherID, "AAD", nil)}
			},
		},
		{
			name:    "declared connection absent",
			params:  map[string]any{"monitoringMode": "already-connected", "applicationInsightsResourceId": testComponentID},
			project: testProjectID, wantErr: "was not found",
		},
		{
			name: "restricted project network", params: reuse(), project: testProjectID,
			wantErr: "restricted public ingestion",
			mutate: func(f *fakeReader) {
				f.resources[strings.ToLower(testProjectID)] = armResource(testProjectID, map[string]any{
					"publicNetworkAccess": "Disabled",
				})
			},
		},
		{
			name:    "reuse-connect without component",
			params:  map[string]any{"monitoringMode": "reuse-connect", "applicationInsightsResourceId": ""},
			project: testProjectID, wantErr: "nonempty",
		},
		{
			name: "reuse-connect validates component", params: reuse(), project: testProjectID, wantErr: "does not exist",
			mutate: func(f *fakeReader) { delete(f.resources, strings.ToLower(testComponentID)) },
		},
		{name: "reuse-connect happy path", params: reuse(), project: testProjectID, wantMode: "reuse-connect"},
		{
			name: "read failure", params: reuse(), project: testProjectID, wantErr: "boom",
			mutate: func(f *fakeReader) { f.resourceErr = errors.New("boom") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := healthyReader()
			if tt.mutate != nil {
				tt.mutate(reader)
			}
			environ := tt.environ
			if environ == nil {
				environ = map[string]string{}
			}
			err := (&MonitoringResolver{Reader: reader}).ResolveTerraformExport(
				t.Context(), tt.params, tt.project, environ)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMode, tt.params["monitoringMode"])
			if tt.wantTarget != "" {
				assert.Equal(t, tt.wantTarget, tt.params["applicationInsightsResourceId"])
			}
		})
	}
}

func TestResolveTerraformMonitoringExportSkipsWhenDisabled(t *testing.T) {
	t.Parallel()
	for _, parameters := range []map[string]any{{}, {"monitoringMode": "none"}} {
		require.NoError(t, ResolveTerraformMonitoringExport(t.Context(), parameters, testProjectID, nil, nil))
	}
}
