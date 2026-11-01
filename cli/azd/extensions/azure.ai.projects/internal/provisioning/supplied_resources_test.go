// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const suppliedGroupID = "/subscriptions/target-sub/resourceGroups/target-rg"
const suppliedStorageID = suppliedGroupID + "/providers/Microsoft.Storage/storageAccounts/customer"
const suppliedHostingID = suppliedGroupID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/hosting"
const suppliedWorkloadID = suppliedGroupID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/workload"
const defaultStorageID = suppliedGroupID + "/providers/Microsoft.Storage/storageAccounts/stabcdefghijklm"
const defaultWorkloadID = suppliedGroupID +
	"/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cog-abcdefghijklm-workload-mi"
const suppliedHostingPrincipal = "22222222-2222-4222-8222-222222222222"
const suppliedWorkloadPrincipal = "33333333-3333-4333-8333-333333333333"
const suppliedOperator = "44444444-4444-4444-8444-444444444444"

type suppliedCredential struct{}

func (suppliedCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	raw := base64.RawURLEncoding.EncodeToString([]byte(`{"oid":"` + suppliedOperator + `"}`))
	return azcore.AccessToken{Token: "header." + raw + ".signature", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type suppliedReply struct {
	status int
	body   any
}

type suppliedARMFake struct {
	mu               sync.Mutex
	base             *byoARMFake
	overrides        map[string]suppliedReply
	existingDefaults map[string]bool
	missingGrant     string
	requests         []string
}

func (f *suppliedARMFake) Do(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request.Method+" "+request.URL.Path)
	if request.Method != http.MethodGet {
		return f.base.Do(request)
	}
	path := request.URL.Path
	lower := strings.ToLower(path)
	respond := func(status int, body any) (*http.Response, error) { return f.base.response(request, status, body), nil }
	if reply, ok := f.overrides[lower]; ok {
		return respond(reply.status, reply.body)
	}
	if strings.HasPrefix(lower, "/subscriptions/") && len(strings.Split(path, "/")) == 3 {
		return respond(200, map[string]any{"id": path, "tenantId": "tenant"})
	}
	if strings.EqualFold(path, suppliedGroupID) {
		return respond(200, map[string]any{"id": suppliedGroupID, "location": "westus"})
	}
	selection := f.base.provider.synthResult.Hosting
	if path != "" && strings.EqualFold(path, selection.StorageAccountResourceID) ||
		strings.EqualFold(path, defaultStorageID) && f.existingDefaults[strings.ToLower(path)] {
		sharedKey := strings.EqualFold(path, defaultStorageID)
		return respond(200, map[string]any{
			"id": path, "kind": "StorageV2", "sku": map[string]any{"name": "Standard_ZRS"},
			"properties": map[string]any{
				"provisioningState": "Succeeded", "accessTier": "Hot", "isHnsEnabled": false,
				"supportsHttpsTrafficOnly": true, "minimumTlsVersion": "TLS1_2",
				"allowSharedKeyAccess": sharedKey, "allowBlobPublicAccess": false,
				"publicNetworkAccess": "Enabled", "networkAcls": map[string]any{"defaultAction": "Allow"},
				"primaryEndpoints": map[string]any{"blob": "https://customer.blob.core.windows.net/"},
			},
		})
	}
	for _, identity := range []struct{ id, principal string }{
		{selection.HostingManagementIdentityResourceID, suppliedHostingPrincipal},
		{selection.WorkloadIdentityResourceID, suppliedWorkloadPrincipal},
	} {
		if identity.id != "" && strings.EqualFold(path, identity.id) {
			return respond(200, map[string]any{"id": path, "properties": map[string]any{
				"principalId": identity.principal, "clientId": identity.principal, "tenantId": "tenant",
			}})
		}
	}
	if strings.EqualFold(path, defaultWorkloadID) && f.existingDefaults[strings.ToLower(path)] {
		return respond(200, map[string]any{"id": path})
	}
	if strings.HasSuffix(lower, "/roledefinitions") {
		return respond(200, map[string]any{"value": []any{map[string]any{
			"id": path + "/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			"properties": map[string]any{
				"roleName": "Managed Identity Federated Identity Credential Contributor", "type": "BuiltInRole",
			},
		}}})
	}
	if strings.Contains(lower, "/roledefinitions/") {
		return respond(200, map[string]any{"id": path, "properties": map[string]any{
			"permissions": []any{map[string]any{"actions": []string{"*"}, "dataActions": []string{"*"}}},
		}})
	}
	const suffix = "/providers/Microsoft.Authorization/roleAssignments"
	if strings.HasSuffix(lower, strings.ToLower(suffix)) {
		scope := path[:len(path)-len(suffix)]
		principals := []string{suppliedOperator}
		if !strings.EqualFold(scope, f.missingGrant) {
			principals = append(principals, suppliedHostingPrincipal, suppliedWorkloadPrincipal)
		}
		var values []any
		for _, principal := range principals {
			values = append(values, map[string]any{
				"id": scope + suffix + "/assignment-" + principal,
				"properties": map[string]any{
					"principalId": principal, "scope": scope,
					"roleDefinitionId": "/subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions/owner",
				},
			})
		}
		return respond(200, map[string]any{"value": values})
	}
	if strings.Contains(lower, "/microsoft.storage/") || strings.Contains(lower, "/microsoft.managedidentity/") {
		return respond(404, map[string]any{"error": map[string]string{"code": "ResourceNotFound"}})
	}
	return f.base.Do(request)
}

func suppliedYAML(mask int) string {
	var raw strings.Builder
	raw.WriteString("services:\n  project:\n    host: azure.ai.project\n    agentHosting:\n" +
		"      clusterResourceId: " + offlineClusterID + "\n")
	for i, selection := range []struct{ name, id string }{
		{"storageAccountResourceId", suppliedStorageID},
		{"hostingManagementIdentityResourceId", suppliedHostingID},
		{"workloadIdentityResourceId", suppliedWorkloadID},
	} {
		if mask&(1<<i) != 0 {
			raw.WriteString("      " + selection.name + ": " + selection.id + "\n")
		}
	}
	return raw.String()
}

func suppliedProvider(t *testing.T, mask int) (*FoundryProvisioningProvider, *suppliedARMFake, *stubCompiler) {
	t.Helper()
	p, base := offlineBYOProvider(t)
	result, err := synthesis.Synthesize(synthesis.Input{ServiceName: "project", RawAzureYAML: []byte(suppliedYAML(mask))})
	require.NoError(t, err)
	p.synthResult, p.credential = result, suppliedCredential{}
	fake := &suppliedARMFake{base: base, overrides: map[string]suppliedReply{}, existingDefaults: map[string]bool{}}
	p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: fake, Retry: policy.RetryOptions{MaxRetries: -1},
	}}
	inner, err := json.Marshal(map[string]any{"parameters": map[string]any{
		"resourceToken": map[string]any{"value": "abcdefghijklm"},
		"result":        map[string]any{"value": "abcdefghijklm"},
	}})
	require.NoError(t, err)
	outer, err := json.Marshal(map[string]any{"parametersJson": string(inner)})
	require.NoError(t, err)
	compiler := &stubCompiler{buildParamResult: bicep.BuildResult{Compiled: string(outer)}}
	p.bicepCliInstance = compiler
	return p, fake, compiler
}

func TestBYOSuppliedResourcesDeployIndependently(t *testing.T) {
	for mask := range 8 {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			p, fake, compiler := suppliedProvider(t, mask)
			var progress strings.Builder
			result, err := p.Deploy(t.Context(), func(message string) { progress.WriteString(message) })
			require.NoError(t, err)
			assert.Equal(t, 1, fake.base.writes)
			assert.Equal(t, "", result.Deployment.Outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"].Value)
			hosting := fake.base.parameters["agentHosting"].(map[string]any)["value"].(map[string]any)
			for i, selection := range []struct{ name, id string }{
				{"storageAccountResourceId", suppliedStorageID},
				{"hostingManagementIdentityResourceId", suppliedHostingID},
				{"workloadIdentityResourceId", suppliedWorkloadID},
			} {
				expected := ""
				if mask&(1<<i) != 0 {
					expected = selection.id
				}
				assert.Equal(t, expected, hosting[selection.name])
			}
			for _, identity := range []struct {
				name, principal string
				selected        bool
			}{
				{"hostingPrincipalId", suppliedHostingPrincipal, mask&2 != 0},
				{"workloadPrincipalId", suppliedWorkloadPrincipal, mask&4 != 0},
			} {
				if identity.selected {
					assert.Equal(t, identity.principal, fake.base.parameters[identity.name].(map[string]any)["value"])
				} else {
					assert.NotContains(t, fake.base.parameters, identity.name)
				}
			}
			if mask == 7 {
				assert.Empty(t, compiler.buildParamCalls)
			} else {
				assert.Len(t, compiler.buildParamCalls, 1)
			}
			assert.Contains(t, progress.String(), "S03")
			assert.Contains(t, progress.String(), "P02 PASS")
			if mask&2 == 0 {
				assert.Contains(t, progress.String(), "P09 PASS ["+offlineClusterGroupID+"]")
			} else {
				assert.NotContains(t, progress.String(), "P09 PASS ["+offlineClusterGroupID+"]")
			}
			assert.Empty(t, p.virtualEnv)
			for _, request := range fake.requests[:len(fake.requests)-1] {
				assert.True(t, strings.HasPrefix(request, "GET "), request)
			}
			assert.True(t, strings.HasPrefix(fake.requests[len(fake.requests)-1], "PUT "))
		})
	}
}

func TestBYOSuppliedPreflightBlocksDeployAndWhatIfBeforeMutations(t *testing.T) {
	for _, preview := range []bool{false, true} {
		for _, failure := range []string{"missing Storage", "forbidden Storage", "missing identity", "tenant", "grant"} {
			t.Run(fmt.Sprintf("%s/preview=%t", failure, preview), func(t *testing.T) {
				p, fake, _ := suppliedProvider(t, 7)
				switch failure {
				case "missing Storage":
					fake.overrides[strings.ToLower(suppliedStorageID)] = suppliedReply{404,
						map[string]any{"error": map[string]string{"code": "ResourceNotFound"}}}
				case "forbidden Storage":
					fake.overrides[strings.ToLower(suppliedStorageID)] = suppliedReply{403,
						map[string]any{"error": map[string]string{"code": "AuthorizationFailed"}}}
				case "missing identity":
					fake.overrides[strings.ToLower(suppliedHostingID)] = suppliedReply{404,
						map[string]any{"error": map[string]string{"code": "ResourceNotFound"}}}
				case "tenant":
					fake.overrides["/subscriptions/cluster-sub"] = suppliedReply{200,
						map[string]any{"tenantId": "other-tenant"}}
				case "grant":
					fake.missingGrant = suppliedWorkloadID
				}
				var err error
				if preview {
					_, err = p.Preview(t.Context(), func(string) {})
				} else {
					_, err = p.Deploy(t.Context(), func(string) {})
				}
				require.ErrorContains(t, err, "preflight blocked")
				assert.Zero(t, fake.base.writes)
				assert.Zero(t, fake.base.previews)
				assert.Empty(t, p.virtualEnv)
				for _, request := range fake.requests {
					assert.True(t, strings.HasPrefix(request, "GET "), request)
				}
			})
		}
	}
}

func TestBYOSuppliedPreviewUsesNormalWhatIfAfterReadOnlyChecks(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 7)
	result, err := p.Preview(t.Context(), func(string) {})
	require.NoError(t, err)
	require.Len(t, result.Preview.Changes, 1)
	assert.Equal(t, 1, fake.base.previews)
	assert.Zero(t, fake.base.writes)
	assert.True(t, strings.HasSuffix(fake.requests[len(fake.requests)-1], "/whatIf"))
}

func TestBYOExistingDefaultStorageRetainsOrdinaryARMUpdate(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 2)
	fake.existingDefaults[strings.ToLower(defaultStorageID)] = true
	var progress strings.Builder
	_, err := p.Deploy(t.Context(), func(message string) { progress.WriteString(message) })
	require.NoError(t, err)
	hosting := fake.base.parameters["agentHosting"].(map[string]any)["value"].(map[string]any)
	assert.Equal(t, "", hosting["storageAccountResourceId"])
	assert.Contains(t, progress.String(), "S01 NOT_APPLICABLE")
	assert.Equal(t, 1, fake.base.writes)
}

func TestBYOExistingDefaultDependencyMissingGrantBlocksRetry(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 2)
	fake.base.failWrite = true
	_, err := p.Deploy(t.Context(), func(string) {})
	require.ErrorContains(t, err, "offline deployment failure")
	assert.Equal(t, 1, fake.base.writes)
	fake.base.failWrite = false
	fake.existingDefaults[strings.ToLower(defaultWorkloadID)] = true
	fake.missingGrant = defaultWorkloadID
	_, err = p.Deploy(t.Context(), func(string) {})
	require.ErrorContains(t, err, "G02 FAIL")
	assert.Equal(t, 1, fake.base.writes, "no historical failed-attempt exemption may open the write gate")
}

func TestBYOSuppliedWorkloadUsesItsOwnSubscriptionForFederationRole(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 7)
	remote := strings.Replace(suppliedWorkloadID, "/target-sub/", "/identity-sub/", 1)
	result, err := synthesis.Synthesize(synthesis.Input{ServiceName: "project",
		RawAzureYAML: []byte(strings.Replace(suppliedYAML(7), suppliedWorkloadID, remote, 1))})
	require.NoError(t, err)
	p.synthResult = result
	_, err = p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, "/subscriptions/identity-sub/providers/Microsoft.Authorization/"+
		"roleDefinitions/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		fake.base.parameters["federatedIdentityRoleDefinitionId"].(map[string]any)["value"])
}

func TestBYOSuppliedTemplateAndBindingConflictsFailBeforeAuthentication(t *testing.T) {
	for _, conflict := range []string{"template", "selection", "empty principal", "inapplicable principal", "target scope"} {
		t.Run(conflict, func(t *testing.T) {
			mask := 7
			if conflict == "inapplicable principal" {
				mask = 1
			}
			p, fake, _ := suppliedProvider(t, mask)
			source, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			switch conflict {
			case "template":
				source.armTemplate["contentVersion"] = "2.0.0.0"
			case "selection":
				source.parameters["agentHosting"] = map[string]any{"value": map[string]any{
					"enabled": true, "hostingType": "ManagedCluster", "name": "default",
					"clusterResourceId": offlineClusterID, "storageAccountResourceId": suppliedStorageID + "-other",
				}}
			case "empty principal":
				source.parameters["hostingPrincipalId"] = map[string]any{"value": ""}
			case "inapplicable principal":
				source.parameters["hostingPrincipalId"] = map[string]any{"value": suppliedHostingPrincipal}
			case "target scope":
				source.parameters["resourceGroupName"] = map[string]any{"value": "invalid/group"}
			}
			p.credential = nil
			require.Error(t, p.prepareBYO(t.Context(), source, func(string) {}))
			assert.Empty(t, fake.requests)
			assert.Empty(t, p.virtualEnv)
		})
	}
}

func TestBYOSuppliedPrincipalConflictDoesNotOverwriteUserInputOrDeploy(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 7)
	p.onDiskSource = &templateSource{
		mode: templateModeBicep, armTemplate: p.armTemplate,
		parameters: map[string]any{"hostingPrincipalId": map[string]any{"value": suppliedOperator}},
	}
	_, err := p.Deploy(t.Context(), func(string) {})
	require.ErrorContains(t, err, "hostingPrincipalId conflicts")
	assert.Zero(t, fake.base.writes)
	assert.Equal(t, suppliedOperator, p.onDiskSource.parameters["hostingPrincipalId"].(map[string]any)["value"])
	assert.Empty(t, p.virtualEnv)
}

func TestBYODisabledSupportingSelectionsAreRejectedRatherThanIgnored(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 7)
	p.synthResult.Hosting = nil
	p.credential = nil
	source := &templateSource{armTemplate: p.armTemplate, parameters: map[string]any{
		"agentHosting": map[string]any{"value": map[string]any{
			"enabled": false, "storageAccountResourceId": suppliedStorageID,
		}},
	}}
	require.ErrorContains(t, p.prepareBYO(t.Context(), source, func(string) {}), "cannot take effect")
	assert.Empty(t, fake.requests)
}
