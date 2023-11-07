package devcentersdk

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

const (
	deployStatusInterval = 10 * time.Second
)

// Environments
type EnvironmentListRequestBuilder struct {
	*EntityListRequestBuilder[EnvironmentListRequestBuilder]
	projectName string
	userId      string
}

func NewEnvironmentListRequestBuilder(
	c *devCenterClient,
	devCenter *DevCenter,
	projectName string,
) *EnvironmentListRequestBuilder {
	builder := &EnvironmentListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, c, devCenter)
	builder.projectName = projectName

	return builder
}

func (c *EnvironmentListRequestBuilder) EnvironmentByName(name string) *EnvironmentItemRequestBuilder {
	return NewEnvironmentItemRequestBuilder(c.client, c.devCenter, c.projectName, c.userId, name)
}

func (c *EnvironmentListRequestBuilder) Get(ctx context.Context) (*EnvironmentListResponse, error) {
	var requestUrl string

	if c.userId != "" {
		requestUrl = fmt.Sprintf("projects/%s/users/%s/environments", c.projectName, c.userId)
	} else {
		requestUrl = fmt.Sprintf("projects/%s/environments", c.projectName)
	}

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

	return httputil.ReadRawResponse[EnvironmentListResponse](res)
}

type EnvironmentItemRequestBuilder struct {
	*EntityItemRequestBuilder[EnvironmentItemRequestBuilder]
	projectName string
	userId      string
}

func NewEnvironmentItemRequestBuilder(
	c *devCenterClient,
	devCenter *DevCenter,
	projectName string,
	userId string,
	environmentName string,
) *EnvironmentItemRequestBuilder {
	builder := &EnvironmentItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, c, devCenter, environmentName)
	builder.projectName = projectName
	builder.userId = userId

	return builder
}

func (c *EnvironmentItemRequestBuilder) Get(ctx context.Context) (*Environment, error) {
	requestUrl := fmt.Sprintf("projects/%s/users/%s/environments/%s", c.projectName, c.userId, c.id)
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

	return httputil.ReadRawResponse[Environment](res)
}

func (c *EnvironmentItemRequestBuilder) BeginPut(
	ctx context.Context,
	spec EnvironmentSpec,
) (*runtime.Poller[*OperationStatus], error) {
	requestUrl := fmt.Sprintf("projects/%s/users/me/environments/%s", c.projectName, c.id)
	req, err := c.createRequest(ctx, http.MethodPut, requestUrl)
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	err = SetHttpRequestBody(req, spec)
	if err != nil {
		return nil, err
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, err
	}

	defer res.Body.Close()

	if !runtime.HasStatusCode(res, http.StatusCreated) {
		return nil, runtime.NewResponseError(res)
	}

	var finalResponse *OperationStatus

	pollerOptions := &runtime.NewPollerOptions[*OperationStatus]{
		Response: &finalResponse,
		Handler:  NewEnvironmentPollingHandler(c.client.pipeline, res),
	}

	return runtime.NewPoller(res, c.client.pipeline, pollerOptions)
}

func (c *EnvironmentItemRequestBuilder) Put(
	ctx context.Context,
	spec EnvironmentSpec,
) error {
	poller, err := c.BeginPut(ctx, spec)
	if err != nil {
		return err
	}

	_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: deployStatusInterval,
	})
	if err != nil {
		return err
	}

	return nil
}

func (c *EnvironmentItemRequestBuilder) BeginDelete(
	ctx context.Context,
) (*runtime.Poller[*OperationStatus], error) {
	req, err := c.createRequest(
		ctx,
		http.MethodDelete,
		fmt.Sprintf("projects/%s/users/me/environments/%s", c.projectName, c.id),
	)
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, err
	}

	defer res.Body.Close()

	if !runtime.HasStatusCode(res, http.StatusAccepted) {
		return nil, runtime.NewResponseError(res)
	}

	var finalResponse *OperationStatus

	pollerOptions := &runtime.NewPollerOptions[*OperationStatus]{
		Response: &finalResponse,
		Handler:  NewEnvironmentPollingHandler(c.client.pipeline, res),
	}

	return runtime.NewPoller(res, c.client.pipeline, pollerOptions)
}

func (c *EnvironmentItemRequestBuilder) Delete(ctx context.Context) error {
	poller, err := c.BeginDelete(ctx)
	if err != nil {
		return err
	}

	_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: deployStatusInterval,
	})
	if err != nil {
		return err
	}

	return nil
}

type EnvironmentPollingHandler struct {
	pipeline runtime.Pipeline
	response *http.Response
	result   *OperationStatus
}

func NewEnvironmentPollingHandler(pipeline runtime.Pipeline, res *http.Response) *EnvironmentPollingHandler {
	return &EnvironmentPollingHandler{
		pipeline: pipeline,
		response: res,
	}
}

func (p *EnvironmentPollingHandler) Poll(ctx context.Context) (*http.Response, error) {
	location := p.response.Header.Get("Location")
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("missing polling location header")
	}

	req, err := runtime.NewRequest(ctx, http.MethodGet, location)
	if err != nil {
		return nil, err
	}

	response, err := p.pipeline.Do(req)
	if err != nil {
		return nil, err
	}

	if !runtime.HasStatusCode(response, http.StatusAccepted) && !runtime.HasStatusCode(response, http.StatusOK) {
		return nil, runtime.NewResponseError(response)
	}

	// If response is 202 - we're still waiting
	if runtime.HasStatusCode(response, http.StatusAccepted) {
		return response, nil
	}

	// Status code is 200 if we get to this point - transform the response
	deploymentStatus, err := httputil.ReadRawResponse[OperationStatus](response)
	if err != nil {
		return nil, err
	}

	p.result = deploymentStatus

	return response, nil
}

func (p *EnvironmentPollingHandler) Done() bool {
	return p.result != nil && ProvisioningState(p.result.Status) == ProvisioningStateSucceeded
}

// Gets the result of the deploy operation
func (h *EnvironmentPollingHandler) Result(ctx context.Context, out **OperationStatus) error {
	*out = h.result
	return nil
}
