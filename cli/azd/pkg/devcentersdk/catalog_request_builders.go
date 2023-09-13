package devcentersdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// Catalogs
type CatalogListRequestBuilder struct {
	*EntityListRequestBuilder[CatalogListRequestBuilder]
	endpoint    string
	projectName string
}

func NewCatalogListRequestBuilder(c *devCenterClient, endpoint string, projectName string) *CatalogListRequestBuilder {
	builder := &CatalogListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c)
	builder.endpoint = endpoint
	builder.projectName = projectName

	return builder
}

func (c *CatalogListRequestBuilder) Get(ctx context.Context) (*CatalogListResponse, error) {
	req, err := c.createRequest(ctx, http.MethodGet, fmt.Sprintf("%s/projects/%s/catalogs", c.endpoint, c.projectName))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, err
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[CatalogListResponse](res)
}

type CatalogItemRequestBuilder struct {
	*EntityItemRequestBuilder[CatalogItemRequestBuilder]
	endpoint    string
	projectName string
}

func NewCatalogItemRequestBuilder(
	c *devCenterClient,
	endpoint string,
	projectName string,
	catalogName string,
) *CatalogItemRequestBuilder {
	builder := &CatalogItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, catalogName)
	builder.endpoint = endpoint

	return builder
}

func (c *CatalogItemRequestBuilder) Get(ctx context.Context) (*Catalog, error) {
	req, err := runtime.NewRequest(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/projects/%s/catalogs/%s", c.endpoint, c.projectName, c.id),
	)
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, err
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[Catalog](res)
}
