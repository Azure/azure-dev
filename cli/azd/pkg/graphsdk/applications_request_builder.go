package graphsdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// A Microsoft Graph Application entity.
type Application struct {
	Id          string `json:"id"`
	AppId       string `json:"appId"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

// A list of applications returned from the Microsoft Graph.
type ApplicationListResponse struct {
	NextLink *string       `json:"@odata.nextLink`
	Value    []Application `json:"value"`
}

type ApplicationsRequestBuilder struct {
	*EntityListRequestBuilder[ApplicationsRequestBuilder]
}

func newApplicationsRequestBuilder(client *GraphClient) *ApplicationsRequestBuilder {
	builder := &ApplicationsRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, client)

	return builder
}

// Gets a list of applications that the current logged in user has access to.
func (c *ApplicationsRequestBuilder) Get(ctx context.Context) (*ApplicationListResponse, error) {
	req, err := c.createRequest(ctx, http.MethodGet, fmt.Sprintf("%s/applications", c.client.host))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, runtime.NewResponseError(res)
	}

	return azsdk.ReadRawResponse[ApplicationListResponse](res)
}

type ApplicationItemRequestBuilder struct {
	*EntityItemRequestBuilder[ApplicationItemRequestBuilder]
}

func newApplicationItemRequestBuilder(client *GraphClient, id string) *ApplicationItemRequestBuilder {
	builder := &ApplicationItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, client, id)

	return builder
}

// Gets a Microsoft Graph Application for the specified application identifier
func (c *ApplicationItemRequestBuilder) Get(ctx context.Context) (*Application, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, fmt.Sprintf("%s/applications/%s", c.client.host, c.id))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, runtime.NewResponseError(res)
	}

	result, err := azsdk.ReadRawResponse[Application](res)
	if err != nil {
		return nil, err
	}

	return result, err
}
