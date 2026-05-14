// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"

	"azureaiagent/internal/version"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// API path prefix for dataset endpoints.
const pathDatasets = "/datasets"

// DatasetClient provides methods for dataset upload, download, and metadata retrieval.
type DatasetClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewDatasetClient creates a new DatasetClient.
func NewDatasetClient(endpoint string, cred azcore.TokenCredential) *DatasetClient {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version)

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{"X-Ms-Correlation-Request-Id", "X-Request-Id"},
			IncludeBody:    true,
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{"https://ai.azure.com/.default"}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(userAgent),
		},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-datasets",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &DatasetClient{
		endpoint: endpoint,
		pipeline: pipeline,
	}
}

// CreateDataset registers a dataset with inline content (upload).
func (c *DatasetClient) CreateDataset(
	ctx context.Context,
	request *CreateDatasetRequest,
	apiVersion string,
) (*Dataset, error) {
	return doRequestTyped[Dataset](c, ctx, http.MethodPost, pathDatasets, nil, request, apiVersion)
}

// GetDataset retrieves metadata for a dataset by name and version.
func (c *DatasetClient) GetDataset(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) (*Dataset, error) {
	path := fmt.Sprintf("%s/%s/versions/%s", pathDatasets, url.PathEscape(name), url.PathEscape(version))
	return doRequestTyped[Dataset](c, ctx, http.MethodGet, path, nil, nil, apiVersion)
}

// GetDatasetCredential retrieves a SAS credential for downloading a dataset from blob storage.
func (c *DatasetClient) GetDatasetCredential(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) (*DatasetCredential, error) {
	path := fmt.Sprintf(
		"%s/%s/versions/%s/credentials",
		pathDatasets, url.PathEscape(name), url.PathEscape(version),
	)
	return doRequestTyped[DatasetCredential](c, ctx, http.MethodPost, path, nil, nil, apiVersion)
}

// DownloadDataset downloads dataset content from blob storage using a SAS-authenticated URL.
// Returns the raw content as bytes. The downloadURL should be the full URL with SAS token
// (e.g., from DatasetCredential.ResolvedDownloadURI()).
func (c *DatasetClient) DownloadDataset(ctx context.Context, downloadURL string) ([]byte, error) {
	log.Printf("[dataset_api] downloading dataset from blob: %s", downloadURL)

	req, err := runtime.NewRequest(ctx, http.MethodGet, downloadURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create download request: %w", err)
	}

	// Use a plain HTTP client for blob downloads — the SAS token in the URL provides
	// authentication, and Azure SDK pipeline policies (bearer token, correlation ID)
	// should not be sent to Azure Blob Storage endpoints.
	httpClient := &http.Client{}
	resp, err := httpClient.Do(req.Raw())
	if err != nil {
		return nil, fmt.Errorf("failed to download dataset from blob: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("blob download failed with status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read dataset content: %w", err)
	}

	log.Printf("[dataset_api] downloaded %d bytes", len(data))
	return data, nil
}

// doRequest performs an HTTP request against the dataset API and returns the raw response body.
func (c *DatasetClient) doRequest(
	ctx context.Context,
	method string,
	path string,
	query map[string]string,
	body any,
	apiVersion string,
) ([]byte, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	u.Path += path
	q := u.Query()
	if apiVersion != "" {
		q.Set("api-version", apiVersion)
	}
	for k, v := range query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	req, err := runtime.NewRequest(ctx, method, u.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	log.Printf("[dataset_api] %s %s", method, u.String())

	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		log.Printf("[dataset_api] request body: %s", string(payload))
		if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
			return nil, fmt.Errorf("failed to set request body: %w", err)
		}
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	log.Printf("[dataset_api] response status: %d", resp.StatusCode)
	log.Printf("[dataset_api] response body: %s", string(respBody))

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated, http.StatusAccepted) {
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return nil, runtime.NewResponseError(resp)
	}

	return respBody, nil
}

// doRequestTyped performs an HTTP request and unmarshals the response into T.
func doRequestTyped[T any](
	c *DatasetClient,
	ctx context.Context,
	method string,
	path string,
	query map[string]string,
	body any,
	apiVersion string,
) (*T, error) {
	respBody, err := c.doRequest(ctx, method, path, query, body, apiVersion)
	if err != nil {
		return nil, err
	}

	if len(respBody) == 0 {
		return new(T), nil
	}

	var result T
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}
