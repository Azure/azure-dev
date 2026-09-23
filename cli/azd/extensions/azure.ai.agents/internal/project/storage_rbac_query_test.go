// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/stretchr/testify/require"
)

const storageTestScope = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/store"

func storageTestAssignment(principal, scope, role string) *armauthorization.RoleAssignment {
	return &armauthorization.RoleAssignment{Properties: &armauthorization.RoleAssignmentProperties{
		PrincipalID: new(principal), Scope: new(scope),
		PrincipalType:    new(armauthorization.PrincipalTypeServicePrincipal),
		RoleDefinitionID: new("/providers/Microsoft.Authorization/roleDefinitions/" + role),
	}}
}

func TestAssessStorageRoles(t *testing.T) {
	t.Parallel()
	conditional := storageTestAssignment("project", storageTestScope, storageBlobContributorRole)
	conditional.Properties.Condition = new("restricted")
	grant := storageTestAssignment("project", storageTestScope, storageBlobContributorRole)
	group := storageTestAssignment("group", storageTestScope, storageBlobContributorRole)
	group.Properties.PrincipalType = new(armauthorization.PrincipalTypeGroup)
	conditionalGroup := storageTestAssignment("group", storageTestScope, storageBlobContributorRole)
	conditionalGroup.Properties.PrincipalType = new(armauthorization.PrincipalTypeGroup)
	conditionalGroup.Properties.Condition = new("restricted")
	readerGroup := storageTestAssignment("group", storageTestScope, "2a2b9908-6ea1-4ae2-8e65-a410df84e7d1")
	readerGroup.Properties.PrincipalType = new(armauthorization.PrincipalTypeGroup)
	cases := []struct {
		name        string
		assignments []*armauthorization.RoleAssignment
		want        storagePermission
	}{
		{"missing", nil, storagePermissionMissing},
		{"contributor", []*armauthorization.RoleAssignment{grant}, storagePermissionGranted},
		{"data owner", []*armauthorization.RoleAssignment{
			storageTestAssignment("project", storageTestScope, storageBlobOwnerRole)}, storagePermissionGranted},
		{"inherited", []*armauthorization.RoleAssignment{
			storageTestAssignment("project", "/subscriptions/sub/resourceGroups/rg", storageBlobContributorRole)},
			storagePermissionGranted},
		{"case insensitive", []*armauthorization.RoleAssignment{
			storageTestAssignment("PROJECT", strings.ToUpper(storageTestScope), storageBlobContributorRole)},
			storagePermissionGranted},
		{"developer is not project", []*armauthorization.RoleAssignment{
			storageTestAssignment("developer", storageTestScope, storageBlobContributorRole)}, storagePermissionMissing},
		{"other account", []*armauthorization.RoleAssignment{
			storageTestAssignment("project", storageTestScope+"other", storageBlobContributorRole)},
			storagePermissionMissing},
		{"container grant is inconclusive", []*armauthorization.RoleAssignment{
			storageTestAssignment("project", storageTestScope+"/blobServices/default/containers/data",
				storageBlobContributorRole)},
			storagePermissionScoped},
		{"standard agent container roles", []*armauthorization.RoleAssignment{
			storageTestAssignment("project", storageTestScope, "17d1049b-9a84-46fb-8f53-869881c3d3ab"),
			storageTestAssignment("project", storageTestScope+"/blobServices/default/containers/project-azureml-blobstore",
				storageBlobContributorRole),
			storageTestAssignment("project", storageTestScope+"/blobServices/default/containers/project-azureml-agent",
				storageBlobOwnerRole)}, storagePermissionScoped},
		{"account grant covers container", []*armauthorization.RoleAssignment{
			storageTestAssignment("project", storageTestScope+"/blobServices/default/containers/data",
				storageBlobContributorRole), grant}, storagePermissionGranted},
		{"management owner", []*armauthorization.RoleAssignment{
			storageTestAssignment("project", storageTestScope, roleOwner)}, storagePermissionMissing},
		{"management contributor", []*armauthorization.RoleAssignment{
			storageTestAssignment("project", storageTestScope, roleContributor)}, storagePermissionMissing},
		{"blob reader", []*armauthorization.RoleAssignment{
			storageTestAssignment("project", storageTestScope, "2a2b9908-6ea1-4ae2-8e65-a410df84e7d1")},
			storagePermissionMissing},
		{"custom role", []*armauthorization.RoleAssignment{
			storageTestAssignment("project", storageTestScope, "custom-role")}, storagePermissionUnknown},
		{"conditional", []*armauthorization.RoleAssignment{conditional}, storagePermissionUnknown},
		{"group returned by assignedTo", []*armauthorization.RoleAssignment{group}, storagePermissionGranted},
		{"conditional group grant", []*armauthorization.RoleAssignment{conditionalGroup}, storagePermissionUnknown},
		{"reader group cannot grant writes", []*armauthorization.RoleAssignment{readerGroup}, storagePermissionMissing},
		{"group and direct grant", []*armauthorization.RoleAssignment{group, grant}, storagePermissionGranted},
		{"management group inherited", []*armauthorization.RoleAssignment{
			storageTestAssignment("project", "/providers/Microsoft.Management/managementGroups/parent",
				storageBlobOwnerRole)},
			storagePermissionGranted},
		{"independent grant", []*armauthorization.RoleAssignment{conditional, grant}, storagePermissionGranted},
		{"incomplete", []*armauthorization.RoleAssignment{nil}, storagePermissionUnknown},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, assessStorageRoles(testCase.assignments, "project", storageTestScope, nil))
		})
	}
}

type storageTestCredential struct{}

func (storageTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type storageTestTransport func(*http.Request) (*http.Response, error)

func (transport storageTestTransport) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestQueryProjectStorageRBAC(t *testing.T) {
	t.Parallel()
	const projectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	const userIdentityID = "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.ManagedIdentity/userAssignedIdentities/id"
	const principalID = "11111111-2222-3333-4444-555555555555"
	const monitoringReaderRole = "43d0d8ad-25c7-4714-9337-8ba259a9fe05"
	connection := func(name, auth string, shared bool) map[string]any {
		target := &url.URL{
			Scheme: "https", Host: "example.invalid", User: url.UserPassword("user", "password"), RawQuery: "sig=secret",
		}
		return map[string]any{"name": name, "properties": map[string]any{
			"category": "AzureStorageAccount", "authType": auth, "isSharedToAll": shared,
			"metadata": map[string]string{"ResourceId": storageTestScope},
			"target":   target.String(),
		}}
	}
	cases := []struct {
		name              string
		auth              string
		principal         string
		role              string
		storageStatus     int
		rolesStatus       int
		accountStatus     int
		projectStatus     int
		projectListStatus int
		paged             bool
		pagedConnections  bool
		duplicate         bool
		userAssigned      bool
		explicitShare     bool
		shared            bool
		empty             bool
		want              string
		wantError         bool
		groupGrant        bool
		conditionalGrant  bool
		assignmentScope   string
		definitionType    string
		definitionStatus  int
		dataActions       []string
		repeatAssignment  bool
		standardSetup     bool
	}{
		{name: "contributor", want: "granted"},
		{name: "project managed identity auth", auth: "ProjectManagedIdentity", want: "granted"},
		{name: "owner", role: storageBlobOwnerRole, want: "granted"},
		{name: "effective group grant", groupGrant: true, want: "granted"},
		{name: "conditional group grant", groupGrant: true, conditionalGrant: true, want: "unknown"},
		{name: "direct account grant", assignmentScope: storageTestScope, want: "granted"},
		{name: "resource group inherited grant", assignmentScope: "/subscriptions/sub/resourceGroups/rg", want: "granted"},
		{name: "container grant needs verification", assignmentScope: storageTestScope + "/blobServices/default/containers/data",
			want: "unknown"},
		{name: "missing grant", role: "none", want: "missing"},
		{name: "standard setup container roles", standardSetup: true, want: "unknown"},
		{name: "unrelated inherited builtin", role: monitoringReaderRole, definitionType: "BuiltInRole", want: "missing"},
		{name: "role definition cached", role: monitoringReaderRole, definitionType: "BuiltInRole",
			repeatAssignment: true, want: "missing"},
		{name: "definition forbidden", role: monitoringReaderRole, definitionStatus: 403, want: "unknown"},
		{name: "custom role unresolved", role: "custom-role", definitionType: "CustomRole", want: "unknown"},
		{name: "other service data builtin", role: "other-builtin", definitionType: "BuiltInRole",
			dataActions: []string{"Microsoft.KeyVault/vaults/secrets/*"}, want: "missing"},
		{name: "potential blob builtin", role: "other-blob-builtin", definitionType: "BuiltInRole",
			dataActions: []string{"Microsoft.Storage/*"}, want: "unknown"},
		{name: "account key", auth: "AccountKey", want: "skip"},
		{name: "unknown auth", auth: "None", want: "unknown"},
		{name: "managed storage", empty: true},
		{name: "shared connection", shared: true, want: "granted"},
		{name: "explicitly shared connection", shared: true, explicitShare: true, want: "granted"},
		{name: "duplicate target", duplicate: true, want: "granted"},
		{name: "paged connections", pagedConnections: true, want: "granted"},
		{name: "missing identity", principal: "none", want: "invalid"},
		{name: "user assigned identity", principal: "none", userAssigned: true, want: "unknown"},
		{name: "bad identity", principal: "bad", want: "unknown"},
		{name: "empty identity", principal: "00000000-0000-0000-0000-000000000000", want: "unknown"},
		{name: "storage missing", storageStatus: 404, want: "invalid"},
		{name: "storage forbidden", storageStatus: 403, want: "unknown"},
		{name: "role lookup forbidden", rolesStatus: 403, want: "unknown"},
		{name: "incomplete discovery", accountStatus: 403, wantError: true},
		{name: "project unreadable", projectStatus: 403, wantError: true},
		{name: "project connections unreadable", projectListStatus: 403, wantError: true},
		{name: "paged roles", paged: true, want: "granted"},
		{name: "second page failure", paged: true, rolesStatus: 403, want: "unknown"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			identity := principalID
			if testCase.principal != "" {
				identity = testCase.principal
			}
			auth := testCase.auth
			if auth == "" {
				auth = "AAD"
			}
			role := testCase.role
			if role == "" {
				role = storageBlobContributorRole
			}
			roleQueries := 0
			definitionQueries := 0
			accountPages, projectPages := 0, 0
			transport := storageTestTransport(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				require.Equal(t, "management.azure.com", request.URL.Host)
				status := http.StatusOK
				var body any
				switch request.URL.Path {
				case projectID:
					body = map[string]any{"identity": map[string]any{"type": "SystemAssigned", "principalId": identity}}
					if identity == "none" {
						body = map[string]any{"identity": map[string]any{"type": "None"}}
					}
					if testCase.userAssigned {
						body = map[string]any{"identity": map[string]any{
							"type": "UserAssigned", "userAssignedIdentities": map[string]any{
								userIdentityID: map[string]string{"principalId": principalID},
							},
						}}
					}
					if testCase.projectStatus != 0 {
						status = testCase.projectStatus
					}
				case strings.TrimSuffix(projectID, "/projects/project") + "/connections":
					accountPages++
					items := []any{connection("unshared", "AAD", false), connection("store", "AccountKey", true)}
					if testCase.shared {
						items = []any{connection("store", "AAD", true)}
					}
					if testCase.explicitShare {
						items = []any{map[string]any{"name": "store", "properties": map[string]any{
							"category": "AzureStorageAccount", "authType": "AAD",
							"sharedUserList": []string{projectID},
							"metadata":       map[string]string{"ResourceId": storageTestScope},
						}}}
					}
					if testCase.empty {
						items = []any{connection("unshared", "AAD", false)}
					}
					body = map[string]any{"value": items}
					if testCase.pagedConnections && accountPages == 1 {
						body = map[string]any{"value": []any{}, "nextLink": request.URL.String() + "&page=2"}
					}
					if testCase.accountStatus != 0 {
						status = testCase.accountStatus
					}
				case projectID + "/connections":
					projectPages++
					items := []any{connection("store", auth, false)}
					if testCase.duplicate {
						items = append(items, connection("store-copy", auth, false))
					}
					if testCase.shared || testCase.empty {
						items = []any{}
					}
					body = map[string]any{"value": items}
					if testCase.pagedConnections && projectPages == 1 {
						body = map[string]any{"value": []any{}, "nextLink": request.URL.String() + "&page=2"}
					}
					if testCase.projectListStatus != 0 {
						status = testCase.projectListStatus
					}
				case storageTestScope:
					body = map[string]string{"id": storageTestScope}
					if testCase.storageStatus != 0 {
						status = testCase.storageStatus
					}
				case storageTestScope + "/providers/Microsoft.Authorization/roleAssignments":
					roleQueries++
					require.Equal(t, "assignedTo('"+principalID+"')", request.URL.Query().Get("$filter"))
					assignmentScope := testCase.assignmentScope
					if assignmentScope == "" {
						assignmentScope = "/subscriptions/sub"
					}
					items := []*armauthorization.RoleAssignment{
						storageTestAssignment(principalID, assignmentScope, role),
					}
					if testCase.groupGrant {
						items[0].Properties.PrincipalID = new("resolved-group")
						items[0].Properties.PrincipalType = new(armauthorization.PrincipalTypeGroup)
					}
					if testCase.conditionalGrant {
						items[0].Properties.Condition = new("restricted")
					}
					if role == "none" {
						items = nil
					}
					if testCase.standardSetup {
						items = []*armauthorization.RoleAssignment{
							storageTestAssignment(principalID, storageTestScope, "17d1049b-9a84-46fb-8f53-869881c3d3ab"),
							storageTestAssignment(principalID,
								storageTestScope+"/blobServices/default/containers/project-azureml-blobstore", storageBlobContributorRole),
							storageTestAssignment(principalID,
								storageTestScope+"/blobServices/default/containers/project-azureml-agent", storageBlobOwnerRole),
						}
					}
					if testCase.repeatAssignment {
						items = append(items, storageTestAssignment(principalID, storageTestScope, role))
					}
					body = map[string]any{"value": items}
					if testCase.paged && roleQueries == 1 {
						body = map[string]any{"value": []any{}, "nextLink": request.URL.String() + "&page=2"}
					} else if testCase.rolesStatus != 0 {
						status = testCase.rolesStatus
					}
				case "/providers/Microsoft.Authorization/roleDefinitions/" + role:
					definitionQueries++
					body = map[string]any{"properties": map[string]any{
						"type":        testCase.definitionType,
						"permissions": []any{map[string]any{"actions": []string{"*/read"}, "dataActions": testCase.dataActions}},
					}}
					if testCase.definitionStatus != 0 {
						status = testCase.definitionStatus
					}
				default:
					t.Fatalf("unexpected request: %s", request.URL.Path)
				}
				if status != http.StatusOK {
					body = map[string]any{"error": map[string]string{"code": "TestError", "message": "secret-error-body"}}
				}
				encoded, err := json.Marshal(body)
				require.NoError(t, err)
				return &http.Response{
					StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(string(encoded))), Request: request,
				}, nil
			})
			options := &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
			}}
			result, err := queryProjectStorageRBAC(t.Context(), projectID, storageTestCredential{}, options)
			if testCase.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if testCase.empty {
				require.Empty(t, result.Findings)
				return
			}
			if testCase.duplicate {
				require.Len(t, result.Findings, 2)
				require.Equal(t, 1, roleQueries)
			} else {
				require.Len(t, result.Findings, 1)
			}
			require.Equal(t, testCase.want, result.Findings[0].Status)
			if testCase.standardSetup {
				require.Contains(t, result.Findings[0].Message, "container-scoped")
			}
			if testCase.definitionType != "" || testCase.definitionStatus != 0 {
				require.Equal(t, 1, definitionQueries)
			} else {
				require.Zero(t, definitionQueries)
			}
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "secret")
			require.NotContains(t, string(encoded), "password")
			if testCase.paged {
				require.Equal(t, 2, roleQueries)
			}
			if testCase.pagedConnections {
				require.Equal(t, 2, accountPages)
				require.Equal(t, 2, projectPages)
			}
		})
	}
}

func TestStorageConnectionFinding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		category string
		auth     string
		resource string
		want     string
		relevant bool
	}{
		{"AAD", "AzureStorageAccount", "AAD", storageTestScope, "", true},
		{"project identity", "AzureStorageAccount", "ProjectManagedIdentity", storageTestScope, "", true},
		{"blob", "AzureBlob", "AAD", storageTestScope, "", true},
		{"not storage", "AzureOpenAI", "AAD", storageTestScope, "", false},
		{"key", "AzureBlob", "AccountKey", "", "skip", true},
		{"SAS", "AzureBlob", "SAS", "", "skip", true},
		{"unknown identity", "AzureBlob", "ManagedIdentity", storageTestScope, "unknown", true},
		{"no resource", "AzureBlob", "AAD", "", "unknown", true},
		{"invalid resource", "AzureBlob", "AAD", "https://user:pass@example.invalid/?sig=secret", "invalid", true},
		{"query in resource", "AzureBlob", "AAD", storageTestScope + "?sig=secret", "invalid", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			connection := &armcognitiveservices.ConnectionPropertiesV2BasicResource{
				Name: new("storage"), Properties: &armcognitiveservices.ConnectionPropertiesV2{
					Category: new(armcognitiveservices.ConnectionCategory(testCase.category)),
					AuthType: new(armcognitiveservices.ConnectionAuthType(testCase.auth)),
					Metadata: map[string]*string{"ResourceId": new(testCase.resource)},
				},
			}
			finding, relevant := storageConnectionFinding(connection)
			require.Equal(t, testCase.relevant, relevant)
			require.Equal(t, testCase.want, finding.Status)
			require.NotContains(t, finding.StorageScope, "secret")
		})
	}
}

func TestStorageConnectionIdentitySelection(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name               string
		auth               armcognitiveservices.ConnectionAuthType
		useProjectIdentity bool
		want               string
	}{
		{"explicit project identity", armcognitiveservices.ConnectionAuthTypeManagedIdentity, true, ""},
		{"project identity auth ignores workspace flag", armcognitiveservices.ConnectionAuthType("ProjectManagedIdentity"),
			false, ""},
		{"other identity", armcognitiveservices.ConnectionAuthTypeManagedIdentity, false, "unknown"},
		{"AAD not using project", armcognitiveservices.ConnectionAuthTypeAAD, false, "skip"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			connection := &armcognitiveservices.ConnectionPropertiesV2BasicResource{
				Name: new("storage"), Properties: &armcognitiveservices.ConnectionPropertiesV2{
					Category: new(armcognitiveservices.ConnectionCategory("AzureStorageAccount")),
					AuthType: new(testCase.auth), UseWorkspaceManagedIdentity: new(testCase.useProjectIdentity),
					Metadata: map[string]*string{"ResourceId": new(storageTestScope)},
				},
			}
			finding, relevant := storageConnectionFinding(connection)
			require.True(t, relevant)
			require.Equal(t, testCase.want, finding.Status)
		})
	}
	for _, connection := range []*armcognitiveservices.ConnectionPropertiesV2BasicResource{
		nil, {}, {Name: new("missing"), Properties: &armcognitiveservices.ConnectionPropertiesV2{}},
		{Name: new("missing-auth"), Properties: &armcognitiveservices.ConnectionPropertiesV2{
			Category: new(armcognitiveservices.ConnectionCategoryAzureBlob),
		}},
	} {
		finding, relevant := storageConnectionFinding(connection)
		require.True(t, relevant)
		require.Equal(t, "unknown", finding.Status)
	}
}

func TestStorageProjectInfo(t *testing.T) {
	t.Parallel()
	for _, resourceID := range []string{
		"", "https://user:password@example.invalid?sig=secret",
		"/subscriptions/sub/resourceGroups/rg/providers/Other/accounts/account/projects/project",
		"/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/account/projects/project?secret",
	} {
		_, err := storageProjectInfo(resourceID)
		require.ErrorIs(t, err, ErrInvalidProjectResourceID)
		require.NotContains(t, err.Error(), "secret")
	}
	info, err := storageProjectInfo(
		"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/projects/project")
	require.NoError(t, err)
	require.Equal(t, "account", info.AccountName)
	require.Equal(t, "project", info.ProjectName)
}

func TestStorageRoleExcludesBlobData(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		roleType    string
		permissions []*armauthorization.Permission
		want        bool
	}{
		{"management builtin", "BuiltInRole", []*armauthorization.Permission{{Actions: []*string{new("*")}}}, true},
		{"other data service", "BuiltInRole", []*armauthorization.Permission{{
			DataActions: []*string{new("Microsoft.KeyVault/vaults/secrets/*")},
		}}, true},
		{"blob read", "BuiltInRole", []*armauthorization.Permission{{
			DataActions: []*string{new("Microsoft.Storage/storageAccounts/blobServices/containers/blobs/read")},
		}}, false},
		{"data wildcard", "BuiltInRole", []*armauthorization.Permission{{DataActions: []*string{new("*")}}}, false},
		{"mixed permissions", "BuiltInRole", []*armauthorization.Permission{
			{Actions: []*string{new("*/read")}}, {DataActions: []*string{new("Microsoft.Storage/*")}},
		}, false},
		{"custom unresolved", "CustomRole", []*armauthorization.Permission{{Actions: []*string{new("*/read")}}}, false},
		{"missing permissions", "BuiltInRole", nil, false},
		{"missing permission entry", "BuiltInRole", []*armauthorization.Permission{nil}, false},
		{"missing action", "BuiltInRole", []*armauthorization.Permission{{DataActions: []*string{nil}}}, false},
		{"missing type", "", []*armauthorization.Permission{{}}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			definition := &armauthorization.RoleDefinition{Properties: &armauthorization.RoleDefinitionProperties{
				RoleType: new(testCase.roleType), Permissions: testCase.permissions,
			}}
			require.Equal(t, testCase.want, storageRoleExcludesBlobData(definition))
		})
	}
	require.False(t, storageRoleExcludesBlobData(nil))
	require.False(t, storageRoleExcludesBlobData(&armauthorization.RoleDefinition{}))
}
