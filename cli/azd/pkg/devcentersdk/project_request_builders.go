package devcentersdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// Projects
type ProjectListRequestBuilder struct {
	*EntityListRequestBuilder[ProjectListRequestBuilder]
	endpoint string
}

func NewProjectListRequestBuilder(c *devCenterClient, endpoint string) *ProjectListRequestBuilder {
	builder := &ProjectListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c)
	builder.endpoint = endpoint

	return builder
}

func (c *ProjectListRequestBuilder) Get(ctx context.Context) (*ProjectListResponse, error) {
	req, err := c.createRequest(ctx, http.MethodGet, fmt.Sprintf("%s/projects", c.endpoint))
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

	return httputil.ReadRawResponse[ProjectListResponse](res)
}

type ProjectItemRequestBuilder struct {
	*EntityItemRequestBuilder[ProjectItemRequestBuilder]
	endpoint string
}

func NewProjectItemRequestBuilder(c *devCenterClient, endpoint string, projectName string) *ProjectItemRequestBuilder {
	builder := &ProjectItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, projectName)
	builder.endpoint = endpoint

	return builder
}

func (c *ProjectItemRequestBuilder) Catalogs() *CatalogListRequestBuilder {
	return NewCatalogListRequestBuilder(c.client, c.endpoint, c.id)
}

func (c *ProjectItemRequestBuilder) CatalogByName(name string) *CatalogItemRequestBuilder {
	return NewCatalogItemRequestBuilder(c.client, c.endpoint, c.id, name)
}

func (c *ProjectItemRequestBuilder) EnvironmentTypes() *EnvironmentTypeListRequestBuilder {
	return NewEnvironmentTypeListRequestBuilder(c.client, c.endpoint, c.id)
}

func (c *ProjectItemRequestBuilder) EnvironmentDefinitions() *EnvironmentDefinitionListRequestBuilder {
	return NewEnvironmentDefinitionListRequestBuilder(c.client, c.endpoint, c.id)
}

func (c *ProjectItemRequestBuilder) Get(ctx context.Context) (*Project, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, fmt.Sprintf("%s/projects/%s", c.endpoint, c.id))
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

	return httputil.ReadRawResponse[Project](res)
}
