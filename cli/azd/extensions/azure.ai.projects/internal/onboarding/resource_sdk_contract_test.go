// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeferredTelemetryHasNoGateOrExclusiveNetworkReads(t *testing.T) {
	input := contractPlanInput()
	input.AgentSubnetID, input.StorageID, input.RegistryID = "", "", ""
	plan, err := BuildPlan(input)
	require.NoError(t, err)
	plan.AgentSubnet, plan.Network = Resource{}, nil
	reads := 0
	options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
		reads++
		return nil, fmt.Errorf("unexpected network request: %s %s", request.Method, request.URL.Path)
	})
	checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
	fixture := newResourceRuleContractFixture(t)
	report := checker.networkChecks(t.Context(), plan, &fixture.Cluster, fixture.Pools, nil)
	require.NoError(t, report.Err())
	assert.Zero(t, reads, "no S03 or N02 selection leaves no network reads for deferred telemetry")
	for _, check := range report {
		assert.NotEqual(t, "N01", check.ID, "deferred telemetry emits neither a gate nor a skipped stub")
	}
}

func TestStorageRecommendationUsesActualARMSKUNotRegionPolicy(t *testing.T) {
	for _, tc := range []struct {
		sku, location string
		warning       bool
	}{
		{"Standard_ZRS", "westus", false},
		{"Standard_GZRS", "westus", false},
		{"Standard_LRS", "eastus", true},
		{"Standard_GRS", "eastus", true},
	} {
		t.Run(tc.sku, func(t *testing.T) {
			input := contractPlanInput()
			input.Location = tc.location
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				require.True(t, strings.EqualFold(plan.Storage.ID, request.URL.Path))
				return contractARMResponse(request, http.StatusOK, fmt.Sprintf(
					`{"id":%q,"location":%q,"kind":"StorageV2","sku":{"name":%q},"properties":{`+
						`"provisioningState":"Succeeded","accessTier":"Hot","isHnsEnabled":false,`+
						`"supportsHttpsTrafficOnly":true,"minimumTlsVersion":"TLS1_2",`+
						`"allowSharedKeyAccess":false,"allowBlobPublicAccess":false}}`,
					plan.Storage.ID, tc.location, tc.sku)), nil
			})
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
			account, report := checker.readStorage(t.Context(), plan.Storage)
			require.NoError(t, report.Err())
			require.NotNil(t, account)
			report = append(report, storageResourceChecks(*account)...)
			require.NoError(t, report.Err())
			check := resourceRuleContractCheck(t, report, "S01")
			assert.Contains(t, check.Observed, "SKU="+tc.sku)
			assert.Equal(t, tc.warning, check.Warning != "")
		})
	}
}

func TestOmittedStorageHasNoZoneRedundancyRecommendation(t *testing.T) {
	input := contractPlanInput()
	input.StorageID, input.RegistryID, input.AgentSubnetID = "", "", ""
	plan, err := BuildPlan(input)
	require.NoError(t, err)
	options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, request.Method)
		assert.NotContains(t, strings.ToLower(request.URL.Path), "/storageaccounts/")
		return contractARMResponse(request, http.StatusForbidden,
			`{"error":{"code":"AuthorizationFailed","message":"offline unrelated reads denied"}}`), nil
	})
	_, report, err := NewResourceChecker(contractCredential{}, options).Check(t.Context(), plan)
	require.Error(t, err, "unrelated mandatory failures must still block")
	check := resourceRuleContractCheck(t, report, "S01")
	assert.Equal(t, NotApplicable, check.Status)
	assert.Empty(t, check.Warning)
}

func TestResourceSDKUsesOwningTenantAcrossUniqueSubscriptions(t *testing.T) {
	for _, tc := range []struct {
		name, otherTenant string
		pass              bool
	}{
		{"same tenant cross subscription", "owner", true},
		{"different owning tenant", "other", false},
		{"managed by tenant is not owning tenant", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := contractPlanInput()
			input.StorageID = strings.Replace(
				input.StorageID, "/subscriptions/foundry-sub/", "/subscriptions/storage-sub/", 1)
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			reads := map[string]int{}
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				assert.Equal(t, "2022-12-01", request.URL.Query().Get("api-version"))
				reads[request.URL.Path]++
				tenant := "owner"
				if strings.EqualFold(request.URL.Path, "/subscriptions/aks-sub") {
					tenant = tc.otherTenant
				}
				return contractARMResponse(request, 200,
					fmt.Sprintf(`{"tenantId":%q,"managedByTenants":[{"tenantId":"owner"}]}`, tenant)), nil
			})
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
			tenant, report := checker.subscriptionTenants(t.Context(), plan)
			if tc.pass {
				require.NoError(t, report.Err())
				assert.Equal(t, "owner", tenant)
			} else {
				require.Error(t, report.Err())
				assert.Empty(t, tenant)
			}
			require.Contains(t, reads, "/subscriptions/"+plan.SubscriptionID)
			for _, count := range reads {
				assert.Equal(t, 1, count, "each owning subscription is read exactly once")
			}
		})
	}
}

func TestI02DoesNotValidateSelectedAgentSubnetOrItsOwningSubscription(t *testing.T) {
	for _, subnetID := range []string{
		"/subscriptions/network-only/resourceGroups/network-rg/providers/Microsoft.Network/virtualNetworks/vnet/subnets/agent",
		"not-an-arm-id",
	} {
		t.Run(subnetID, func(t *testing.T) {
			plan, err := BuildPlan(contractPlanInput())
			require.NoError(t, err)
			plan.AgentSubnet = Resource{ID: subnetID, Supplied: true}
			plan.LegacyNetwork = &LegacyNetwork{Enabled: true, VNetID: "not-an-i02-reference"}
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				assert.NotContains(t, request.URL.Path, "network-only")
				assert.NotContains(t, strings.ToLower(request.URL.Path), "/virtualnetworks/")
				return contractARMResponse(request, http.StatusOK, `{"tenantId":"owner"}`), nil
			})
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
			tenant, report := checker.subscriptionTenants(t.Context(), plan)
			require.NoError(t, report.Err())
			assert.Equal(t, "owner", tenant)
			for _, check := range report {
				assert.NotEqual(t, subnetID, check.Scope)
			}
		})
	}
}

func TestSelectedAgentSubnetHasNoI02ReadinessGate(t *testing.T) {
	for _, state := range []string{"Succeeded", "Failed", ""} {
		t.Run("state="+state, func(t *testing.T) {
			f := newNetworkARMContract(t)
			input := contractPlanInput()
			input.StorageID, input.RegistryID = "", ""
			input.AgentSubnetID = resourceValue(f.vnet.ID) + "/subnets/selected"
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			f.subnet = map[string]any{"id": plan.AgentSubnet.ID, "properties": map[string]any{
				"addressPrefix": "10.0.1.0/27", "provisioningState": state,
				"delegations": []any{map[string]any{
					"properties": map[string]any{"serviceName": "Microsoft.App/environments"},
				}},
			}}
			checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(f.options())}
			report := checker.networkChecks(t.Context(), plan, &f.fixture.Cluster, f.fixture.Pools, nil)
			require.NoError(t, report.Err())
			var n02 Report
			for _, check := range report {
				assert.False(t, check.ID == "I02" && strings.EqualFold(check.Scope, plan.AgentSubnet.ID))
				if check.ID == "N02" {
					n02 = append(n02, check)
				}
			}
			require.NotEmpty(t, n02, "independent basic-subnet N02 remains applicable")
			assert.Contains(t, f.methods, "GET "+plan.AgentSubnet.ID, "N02 still reads necessary subnet metadata")
			for _, operation := range []Operation{
				{CheckID: "P03", Action: "Microsoft.Network/virtualNetworks/write",
					Scope: "/subscriptions/aks-sub/resourceGroups/aks-rg"},
			} {
				assert.Contains(t, plan.Operations, operation, "independent network permission planning remains")
			}
		})
	}
}

func TestResourceSDKPreflightNeverRegistersProvidersOrReadsFutureIdentities(t *testing.T) {
	plan, err := BuildPlan(contractPlanInput())
	require.NoError(t, err)
	var requests int
	options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
		requests++
		assert.Equal(t, http.MethodGet, request.Method)
		assert.NotContains(t, strings.ToLower(request.URL.Path), "/userassignedidentities/")
		assert.NotContains(t, strings.ToLower(request.URL.Path), "/listkeys")
		assert.NotContains(t, strings.ToLower(request.URL.Path), "/register")
		return contractARMResponse(request, http.StatusConflict,
			`{"error":{"code":"MissingSubscriptionRegistration","message":"register administratively"}}`), nil
	})
	checker := NewResourceChecker(contractCredential{}, options)
	_, report, err := checker.Check(t.Context(), plan)
	require.Error(t, err)
	require.Positive(t, requests)
	require.Error(t, report.Err())
	require.False(t, options.DisableRPRegistration, "the caller's client options must not be mutated")
	var response *azcore.ResponseError
	require.True(t, errors.As(err, &response))
	assert.Equal(t, "MissingSubscriptionRegistration", response.ErrorCode)
}

func TestResourceSDKReadsAgentPoolChildrenAndLocationSKUs(t *testing.T) {
	plan, err := BuildPlan(contractPlanInput())
	require.NoError(t, err)
	var clusterRead, poolRead, skuRead bool
	options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, request.Method)
		var body string
		switch {
		case strings.HasSuffix(strings.ToLower(request.URL.Path), "/agentpools"):
			poolRead = true
			body = `{"value":[{"name":"actual-child","properties":{"mode":"User","vmSize":"Standard_D8as_v5"}}]}`
		case strings.Contains(strings.ToLower(request.URL.Path), "/providers/microsoft.compute/skus"):
			skuRead = true
			assert.Contains(t, request.URL.Query().Get("$filter"), "westus")
			body = `{"value":[{"name":"Standard_D8as_v5","resourceType":"virtualMachines","locations":["westus"]}]}`
		case strings.EqualFold(request.URL.Path, plan.Cluster.ID):
			clusterRead = true
			body = `{"id":"` + plan.Cluster.ID + `","location":"westus","properties":{"provisioningState":"Succeeded"}}`
		default:
			t.Fatalf("unexpected resource read: %s", request.URL.Path)
		}
		return contractARMResponse(request, 200, body), nil
	})
	checker := &sdkResourceChecker{credential: contractCredential{}, options: onboardingARMOptions(options)}
	cluster, pools, skus, report := checker.readCluster(t.Context(), plan.Cluster)
	require.NoError(t, report.Err())
	require.NotNil(t, cluster)
	require.Len(t, pools, 1)
	assert.Equal(t, "actual-child", *pools[0].Name)
	require.Len(t, skus, 1)
	assert.True(t, clusterRead && poolRead && skuRead)
}

func TestResourceSDKI02OnlyChecksReferencedResourcesAndOwningTenants(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		t.Run(fmt.Sprintf("storage-and-registry-supplied=%t", supplied), func(t *testing.T) {
			input := contractPlanInput()
			if !supplied {
				input.StorageID, input.RegistryID = "", ""
			}
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				path := strings.ToLower(request.URL.Path)
				require.NotContains(t, path, "/register")
				require.NotContains(t, path, "/userassignedidentities/")
				var body any
				switch {
				case strings.Count(path, "/") == 2 && strings.HasPrefix(path, "/subscriptions/"):
					body = map[string]any{"tenantId": "owning-tenant"}
				case strings.EqualFold(request.URL.Path, plan.Cluster.ID):
					body = map[string]any{"id": plan.Cluster.ID, "location": plan.Location,
						"properties": map[string]any{"provisioningState": "Succeeded"}}
				case supplied && strings.EqualFold(request.URL.Path, plan.Storage.ID):
					body = storageContractBody(plan.Storage.ID)
				case supplied && strings.EqualFold(request.URL.Path, plan.Registry.ID):
					body = map[string]any{"id": plan.Registry.ID, "properties": map[string]any{
						"provisioningState": "Succeeded", "roleAssignmentMode": "LegacyRegistryPermissions",
						"loginServer": "registry.azurecr.io", "publicNetworkAccess": "Enabled",
					}}
				case strings.Contains(path, "/subnets/"):
					body = map[string]any{"id": request.URL.Path, "properties": map[string]any{
						"provisioningState": "Succeeded", "addressPrefix": "10.20.0.0/27",
					}}
				case strings.HasSuffix(path, "/agentpools"), strings.HasSuffix(path, "/skus"):
					body = map[string]any{"value": []any{}}
				default:
					return contractARMResponse(request, 404, `{"error":{"code":"ResourceNotFound"}}`), nil
				}
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				return contractARMResponse(request, 200, string(raw)), nil
			})
			_, report, _ := NewResourceChecker(contractCredential{}, options).Check(t.Context(), plan)
			var inputs Report
			var networkUnknown bool
			for _, check := range report {
				if check.ID == "I02" {
					inputs = append(inputs, check)
					assert.Equal(t, Pass, check.Status, check.Observed)
				}
				assert.NotEqual(t, "N01", check.ID, "deferred telemetry checks must not emit any gate")
				if check.ID == "N02" && check.Status == Unverified {
					networkUnknown = true
				}
			}
			require.NotEmpty(t, inputs)
			require.NoError(t, inputs.Err(), "resource evidence must not require subscription/region preview admission")
			require.True(t, networkUnknown, "removing admission must not relax applicable hosting-subnet ARM checks")
		})
	}
}

func TestReportRedactsURLSchemesWithoutDiscardingServiceCauses(t *testing.T) {
	for _, scheme := range []string{"https", "HTTPS", "http", "HTTP"} {
		t.Run(scheme, func(t *testing.T) {
			// #nosec G101 -- Synthetic URL credentials exercise output non-disclosure.
			endpoint := scheme + "://user:password@account.invalid/blob?sig=secret#private"
			cause := fmt.Errorf("service diagnostic: %s", endpoint)
			report := Report{{
				ID: "S03", Status: Unverified, Mandatory: true, Scope: endpoint,
				Expected: endpoint, Observed: cause.Error(), Missing: endpoint, Remediation: endpoint, Cause: cause,
				Warning: endpoint,
			}}
			var output bytes.Buffer
			require.NoError(t, report.Write(&output))
			err := report.Err()
			require.Error(t, err)
			require.ErrorIs(t, err, cause)
			for _, secret := range []string{"user:", "password", "?sig", "secret", "#private"} {
				assert.NotContains(t, output.String(), secret)
				assert.NotContains(t, err.Error(), secret)
			}

			assert.Contains(t, output.String(), "account.invalid/blob")
		})
	}
}

func TestSuppliedRegistryPreflightUsesSelectedARMConfiguration(t *testing.T) {
	for _, scenario := range []string{"public RBAC", "private ABAC", "not ready", "private rejected", "unreadable"} {
		t.Run(scenario, func(t *testing.T) {
			input := contractPlanInput()
			input.StorageID, input.AgentSubnetID = "", ""
			plan, err := BuildPlan(input)
			require.NoError(t, err)
			fixture := newResourceRuleContractFixture(t)
			fixture.Cluster.ID = new(plan.Cluster.ID)
			reads := map[string]int{}
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodGet, request.Method)
				path := request.URL.Path
				reads[path]++
				var body any
				switch {
				case strings.Count(path, "/") == 2 && strings.HasPrefix(path, "/subscriptions/"):
					body = map[string]any{"tenantId": "tenant"}
				case strings.EqualFold(path, plan.Cluster.ID):
					body = fixture.Cluster
				case strings.EqualFold(path, plan.Cluster.ID+"/agentPools"):
					body = map[string]any{"value": fixture.Pools}
				case strings.HasSuffix(path, "/skus"):
					body = map[string]any{"value": fixture.SKUs}
				case strings.EqualFold(path, plan.Registry.ID):
					if scenario == "unreadable" {
						return contractARMResponse(request, http.StatusForbidden,
							`{"error":{"code":"AuthorizationFailed"}}`), nil
					}
					properties := map[string]any{
						"provisioningState": "Succeeded", "loginServer": "registry.azurecr.io",
						"publicNetworkAccess": "Enabled", "roleAssignmentMode": "LegacyRegistryPermissions",
					}
					if strings.HasPrefix(scenario, "private") {
						status := "Approved"
						if scenario == "private rejected" {
							status = "Rejected"
						}
						properties["publicNetworkAccess"] = "Disabled"
						properties["roleAssignmentMode"] = "AbacRepositoryPermissions"
						properties["privateEndpointConnections"] = []any{map[string]any{
							"properties": map[string]any{
								"privateLinkServiceConnectionState": map[string]any{"status": status},
							},
						}}
					}
					if scenario == "not ready" {
						properties["provisioningState"] = "Failed"
					}
					body = map[string]any{"id": plan.Registry.ID, "properties": properties}
				default:
					t.Fatalf("unexpected resource read: %s", path)
				}
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				return contractARMResponse(request, http.StatusOK, string(raw)), nil
			})
			pipeline := Pipeline{
				Resources: NewResourceChecker(contractCredential{}, options),
				Access:    contractAccess(func(context.Context, Plan, Facts) (Report, error) { return nil, nil }),
			}
			facts, report, err := pipeline.Preflight(t.Context(), plan)
			check := resourceRuleContractCheck(t, report, "C01")
			if scenario == "public RBAC" || scenario == "private ABAC" {
				require.NoError(t, err)
				require.NoError(t, report.Err())
				assert.Equal(t, Pass, check.Status)
				assert.Equal(t, scenario == "private ABAC", facts.RegistryABAC)
			} else {
				require.Error(t, err)
				assert.True(t, check.Mandatory)
				assert.NotEqual(t, Pass, check.Status)
			}
			if scenario == "unreadable" {
				response, ok := errors.AsType[*azcore.ResponseError](err)
				require.True(t, ok)
				assert.Equal(t, http.StatusForbidden, response.StatusCode)
			}
			assert.Equal(t, 1, reads[plan.Registry.ID])
			assert.Equal(t, NotApplicable, resourceRuleContractCheck(t, report, "S03").Status)
			assert.Equal(t, NotApplicable, resourceRuleContractCheck(t, report, "N02").Status)
		})
	}
}
