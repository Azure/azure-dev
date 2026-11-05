// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	selection        *synthesis.AgentHostingInput
	beforeRead       func(*http.Request) error
	reads            []string
	identityReply    map[string]any
	replies          map[string]any
}

func (f *suppliedARMFake) Do(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet && f.beforeRead != nil {
		if err := f.beforeRead(request); err != nil {
			return nil, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request.Method+" "+request.URL.Path)
	if request.Method != http.MethodGet {
		return f.base.Do(request)
	}
	f.reads = append(f.reads, request.URL.Path)
	if reply, ok := f.replies[request.URL.Path]; ok {
		return f.base.response(request, 200, reply), nil
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
	if selection == nil {
		selection = f.selection
	}
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
			if f.identityReply != nil {
				return respond(200, f.identityReply)
			}
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
	fake := &suppliedARMFake{
		base: base, overrides: map[string]suppliedReply{}, existingDefaults: map[string]bool{}, selection: result.Hosting,
	}
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

func TestBYOPrepareConcurrentReads(t *testing.T) {
	paths := []string{
		offlineClusterID,
		"/subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions",
		suppliedHostingID,
		suppliedWorkloadID,
	}
	for _, test := range []struct {
		name     string
		failures []string
		cancel   bool
	}{
		{name: "success"},
		{name: "cluster failure", failures: paths[:1]},
		{name: "role failure", failures: paths[1:2]},
		{name: "hosting identity failure", failures: paths[2:3]},
		{name: "workload identity failure", failures: paths[3:]},
		{name: "all reads fail", failures: paths},
		{name: "role and identity failures", failures: paths[1:]},
		{name: "identity failures", failures: paths[2:]},
		{name: "cancellation", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, fake, _ := suppliedProvider(t, 7)
			source, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			source.parameters["includeAcr"] = map[string]any{"value": true}
			original, err := json.Marshal(source.parameters)
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			started := make(chan string, len(paths))
			release := make(chan struct{})
			releaseReads := sync.OnceFunc(func() { close(release) })
			readFailure := errors.New("offline binding read failure")
			var bindingReads atomic.Bool
			fake.beforeRead = func(request *http.Request) error {
				if !bindingReads.Load() {
					return nil
				}
				started <- request.URL.Path
				select {
				case <-request.Context().Done():
					return request.Context().Err()
				case <-release:
					if slices.Contains(test.failures, request.URL.Path) {
						return fmt.Errorf("%s: %w", request.URL.Path, readFailure)
					}
					if len(test.failures) > 0 {
						<-request.Context().Done()
						return request.Context().Err()
					}
					return nil
				}
			}
			done := make(chan struct{})
			var prepareErr error
			go func() {
				defer close(done)
				prepareErr = p.prepareBYO(ctx, source, func(string) { bindingReads.Store(true) })
			}()
			t.Cleanup(func() {
				cancel()
				releaseReads()
				<-done
			})

			var observed []string
			for range paths {
				select {
				case path := <-started:
					observed = append(observed, path)
				case <-ctx.Done():
					t.Fatalf("binding reads did not start concurrently: %v", observed)
				}
			}
			assert.ElementsMatch(t, paths, observed)
			if test.cancel {
				cancel()
			} else {
				releaseReads()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("binding reads did not finish")
			}

			switch {
			case test.cancel:
				require.ErrorIs(t, prepareErr, context.Canceled)
			case len(test.failures) > 0:
				require.ErrorIs(t, prepareErr, readFailure)
				assert.True(t, slices.ContainsFunc(test.failures, func(path string) bool {
					return strings.Contains(prepareErr.Error(), path)
				}), "expected the original error from a failed binding read, got %v", prepareErr)
			default:
				require.NoError(t, prepareErr)
				for name, value := range map[string]string{
					"kubeletObjectId":                   offlineKubeletObjectID,
					"hostingPrincipalId":                suppliedHostingPrincipal,
					"workloadPrincipalId":               suppliedWorkloadPrincipal,
					"federatedIdentityRoleDefinitionId": paths[1] + "/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
				} {
					assert.Equal(t, map[string]any{"value": value}, source.parameters[name])
				}
			}
			if prepareErr != nil {
				current, err := json.Marshal(source.parameters)
				require.NoError(t, err)
				assert.JSONEq(t, string(original), string(current), "failed reads must not publish partial bindings")
			}
			assert.Zero(t, fake.base.writes)
			assert.Zero(t, fake.base.previews)
		})
	}
}

func TestBYOSuppliedResourcesDeployIndependently(t *testing.T) {
	for mask := range 8 {
		for _, registry := range []bool{false, true} {
			for _, preview := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/registry=%t/preview=%t", mask, registry, preview), func(t *testing.T) {
					p, fake, compiler := suppliedProvider(t, mask)
					p.armTemplate["contentVersion"] = "2.0.0.0"
					p.onDiskSource = &templateSource{
						mode: templateModeBicep, armTemplate: p.armTemplate,
						parameters: map[string]any{"includeAcr": map[string]any{"value": registry}},
					}
					var progress strings.Builder
					if preview {
						result, err := p.Preview(t.Context(), func(message string) { progress.WriteString(message) })
						require.NoError(t, err)
						require.NotNil(t, result.Preview)
						assert.Zero(t, fake.base.writes)
						assert.Equal(t, 1, fake.base.previews)
					} else {
						result, err := p.Deploy(t.Context(), func(message string) { progress.WriteString(message) })
						require.NoError(t, err)
						expected := ""
						if registry {
							expected = offlineRegistryID
						}
						assert.Equal(t, expected, result.Deployment.Outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"].Value)
						assert.Equal(t, 1, fake.base.writes)
					}
					assert.Equal(t, "2.0.0.0", fake.base.template["contentVersion"])
					if registry {
						assert.Equal(t, map[string]any{"value": offlineKubeletObjectID},
							fake.base.parameters["kubeletObjectId"])
					} else {
						assert.NotContains(t, fake.base.parameters, "kubeletObjectId")
					}
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
							assert.Equal(t, identity.principal,
								fake.base.parameters[identity.name].(map[string]any)["value"])
						} else {
							assert.NotContains(t, fake.base.parameters, identity.name)
						}
					}
					assert.NotEmpty(t, compiler.buildParamCalls, "the actual Foundry account name uses the resource token")
					assert.Contains(t, progress.String(), "S03")
					assert.Contains(t, progress.String(), "P02 PASS")
					if mask&2 == 0 {
						assert.Contains(t, progress.String(), "P09 PASS ["+offlineClusterGroupID+"]")
					} else {
						assert.NotContains(t, progress.String(), "P09 PASS ["+offlineClusterGroupID+"]")
					}
					assert.Empty(t, p.virtualEnv)
					method := "PUT "
					if preview {
						method = "POST "
					}
					submissions := 0
					for _, request := range fake.requests {
						if strings.HasPrefix(request, method) {
							submissions++
						} else {
							assert.True(t, strings.HasPrefix(request, "GET "), request)
						}
					}
					assert.Equal(t, 1, submissions)
				})
			}
		}
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

func TestBYOSuppliedExplicitPrincipalDoesNotOverwriteUserInput(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 7)
	p.onDiskSource = &templateSource{
		mode: templateModeBicep, armTemplate: p.armTemplate,
		parameters: map[string]any{"hostingPrincipalId": map[string]any{"value": suppliedOperator}},
	}
	_, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, 1, fake.base.writes)
	assert.Equal(t, map[string]any{"value": suppliedOperator}, fake.base.parameters["hostingPrincipalId"])
	assert.Equal(t, suppliedOperator, p.onDiskSource.parameters["hostingPrincipalId"].(map[string]any)["value"])
	assert.Empty(t, p.virtualEnv)
}

func TestBYODisabledSupportingSelectionsRetainARMPrecedence(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 7)
	p.synthResult.Hosting = nil
	p.credential = nil
	source := &templateSource{armTemplate: p.armTemplate, parameters: map[string]any{
		"agentHosting": map[string]any{"value": map[string]any{
			"enabled": false, "storageAccountResourceId": suppliedStorageID,
		}},
	}}
	require.NoError(t, p.prepareBYO(t.Context(), source, func(string) {}))
	assert.Equal(t, map[string]any{"value": map[string]any{
		"enabled": false, "storageAccountResourceId": suppliedStorageID,
	}}, source.parameters["agentHosting"])
	assert.Empty(t, fake.requests)
}
