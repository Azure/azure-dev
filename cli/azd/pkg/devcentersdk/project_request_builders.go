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
}

func NewProjectListRequestBuilder(c *devCenterClient) *ProjectListRequestBuilder {
	builder := &ProjectListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c)

	return builder
}

func (c *ProjectListRequestBuilder) Get(ctx context.Context) (*ProjectListResponse, error) {
	req, err := c.createRequest(ctx, http.MethodGet, "projects")
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
}

func NewProjectItemRequestBuilder(c *devCenterClient, projectName string) *ProjectItemRequestBuilder {
	builder := &ProjectItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, projectName)

	return builder
}

func (c *ProjectItemRequestBuilder) Catalogs() *CatalogListRequestBuilder {
	return NewCatalogListRequestBuilder(c.client, c.id)
}

func (c *ProjectItemRequestBuilder) CatalogByName(catalogName string) *CatalogItemRequestBuilder {
	return NewCatalogItemRequestBuilder(c.client, c.id, catalogName)
}

func (c *ProjectItemRequestBuilder) EnvironmentTypes() *EnvironmentTypeListRequestBuilder {
	return NewEnvironmentTypeListRequestBuilder(c.client, c.id)
}

func (c *ProjectItemRequestBuilder) EnvironmentDefinitions() *EnvironmentDefinitionListRequestBuilder {
	return NewEnvironmentDefinitionListRequestBuilder(c.client, c.id, "")
}

func (c *ProjectItemRequestBuilder) Environments() *EnvironmentListRequestBuilder {
	return NewEnvironmentListRequestBuilder(c.client, c.id)
}

func (c *ProjectItemRequestBuilder) EnvironmentsByUser(userId string) *EnvironmentListRequestBuilder {
	builder := NewEnvironmentListRequestBuilder(c.client, c.id)
	builder.userId = userId

	return builder
}

func (c *ProjectItemRequestBuilder) EnvironmentsByMe() *EnvironmentListRequestBuilder {
	builder := NewEnvironmentListRequestBuilder(c.client, c.id)
	builder.userId = "me"

	return builder
}

func (c *ProjectItemRequestBuilder) EnvironmentByName(environmentName string) *EnvironmentItemRequestBuilder {
	return NewEnvironmentItemRequestBuilder(c.client, c.id, environmentName)
}

func (c *ProjectItemRequestBuilder) Get(ctx context.Context) (*Project, error) {
	req, err := c.client.createRequest(ctx, http.MethodGet, fmt.Sprintf("projects/%s", c.id))
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
