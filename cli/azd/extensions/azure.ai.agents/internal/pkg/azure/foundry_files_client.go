// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"

	"azureaiagent/internal/version"
)

// filesAPIPathVersion is the version path segment for the OpenAI-compatible
// Files and Vector Stores endpoints. These endpoints require the version in the
// path (/openai/v1/...) and reject an api-version query parameter.
const filesAPIPathVersion = "v1"

// FoundryFilesClient talks to the OpenAI-compatible Files and Vector Stores
// endpoints exposed under a Foundry project data-plane endpoint. It is used by
// the prompt-agent deploy engine to turn a local `files/` folder into a vector
// store that backs a `file_search` tool.
type FoundryFilesClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewFoundryFilesClient creates a client rooted at a Foundry project endpoint
// (e.g. https://<account>.services.ai.azure.com/api/projects/<project>).
func NewFoundryFilesClient(endpoint string, cred azcore.TokenCredential) *FoundryFilesClient {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version)

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{azsdk.MsCorrelationIdHeader, "X-Request-Id"},
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{"https://ai.azure.com/.default"}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(userAgent),
		},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-agents",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &FoundryFilesClient{
		endpoint: strings.TrimRight(endpoint, "/"),
		pipeline: pipeline,
	}
}

// FileObject is the response for an uploaded file.
type FileObject struct {
	Id       string `json:"id"`
	Object   string `json:"object"`
	Bytes    int64  `json:"bytes"`
	Filename string `json:"filename"`
	Purpose  string `json:"purpose"`
}

// VectorStoreObject is the response for a vector store.
type VectorStoreObject struct {
	Id     string `json:"id"`
	Object string `json:"object"`
	Name   string `json:"name"`
}

// UploadFile uploads a single file's content to the Foundry Files endpoint and
// returns the created file object. purpose defaults to "assistants" when empty.
func (c *FoundryFilesClient) UploadFile(
	ctx context.Context,
	filename string,
	content []byte,
	purpose string,
) (*FileObject, error) {
	if strings.TrimSpace(purpose) == "" {
		purpose = "assistants"
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("purpose", purpose); err != nil {
		return nil, fmt.Errorf("writing purpose field: %w", err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("creating file part: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return nil, fmt.Errorf("writing file content: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("closing multipart writer: %w", err)
	}

	targetURL := fmt.Sprintf("%s/openai/%s/files", c.endpoint, filesAPIPathVersion)
	req, err := runtime.NewRequest(ctx, http.MethodPost, targetURL)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if err := req.SetBody(
		streaming.NopCloser(bytes.NewReader(body.Bytes())),
		writer.FormDataContentType(),
	); err != nil {
		return nil, fmt.Errorf("setting request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}

	var result FileObject
	if err := decodeJSON(resp.Body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// createVectorStoreRequest is the body for creating a vector store.
type createVectorStoreRequest struct {
	Name    string   `json:"name,omitempty"`
	FileIds []string `json:"file_ids"`
}

// CreateVectorStore creates a vector store from the given file ids and returns
// the created store. name is optional but recommended for later lookup.
func (c *FoundryFilesClient) CreateVectorStore(
	ctx context.Context,
	name string,
	fileIDs []string,
) (*VectorStoreObject, error) {
	payload, err := json.Marshal(createVectorStoreRequest{Name: name, FileIds: fileIDs})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	targetURL := fmt.Sprintf("%s/openai/%s/vector_stores", c.endpoint, filesAPIPathVersion)
	req, err := runtime.NewRequest(ctx, http.MethodPost, targetURL)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if err := req.SetBody(
		streaming.NopCloser(bytes.NewReader(payload)),
		"application/json",
	); err != nil {
		return nil, fmt.Errorf("setting request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}

	var result VectorStoreObject
	if err := decodeJSON(resp.Body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// decodeJSON reads and unmarshals a JSON response body.
func decodeJSON(r io.Reader, v any) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}
	return nil
}
