package devcentersdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

type OutputsRequestBuilder struct {
	*EntityItemRequestBuilder[OutputsRequestBuilder]
	projectName string
	userId      string
}

func NewOutputsRequestBuilder(
	c *devCenterClient,
	devCenter *DevCenter,
	projectName string,
	userId string,
	environmentName string,
) *OutputsRequestBuilder {
	builder := &OutputsRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, devCenter, environmentName)
	builder.projectName = projectName
	builder.userId = userId

	return builder
}

func (c *OutputsRequestBuilder) Get(ctx context.Context) (*OutputListResponse, error) {
	requestUrl := fmt.Sprintf("projects/%s/users/%s/environments/%s/outputs", c.projectName, c.userId, c.id)
	req, err := c.createRequest(ctx, http.MethodGet, requestUrl)
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

	return httputil.ReadRawResponse[OutputListResponse](res)
}
