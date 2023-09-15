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
	projectName string
}

func NewCatalogListRequestBuilder(c *devCenterClient, projectName string) *CatalogListRequestBuilder {
	builder := &CatalogListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c)
	builder.projectName = projectName

	return builder
}

func (c *CatalogListRequestBuilder) Get(ctx context.Context) (*CatalogListResponse, error) {
	req, err := c.createRequest(ctx, http.MethodGet, fmt.Sprintf("projects/%s/catalogs", c.projectName))
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
	projectName string
}

func NewCatalogItemRequestBuilder(
	c *devCenterClient,
	projectName string,
	catalogName string,
) *CatalogItemRequestBuilder {
	builder := &CatalogItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, catalogName)
	builder.projectName = projectName

	return builder
}

func (c *CatalogItemRequestBuilder) EnvironmentDefinitions() *EnvironmentDefinitionListRequestBuilder {
	return NewEnvironmentDefinitionListRequestBuilder(c.client, c.projectName, c.id)
}

func (c *CatalogItemRequestBuilder) Get(ctx context.Context) (*Catalog, error) {
	req, err := c.client.createRequest(ctx, http.MethodGet, fmt.Sprintf("projects/%s/catalogs/%s", c.projectName, c.id))
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
