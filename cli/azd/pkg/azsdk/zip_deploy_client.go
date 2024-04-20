package azsdk

import (
	"context"
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
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

const (
	deployStatusInterval = 10 * time.Second
)

var (
	deploymentBuildStatusBuildFailed       armappservice.DeploymentBuildStatus = "BuildFailed"
	deploymentBuildStatusBuildInProgress   armappservice.DeploymentBuildStatus = "BuildInProgress"
	deploymentBuildStatusRuntimeFailed     armappservice.DeploymentBuildStatus = "RuntimeFailed"
	deploymentBuildStatusRuntimeStarting   armappservice.DeploymentBuildStatus = "RuntimeStarting"
	deploymentBuildStatusRuntimeSuccessful armappservice.DeploymentBuildStatus = "RuntimeSuccessful"
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

func (c *ZipDeployClient) getDeploymentStatusSyntax() string {
	if c.hostName == "" {
		return ""
	}
	url := c.hostName + "/api/deployments/"
	return url
}

func (c *ZipDeployClient) getLatestDeploymentUrl() string {
	return c.getDeploymentStatusSyntax() + "latest"
}

func (c *ZipDeployClient) getDeploymentLog(deploymentId string) string {
	log := c.getDeploymentStatusSyntax() + deploymentId + "/log"
	return log
}

func (c *ZipDeployClient) getDeploymentUrl(deploymentId string) string {
	url := c.getDeploymentStatusSyntax() + deploymentId
	return url
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
func (c *ZipDeployClient) Deploy(ctx context.Context, zipFile io.Reader, subscriptionId string, resourceGroup string, appName string, buildProgress io.Writer) (*DeployResponse, error) {
	var res *http.Response
	var deploymentId string
	withCaptureResponseCtx := policy.WithCaptureResponse(ctx, &res)

	poller, err := c.BeginDeploy(withCaptureResponseCtx, zipFile)
	if err != nil {
		return nil, err
	}

	currentTime := time.Now()
	cancelProgress := make(chan bool)

	// If host name is not empty for web apps
	if c.getDeploymentStatusSyntax() != "" && buildProgress != nil {
		// Deployment API currently only supports Linux Web Apps
		defer func() { cancelProgress <- true }()
		go func() {
			// get deployment id
			if res == nil {
				panic("http.Response is nil, unable to get deployment id")
			} else {
				deploymentId = res.Header.Get("Scm-Deployment-Id")
			}

			initialDelay := 3 * time.Second
			regularDelay := 10 * time.Second
			timer := time.NewTimer(initialDelay)

			for {
				select {
				case <-cancelProgress:
					timer.Stop()
					return
				case <-timer.C:
					deploymentMessage, err := c.checkRunTimeStatus(ctx, currentTime, subscriptionId, resourceGroup, appName, deploymentId)
					if err != nil {
						log.Printf("checking deployment status fail, skip monitoring deployment status: %s", err.Error())
					}

					if deploymentMessage != "" {
						buildProgress.Write([]byte(deploymentMessage))
					}

					timer.Reset(regularDelay)
				}
			}
		}()
	}

	response, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: deployStatusInterval,
	})
	if err != nil {
		return nil, err
	}

	return response, nil
}

func (c *ZipDeployClient) checkRunTimeStatus(ctx context.Context, currentTime time.Time, subscriptionId, resourceGroup, appName, deploymentId string) (string, error) {
	res, err := getProductionSiteDeploymentStatus(ctx, subscriptionId, resourceGroup, appName, deploymentId)
	if err != nil {
		return "", err
	}

	properties := res.CsmDeploymentStatus.Properties
	status := properties.Status
	errorString := ""
	deploymentResult := ""
	inProgressNumber := int(*properties.NumberOfInstancesInProgress)
	successNumber := int(*properties.NumberOfInstancesSuccessful)
	failNumber := int(*properties.NumberOfInstancesFailed)
	totalNumber := inProgressNumber + successNumber + failNumber
	failLog := properties.FailedInstancesLogs
	runTime := time.Since(currentTime)
	maxTime := 1000 * time.Second

	// Print out err instead of return err
	if status != nil {
		switch *status {
		case deploymentBuildStatusRuntimeStarting:
			return fmt.Sprintf("In progress instances: %d, Successful instances: %d\n", inProgressNumber, successNumber), nil
		case deploymentBuildStatusRuntimeSuccessful:
			return "Deployment success", nil
		case deploymentBuildStatusRuntimeFailed:
			if successNumber > 0 {
				deploymentResult += fmt.Sprintf("Site started with errors: %d/%d instances failed to start successfully\n",
					failNumber, totalNumber)
			} else if totalNumber > 0 {
				deploymentResult += fmt.Sprintf("Deployment failed. In progress instances: %d, Successful instances: %d, Failed Instances: %d\n",
					inProgressNumber, successNumber, failNumber)
			}

			errors := properties.Errors
			errorExtendedCode := errors[0].ExtendedCode
			errorMessage := errors[0].Message

			if len(errors) > 0 {
				if errorMessage != nil {
					errorString += fmt.Sprintf("Error: %s\n", *errorMessage)
				} else if errorExtendedCode != nil {
					errorString += fmt.Sprintf("Extended ErrorCode: %s\n", *errorExtendedCode)
				}
			}

			if len(failLog) > 0 {
				deploymentResult += fmt.Sprintf("Please check the runtime logs for more info: %s\n", *failLog[0])
			}

			return deploymentResult, fmt.Errorf(errorString)
		case deploymentBuildStatusBuildFailed:
			deploymentResult += "Deployment failed because the build process failed\n"
			errors := properties.Errors
			errorExtendedCode := errors[0].ExtendedCode
			errorMessage := errors[0].Message

			if len(errors) > 0 {
				if errorMessage != nil {
					errorString += fmt.Sprintf("Error: %s\n", *errorMessage)
				} else if errorExtendedCode != nil {
					errorString += fmt.Sprintf("Extended ErrorCode: %s\n", *errorExtendedCode)
				}
			}

			var deploymentLog string

			if len(failLog) == 0 {
				deploymentLog = c.getLatestDeploymentUrl()
			} else {
				deploymentLog = *failLog[0]
			}

			deploymentResult += fmt.Sprintf("Please check the build logs for more info: %s\n", deploymentLog)

			return deploymentResult, fmt.Errorf(errorString)
		}
	}

	if runTime > maxTime && *status != deploymentBuildStatusRuntimeSuccessful {
		if *status == deploymentBuildStatusBuildInProgress {
			return fmt.Sprintf("Timeout reached while build was still in progress. Navigate to %s to check the build logs for your app", c.getDeploymentLog(deploymentId)), nil
		}

		deploymentResult += fmt.Sprintf("Timeout reached while tracking deployment status, however, the deployment"+
			" operation is still on-going. Navigate to %s to check the deployment status of your app",
			c.getDeploymentUrl(deploymentId))

		if inProgressNumber+successNumber+failNumber > 0 {
			deploymentResult += fmt.Sprintf("In progress instances: %d, Successful instances: %d, Failed Instances: %d\n",
				inProgressNumber, successNumber, failNumber)

		}

		return deploymentResult, nil
	}

	return deploymentResult, nil
}

// Example definition: https://github.com/Azure/azure-rest-api-specs/tree/main/specification/web/resource-manager/Microsoft.Web/stable/2022-03-01/examples/GetSiteDeploymentStatus.json
func getProductionSiteDeploymentStatus(ctx context.Context, subscriptionId, resourceGroup, appName, deploymentId string) (armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse, error) {
	var result armappservice.WebAppsClientGetProductionSiteDeploymentStatusResponse

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return result, fmt.Errorf("obtaining azure credential: %v", err)
	}

	client, err := armappservice.NewWebAppsClient(subscriptionId, cred, nil)
	if err != nil {
		return result, fmt.Errorf("creating web app client: %v", err)
	}

	poller, err := client.BeginGetProductionSiteDeploymentStatus(ctx, resourceGroup, appName, deploymentId, nil)
	if err != nil {
		return result, fmt.Errorf("getting deployment status: %v", err)
	}

	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return result, err
	}

	return res, nil
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
