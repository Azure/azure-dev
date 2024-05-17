package azsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	armruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

const (
	deployStatusInterval = 10 * time.Second
)

// ZipDeployClient wraps usage of app service zip deploy used for application deployments
// More info can be found at the following:
// https://github.com/MicrosoftDocs/azure-docs/blob/main/includes/app-service-deploy-zip-push-rest.md
// https://github.com/projectkudu/kudu/wiki/REST-API
type ZipDeployClient struct {
	hostName string
	pipeline runtime.Pipeline
}

type DeployResponse struct {
	DeployStatus
}

type DeployStatusResponse struct {
	DeployStatus
}

type DeployStatus struct {
	Id           string     `json:"id"`
	Status       int        `json:"status"`
	StatusText   string     `json:"status_text"`
	Message      string     `json:"message"`
	Progress     *string    `json:"progress"`
	ReceivedTime *time.Time `json:"received_time"`
	StartTime    *time.Time `json:"start_time"`
	EndTime      *time.Time `json:"end_time"`
	Complete     bool       `json:"complete"`
	Active       bool       `json:"active"`
	LogUrl       string     `json:"log_url"`
	SiteName     string     `json:"site_name"`
}

const (
	PublishStatusCancelled      int = -1
	PublishStatusPending        int = 0
	PublishStatusBuilding       int = 1
	PublishStatusDeploying      int = 2
	PublishStatusFailed         int = 3
	PublishStatusSuccess        int = 4
	PublishStatusConflict       int = 5
	PublishStatusPartialSuccess int = 6
)

// Creates a new ZipDeployClient instance
func NewZipDeployClient(
	hostName string,
	credential azcore.TokenCredential,
	options *arm.ClientOptions,
) (*ZipDeployClient, error) {
	if options == nil {
		options = &arm.ClientOptions{}
	}

	// We do not have a Resource provider to register
	options.DisableRPRegistration = true

	// Increase default retry attempts from 3 to 4 as zipdeploy often fails with 3 retries.
	// With the default azcore.policy options of 800ms RetryDelay, this introduces up to 20 seconds of exponential back-off.
	options.Retry = policy.RetryOptions{
		MaxRetries: 4,
	}

	pipeline, err := armruntime.NewPipeline("zip-deploy", "1.0.0", credential, runtime.PipelineOptions{}, options)
	if err != nil {
		return nil, fmt.Errorf("failed creating HTTP pipeline: %w", err)
	}

	return &ZipDeployClient{
		hostName: hostName,
		pipeline: pipeline,
	}, nil
}

// Begins a zip deployment and returns a poller to check for status
func (c *ZipDeployClient) BeginDeploy(
	ctx context.Context,
	zipFile io.Reader,
) (*runtime.Poller[*DeployResponse], error) {
	request, err := c.createDeployRequest(ctx, zipFile)
	if err != nil {
		return nil, err
	}

	response, err := c.pipeline.Do(request)
	if err != nil {
		return nil, err
	}

	defer response.Body.Close()

	if !runtime.HasStatusCode(response, http.StatusAccepted) {
		return nil, runtime.NewResponseError(response)
	}

	var finalResponse *DeployResponse

	pollerOptions := &runtime.NewPollerOptions[*DeployResponse]{
		Response: &finalResponse,
		Handler:  newDeployPollingHandler(c.pipeline, response),
	}

	return runtime.NewPoller(response, c.pipeline, pollerOptions)
}

// Deploys the specified application zip to the azure app service and waits for completion
func (c *ZipDeployClient) Deploy(ctx context.Context, zipFile io.Reader) (*DeployResponse, error) {
	poller, err := c.BeginDeploy(ctx, zipFile)
	if err != nil {
		return nil, err
	}

	response, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: deployStatusInterval,
	})
	if err != nil {
		return nil, err
	}

	return response, nil
}

// Creates the HTTP request for the zip deployment operation
func (c *ZipDeployClient) createDeployRequest(
	ctx context.Context,
	zipFile io.Reader,
) (*policy.Request, error) {
	endpoint := fmt.Sprintf("https://%s/api/zipdeploy", c.hostName)
	req, err := runtime.NewRequest(ctx, http.MethodPost, endpoint)
	if err != nil {
		return nil, fmt.Errorf("creating deploy request: %w", err)
	}

	rawRequest := req.Raw()
	rawRequest.Body = io.NopCloser(zipFile)
	query := rawRequest.URL.Query()
	query.Set("isAsync", "true")
	rawRequest.Header.Set("Content-Type", "application/octet-stream")
	rawRequest.Header.Set("Accept", "application/json")
	rawRequest.URL.RawQuery = query.Encode()

	return req, nil
}

// Implementation of a Go SDK polling handler for async zip deploy operations
type deployPollingHandler struct {
	pipeline runtime.Pipeline
	response *http.Response
	result   *DeployStatusResponse
}

func newDeployPollingHandler(pipeline runtime.Pipeline, response *http.Response) *deployPollingHandler {
	return &deployPollingHandler{
		pipeline: pipeline,
		response: response,
	}
}

// Checks whether the long running deploy operation is complete
func (h *deployPollingHandler) Done() bool {
	return h.result != nil && h.result.Complete
}

// Executing the polling logic to check the status of the deploy operation
func (h *deployPollingHandler) Poll(ctx context.Context) (*http.Response, error) {
	location := h.response.Header.Get("Location")
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("missing polling location header")
	}

	req, err := runtime.NewRequest(ctx, http.MethodGet, location)
	if err != nil {
		return nil, err
	}

	response, err := h.pipeline.Do(req)
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
	deploymentStatus, err := httputil.ReadRawResponse[DeployStatusResponse](response)
	if err != nil {
		return nil, err
	}

	h.result = deploymentStatus

	return response, nil
}

// Gets the result of the deploy operation
func (h *deployPollingHandler) Result(ctx context.Context, out **DeployResponse) error {
	*out = &DeployResponse{
		DeployStatus: h.result.DeployStatus,
	}

	return nil
}

type PublishOptions struct {
	RemoteBuild bool
}

func (c *ZipDeployClient) Publish(
	ctx context.Context,
	zipFile io.ReadSeekCloser,
	options *PublishOptions) (*DeployResponse, error) {
	if options == nil {
		options = &PublishOptions{}
	}

	endpoint := fmt.Sprintf("https://%s/api/publish", c.hostName)
	request, err := runtime.NewRequest(ctx, http.MethodPost, endpoint)
	if err != nil {
		return nil, fmt.Errorf("creating deploy request: %w", err)
	}

	rawRequest := request.Raw()
	query := rawRequest.URL.Query()
	if options.RemoteBuild {
		query.Set("RemoteBuild", "true")
	}
	rawRequest.URL.RawQuery = query.Encode()

	err = request.SetBody(zipFile, "application/zip")
	if err != nil {
		return nil, fmt.Errorf("setting request body: %w", err)
	}
	// server currently does not honor Accept header
	//rawRequest.Header.Set("Accept", "application/json")

	response, err := c.pipeline.Do(request)
	if err != nil {
		return nil, err
	}

	defer response.Body.Close()

	if !runtime.HasStatusCode(response, http.StatusAccepted) {
		return nil, runtime.NewResponseError(response)
	}

	body, err := runtime.Payload(response)
	if err != nil {
		return nil, err
	}
	// the body is the deployment id
	var deploymentId string
	if err := json.Unmarshal(body, &deploymentId); err != nil {
		return nil, err
	}

	if deploymentId == "" {
		return nil, fmt.Errorf("missing deployment id")
	}

	logResponse(response)

	var finalResponse *DeployResponse

	location := fmt.Sprintf("https://%s/api/deployments/%s", c.hostName, deploymentId)
	handler := newPublishPollingHandler(c.pipeline, location)
	pollerOptions := &runtime.NewPollerOptions[*DeployResponse]{
		Response: &finalResponse,
		Handler:  handler,
	}
	poller, err := runtime.NewPoller(response, c.pipeline, pollerOptions)
	if err != nil {
		return nil, err
	}

	rsp, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: deployStatusInterval,
	})
	if err != nil {
		return nil, err
	}

	return rsp, nil
}

// Implementation of a Go SDK polling handler for async api/publish operations
type publishPollingHandler struct {
	pipeline runtime.Pipeline
	location string

	// final result
	result *DeployStatusResponse
}

func newPublishPollingHandler(
	pipeline runtime.Pipeline,
	location string) *publishPollingHandler {
	return &publishPollingHandler{
		pipeline: pipeline,
		location: location,
	}
}

// Checks whether the long running deploy operation is complete
func (h *publishPollingHandler) Done() bool {
	return h.result != nil && h.result.Complete
}

// Executing the polling logic to check the status of the deploy operation
func (h *publishPollingHandler) Poll(ctx context.Context) (*http.Response, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, h.location)
	if err != nil {
		return nil, err
	}

	response, err := h.pipeline.Do(req)
	if err != nil {
		return nil, err
	}

	logResponse(response)

	// todo: according to the service team, it's possible to observe a 404
	// after the deployment is in-progress
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		return nil, runtime.NewResponseError(response)
	}

	deploymentStatus, err := httputil.ReadRawResponse[DeployStatusResponse](response)
	if err != nil {
		return nil, err
	}

	// server returns response with header 200 even if the deployment is still in progress.
	// as a result, we need to read the body in full to determine the actual status.
	switch deploymentStatus.Status {
	case PublishStatusCancelled:
		return nil, fmt.Errorf("deployment was cancelled")
	case PublishStatusFailed:
		return nil, fmt.Errorf("zip deployment failed: %s", deploymentStatus.StatusText)
	case PublishStatusSuccess:
		h.result = deploymentStatus
	case PublishStatusConflict:
		return nil, fmt.Errorf("deployment was cancelled due to another deployment being in progress")
	case PublishStatusPartialSuccess:
		return nil, fmt.Errorf("deployment was partially successful")
	}

	return response, nil
}

// Gets the result of the deploy operation
func (h *publishPollingHandler) Result(ctx context.Context, out **DeployResponse) error {
	*out = &DeployResponse{
		DeployStatus: h.result.DeployStatus,
	}

	return nil
}

func logResponse(response *http.Response) {
	log.Println("==== RESPONSE HEADERS ===== ")
	for k, v := range response.Header {
		for _, vv := range v {
			log.Printf("%s: %s\n", k, vv)
		}
	}
	log.Println("==== END RESPONSE HEADERS ===== ")

	log.Println("==== RESPONSE BODY ===== ")
	body, err := runtime.Payload(response)
	if err != nil {
		log.Println("failed to read response body")
	} else {
		log.Println(string(body))
	}
	log.Println("==== END RESPONSE BODY ===== ")
}
