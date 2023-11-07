package devcentersdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// EnvironmentDefinitions
type EnvironmentDefinitionListRequestBuilder struct {
	*EntityListRequestBuilder[EnvironmentDefinitionListRequestBuilder]
	projectName string
	catalogName string
}

func NewEnvironmentDefinitionListRequestBuilder(
	c *devCenterClient,
	devCenter *DevCenter,
	projectName string,
	catalogName string,
) *EnvironmentDefinitionListRequestBuilder {
	builder := &EnvironmentDefinitionListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c, devCenter)
	builder.projectName = projectName
	builder.catalogName = catalogName

	return builder
}

func (c *EnvironmentDefinitionListRequestBuilder) Get(ctx context.Context) (*EnvironmentDefinitionListResponse, error) {
	var requestPath string
	if c.catalogName != "" {
		requestPath = fmt.Sprintf("projects/%s/catalogs/%s/environmentDefinitions", c.projectName, c.catalogName)
	} else {
		requestPath = fmt.Sprintf("projects/%s/environmentDefinitions", c.projectName)
	}

	req, err := c.createRequest(ctx, http.MethodGet, requestPath)
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

	return httputil.ReadRawResponse[EnvironmentDefinitionListResponse](res)
}

type EnvironmentDefinitionItemRequestBuilder struct {
	*EntityItemRequestBuilder[EnvironmentDefinitionItemRequestBuilder]
	projectName string
	catalogName string
}

func NewEnvironmentDefinitionItemRequestBuilder(
	c *devCenterClient,
	devCenter *DevCenter,
	projectName string,
	catalogName string,
	environmentDefinitionName string,
) *EnvironmentDefinitionItemRequestBuilder {
	builder := &EnvironmentDefinitionItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, devCenter, environmentDefinitionName)
	builder.projectName = projectName
	builder.catalogName = catalogName

	return builder
}

func (c *EnvironmentDefinitionItemRequestBuilder) Get(ctx context.Context) (*EnvironmentDefinition, error) {
	req, err := c.createRequest(
		ctx,
		http.MethodGet,
		fmt.Sprintf("projects/%s/catalogs/%s/environmentDefinitions/%s", c.projectName, c.catalogName, c.id),
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

	return httputil.ReadRawResponse[EnvironmentDefinition](res)
}
