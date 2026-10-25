// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBYOFoundryFailurePreservesNestedStageAndCorrelation(t *testing.T) {
	for _, diagnosticsDenied := range []bool{false, true} {
		name := map[bool]string{false: "nested service failure", true: "diagnostics unreadable"}[diagnosticsDenied]
		t.Run(name, func(t *testing.T) {
			p := byoContractProvider(t)
			nested := "/subscriptions/foundry-sub/resourceGroups/foundry-rg/providers/" +
				"Microsoft.Resources/deployments/foundry-resources"
			var reads int
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: byoHTTPContract(func(request *http.Request) (*http.Response, error) {
					reads++
					require.Equal(t, http.MethodGet, request.Method)
					require.Equal(t, "2021-04-01", request.URL.Query().Get("api-version"))
					if diagnosticsDenied {
						return contractLifecycleResponse(request, 403,
							`{"error":{"code":"AuthorizationFailed","message":"operation evidence denied"}}`), nil
					}
					if strings.EqualFold(request.URL.Path,
						nested+"/operations") {
						// #nosec G101 -- Synthetic diagnostic credentials verify non-disclosure.
						message := "Blueprint federatedIdentityCredentials was denied; " +
							"https://user:password@graph.invalid/applications?sig=secret#private"
						body := `{"value":[{"id":"nested-operation","properties":{
							"provisioningState":"Failed","serviceRequestId":"nested-service-request",
							"statusCode":"Forbidden","statusMessage":{"error":{"code":"Authorization_RequestDenied",
							"message":"` + message + `"}}}}]}`
						return contractLifecycleResponse(request, 200, body), nil
					}
					require.Equal(t, "/subscriptions/foundry-sub/providers/Microsoft.Resources/deployments/"+
						p.deploymentName()+"/operations", request.URL.Path)
					return contractLifecycleResponse(request, 200, `{"value":[{"id":"outer-operation","properties":{
						"provisioningState":"Failed","serviceRequestId":"outer-service-request",
						"statusMessage":{"error":{"code":"ResourceDeploymentFailure","message":"nested module failed"}},
						"targetResource":{"id":"`+nested+`","resourceType":"Microsoft.Resources/deployments"}}}]}`), nil
				}),
			}}
			root := &azcore.ResponseError{
				StatusCode: 400, ErrorCode: "DeploymentFailed",
				RawResponse: contractLifecycleResponse(&http.Request{
					Method: http.MethodPut, URL: &url.URL{
						Scheme: "https", Host: "management.azure.com", Path: "/deployment",
					},
					Host: "management.azure.com",
				}, 400, `{"error":{"code":"DeploymentFailed","message":"outer ARM failure"}}`),
			}
			root.RawResponse.Header.Set("x-ms-correlation-request-id", "root-correlation")
			err := p.deploymentFailure(t.Context(), root)
			require.ErrorIs(t, err, root)
			service, ok := errors.AsType[*azdext.ServiceError](err)
			require.True(t, ok)
			assert.Equal(t, 400, service.StatusCode)
			assert.Equal(t, "management.azure.com", service.ServiceName, "do not reattribute every ARM 403 to Graph")
			assert.Contains(t, service.ErrorCode, "DeploymentFailed")
			assert.Contains(t, service.Message, "F01/F02 Foundry ARM service stage")
			assert.Contains(t, service.Message, "root-correlation")
			assert.Contains(t, service.Suggestion, "tenant/policy administrator")
			if diagnosticsDenied {
				assert.Equal(t, 1, reads)
				assert.Contains(t, service.Message, "Additional deployment-operation evidence unavailable")
				assert.Contains(t, service.Message, "operation evidence denied")
			} else {
				assert.Equal(t, 2, reads)
				assert.Contains(t, service.Message, "nested-service-request")
				assert.Contains(t, service.Message, "Authorization_RequestDenied")
				assert.Contains(t, service.Message, "Blueprint federatedIdentityCredentials")
				assert.Contains(t, service.Message, "https://graph.invalid/applications")
			}
			for _, secret := range []string{"user:", "password", "?sig", "secret", "#private"} {
				assert.NotContains(t, service.Message, secret)
				assert.NotContains(t, err.Error(), secret)
			}
		})
	}
}

func TestBYODeploymentCancellationDoesNotReadMoreAzureEvidence(t *testing.T) {
	p := byoContractProvider(t)
	p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
		Transport: byoHTTPContract(func(*http.Request) (*http.Response, error) {
			t.Fatal("cancellation must not start diagnostic Azure reads")
			return nil, nil
		}),
	}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, p.deploymentFailure(ctx, context.Canceled), context.Canceled)
}
