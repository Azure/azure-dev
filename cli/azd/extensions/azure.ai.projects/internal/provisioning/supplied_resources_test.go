// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"azure.ai.projects/internal/onboarding"
	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
const byoRequirementsURL = "https://aka.ms/byo-aks-onboarding-prerequisites"

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

func byoPreparationStages(progress []string) []string {
	return slices.Compact(slices.DeleteFunc(slices.Clone(progress), func(message string) bool {
		return strings.HasPrefix(message, "Checking AKS and supporting resources: ")
	}))
}

func roundTripProvisioningError(t *testing.T, err error) error {
	t.Helper()
	envelope := azdext.NewProvisioningEnvelope()
	message := new(azdext.ProvisioningMessage)
	envelope.SetError(message, err)
	wire, marshalErr := proto.Marshal(message)
	require.NoError(t, marshalErr)
	received := new(azdext.ProvisioningMessage)
	require.NoError(t, proto.Unmarshal(wire, received))
	return envelope.GetError(received)
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
					assert.NotContains(t, progress.String(), "preflight")
					assert.NotContains(t, progress.String(), "resource-refs")
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

func TestBYODeployProgressFollowsPreparationStages(t *testing.T) {
	for _, failWrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("deployment failure=%t", failWrite), func(t *testing.T) {
			p, fake, _ := suppliedProvider(t, 7)
			fake.base.failWrite = failWrite
			var progress []string
			resourcesStarted, permissionsStarted := false, false
			_, err := p.Deploy(t.Context(), func(message string) {
				progress = append(progress, message)
				if strings.HasPrefix(message, "Checking AKS and supporting resources") {
					assert.False(t, permissionsStarted, "resource progress must finish before the permission stage")
				}
				switch message {
				case "Preparing Foundry provisioning template...":
					assert.Empty(t, fake.requests)
				case "Checking AKS and supporting resources...":
					if !resourcesStarted {
						assert.NotContains(t, fake.requests, "GET "+offlineClusterID)
						resourcesStarted = true
					}
				case "Checking required permissions...":
					permissionsStarted = true
					for _, id := range []string{
						offlineClusterID, offlineClusterID + "/agentPools",
						suppliedStorageID, suppliedHostingID, suppliedWorkloadID,
					} {
						assert.Contains(t, fake.requests, "GET "+id)
					}
					for _, request := range fake.requests {
						assert.NotContains(t, request, "/roleAssignments")
					}
				case "Resolving deployment identity and role bindings...":
					for _, scope := range []string{offlineClusterID, suppliedWorkloadID, suppliedStorageID} {
						assert.Contains(t, fake.requests,
							"GET "+scope+"/providers/Microsoft.Authorization/roleAssignments")
					}
					assert.Zero(t, fake.base.writes)
				}
			})
			if failWrite {
				require.ErrorContains(t, err, "offline deployment failure")
			} else {
				require.NoError(t, err)
			}
			stages := byoPreparationStages(progress)
			require.GreaterOrEqual(t, len(stages), 4)
			assert.Equal(t, []string{
				"Preparing Foundry provisioning template...",
				"Checking AKS and supporting resources...",
				"Checking required permissions...",
				"Resolving deployment identity and role bindings...",
			}, stages[:4])
			for _, operation := range []string{
				"subscription tenants", "reading AKS", "node pools", "Storage", "hosting identity", "workload identity",
			} {
				assert.True(t, slices.ContainsFunc(progress, func(message string) bool {
					return strings.HasPrefix(message, "Checking AKS and supporting resources: ") &&
						strings.Contains(message, operation)
				}), "missing resource-check progress for %s: %v", operation, progress)
			}
			assert.Equal(t, 1, fake.base.writes)
		})
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
				var reason string
				switch failure {
				case "missing Storage":
					reason = "Storage account does not exist"
					fake.overrides[strings.ToLower(suppliedStorageID)] = suppliedReply{404,
						map[string]any{"error": map[string]string{"code": "ResourceNotFound"}}}
				case "forbidden Storage":
					reason = "Permission to read Storage account was denied"
					fake.overrides[strings.ToLower(suppliedStorageID)] = suppliedReply{403,
						map[string]any{"error": map[string]string{"code": "AuthorizationFailed"}}}
				case "missing identity":
					reason = "Managed identity does not exist"
					fake.overrides[strings.ToLower(suppliedHostingID)] = suppliedReply{404,
						map[string]any{"error": map[string]string{"code": "ResourceNotFound"}}}
				case "tenant":
					reason = "Referenced and target subscriptions do not belong to the same tenant"
					fake.overrides["/subscriptions/cluster-sub"] = suppliedReply{200,
						map[string]any{"tenantId": "other-tenant"}}
				case "grant":
					reason = "Required Managed Identity Federated Identity Credential Contributor permission " +
						"is missing or conditional"
					fake.missingGrant = suppliedWorkloadID
				case "Automatic cluster":
					reason = "AKS Standard is required; the cluster does not use the supported Base SKU"
					fake.base.clusterSKUName = "Automatic"
				case "missing cluster SKU name":
					reason = "AKS Standard cluster type could not be verified"
					fake.base.clusterSKUName = ""
				}
				var progress []string
				writeProgress := func(message string) { progress = append(progress, message) }
				var err error
				if preview {
					_, err = p.Preview(t.Context(), writeProgress)
				} else {
					_, err = p.Deploy(t.Context(), writeProgress)
				}
				require.ErrorContains(t, err, "does not meet requirements.")
				assert.Contains(t, err.Error(), reason)
				initialProgress := "Preparing Foundry provisioning template..."
				if preview {
					initialProgress = "Computing deployment plan..."
				}
				assert.Equal(t, []string{
					initialProgress,
					"Checking AKS and supporting resources...",
					"Checking required permissions...",
				}, byoPreparationStages(progress))
				assert.NotContains(t, err.Error(), "BYO-AKS preflight report:")
				assert.NotContains(t, err.Error(), "resource-refs PASS")
				for _, hidden := range []string{
					"aks-type", offlineClusterID, suppliedStorageID, suppliedHostingID, suppliedWorkloadID,
					"Scope:", "Observed:", "Remediation:",
				} {
					assert.NotContains(t, err.Error(), hidden)
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
	t.Setenv("NO_COLOR", "1")
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
				resourceType := "Hosting managed identity"
				if test.role == "Storage Blob Data Contributor" {
					resourceType = "Workload managed identity"
				}
				expected := resourceType + " does not meet requirements. Review the requirements (" +
					byoRequirementsURL + ") and edit your configuration before proceeding.\n" +
					"- Required " + test.role + " role assignment has a condition"
				require.EqualError(t, err, expected)
				assert.Zero(t, fake.base.writes)
				assert.Zero(t, fake.base.previews)
				assert.Empty(t, p.virtualEnv)
				exitErr := fmt.Errorf("provisioning deploy failed: %w", roundTripProvisioningError(t, err))
				exitOutput := (&ux.ActionResult{Err: exitErr}).ToString("")
				assert.Equal(t, "ERROR: provisioning deploy failed: "+expected, strings.TrimSpace(exitOutput))
				for _, detail := range []string{
					assignmentID, test.identity, test.principal, test.scope, roleID,
					"restricted test condition", "remove its condition", "replace it with an unconditional",
					"different ID conflicts", "azd does not modify existing conditions",
					"resolve existing BYO AKS role assignments",
				} {
					assert.NotContains(t, exitOutput, detail)
				}
			})
		}
	}
}

func TestBYOReadFailuresPreserveCausesWithoutReport(t *testing.T) {
	//nolint:gosec // G101: synthetic URL credentials exercise final error redaction.
	const endpoint = "https://cause-user:cause-password@example.test/resource?sig=cause-secret#cause-fragment"
	for _, failure := range []string{"transport", "authentication", "decoding"} {
		for _, preview := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/preview=%t", failure, preview), func(t *testing.T) {
				p, fake, _ := suppliedProvider(t, 7)
				cause := errors.New("connection reset while reading " + endpoint)
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
				require.ErrorContains(t, err, "AKS does not meet requirements.")
				reason := "AKS cluster could not be read"
				if failure == "authentication" {
					reason = "Permission to read AKS cluster was denied"
				}
				assert.Contains(t, err.Error(), "- "+reason)
				blocked, ok := errors.AsType[*onboarding.BlockedError](err)
				require.True(t, ok)
				switch failure {
				case "transport":
					require.ErrorIs(t, err, cause)
				case "authentication":
					service, ok := errors.AsType[*azcore.ResponseError](err)
					require.True(t, ok)
					assert.Equal(t, http.StatusForbidden, service.StatusCode)
					assert.Equal(t, "AuthorizationFailed", service.ErrorCode)
				case "decoding":
					require.ErrorContains(t, errors.Join(blocked.Unwrap()...), "cannot unmarshal")
				}
				exitErr := fmt.Errorf("provisioning failed: %w", roundTripProvisioningError(t, err))
				if failure == "authentication" {
					service, ok := errors.AsType[*azdext.ServiceError](exitErr)
					require.True(t, ok)
					assert.Equal(t, http.StatusForbidden, service.StatusCode)
					assert.Equal(t, "AuthorizationFailed", service.ErrorCode)
				}
				exitOutput := (&ux.ActionResult{Err: exitErr}).ToString("")
				assert.Contains(t, exitOutput, "ERROR:")
				assert.Contains(t, exitOutput, "AKS does not meet requirements.")
				assert.Contains(t, exitOutput, "- "+reason)
				assert.NotContains(t, progress.String(), "preflight")
				for _, output := range []string{err.Error(), exitOutput, progress.String()} {
					assert.NotContains(t, output, "BYO-AKS preflight report:")
					assert.NotContains(t, output, "resource-refs PASS")
					assert.NotContains(t, output, "  Expected:")
					assert.NotContains(t, output, "  Cause:")
					assert.NotContains(t, output, offlineClusterID)
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

func TestBYOBlockedErrorPresentationSurvivesTransport(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprintf("terminal=%t", terminal), func(t *testing.T) {
			t.Setenv("FORCE_COLOR", "")
			if terminal {
				t.Setenv("FORCE_COLOR", "1")
			}
			t.Setenv("NO_COLOR", "")
			t.Setenv("CI", "")
			t.Setenv("TERM", "xterm-256color")
			t.Setenv("WT_SESSION", "test-terminal")
			t.Setenv("TERM_PROGRAM", "")
			p, fake, _ := suppliedProvider(t, 7)
			fake.base.clusterSKUName = "Automatic"
			fake.overrides[strings.ToLower(suppliedStorageID)] = suppliedReply{404,
				map[string]any{"error": map[string]string{"code": "ResourceNotFound"}}}
			_, err := p.Deploy(t.Context(), func(string) {})
			require.Error(t, err)
			link := "requirements (" + byoRequirementsURL + ")"
			if terminal {
				link = "\x1b]8;;" + byoRequirementsURL + "\x1b\\requirements\x1b]8;;\x1b\\"
			}
			expected := "Storage account does not meet requirements. Review the " + link +
				" and edit your configuration before proceeding.\n" +
				"- Storage account does not exist\n- Storage configuration could not be read\n\n" +
				"AKS does not meet requirements. Review the " + link +
				" and edit your configuration before proceeding.\n" +
				"- AKS Standard is required; the cluster does not use the supported Base SKU"
			assert.EqualError(t, err, expected)
			transported := roundTripProvisioningError(t, err)
			service, ok := errors.AsType[*azdext.ServiceError](transported)
			require.True(t, ok)
			assert.Equal(t, "ResourceNotFound", service.ErrorCode)
			assert.Equal(t, http.StatusNotFound, service.StatusCode)
			exitOutput := (&ux.ActionResult{Err: transported}).ToString("")
			assert.Equal(t, "ERROR: "+expected, strings.TrimSpace(exitOutput))
			if !terminal {
				assert.NotContains(t, exitOutput, "\x1b")
			}
			assert.Zero(t, fake.base.writes)
		})
	}
}

func TestBYORoleAssignmentReadFailureKeepsContext(t *testing.T) {
	for _, preview := range []bool{false, true} {
		t.Run(fmt.Sprintf("preview=%t", preview), func(t *testing.T) {
			p, fake, _ := suppliedProvider(t, 2)
			scope := offlineClusterID + "/providers/Microsoft.Authorization/roleAssignments"
			fake.overrides[strings.ToLower(scope)] = suppliedReply{403,
				map[string]any{"error": map[string]string{"code": "AuthorizationFailed"}}}
			var err error
			if preview {
				_, err = p.Preview(t.Context(), func(string) {})
			} else {
				_, err = p.Deploy(t.Context(), func(string) {})
			}
			require.ErrorContains(t, err, "resolve existing BYO AKS role assignments: read applicable role assignments")
			_, blocked := errors.AsType[*onboarding.BlockedError](err)
			assert.False(t, blocked)
			service, ok := errors.AsType[*azcore.ResponseError](err)
			require.True(t, ok)
			assert.Equal(t, http.StatusForbidden, service.StatusCode)
			transported := roundTripProvisioningError(t, err)
			assert.Contains(t, transported.Error(), "resolve existing BYO AKS role assignments:")
			assert.NotContains(t, transported.Error(), "does not meet requirements")
			wireService, ok := errors.AsType[*azdext.ServiceError](transported)
			require.True(t, ok)
			assert.Equal(t, "AuthorizationFailed", wireService.ErrorCode)
			assert.Zero(t, fake.base.writes)
			assert.Zero(t, fake.base.previews)
		})
	}
}

func TestBYOLateReadErrorsRedactURLs(t *testing.T) {
	//nolint:gosec // G101: synthetic URL credentials exercise the preparation error boundary.
	const endpoint = "https://lookup-user:lookup-password@example.test/resource?sig=lookup-secret#lookup-fragment"
	for _, query := range []struct{ name, path, context string }{
		{"federation role", "/subscriptions/target-sub/providers/Microsoft.Authorization/roleDefinitions",
			"resolve built-in federation role:"},
		{"existing role assignments", offlineClusterID + "/providers/Microsoft.Authorization/roleAssignments",
			"resolve existing BYO AKS role assignments: read applicable role assignments"},
	} {
		for _, failure := range []string{"transport", "response"} {
			for _, preview := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/preview=%t", query.name, failure, preview), func(t *testing.T) {
					p, fake, _ := suppliedProvider(t, 2)
					cause := &url.Error{Op: http.MethodGet, URL: endpoint, Err: errors.New("connection reset")}
					queried := false
					p.armClientOptions.Transport = acrTestTransport(func(request *http.Request) (*http.Response, error) {
						if request.Method != http.MethodGet || request.URL.Path != query.path {
							return fake.Do(request)
						}
						queried = true
						if failure == "response" {
							return fake.base.response(request, http.StatusForbidden, map[string]any{
								"error": map[string]string{
									"code": "AuthorizationFailed", "message": "service rejected " + endpoint,
								},
							}), nil
						}
						return nil, cause
					})
					var err error
					var progress []string
					if preview {
						_, err = p.Preview(t.Context(), func(message string) { progress = append(progress, message) })
					} else {
						_, err = p.Deploy(t.Context(), func(message string) { progress = append(progress, message) })
					}
					require.True(t, queried)
					require.ErrorContains(t, err, query.context)
					assert.Contains(t, progress, "Resolving deployment identity and role bindings...")
					_, blocked := errors.AsType[*onboarding.BlockedError](err)
					assert.False(t, blocked)
					transported := roundTripProvisioningError(t, err)
					if failure == "transport" {
						require.ErrorIs(t, err, cause)
						original, ok := errors.AsType[*url.Error](err)
						require.True(t, ok)
						assert.Same(t, cause, original)
						assert.Equal(t, endpoint, original.URL)
					} else {
						original, ok := errors.AsType[*azcore.ResponseError](err)
						require.True(t, ok)
						assert.Equal(t, http.StatusForbidden, original.StatusCode)
						assert.Equal(t, "AuthorizationFailed", original.ErrorCode)
						assert.Contains(t, original.Error(), endpoint)
						service, ok := errors.AsType[*azdext.ServiceError](transported)
						require.True(t, ok)
						assert.Equal(t, original.StatusCode, service.StatusCode)
						assert.Equal(t, original.ErrorCode, service.ErrorCode)
					}
					for _, output := range []string{
						err.Error(), azdext.WrapError(err).GetMessage(), transported.Error(),
						(&ux.ActionResult{Err: transported}).ToString(""),
						fmt.Sprintf("Handler returned error: %v", err),
					} {
						assert.Contains(t, output, query.context)
						assert.Contains(t, output, "https://example.test/resource")
						for _, secret := range []string{
							"lookup-user", "lookup-password", "sig=", "lookup-secret", "lookup-fragment",
						} {
							assert.NotContains(t, output, secret)
						}
						assert.NotContains(t, output, "BYO-AKS preflight report:")
					}
					assert.Zero(t, fake.base.writes)
					assert.Zero(t, fake.base.previews)
				})
			}
		}
	}
}

func TestBYOPreparationErrorRedactionPreservesClassification(t *testing.T) {
	//nolint:gosec // G101: synthetic URL credentials exercise structured error serialization.
	const endpoint = "https://lookup-user:lookup-password@example.test/resource?sig=lookup-secret#lookup-fragment"
	message := "request to " + endpoint + " failed"
	toolCause := errors.New("process exited")
	for _, test := range []struct {
		name      string
		cause     error
		canonical error
	}{
		{"service", &azdext.ServiceError{
			Message: message, StatusCode: http.StatusForbidden,
			ErrorCode: "AuthorizationFailed", ServiceName: "example.test",
		}, nil},
		{"local", &azdext.LocalError{
			Message: message, Category: azdext.LocalErrorCategoryAuth, Code: "login_required",
		}, nil},
		{"tool", &azdext.ToolError{
			Message: message, ToolName: "bicep", Kind: azdext.ToolErrorKindFailed, ExitCode: new(1), Err: toolCause,
		}, nil},
		{"canceled", fmt.Errorf("%s: %w", message, context.Canceled), context.Canceled},
		{"deadline", fmt.Errorf("%s: %w", message, context.DeadlineExceeded), context.DeadlineExceeded},
		{"grpc auth", status.Error(codes.Unauthenticated, message), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := fmt.Errorf("BYO lookup: %w", test.cause)
			before := original.Error()
			expected := strings.ReplaceAll(before, endpoint, "https://example.test/resource")
			classified := azdext.WrapError(original)
			classified.Message = expected
			redacted := redactBYOPreparationError(original)
			assert.EqualError(t, redacted, expected)
			require.ErrorIs(t, redacted, original)
			require.ErrorIs(t, redacted, test.cause)
			assert.Equal(t, before, original.Error(), "the original shared cause must not be modified")
			assert.True(t, proto.Equal(classified, azdext.WrapError(redacted)))
			transported := roundTripProvisioningError(t, redacted)
			assert.EqualError(t, transported, expected)
			assert.Equal(t, "ERROR: "+expected, strings.TrimSpace((&ux.ActionResult{Err: transported}).ToString("")))
			assert.Equal(t, "Handler returned error: "+expected, fmt.Sprintf("Handler returned error: %v", redacted))
			if test.canonical != nil {
				require.ErrorIs(t, redacted, test.canonical)
				require.ErrorIs(t, transported, test.canonical)
			}
			if originalTool, ok := errors.AsType[*azdext.ToolError](test.cause); ok {
				copy, ok := errors.AsType[*azdext.ToolError](redacted)
				require.True(t, ok)
				assert.NotSame(t, originalTool, copy)
				expectedTool := *originalTool
				expectedTool.Message = expected
				assert.Equal(t, &expectedTool, copy)
				require.ErrorIs(t, redacted, toolCause)
			}
		})
	}
}

func TestBYOPreparationErrorRedactionPreservesBlockedLinks(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprintf("terminal=%t", terminal), func(t *testing.T) {
			t.Setenv("FORCE_COLOR", "1")
			t.Setenv("NO_COLOR", "1")
			if terminal {
				t.Setenv("NO_COLOR", "")
			}
			t.Setenv("CI", "")
			t.Setenv("TERM", "xterm-256color")
			t.Setenv("WT_SESSION", "test-terminal")
			t.Setenv("TERM_PROGRAM", "")
			//nolint:gosec // G101: a hidden structured cause must not bypass the short blocked message.
			const endpoint = "https://lookup-user:lookup-password@example.test/resource?sig=lookup-secret#lookup-fragment"
			cause := &azdext.ServiceError{
				Message:    "request to " + endpoint + " failed",
				StatusCode: http.StatusForbidden, ErrorCode: "AuthorizationFailed",
			}
			blocked := onboarding.Report{{
				ResourceType: "AKS", Status: onboarding.Unverified, Mandatory: true,
				FailureReasons: []string{"AKS cluster could not be read"}, Cause: cause,
			}}.Err()
			expected := blocked.Error()
			redacted := redactBYOPreparationError(blocked)
			require.ErrorIs(t, redacted, blocked)
			assert.EqualError(t, redacted, expected)
			assert.Equal(t, expected, azdext.WrapError(redacted).GetMessage())
			assert.EqualError(t, roundTripProvisioningError(t, redacted), expected)
			assert.Contains(t, cause.Error(), endpoint)
			if terminal {
				assert.Contains(t, redacted.Error(), "\x1b]8;;"+byoRequirementsURL+"\x1b\\requirements\x1b]8;;\x1b\\")
			} else {
				assert.Contains(t, redacted.Error(), "requirements ("+byoRequirementsURL+")")
				assert.NotContains(t, redacted.Error(), "\x1b")
			}
		})
	}
}

func TestBYOPreparationErrorRedactionLeavesSafeErrorsUnchanged(t *testing.T) {
	require.NoError(t, redactBYOPreparationError(nil))
	cause := errors.New("required role is unavailable")
	assert.Same(t, cause, redactBYOPreparationError(cause))
	assert.Equal(t, "request to [redacted URL] failed",
		redactBYOErrorMessage("request to https://example.test/%invalid?sig=secret failed"))
}

func TestBYOCanceledReadsRetainCancellation(t *testing.T) {
	for _, preview := range []bool{false, true} {
		t.Run(fmt.Sprintf("preview=%t", preview), func(t *testing.T) {
			p, fake, _ := suppliedProvider(t, 7)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p.armClientOptions.Transport = acrTestTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == offlineClusterID {
					cancel()
					return nil, ctx.Err()
				}
				return fake.Do(request)
			})
			var err error
			if preview {
				_, err = p.Preview(ctx, func(string) {})
			} else {
				_, err = p.Deploy(ctx, func(string) {})
			}
			require.ErrorIs(t, err, context.Canceled)
			assert.NotContains(t, err.Error(), "does not meet requirements")
			transported := roundTripProvisioningError(t, err)
			require.ErrorIs(t, transported, context.Canceled)
			local, ok := errors.AsType[*azdext.LocalError](transported)
			require.True(t, ok)
			assert.Equal(t, azdext.LocalErrorCategoryUser, local.Category)
			assert.Equal(t, "canceled", local.Code)
			assert.Zero(t, fake.base.writes)
			assert.Zero(t, fake.base.previews)
		})
	}
}

func TestBYOSuppliedPreviewUsesNormalWhatIfAfterReadOnlyChecks(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 7)
	var progress []string
	result, err := p.Preview(t.Context(), func(message string) { progress = append(progress, message) })
	require.NoError(t, err)
	stages := byoPreparationStages(progress)
	require.GreaterOrEqual(t, len(stages), 4)
	assert.Equal(t, []string{
		"Computing deployment plan...",
		"Checking AKS and supporting resources...",
		"Checking required permissions...",
		"Resolving deployment identity and role bindings...",
	}, stages[:4])
	require.Len(t, result.Preview.Changes, 1)
	assert.Equal(t, 1, fake.base.previews)
	assert.Zero(t, fake.base.writes)
	assert.True(t, strings.HasSuffix(fake.requests[len(fake.requests)-1], "/whatIf"))
}

func TestBYODefaultStorageRetainsOrdinaryARMUpdate(t *testing.T) {
	p, fake, _ := suppliedProvider(t, 2)
	_, err := p.Deploy(t.Context(), func(string) {})
	require.NoError(t, err)
	hosting := fake.base.parameters["agentHosting"].(map[string]any)["value"].(map[string]any)
	assert.Equal(t, "", hosting["storageAccountResourceId"])
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
