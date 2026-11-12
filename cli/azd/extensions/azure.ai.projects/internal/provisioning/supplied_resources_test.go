// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
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

type suppliedCredential struct{}

func (suppliedCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "offline-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type suppliedReply struct {
	status int
	body   any
}

type suppliedARMFake struct {
	mu           sync.Mutex
	base         *byoARMFake
	overrides    map[string]suppliedReply
	missingGrant string
	requests     []string
	selection    *synthesis.AgentHostingInput
	reads        []string
}

func (f *suppliedARMFake) Do(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request.Method+" "+request.URL.Path)
	if request.Method != http.MethodGet {
		return f.base.Do(request)
	}
	f.reads = append(f.reads, request.URL.Path)
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
	if path != "" && strings.EqualFold(path, selection.StorageAccountResourceID) {
		return respond(200, map[string]any{
			"id": path, "kind": "StorageV2", "sku": map[string]any{"name": "Standard_ZRS"},
			"properties": map[string]any{
				"provisioningState": "Succeeded", "accessTier": "Hot", "isHnsEnabled": false,
				"supportsHttpsTrafficOnly": true, "minimumTlsVersion": "TLS1_2",
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
	if strings.HasSuffix(lower, "/roledefinitions") {
		return respond(200, map[string]any{"value": []any{map[string]any{
			"id": path + "/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			"properties": map[string]any{
				"roleName": "Managed Identity Federated Identity Credential Contributor", "type": "BuiltInRole",
			},
		}}})
	}
	const suffix = "/providers/Microsoft.Authorization/roleAssignments"
	if strings.HasSuffix(lower, strings.ToLower(suffix)) {
		scope := path[:len(path)-len(suffix)]
		var principals []string
		if !strings.EqualFold(scope, f.missingGrant) {
			principals = append(principals, suppliedHostingPrincipal, suppliedWorkloadPrincipal)
		}
		values := []any{}
		for _, principal := range principals {
			role := "8e3af657-a8ff-443c-a75c-2fe8c4bcb635"
			if strings.Contains(strings.ToLower(scope), strings.ToLower("/Microsoft.Storage/storageAccounts/")) {
				role = "b7e6dc6d-f1e8-4753-8033-0f276bb0955b"
			}
			values = append(values, map[string]any{
				"id": scope + suffix + "/assignment-" + principal,
				"properties": map[string]any{
					"principalId": principal, "scope": scope,
					"roleDefinitionId": "/subscriptions/target-sub/providers/" +
						"Microsoft.Authorization/roleDefinitions/" + role,
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
	raw.WriteString("services:\n  project:\n    host: azure.ai.project\n" +
		"    monitoring: {enabled: false}\n    agentHosting:\n" +
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
		base: base, overrides: map[string]suppliedReply{}, selection: result.Hosting,
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

func TestBYOSuppliedResourcesDeployIndependently(t *testing.T) {
	for mask := range 8 {
		for _, registry := range []bool{false, true} {
			for _, preview := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/registry=%t/preview=%t", mask, registry, preview), func(t *testing.T) {
					p, fake, compiler := suppliedProvider(t, mask)
					p.armTemplate["contentVersion"] = "2.0.0.0"
					p.onDiskSource = &templateSource{
						mode: templateModeBicep, armTemplate: p.armTemplate,
						parameters: map[string]any{
							"includeAcr":     map[string]any{"value": registry},
							"monitoringMode": map[string]any{"value": "none"},
						},
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
		for _, failure := range []string{
			"missing Storage", "forbidden Storage", "missing identity", "tenant", "grant",
			"Automatic cluster", "missing cluster SKU name",
		} {
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
				case "Automatic cluster":
					fake.base.clusterSKUName = "Automatic"
				case "missing cluster SKU name":
					fake.base.clusterSKUName = ""
				}
				var err error
				if preview {
					_, err = p.Preview(t.Context(), func(string) {})
				} else {
					_, err = p.Deploy(t.Context(), func(string) {})
				}
				require.ErrorContains(t, err, "preflight blocked")
				if failure == "Automatic cluster" || failure == "missing cluster SKU name" {
					assert.Contains(t, err.Error(), "aks-type")
					assert.Contains(t, err.Error(), offlineClusterID)
					assert.Contains(t, err.Error(), "sku.name=Base")
				}
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

func TestBYOConditionalGrantRetainsExitDiagnostic(t *testing.T) {
	for _, test := range []struct {
		name, scope, identity, principal, role, roleID string
		mask                                           int
	}{
		{"storage", suppliedStorageID, suppliedWorkloadID, suppliedWorkloadPrincipal,
			"Storage Blob Data Contributor", "ba92f5b4-2d11-453d-a403-e96b0029c9fe", 5},
		{"cluster", offlineClusterID, suppliedHostingID, suppliedHostingPrincipal,
			"Reader", "acdd72a7-3385-48ef-bd42-f606fba81ae7", 2},
		{"federation", defaultWorkloadID, suppliedHostingID, suppliedHostingPrincipal,
			"Managed Identity Federated Identity Credential Contributor", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", 2},
		{"explicit principal", suppliedStorageID, suppliedWorkloadID, "44444444-4444-4444-8444-444444444444",
			"Storage Blob Data Contributor", "ba92f5b4-2d11-453d-a403-e96b0029c9fe", 5},
		{"explicit federation", defaultWorkloadID, suppliedHostingID, suppliedHostingPrincipal,
			"Managed Identity Federated Identity Credential Contributor", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", 2},
	} {
		for _, preview := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/preview=%t", test.name, preview), func(t *testing.T) {
				p, fake, _ := suppliedProvider(t, test.mask)
				const suffix = "/providers/Microsoft.Authorization/roleAssignments"
				assignmentID := test.scope + suffix + "/55555555-5555-4555-8555-555555555555"
				roleID := "/subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions/" + test.roleID
				if strings.HasPrefix(test.name, "explicit ") {
					name, value := "workloadPrincipalId", test.principal
					if test.name == "explicit federation" {
						name, value = "federatedIdentityRoleDefinitionId", roleID
					}
					p.onDiskSource = &templateSource{
						mode: templateModeBicep, armTemplate: p.armTemplate,
						parameters: map[string]any{name: map[string]any{"value": value}},
					}
				}
				fake.overrides[strings.ToLower(test.scope+suffix)] = suppliedReply{200, map[string]any{
					"value": []any{map[string]any{
						"id": assignmentID,
						"properties": map[string]any{
							"scope": test.scope, "principalId": test.principal, "roleDefinitionId": roleID,
							"condition": "restricted test condition", "conditionVersion": "2.0",
						},
					}},
				}}
				var err error
				if preview {
					_, err = p.Preview(t.Context(), func(string) {})
				} else {
					_, err = p.Deploy(t.Context(), func(string) {})
				}
				require.ErrorContains(t, err, "BYO-AKS preflight blocked")
				assert.Zero(t, fake.base.writes)
				assert.Zero(t, fake.base.previews)
				assert.Empty(t, p.virtualEnv)
				envelope := azdext.NewProvisioningEnvelope()
				message := new(azdext.ProvisioningMessage)
				envelope.SetError(message, err)
				wire, marshalErr := proto.Marshal(message)
				require.NoError(t, marshalErr)
				received := new(azdext.ProvisioningMessage)
				require.NoError(t, proto.Unmarshal(wire, received))
				exitErr := fmt.Errorf("provisioning deploy failed: %w", envelope.GetError(received))
				exitOutput := (&ux.ActionResult{Err: exitErr}).ToString("")
				for _, detail := range []string{
					"ERROR:", assignmentID, test.identity, test.principal, test.scope, test.role, roleID,
					"restricted test condition", "remove its condition", "replace it with an unconditional",
					"different ID conflicts", "azd does not modify existing conditions",
				} {
					assert.Contains(t, exitOutput, detail)
				}
			})
		}
	}
}

func TestBYOReadFailureCauseRetainsExitDiagnostic(t *testing.T) {
	//nolint:gosec // G101: synthetic URL credentials exercise final error redaction.
	const endpoint = "https://cause-user:cause-password@example.test/resource?sig=cause-secret#cause-fragment"
	for _, failure := range []string{"transport", "authentication", "decoding"} {
		for _, preview := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/preview=%t", failure, preview), func(t *testing.T) {
				p, fake, _ := suppliedProvider(t, 7)
				cause := errors.New("connection reset while reading " + endpoint)
				expected := "connection reset while reading https://example.test/resource"
				if failure == "authentication" {
					expected = "authentication challenge failed for https://example.test/resource"
				} else if failure == "decoding" {
					expected = "cannot unmarshal"
				}
				p.armClientOptions.Transport = acrTestTransport(func(request *http.Request) (*http.Response, error) {
					if request.Method != http.MethodGet || request.URL.Path != offlineClusterID {
						return fake.Do(request)
					}
					switch failure {
					case "authentication":
						return fake.base.response(request, http.StatusForbidden, map[string]any{
							"error": map[string]any{
								"code": "AuthorizationFailed", "message": "authentication challenge failed for " + endpoint,
							},
						}), nil
					case "decoding":
						return fake.base.response(request, http.StatusOK, map[string]any{"properties": "invalid"}), nil
					default:
						return nil, cause
					}
				})
				var progress strings.Builder
				writeProgress := func(message string) { progress.WriteString(message) }
				var err error
				if preview {
					_, err = p.Preview(t.Context(), writeProgress)
				} else {
					_, err = p.Deploy(t.Context(), writeProgress)
				}
				require.ErrorContains(t, err, "BYO-AKS preflight blocked")
				if failure == "transport" {
					require.ErrorIs(t, err, cause)
				} else if failure == "authentication" {
					service, ok := errors.AsType[*azcore.ResponseError](err)
					require.True(t, ok)
					assert.Equal(t, http.StatusForbidden, service.StatusCode)
					assert.Equal(t, "AuthorizationFailed", service.ErrorCode)
				}
				envelope := azdext.NewProvisioningEnvelope()
				message := new(azdext.ProvisioningMessage)
				envelope.SetError(message, err)
				wire, marshalErr := proto.Marshal(message)
				require.NoError(t, marshalErr)
				received := new(azdext.ProvisioningMessage)
				require.NoError(t, proto.Unmarshal(wire, received))
				exitErr := fmt.Errorf("provisioning failed: %w", envelope.GetError(received))
				exitOutput := (&ux.ActionResult{Err: exitErr}).ToString("")
				assert.Contains(t, exitOutput, "ERROR:")
				assert.Contains(t, progress.String(), "BYO AKS preflight checks failed")
				assert.NotContains(t, progress.String(), "Cause:")
				for _, output := range []string{p.preflightReport.String(), exitOutput} {
					assert.Contains(t, output, "Cause:")
					assert.Contains(t, output, expected)
					for _, secret := range []string{"cause-user", "cause-password", "sig=", "cause-secret", "cause-fragment"} {
						assert.NotContains(t, output, secret)
					}
				}
				assert.Zero(t, fake.base.writes)
				assert.Zero(t, fake.base.previews)
				assert.Empty(t, p.virtualEnv)
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

func TestBYODefaultStorageRetainsOrdinaryARMUpdate(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 2)
	var progress strings.Builder
	_, err := p.Deploy(t.Context(), func(message string) { progress.WriteString(message) })
	require.NoError(t, err)
	hosting := fake.base.parameters["agentHosting"].(map[string]any)["value"].(map[string]any)
	assert.Equal(t, "", hosting["storageAccountResourceId"])
	assert.Contains(t, progress.String(), "BYO AKS preflight checks completed")
	assert.NotContains(t, progress.String(), "storage-type NOT_APPLICABLE")
	assert.Contains(t, p.preflightReport.String(), "storage-type NOT_APPLICABLE")
	assert.Equal(t, 1, fake.base.writes)
}

func TestBYOExistingDefaultDependencyMissingGrantRetainsARMRetry(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 2)
	fake.base.failWrite = true
	_, err := p.Deploy(t.Context(), func(string) {})
	require.ErrorContains(t, err, "offline deployment failure")
	assert.Equal(t, 1, fake.base.writes)
	fake.base.failWrite = false
	fake.missingGrant = defaultWorkloadID
	_, err = p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, 2, fake.base.writes, "mixed selections retain ARM resource and grant creation on retry")
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
	const principal = "44444444-4444-4444-8444-444444444444"
	p, fake, _ := suppliedProvider(t, 7)
	p.onDiskSource = &templateSource{
		mode: templateModeBicep, armTemplate: p.armTemplate,
		parameters: map[string]any{
			"hostingPrincipalId": map[string]any{"value": principal},
			"monitoringMode":     map[string]any{"value": "none"},
		},
	}
	_, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, 1, fake.base.writes)
	assert.Equal(t, map[string]any{"value": principal}, fake.base.parameters["hostingPrincipalId"])
	assert.Equal(t, principal, p.onDiskSource.parameters["hostingPrincipalId"].(map[string]any)["value"])
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
