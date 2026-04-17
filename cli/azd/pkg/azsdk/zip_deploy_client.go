// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azsdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	armruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"go.opentelemetry.io/otel/trace"
)

const (
	deployStatusInterval = 10 * time.Second

	// noInstancesThreshold is the number of consecutive polls with 0 total instances
	// (in progress + successful + failed) during RuntimeStarting before we consider
	// the deployment successful. This handles the case where the web app is administratively
	// stopped and will never transition to RuntimeSuccessful.
	noInstancesThreshold = 3
)

// ZipDeployClient wraps usage of app service zip deploy used for application deployments
// More info can be found at the following:
// https://github.com/MicrosoftDocs/azure-docs/blob/main/includes/app-service-deploy-zip-push-rest.md
// https://github.com/projectkudu/kudu/wiki/REST-API
type ZipDeployClient struct {
	hostName         string
	pipeline         runtime.Pipeline
	cred             azcore.TokenCredential
	armClientOptions *arm.ClientOptions
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

// Creates a new ZipDeployClient instance
func NewZipDeployClient(
	hostName string,
	credential azcore.TokenCredential,
	armClientOptions *arm.ClientOptions,
) (*ZipDeployClient, error) {
	zipDeployOptions := &arm.ClientOptions{}
	if armClientOptions != nil {
		optionsCopy := *armClientOptions
		zipDeployOptions = &optionsCopy
	}

	// We do not have a Resource provider to register
	zipDeployOptions.DisableRPRegistration = true

	// Increase default retry attempts from 3 to 4 as zipdeploy often fails with 3 retries.
	// With the default azcore.policy options of 800ms RetryDelay, this introduces up to 20 seconds of exponential back-off.
	zipDeployOptions.Retry = policy.RetryOptions{
		MaxRetries: 4,
	}

	pipeline, err := armruntime.NewPipeline("zip-deploy", "1.0.0", credential, runtime.PipelineOptions{}, zipDeployOptions)
	if err != nil {
		return nil, fmt.Errorf("failed creating HTTP pipeline: %w", err)
	}

	return &ZipDeployClient{
		hostName:         hostName,
		pipeline:         pipeline,
		cred:             credential,
		armClientOptions: armClientOptions,
	}, nil
}

// Begins a zip deployment and returns a poller to check for status
func (c *ZipDeployClient) BeginDeploy(
	ctx context.Context,
	zipFile io.ReadSeeker,
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

// Deploys the specified application zip to the azure app service using deployment status api and waits for completion
func (c *ZipDeployClient) BeginDeployTrackStatus(
	ctx context.Context,
	zipFile io.ReadSeeker,
	subscriptionId,
	resourceGroup,
	appName string,
) (*runtime.Poller[armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse], error) {
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

	client, err := armappservice.NewWebAppsClient(subscriptionId, c.cred, c.armClientOptions)

	if err != nil {
		return nil, fmt.Errorf("creating web app client: %w", err)
	}

	deploymentStatusId := response.Header.Get("Scm-Deployment-Id")
	if deploymentStatusId == "" {
		return nil, fmt.Errorf("empty deployment status id")
	}

	// Add 404 to default retry errors in azure-sdk-for-go. We get temporary 404s when the KUDO API received the request
	// and created a temp deployment id as a intermediate step before deployed with actual deployment id
	retryCtx := policy.WithRetryOptions(ctx, policy.RetryOptions{
		MaxRetries: 4,
		RetryDelay: 5 * time.Second,
		StatusCodes: append([]int{
			http.StatusRequestTimeout,      // 408
			http.StatusTooManyRequests,     // 429
			http.StatusInternalServerError, // 500
			http.StatusBadGateway,          // 502
			http.StatusServiceUnavailable,  // 503
			http.StatusGatewayTimeout,      // 504
		}, http.StatusNotFound), // 404
	})

	// nolint:lll
	// Example definition: https://github.com/Azure/azure-rest-api-specs/tree/main/specification/web/resource-manager/Microsoft.Web/stable/2022-03-01/examples/GetSiteDeploymentStatus.json
	poller, err := client.BeginGetProductionSiteDeploymentStatus(retryCtx, resourceGroup, appName, deploymentStatusId, nil)
	if err != nil {
		return nil, fmt.Errorf("getting deployment status: %w", err)
	}

	return poller, nil
}

// deploymentStatusResult holds the result of evaluating a deployment status response.
type deploymentStatusResult struct {
	// err is set when the deployment has failed.
	err error
	// noInstances is true when the status is RuntimeStarting but there are zero total instances.
	// This indicates the web app may be administratively stopped.
	noInstances bool
}

func logWebAppDeploymentStatus(
	res armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse,
	traceId string,
	progressLog func(string),
) deploymentStatusResult {
	if (res == armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse{} ||
		res.CsmDeploymentStatus == armappservice.CsmDeploymentStatus{} ||
		res.CsmDeploymentStatus.Properties == nil) {
		return deploymentStatusResult{err: fmt.Errorf("response or its properties are empty")}
	}
	properties := res.CsmDeploymentStatus.Properties

	if properties.Status == nil {
		return deploymentStatusResult{err: fmt.Errorf("deployment status is nil")}
	}

	var inProgressNumber, successNumber, failNumber int
	if properties.NumberOfInstancesInProgress != nil {
		inProgressNumber = int(*properties.NumberOfInstancesInProgress)
	}
	if properties.NumberOfInstancesSuccessful != nil {
		successNumber = int(*properties.NumberOfInstancesSuccessful)
	}
	if properties.NumberOfInstancesFailed != nil {
		failNumber = int(*properties.NumberOfInstancesFailed)
	}

	errorString := ""
	logErrorFunction := func(properties *armappservice.CsmDeploymentStatusProperties, message string) {
		for _, err := range properties.Errors {
			if err.Message != nil {
				errorString += fmt.Sprintf("Error: %s\n", *err.Message)
			}
		}

		errorString += "\n"
		for _, log := range properties.FailedInstancesLogs {
			errorString += fmt.Sprintf("Please check the %slogs for more info: %s\n", message, *log)
		}

		errorString += "\n"
		if traceId != "" {
			errorString += fmt.Sprintf("Trace ID: %s\n", traceId)
		}
	}
	status := *properties.Status

	switch status {
	case armappservice.DeploymentBuildStatusBuildRequestReceived:
		progressLog("Received build request, starting build process")
		return deploymentStatusResult{}
	case armappservice.DeploymentBuildStatusBuildInProgress:
		progressLog("Running build process")
		return deploymentStatusResult{}
	case armappservice.DeploymentBuildStatusRuntimeStarting:
		totalNumber := inProgressNumber + successNumber + failNumber
		if totalNumber == 0 {
			progressLog("Starting runtime process, 0 total instances detected (app may be stopped)")
			return deploymentStatusResult{noInstances: true}
		}
		progressLog(fmt.Sprintf("Starting runtime process, %d in progress instances, %d successful instances",
			inProgressNumber, successNumber))
		return deploymentStatusResult{}
	case armappservice.DeploymentBuildStatusRuntimeSuccessful, armappservice.DeploymentBuildStatusBuildSuccessful:
		return deploymentStatusResult{}
	case armappservice.DeploymentBuildStatusRuntimeFailed:
		totalNumber := inProgressNumber + successNumber + failNumber

		if successNumber > 0 {
			errorString += fmt.Sprintf("%d/%d instances failed to start successfully\n", failNumber, totalNumber)
		} else if totalNumber > 0 {
			errorString += fmt.Sprintf("Deployment failed because the runtime process failed. In progress instances: %d, "+
				"Successful instances: %d, Failed Instances: %d\n",
				inProgressNumber, successNumber, failNumber)
		}

		logErrorFunction(properties, "runtime ")
		return deploymentStatusResult{err: errors.New(errorString)}
	case armappservice.DeploymentBuildStatusBuildFailed:
		errorString += "Deployment failed because the build process failed\n"
		logErrorFunction(properties, "build ")
		return deploymentStatusResult{err: errors.New(errorString)}
	// Progress Log for other states
	default:
		if len(status) > 0 {
			progressLog(fmt.Sprintf("Running deployment status api in stage %s", status))
		}
		return deploymentStatusResult{}
	}
}

func (c *ZipDeployClient) DeployTrackStatus(
	ctx context.Context,
	zipFile io.ReadSeeker,
	subscriptionId string,
	resourceGroup string,
	appName string,
	progressLog func(string)) error {
	var response armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse

	poller, err := c.BeginDeployTrackStatus(ctx, zipFile, subscriptionId, resourceGroup, appName)
	if err != nil {
		return err
	}

	delay := 3 * time.Second
	pollCount := 0
	noInstancesCount := 0
	for {
		var resp *http.Response

		resp, err = poller.Poll(ctx)
		if err != nil {
			return err
		}

		if resp.StatusCode == http.StatusInternalServerError {
			return runtime.NewResponseError(resp)
		}

		if err := runtime.UnmarshalAsJSON(resp, &response); err != nil {
			return err
		}

		if poller.Done() {
			if response.Properties == nil || response.Properties.Status == nil {
				return fmt.Errorf("deployment status API completed with nil properties or status")
			}
			status := *response.Properties.Status
			if status != armappservice.DeploymentBuildStatusRuntimeSuccessful &&
				status != armappservice.DeploymentBuildStatusBuildSuccessful &&
				status != armappservice.DeploymentBuildStatusBuildFailed &&
				status != armappservice.DeploymentBuildStatusRuntimeFailed {
				return fmt.Errorf("deployment status API unexpectedly terminated at stage %s",
					status)
			}
			spanCtx := trace.SpanContextFromContext(ctx)
			traceId := spanCtx.TraceID().String()
			result := logWebAppDeploymentStatus(response, traceId, progressLog)
			if result.err != nil {
				return result.err
			}
			break
		}

		result := logWebAppDeploymentStatus(response, "", progressLog)
		if result.err != nil {
			return result.err
		}

		if result.noInstances {
			noInstancesCount++
			if noInstancesCount >= noInstancesThreshold {
				progressLog("Web app has no running instances (app may be stopped). " +
					"Deployment package was uploaded successfully, skipping runtime status check.")
				return nil
			}
		} else {
			noInstancesCount = 0
		}

		// Wait longer after a few initial tries
		if pollCount > 20 {
			delay = 20 * time.Second
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			pollCount++
		}
	}

	return nil
}

// Deploys the specified application zip to the azure app service and waits for completion
func (c *ZipDeployClient) Deploy(ctx context.Context, zipFile io.ReadSeeker) (*DeployResponse, error) {
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
	zipFile io.ReadSeeker,
) (*policy.Request, error) {
	endpoint := fmt.Sprintf("https://%s/api/zipdeploy", c.hostName)
	req, err := runtime.NewRequest(ctx, http.MethodPost, endpoint)
	if err != nil {
		return nil, fmt.Errorf("creating deploy request: %w", err)
	}

	if err = req.SetBody(streaming.NopCloser(zipFile), "application/octet-stream"); err != nil {
		return nil, fmt.Errorf("setting request body: %w", err)
	}

	rawRequest := req.Raw()
	query := rawRequest.URL.Query()
	query.Set("isAsync", "true")
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

// IsScmReady pings the SCM /api/deployments endpoint to check if the Kudu
// service is responsive. Returns true when the endpoint responds with HTTP 200,
// false for transient errors (503, connection refused, etc.), or an error for
// unexpected failures. This is used to detect SCM container restarts that occur
// when ARM applies site configuration changes after provisioning.
func (c *ZipDeployClient) IsScmReady(ctx context.Context) (bool, error) {
	endpoint := fmt.Sprintf("https://%s/api/deployments", c.hostName)
	req, err := runtime.NewRequest(ctx, http.MethodGet, endpoint)
	if err != nil {
		return false, fmt.Errorf("creating SCM readiness request: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		// Propagate context cancellation / deadline so callers (and Ctrl+C) react immediately.
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// Only suppress clearly transient transport errors that indicate the SCM
		// container is still restarting. Propagate unexpected errors (e.g. offline,
		// TLS failures, bad proxy) so callers can surface meaningful diagnostics.
		errMsg := err.Error()
		if strings.Contains(errMsg, "connection refused") ||
			strings.Contains(errMsg, "no such host") {
			return false, nil
		}
		if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
			return false, nil
		}
		return false, fmt.Errorf("SCM readiness probe: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return false, nil
	default:
		return false, runtime.NewResponseError(resp)
	}
}
