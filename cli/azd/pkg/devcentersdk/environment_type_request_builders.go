package devcentersdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// EnvironmentTypes
type EnvironmentTypeListRequestBuilder struct {
	*EntityListRequestBuilder[EnvironmentTypeListRequestBuilder]
	endpoint    string
	projectName string
}

func NewEnvironmentTypeListRequestBuilder(
	c *devCenterClient,
	endpoint string,
	projectName string,
) *EnvironmentTypeListRequestBuilder {
	builder := &EnvironmentTypeListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c)
	builder.endpoint = endpoint
	builder.projectName = projectName

	return builder
}

func (c *EnvironmentTypeListRequestBuilder) Get(ctx context.Context) (*EnvironmentTypeListResponse, error) {
	req, err := c.createRequest(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/projects/%s/environmentTypes", c.endpoint, c.projectName),
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

	return httputil.ReadRawResponse[EnvironmentTypeListResponse](res)
}

type EnvironmentTypeItemRequestBuilder struct {
	*EntityItemRequestBuilder[EnvironmentTypeItemRequestBuilder]
	endpoint    string
	projectName string
}

func NewEnvironmentTypeItemRequestBuilder(
	c *devCenterClient,
	endpoint string,
	projectName string,
	environmentTypeName string,
) *EnvironmentTypeItemRequestBuilder {
	builder := &EnvironmentTypeItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, environmentTypeName)
	builder.endpoint = endpoint

	return builder
}

func (c *EnvironmentTypeItemRequestBuilder) Get(ctx context.Context) (*EnvironmentType, error) {
	req, err := runtime.NewRequest(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/projects/%s/environmentTypes/%s",
			c.endpoint,
			c.projectName,
			c.id,
		))
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

	return httputil.ReadRawResponse[EnvironmentType](res)
}
