package azsdk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	armruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/sethvargo/go-retry"
)

const (
	maxDeployDuration    = 5 * time.Minute
	deployStatusInterval = 5 * time.Second
)

type ZipDeployClient struct {
	subscriptionId string
	pipeline       runtime.Pipeline
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

func NewZipDeployClient(subscriptionId string, credential azcore.TokenCredential, options *arm.ClientOptions) (*ZipDeployClient, error) {
	if options == nil {
		options = &arm.ClientOptions{}
	}

	pipeline, err := armruntime.NewPipeline("zip-deploy", "1.0.0", credential, runtime.PipelineOptions{}, options)
	if err != nil {
		return nil, fmt.Errorf("failed creating HTTP pipeline: %w", err)
	}

	return &ZipDeployClient{
		subscriptionId: subscriptionId,
		pipeline:       pipeline,
	}, nil
}

// Deploys the specified application zip to the azure app service
func (c *ZipDeployClient) Deploy(ctx context.Context, appName string, zipFilePath string) (*DeployResponse, error) {
	request, err := c.createDeployRequest(ctx, appName, zipFilePath)
	if err != nil {
		return nil, err
	}

	response, err := c.pipeline.Do(request)
	if err != nil {
		return nil, runtime.NewResponseError(response)
	}

	if !runtime.HasStatusCode(response, http.StatusAccepted) {
		return nil, runtime.NewResponseError(response)
	}

	var deploymentStatus *DeployStatusResponse
	statusUrl := response.Header.Get("Location")

	err = retry.Do(ctx, retry.WithMaxDuration(maxDeployDuration, retry.NewConstant(deployStatusInterval)), func(ctx context.Context) error {
		deploymentStatus, err = c.getDeploymentStatus(ctx, statusUrl)
		if err != nil {
			return err
		}

		if !deploymentStatus.Complete {
			return retry.RetryableError(errors.New(*deploymentStatus.Progress))
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("deployment failed: %w", err)
	}

	return &DeployResponse{
		DeployStatus: deploymentStatus.DeployStatus,
	}, nil
}

func (c *ZipDeployClient) createDeployRequest(ctx context.Context, appName string, zipFilePath string) (*policy.Request, error) {
	endpoint := fmt.Sprintf("https://%s.scm.azurewebsites.net/api/zipdeploy", appName)
	req, err := runtime.NewRequest(ctx, http.MethodPost, endpoint)
	if err != nil {
		return nil, fmt.Errorf("creating deploy request: %w", err)
	}

	fileBytes, err := os.ReadFile(zipFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed reading file '%s' : %w", zipFilePath, err)
	}

	rawRequest := req.Raw()
	rawRequest.Body = io.NopCloser(bytes.NewBuffer(fileBytes))
	query := rawRequest.URL.Query()
	query.Set("isAsync", "true")
	rawRequest.Header.Set("Content-Type", "application/octet-stream")
	rawRequest.Header.Set("Accept", "application/json")
	rawRequest.URL.RawQuery = query.Encode()

	return req, nil
}

// Gets the deployment status for the specified deployment URL.
func (c *ZipDeployClient) getDeploymentStatus(ctx context.Context, deploymentUrl string) (*DeployStatusResponse, error) {
	request, err := c.createGetDeploymentStatusRequest(ctx, deploymentUrl)
	if err != nil {
		return nil, err
	}

	response, err := c.pipeline.Do(request)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}

	if !runtime.HasStatusCode(response, http.StatusOK) && !runtime.HasStatusCode(response, http.StatusAccepted) {
		return nil, runtime.NewResponseError(response)
	}

	deploymentStatus, err := ReadRawResponse[DeployStatusResponse](response)
	if err != nil {
		return nil, err
	}

	return deploymentStatus, nil
}

func (c *ZipDeployClient) createGetDeploymentStatusRequest(ctx context.Context, deploymentUrl string) (*policy.Request, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, deploymentUrl)
	if err != nil {
		return nil, fmt.Errorf("creating deploy request: %w", err)
	}

	return req, nil
}
