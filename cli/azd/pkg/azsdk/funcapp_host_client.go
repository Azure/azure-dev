package azsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	pipeline runtime.Pipeline
}

func NewFuncAppHostClient(
	hostName string,
	credential azcore.TokenCredential,
	options *arm.ClientOptions,
) (*FuncAppHostClient, error) {
	funcAppHostOptions := &arm.ClientOptions{}
	if options != nil {
		optionsCopy := *options
		funcAppHostOptions = &optionsCopy
	}

	funcAppHostOptions.DisableRPRegistration = true

	pipeline, err := armruntime.NewPipeline(
		"funcapp-deploy", "1.0.0", credential, runtime.PipelineOptions{}, funcAppHostOptions)
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
// This is currently only supported for flexconsumption plans.
func (c *FuncAppHostClient) Publish(
	ctx context.Context,
	zipFile io.ReadSeeker,
	options *PublishOptions) (PublishResponse, error) {
	if options == nil {
		options = &PublishOptions{}
	}

	endpoint := fmt.Sprintf("https://%s/api/publish", c.hostName)
	request, err := runtime.NewRequest(ctx, http.MethodPost, endpoint)
	if err != nil {
		return PublishResponse{}, fmt.Errorf("creating deploy request: %w", err)
	}

	rawRequest := request.Raw()
	rawRequest.Header.Set("Accept", "application/json")

	query := rawRequest.URL.Query()
	if options.RemoteBuild {
		query.Set("RemoteBuild", "true")
	}
	rawRequest.URL.RawQuery = query.Encode()

	err = request.SetBody(streaming.NopCloser(zipFile), "application/zip")
	if err != nil {
		return PublishResponse{}, fmt.Errorf("setting request body: %w", err)
	}

	response, err := c.pipeline.Do(request)
	if err != nil {
		return PublishResponse{}, err
	}

	defer response.Body.Close()

	if !runtime.HasStatusCode(response, http.StatusAccepted) {
		return PublishResponse{}, runtime.NewResponseError(response)
	}

	body, err := runtime.Payload(response)
	if err != nil {
		return PublishResponse{}, err
	}

	// the response body is the deployment id and nothing else.
	var deploymentId string
	if err := json.Unmarshal(body, &deploymentId); err != nil {
		return PublishResponse{}, err
	}

	if deploymentId == "" {
		return PublishResponse{}, fmt.Errorf("missing deployment id")
	}

	location := fmt.Sprintf("https://%s/api/deployments/%s", c.hostName, deploymentId)
	return c.waitForDeployment(ctx, location)
}

func (c *FuncAppHostClient) waitForDeployment(ctx context.Context, location string) (PublishResponse, error) {
	// This frequency is recommended by the service team.
	polLDelay := 1 * time.Second
	var lastResponse *PublishResponse

	for {
		req, err := runtime.NewRequest(ctx, http.MethodGet, location)
		if err != nil {
			return PublishResponse{}, err
		}

		response, err := c.pipeline.Do(req)
		if err != nil {
			return PublishResponse{}, err
		}

		// It's possible to observe a 404 response after the deployment is complete.
		// If a 404 is observed, we assume the deployment is complete and return the last response.
		//
		// This 404 is due to the deployment worker being "recycled".
		// This shortcoming would be fixed by work item https://msazure.visualstudio.com/Antares/_workitems/edit/24715654.
		if response.StatusCode == http.StatusNotFound && lastResponse != nil {
			return *lastResponse, nil
		}

		if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
			return PublishResponse{}, runtime.NewResponseError(response)
		}

		// Server always returns status code of OK-200 whether the deployment is in-progress or complete.
		// Thus, we always read the response body to determine the actual status.
		resp := PublishResponse{}
		if err := runtime.UnmarshalAsJSON(response, &resp); err != nil {
			return PublishResponse{}, err
		}

		switch resp.Status {
		case PublishStatusCancelled:
			return PublishResponse{}, fmt.Errorf("deployment was cancelled")
		case PublishStatusFailed:
			return PublishResponse{}, fmt.Errorf("deployment failed: %s", resp.StatusText)
		case PublishStatusSuccess:
			return resp, nil
		case PublishStatusConflict:
			return PublishResponse{}, fmt.Errorf("deployment was cancelled due to another deployment being in progress")
		case PublishStatusPartialSuccess:
			return PublishResponse{}, fmt.Errorf("deployment was partially successful: %s", resp.StatusText)
		}

		// Record the latest response
		lastResponse = &resp

		delay := polLDelay
		if retryAfter := httputil.RetryAfter(response); retryAfter > 0 {
			delay = polLDelay
		}

		select {
		case <-ctx.Done():
			return PublishResponse{}, ctx.Err()
		case <-time.After(delay):
		}
	}
}

// The response for a deployment located at api/deployments/{id} that represents the deployment initiated by api/publish.
type PublishResponse struct {
	Id                 string              `json:"id"`
	Status             PublishStatus       `json:"status"`
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

type PublishStatus int

const (
	PublishStatusCancelled      PublishStatus = -1
	PublishStatusPending        PublishStatus = 0
	PublishStatusBuilding       PublishStatus = 1
	PublishStatusDeploying      PublishStatus = 2
	PublishStatusFailed         PublishStatus = 3
	PublishStatusSuccess        PublishStatus = 4
	PublishStatusConflict       PublishStatus = 5
	PublishStatusPartialSuccess PublishStatus = 6
)
