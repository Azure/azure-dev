package azsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	armruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// FuncAppHostClient contains methods for interacting with the Azure Functions application host, usually located
// at *.scm.azurewebsites.net
type FuncAppHostClient struct {
	hostName string
	options  *arm.ClientOptions
	pipeline runtime.Pipeline
}

func NewFuncAppHostClient(
	hostName string,
	credential azcore.TokenCredential,
	options *arm.ClientOptions,
) (*FuncAppHostClient, error) {
	if options == nil {
		options = &arm.ClientOptions{}
	}

	options.DisableRPRegistration = true

	pipeline, err := armruntime.NewPipeline("funcapp-deploy", "1.0.0", credential, runtime.PipelineOptions{}, options)
	if err != nil {
		return nil, fmt.Errorf("failed creating HTTP pipeline: %w", err)
	}

	return &FuncAppHostClient{
		hostName: hostName,
		pipeline: pipeline,
	}, nil
}

type PublishOptions struct {
	// If true, the remote host will run Oryx remote build steps after publishing.
	// This would run steps like `npm i` or `npm run build` for Node apps,
	// or activating the virtual environment for Python apps.
	RemoteBuild bool
}

// Publish deploys an application zip file to the function app host.
// This is currently only supported for Flex-consumption plans.
func (c *FuncAppHostClient) Publish(
	ctx context.Context,
	zipFile io.ReadSeeker,
	options *PublishOptions) (*PublishResponse, error) {
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

	err = request.SetBody(streaming.NopCloser(zipFile), "application/zip")
	if err != nil {
		return nil, fmt.Errorf("setting request body: %w", err)
	}
	rawRequest.Header.Set("Accept", "application/json")

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

	// the response body is the deployment id and nothing else.
	var deploymentId string
	if err := json.Unmarshal(body, &deploymentId); err != nil {
		return nil, err
	}

	if deploymentId == "" {
		return nil, fmt.Errorf("missing deployment id")
	}

	logResponse(response)

	var finalResponse *PublishResponse

	location := fmt.Sprintf("https://%s/api/deployments/%s", c.hostName, deploymentId)
	handler := newPublishPollingHandler(c.pipeline, location)
	pollerOptions := &runtime.NewPollerOptions[*PublishResponse]{
		Response: &finalResponse,
		Handler:  handler,
	}
	poller, err := runtime.NewPoller(response, c.pipeline, pollerOptions)
	if err != nil {
		return nil, err
	}

	rsp, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		// This frequency is recommended by the service team.
		Frequency: 1 * time.Second,
	})
	if err != nil {
		return nil, err
	}

	return rsp, nil
}

// The response for a deployment located at api/deployments/{id} that represents the deployment initiated by api/publish.
type PublishResponse struct {
	Id                 string              `json:"id"`
	Status             int                 `json:"status"`
	StatusText         string              `json:"status_text"`
	AuthorEmail        string              `json:"author_email"`
	Author             string              `json:"author"`
	Deployer           string              `json:"deployer"`
	RemoteBuild        bool                `json:"remoteBuild"`
	Message            string              `json:"message"`
	Progress           string              `json:"progress"`
	ReceivedTime       time.Time           `json:"received_time"`
	StartTime          time.Time           `json:"start_time"`
	EndTime            time.Time           `json:"end_time"`
	LastSuccessEndTime time.Time           `json:"last_success_end_time"`
	Complete           bool                `json:"complete"`
	Active             bool                `json:"active"`
	IsTemp             bool                `json:"is_temp"`
	IsReadonly         bool                `json:"is_readonly"`
	Url                string              `json:"url"`
	LogUrl             string              `json:"log_url"`
	SiteName           string              `json:"site_name"`
	BuildSummary       PublishBuildSummary `json:"build_summary"`
}

type PublishBuildSummary struct {
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
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

// publishPollingHandler implements [runtime.PollingHandler].
type publishPollingHandler struct {
	pipeline runtime.Pipeline
	location string

	lastResponse *PublishResponse
	// final result
	result *PublishResponse
}

func newPublishPollingHandler(
	pipeline runtime.Pipeline,
	location string) *publishPollingHandler {
	return &publishPollingHandler{
		pipeline: pipeline,
		location: location,
	}
}

func (h *publishPollingHandler) Done() bool {
	return h.result != nil && h.result.Complete
}

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

	// According to the service team, it's possible to observe a 404 response after the deployment is complete.
	// This 404 is due to the deployment record being "recycled". See work item TODO.
	if response.StatusCode == http.StatusNotFound && h.lastResponse != nil {
		h.result = h.lastResponse
		return response, nil
	}

	// after the deployment is in-progress
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		return nil, runtime.NewResponseError(response)
	}

	resp, err := httputil.ReadRawResponse[PublishResponse](response)
	if err != nil {
		return nil, err
	}

	// Server returns response with status code 200 even if the deployment is still in progress.
	// as a result, we need to read the body in full to determine the actual status.
	switch resp.Status {
	case PublishStatusCancelled:
		return nil, fmt.Errorf("deployment was cancelled")
	case PublishStatusFailed:
		return nil, fmt.Errorf("deployment failed: %s", resp.StatusText)
	case PublishStatusSuccess:
		h.result = resp
	case PublishStatusConflict:
		return nil, fmt.Errorf("deployment was cancelled due to another deployment being in progress")
	case PublishStatusPartialSuccess:
		return nil, fmt.Errorf("deployment was partially successful")
	}

	h.lastResponse = resp
	return response, nil
}

// Gets the result of the deploy operation
func (h *publishPollingHandler) Result(ctx context.Context, out **PublishResponse) error {
	*out = h.result
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
