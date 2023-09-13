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
	endpoint    string
	projectName string
}

func NewEnvironmentDefinitionListRequestBuilder(
	c *devCenterClient,
	endpoint string,
	projectName string,
) *EnvironmentDefinitionListRequestBuilder {
	builder := &EnvironmentDefinitionListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c)
	builder.endpoint = endpoint
	builder.projectName = projectName

	return builder
}

func (c *EnvironmentDefinitionListRequestBuilder) Get(ctx context.Context) (*EnvironmentDefinitionListResponse, error) {
	req, err := c.createRequest(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/projects/%s/environmentDefinitions", c.endpoint, c.projectName),
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

	return httputil.ReadRawResponse[EnvironmentDefinitionListResponse](res)
}

type EnvironmentDefinitionItemRequestBuilder struct {
	*EntityItemRequestBuilder[EnvironmentDefinitionItemRequestBuilder]
	endpoint    string
	projectName string
}

func NewEnvironmentDefinitionItemRequestBuilder(
	c *devCenterClient,
	endpoint string,
	projectName string,
	environmentDefinitionName string,
) *EnvironmentDefinitionItemRequestBuilder {
	builder := &EnvironmentDefinitionItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, environmentDefinitionName)
	builder.endpoint = endpoint

	return builder
}

func (c *EnvironmentDefinitionItemRequestBuilder) Get(ctx context.Context) (*EnvironmentDefinition, error) {
	req, err := runtime.NewRequest(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/projects/%s/environmentDefinitions/%s", c.endpoint, c.projectName, c.id),
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
