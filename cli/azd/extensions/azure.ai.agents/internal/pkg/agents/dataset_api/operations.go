// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

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
			IncludeBody:    false,
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

// NewDatasetClientFromPipeline creates a DatasetClient with a pre-built pipeline.
// This is intended for tests that need to bypass auth policies.
func NewDatasetClientFromPipeline(endpoint string, pipeline runtime.Pipeline) *DatasetClient {
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

// UploadNewVersion reads the first JSONL file from localDir, computes the next
// version from currentVersion, and uploads it as a new dataset version using
// the 3-step pending upload flow:
//  1. startPendingUpload → get SAS URI
//  2. Upload blob to SAS URI
//  3. Finalize dataset version with dataUri
func (c *DatasetClient) UploadNewVersion(
	ctx context.Context,
	name string,
	currentVersion string,
	localDir string,
	apiVersion string,
) (*Dataset, error) {
	content, err := ReadFirstJSONLFile(localDir)
	if err != nil {
		return nil, fmt.Errorf("reading dataset from %s: %w", localDir, err)
	}

	newVersion := NextVersion(currentVersion)

	// Step 1: Start pending upload to get a SAS URI.
	pending, err := c.StartPendingUpload(ctx, name, newVersion, apiVersion)
	if err != nil {
		return nil, fmt.Errorf("starting pending upload: %w", err)
	}

	uploadURI := pending.ResolvedUploadURI()
	if uploadURI == "" {
		return nil, fmt.Errorf("no upload SAS URI returned from startPendingUpload")
	}

	// Step 2: Upload the JSONL file to blob storage.
	blobName := name + ".jsonl"
	if err := c.UploadBlob(ctx, uploadURI, blobName, []byte(content)); err != nil {
		return nil, fmt.Errorf("uploading blob: %w", err)
	}

	// Step 3: Finalize the dataset version with the full blob URI.
	dataURI := strings.TrimSuffix(pending.ResolvedBlobURI(), "/") + "/" + blobName
	return c.FinalizeDatasetVersion(ctx, name, newVersion, dataURI, apiVersion)
}

// StartPendingUpload initiates a pending upload for a dataset version.
// Returns the SAS URI and blob reference for uploading data.
func (c *DatasetClient) StartPendingUpload(
	ctx context.Context,
	name string,
	version string,
	apiVersion string,
) (*PendingUploadResponse, error) {
	path := fmt.Sprintf(
		"%s/%s/versions/%s/startPendingUpload",
		pathDatasets, url.PathEscape(name), url.PathEscape(version),
	)
	return doRequestTyped[PendingUploadResponse](c, ctx, http.MethodPost, path, nil, json.RawMessage(`{}`), apiVersion)
}

// UploadBlob uploads data to a container SAS URI as a block blob.
func (c *DatasetClient) UploadBlob(ctx context.Context, containerSASUri, blobName string, data []byte) error {
	u, err := url.Parse(containerSASUri)
	if err != nil {
		return fmt.Errorf("invalid container SAS URI: %w", err)
	}

	// Append blob name to the container path.
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + blobName

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create upload request: %w", err)
	}
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("Content-Type", "application/octet-stream")

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload blob: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("blob upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// FinalizeDatasetVersion completes the dataset version after blob upload
// by sending the metadata (name, version, dataUri) to the API.
func (c *DatasetClient) FinalizeDatasetVersion(
	ctx context.Context,
	name string,
	version string,
	dataURI string,
	apiVersion string,
) (*Dataset, error) {
	path := fmt.Sprintf("%s/%s/versions/%s", pathDatasets, url.PathEscape(name), url.PathEscape(version))
	request := &FinalizeDatasetRequest{
		Name:    name,
		Version: version,
		Type:    "uri_file",
		DataURI: dataURI,
	}
	return doRequestTyped[Dataset](c, ctx, http.MethodPut, path, nil, request, apiVersion)
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

// ListContainerBlobs lists blobs in a container using a container-level SAS URI.
// The containerSASUri should include the SAS token (e.g., from credential.sasUri with sr=c).
// Returns a list of blob names found in the container.
func (c *DatasetClient) ListContainerBlobs(ctx context.Context, containerSASUri string) ([]string, error) {
	// Parse the container URI and append list query parameters.
	u, err := url.Parse(containerSASUri)
	if err != nil {
		return nil, fmt.Errorf("invalid container SAS URI: %w", err)
	}

	q := u.Query()
	q.Set("restype", "container")
	q.Set("comp", "list")
	u.RawQuery = q.Encode()

	log.Printf("[dataset_api] listing blobs: %s", u.Redacted())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create list request: %w", err)
	}

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list container blobs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("container list failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read list response: %w", err)
	}

	// Parse XML blob listing to extract blob names.
	names := parseBlobNames(string(body))
	log.Printf("[dataset_api] found %d blobs in container", len(names))
	return names, nil
}

// DownloadBlob downloads a single blob from a container using the container SAS URI
// and the blob name. Returns the blob content as bytes.
func (c *DatasetClient) DownloadBlob(ctx context.Context, containerSASUri, blobName string) ([]byte, error) {
	u, err := url.Parse(containerSASUri)
	if err != nil {
		return nil, fmt.Errorf("invalid container SAS URI: %w", err)
	}

	// Append blob name to the container path.
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + blobName

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create blob download request: %w", err)
	}

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download blob: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("blob download failed with status %d for %s", resp.StatusCode, blobName)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read blob content: %w", err)
	}

	log.Printf("[dataset_api] downloaded blob %s (%d bytes)", blobName, len(data))
	return data, nil
}

// parseBlobNames extracts blob names from the Azure Blob Storage XML list response
// using proper XML parsing against the EnumerationResults schema.
func parseBlobNames(xmlBody string) []string {
	type blob struct {
		Name string `xml:"Name"`
	}
	type blobs struct {
		Blob []blob `xml:"Blob"`
	}
	type enumerationResults struct {
		Blobs blobs `xml:"Blobs"`
	}

	var result enumerationResults
	if err := xml.Unmarshal([]byte(xmlBody), &result); err != nil {
		return nil
	}

	names := make([]string, 0, len(result.Blobs.Blob))
	for _, b := range result.Blobs.Blob {
		if b.Name != "" {
			names = append(names, b.Name)
		}
	}
	return names
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

	log.Printf("[dataset_api] %s %s", method, u.Redacted())

	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
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
