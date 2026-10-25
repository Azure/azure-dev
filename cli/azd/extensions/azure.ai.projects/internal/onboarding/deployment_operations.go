// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// NewDeploymentOperationsPager reads operations at the exact subscription or resource-group deployment ID.
// The pinned SDK's resource-group pager omits providers/Microsoft.Resources from its request path.
func NewDeploymentOperationsPager(
	deploymentID string, credential azcore.TokenCredential, options *arm.ClientOptions,
) (*runtime.Pager[armresources.DeploymentOperationsListResult], error) {
	id, err := arm.ParseResourceID(deploymentID)
	if err != nil || !validResourceIDSyntax(deploymentID) || id.SubscriptionID == "" ||
		!strings.EqualFold(id.ResourceType.String(), "Microsoft.Resources/deployments") {
		return nil, fmt.Errorf("deployment operations require an exact ARM deployment ID")
	}
	client, err := arm.NewClient("azure.ai.projects.onboarding", "v1.0.0", credential, onboardingARMOptions(options))
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(client.Endpoint())
	if err != nil {
		return nil, err
	}
	first := strings.TrimRight(client.Endpoint(), "/") + deploymentID + "/operations?api-version=2021-04-01"
	return runtime.NewPager(runtime.PagingHandler[armresources.DeploymentOperationsListResult]{
		More: func(page armresources.DeploymentOperationsListResult) bool {
			return authString(page.NextLink) != ""
		},
		Fetcher: func(ctx context.Context, previous *armresources.DeploymentOperationsListResult) (
			armresources.DeploymentOperationsListResult, error,
		) {
			var page armresources.DeploymentOperationsListResult
			next := first
			if previous != nil {
				next = authString(previous.NextLink)
			}
			target, err := url.Parse(next)
			if err != nil || !strings.EqualFold(target.Scheme, endpoint.Scheme) ||
				!strings.EqualFold(target.Host, endpoint.Host) || target.User != nil || target.Fragment != "" {
				return page, fmt.Errorf("deployment operations continuation must use the configured ARM endpoint")
			}
			request, err := runtime.NewRequest(ctx, http.MethodGet, next)
			if err != nil {
				return page, err
			}
			response, err := client.Pipeline().Do(request)
			if err != nil {
				return page, err
			}
			if !runtime.HasStatusCode(response, http.StatusOK) {
				return page, runtime.NewResponseError(response)
			}
			err = runtime.UnmarshalAsJSON(response, &page)
			return page, err
		},
	}), nil
}
