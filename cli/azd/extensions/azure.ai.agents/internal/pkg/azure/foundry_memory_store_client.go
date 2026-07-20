// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"

	"azureaiagent/internal/version"
)

// memoryStoreAPIVersion is the data-plane API version for the Foundry Memory Store API (preview).
const memoryStoreAPIVersion = "2025-11-15-preview"

// MemoryStoreKindDefault is the only memory store kind currently supported by the service.
const MemoryStoreKindDefault = "default"

// FoundryMemoryStoreClient provides methods for interacting with the Foundry Memory Store API.
type FoundryMemoryStoreClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewFoundryMemoryStoreClient creates a new FoundryMemoryStoreClient. The endpoint is the
// Foundry project root (for example,
// https://<account>.services.ai.azure.com/api/projects/<project>).
func NewFoundryMemoryStoreClient(
	endpoint string,
	cred azcore.TokenCredential,
) *FoundryMemoryStoreClient {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version)

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{azsdk.MsCorrelationIdHeader, "X-Request-Id"},
			IncludeBody:    true,
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

	return &FoundryMemoryStoreClient{
		endpoint: strings.TrimRight(endpoint, "/"),
		pipeline: pipeline,
	}
}

// MemoryStoreOptions controls extraction behavior and retention defaults for a memory store.
type MemoryStoreOptions struct {
	// ChatSummaryEnabled enables rolling chat-summary memory.
	ChatSummaryEnabled *bool `json:"chat_summary_enabled,omitempty"`
	// UserProfileEnabled enables durable user-profile memory.
	UserProfileEnabled *bool `json:"user_profile_enabled,omitempty"`
	// ProceduralMemoryEnabled enables procedural (how-to) memory.
	ProceduralMemoryEnabled *bool `json:"procedural_memory_enabled,omitempty"`
	// DefaultTTLSeconds is the default time-to-live for newly created memory entries.
	// A value of 0 indicates no expiration.
	DefaultTTLSeconds *int `json:"default_ttl_seconds,omitempty"`
	// UserProfileDetails guides what user-profile information the agent should retain or avoid.
	UserProfileDetails string `json:"user_profile_details,omitempty"`
}

// MemoryStoreDefinition describes how a memory store processes and stores memory content.
type MemoryStoreDefinition struct {
	// Kind is the memory store kind. Currently only "default" is supported.
	Kind string `json:"kind"`
	// ChatModel is the chat model deployment name used by the memory store.
	ChatModel string `json:"chat_model"`
	// EmbeddingModel is the embedding model deployment name used by the memory store.
	EmbeddingModel string `json:"embedding_model"`
	// Options holds optional extraction and retention settings.
	Options *MemoryStoreOptions `json:"options,omitempty"`
}

// CreateMemoryStoreRequest is the request body for creating a memory store.
type CreateMemoryStoreRequest struct {
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Definition  MemoryStoreDefinition `json:"definition"`
}

// MemoryStoreObject is the response for a memory store.
type MemoryStoreObject struct {
	Id          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Definition reflects the live configuration of the memory store as stored by the
	// service. It lets callers detect when a declared definition has diverged from an
	// existing store.
	Definition MemoryStoreDefinition `json:"definition"`
}

// CreateMemoryStore creates a new memory store in the Foundry project.
func (c *FoundryMemoryStoreClient) CreateMemoryStore(
	ctx context.Context,
	request *CreateMemoryStoreRequest,
) (*MemoryStoreObject, error) {
	targetUrl := fmt.Sprintf(
		"%s/memory_stores?api-version=%s",
		c.endpoint, memoryStoreAPIVersion,
	)

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, targetUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := req.SetBody(
		streaming.NopCloser(bytes.NewReader(payload)),
		"application/json",
	); err != nil {
		return nil, fmt.Errorf("failed to set request body: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var result MemoryStoreObject
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// GetMemoryStore retrieves a memory store by name. The returned error is an
// *azcore.ResponseError with StatusCode http.StatusNotFound when the store does not exist.
func (c *FoundryMemoryStoreClient) GetMemoryStore(
	ctx context.Context,
	name string,
) (*MemoryStoreObject, error) {
	targetUrl := fmt.Sprintf(
		"%s/memory_stores/%s?api-version=%s",
		c.endpoint, url.PathEscape(name), memoryStoreAPIVersion,
	)

	req, err := runtime.NewRequest(ctx, http.MethodGet, targetUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var result MemoryStoreObject
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// EnsureMemoryStore creates the memory store named by request.Name if it does not already
// exist, and returns the store. When a store with the same name already exists it is left
// unchanged and (created=false) is returned. This makes deployment idempotent and safe to
// re-run, mirroring the reference provisioning behavior.
func (c *FoundryMemoryStoreClient) EnsureMemoryStore(
	ctx context.Context,
	request *CreateMemoryStoreRequest,
) (store *MemoryStoreObject, created bool, err error) {
	existing, getErr := c.GetMemoryStore(ctx, request.Name)
	if getErr == nil {
		return existing, false, nil
	}

	// Only fall back to create on 404; propagate other errors (auth, 5xx, network).
	if respErr, ok := errors.AsType[*azcore.ResponseError](getErr); !ok ||
		respErr.StatusCode != http.StatusNotFound {
		return nil, false, getErr
	}

	newStore, createErr := c.CreateMemoryStore(ctx, request)
	if createErr != nil {
		return nil, false, createErr
	}

	return newStore, true, nil
}
