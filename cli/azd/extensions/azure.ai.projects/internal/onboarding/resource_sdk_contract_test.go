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
				if check.ID == "N01" && check.Status == Unverified {
					networkUnknown = true
				}
			}
			require.NotEmpty(t, inputs)
			require.NoError(t, inputs.Err(), "resource evidence must not require subscription/region preview admission")
			require.True(t, networkUnknown, "removing admission must not relax source-bound network evidence")
		})
	}
}

type networkEvidenceContract func(context.Context, NetworkRequest) (NetworkEvidence, error)

func (f networkEvidenceContract) Evidence(ctx context.Context, request NetworkRequest) (NetworkEvidence, error) {
	return f(ctx, request)
}

func TestNetworkEvidenceOutputDoesNotDiscloseURLCredentials(t *testing.T) {
	// #nosec G101 -- Deliberately synthetic credentials verify that diagnostic URLs are redacted.
	const diagnosticURL = "https://username:password@account.blob.core.windows.net/container?sig=credential#private"
	endpoint := safeResourceEndpoint(diagnosticURL)
	assert.Equal(t, "https://account.blob.core.windows.net/container", endpoint)
	provider := networkEvidenceContract(func(_ context.Context, request NetworkRequest) (NetworkEvidence, error) {
		return NetworkEvidence{
			SourceSubnetIDs: request.SourceSubnetIDs, Destination: request.Destination, Description: diagnosticURL,
			ExistingWorkload: true, DNSVerified: true, TCP443Verified: true,
			EffectiveRoutesVerified: true, EffectivePolicyVerified: true,
		}, nil
	})
	check := networkEvidenceCheck(t.Context(), provider, NetworkRequest{
		CheckID: "S03", Scope: "storage", SourceSubnetIDs: []string{"agent-source"}, Destination: endpoint,
	}, resourceCheck("S03", "storage", "Blob network evidence"), false)
	require.Equal(t, Pass, check.Status)
	check.Observed += "; endpoint=" + diagnosticURL
	var output bytes.Buffer
	require.NoError(t, (Report{check}).Write(&output))
	for _, secret := range []string{"username", "password", "sig=", "credential", "#private"} {
		assert.NotContains(t, output.String(), secret)
	}

	check.Status = Fail
	for _, secret := range []string{"username", "password", "sig=", "credential", "#private"} {
		assert.NotContains(t, (Report{check}).Err().Error(), secret)
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
