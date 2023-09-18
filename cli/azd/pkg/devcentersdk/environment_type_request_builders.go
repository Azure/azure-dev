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
	projectName string
}

func NewEnvironmentTypeListRequestBuilder(
	c *devCenterClient,
	devCenter *DevCenter,
	projectName string,
) *EnvironmentTypeListRequestBuilder {
	builder := &EnvironmentTypeListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c, devCenter)
	builder.projectName = projectName

	return builder
}

func (c *EnvironmentTypeListRequestBuilder) Get(ctx context.Context) (*EnvironmentTypeListResponse, error) {
	req, err := c.createRequest(
		ctx,
		http.MethodGet,
		fmt.Sprintf("projects/%s/environmentTypes", c.projectName),
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
	projectName string
}

func NewEnvironmentTypeItemRequestBuilder(
	c *devCenterClient,
	devCenter *DevCenter,
	projectName string,
	environmentTypeName string,
) *EnvironmentTypeItemRequestBuilder {
	builder := &EnvironmentTypeItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, devCenter, environmentTypeName)

	return builder
}

func (c *EnvironmentTypeItemRequestBuilder) Get(ctx context.Context) (*EnvironmentType, error) {
	req, err := c.createRequest(
		ctx,
		http.MethodGet,
		fmt.Sprintf("projects/%s/environmentTypes/%s", c.projectName, c.id),
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

	return httputil.ReadRawResponse[EnvironmentType](res)
}
