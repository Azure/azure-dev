package graphsdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

// A Microsoft Graph Service Principal entity.
type ServicePrincipal struct {
	Id          string `json:"id"`
	AppId       string `json:"appId"`
	DisplayName string `json:"appDisplayName"`
	Description string `json:"appDescription"`
	Type        string `json:"servicePrincipalType"`
}

// A list of service principals returned from the Microsoft Graph.
type ServicePrincipalListResponse struct {
	NextLink *string            `json:"@odata.nextLink`
	Value    []ServicePrincipal `json:"value"`
}

type ServicePrincipalListRequestBuilder struct {
	*EntityListRequestBuilder[ServicePrincipalListRequestBuilder]
}

func newServicePrincipalListRequestBuilder(client *GraphClient) *ServicePrincipalListRequestBuilder {
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
		return nil, runtime.NewResponseError(res)
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	return azsdk.ReadRawResponse[ServicePrincipalListResponse](res)
}

func (c *ServicePrincipalListRequestBuilder) Post(ctx context.Context, servicePrincipal *ServicePrincipal) (*ServicePrincipal, error) {
	req, err := c.createRequest(ctx, http.MethodPost, fmt.Sprintf("%s/servicePrincipals", c.client.host))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	body, err := convert.ToHttpRequestBody(servicePrincipal)
	if err != nil {
		return nil, err
	}

	req.Raw().Body = body

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, runtime.NewResponseError(res)
	}

	if !runtime.HasStatusCode(res, http.StatusCreated) {
		return nil, runtime.NewResponseError(res)
	}

	return azsdk.ReadRawResponse[ServicePrincipal](res)
}

type ServicePrincipalItemRequestBuilder struct {
	*EntityItemRequestBuilder[ServicePrincipalItemRequestBuilder]
}

func newServicePrincipalItemRequestBuilder(client *GraphClient, id string) *ServicePrincipalItemRequestBuilder {
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
		return nil, runtime.NewResponseError(res)
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	return azsdk.ReadRawResponse[ServicePrincipal](res)
}
