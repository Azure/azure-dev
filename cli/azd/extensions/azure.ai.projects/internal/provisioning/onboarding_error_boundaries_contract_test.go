// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBYODiagnosticCancellationStopsPaginationAndRecursion(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{
			name: "next page",
			body: `{"value":[],"nextLink":"https://management.azure.com/subscriptions/foundry-sub/` +
				`providers/Microsoft.Resources/deployments/next/operations?api-version=2021-04-01"}`,
		},
		{
			name: "nested deployment",
			body: `{"value":[{"id":"outer-operation","properties":{` +
				`"provisioningState":"Failed",` +
				`"statusMessage":{"error":{"code":"DeploymentFailed","message":"nested failure"}},` +
				`"targetResource":{"id":"/subscriptions/foundry-sub/resourceGroups/foundry-rg/` +
				`providers/Microsoft.Resources/deployments/nested","resourceType":"Microsoft.Resources/deployments"}}}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := byoContractProvider(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reads := 0
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: byoHTTPContract(func(request *http.Request) (*http.Response, error) {
					reads++
					if reads > 1 {
						return nil, context.Canceled
					}
					require.Equal(t, http.MethodGet, request.Method)
					require.Equal(t, "/subscriptions/foundry-sub/providers/Microsoft.Resources/deployments/"+
						p.deploymentName()+"/operations", request.URL.Path)
					cancel()
					return contractLifecycleResponse(request, http.StatusOK, tc.body), nil
				}),
			}}
			root := &azcore.ResponseError{
				StatusCode: http.StatusBadRequest,
				ErrorCode:  "DeploymentFailed",
				RawResponse: contractLifecycleResponse(&http.Request{
					Method: http.MethodPut,
					URL:    &url.URL{Scheme: "https", Host: "management.azure.com", Path: "/deployment"},
					Host:   "management.azure.com",
				}, http.StatusBadRequest, `{"error":{"code":"DeploymentFailed","message":"original ARM failure"}}`),
			}

			err := p.deploymentFailure(ctx, root)

			require.ErrorIs(t, err, root)
			require.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, 1, reads, "cancelled diagnostics must not start another page or nested deployment read")
			service, ok := errors.AsType[*azdext.ServiceError](err)
			require.True(t, ok)
			assert.Equal(t, http.StatusBadRequest, service.StatusCode)
			assert.Equal(t, "management.azure.com", service.ServiceName)
			assert.Contains(t, service.Message, "original ARM failure")
		})
	}
}
