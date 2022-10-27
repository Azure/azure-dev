package graphsdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

func NewServicePrincipalListRequestBuilder(client *GraphClient) *ServicePrincipalListRequestBuilder {
	builder := &ServicePrincipalListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, client)

	return builder
}

// Gets a list of Microsoft Graph Service Principals that the current logged in user has access to.
func (c *ServicePrincipalListRequestBuilder) Get(ctx context.Context) (*ServicePrincipalListResponse, error) {
	req, err := c.createRequest(ctx, http.MethodGet, fmt.Sprintf("%s/servicePrincipals", c.client.host))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, httputil.HandleRequestError(res, err)
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[ServicePrincipalListResponse](res)
}

func (c *ServicePrincipalListRequestBuilder) Post(
	ctx context.Context,
	servicePrincipal *ServicePrincipal,
) (*ServicePrincipal, error) {
	req, err := c.createRequest(ctx, http.MethodPost, fmt.Sprintf("%s/servicePrincipals", c.client.host))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	createRequest := ServicePrincipalCreateRequest{
		AppId: servicePrincipal.AppId,
	}

	err = SetHttpRequestBody(req, createRequest)
	if err != nil {
		return nil, err
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, httputil.HandleRequestError(res, err)
	}

	if !runtime.HasStatusCode(res, http.StatusCreated) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[ServicePrincipal](res)
}

type ServicePrincipalItemRequestBuilder struct {
	*EntityItemRequestBuilder[ServicePrincipalItemRequestBuilder]
}

func NewServicePrincipalItemRequestBuilder(client *GraphClient, id string) *ServicePrincipalItemRequestBuilder {
	builder := &ServicePrincipalItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, client, id)

	return builder
}

// Gets a Microsoft Graph Service Principal for the specified service principal identifier
func (b *ServicePrincipalItemRequestBuilder) Get(ctx context.Context) (*ServicePrincipal, error) {
	req, err := b.createRequest(ctx, http.MethodGet, fmt.Sprintf("%s/servicePrincipals/%s", b.client.host, b.id))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := b.client.pipeline.Do(req)
	if err != nil {
		return nil, httputil.HandleRequestError(res, err)
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[ServicePrincipal](res)
}
