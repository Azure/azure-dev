// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"bytes"
	"context"
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
