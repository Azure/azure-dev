// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"

	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"

	"azure.ai.toolboxes/internal/version"
)

const (
	toolboxesApiVersion    = "v1"
	toolboxesFeatureHeader = "Toolboxes=V1Preview"
)

// FoundryToolboxClient provides methods for interacting with the Foundry Toolboxes API.
type FoundryToolboxClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewFoundryToolboxClient creates a new FoundryToolboxClient.
func NewFoundryToolboxClient(
	endpoint string,
	cred azcore.TokenCredential,
) *FoundryToolboxClient {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-toolboxes/%s", version.Version)

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
		"azure-ai-toolboxes",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &FoundryToolboxClient{
		endpoint: strings.TrimRight(endpoint, "/"),
		pipeline: pipeline,
	}
}

// Endpoint returns the toolbox endpoint root used by this client (without trailing slash).
// Used by the CLI to compute the runtime MCP consumption URL surfaced by `toolbox show`.
func (c *FoundryToolboxClient) Endpoint() string {
	return c.endpoint
}

// doJSON sends `method url` with an optional JSON body and decodes the response
// body into `out` (pass nil to discard). `okCodes` selects which HTTP status
// codes count as success; defaults to {200} when empty. The Foundry-Features
// header is set on every request.
func (c *FoundryToolboxClient) doJSON(
	ctx context.Context, method, target string, body any, out any, okCodes ...int,
) error {
	if len(okCodes) == 0 {
		okCodes = []int{http.StatusOK}
	}

	req, err := runtime.NewRequest(ctx, method, target)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", toolboxesFeatureHeader)

	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		if err := req.SetBody(
			streaming.NopCloser(bytes.NewReader(payload)),
			"application/json",
		); err != nil {
			return fmt.Errorf("failed to set request body: %w", err)
		}
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, okCodes...) {
		return runtime.NewResponseError(resp)
	}

	if out == nil {
		return nil
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	return nil
}

// listPagedFromClient walks `cursor`-style pagination on a toolbox endpoint.
// Subsequent pages use the last item's id as the `after` cursor while
// `has_more=true`. Capped at maxPaginationPages to guard against a server that
// keeps reporting has_more.
func listPagedFromClient[T any](
	ctx context.Context, c *FoundryToolboxClient, initialURL string,
	pickLastID func(t T) string,
) ([]T, error) {
	type page struct {
		Data    []T    `json:"data"`
		HasMore bool   `json:"has_more,omitempty"`
		LastID  string `json:"last_id,omitempty"`
	}

	out := []T{}
	target := initialURL
	for range maxPaginationPages {
		var p page
		if err := c.doJSON(ctx, http.MethodGet, target, nil, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Data...)
		if !p.HasMore || len(p.Data) == 0 {
			return out, nil
		}
		last := p.LastID
		if last == "" && pickLastID != nil {
			last = pickLastID(p.Data[len(p.Data)-1])
		}
		if last == "" {
			// HasMore=true with no cursor: log a warning and return the partial
			// results rather than spin. Callers may receive incomplete data.
			log.Printf(
				"foundry_toolsets_client: pagination has_more=true but no cursor for %s; returning %d items",
				initialURL, len(out),
			)
			return out, nil
		}
		sep := "&"
		if !strings.Contains(target, "?") {
			sep = "?"
		}
		target = initialURL + sep + "after=" + url.QueryEscape(last)
	}
	return out, fmt.Errorf(
		"pagination cap reached: more than %d pages returned for %s",
		maxPaginationPages, initialURL,
	)
}

const maxPaginationPages = 1000

// CreateToolboxVersionRequest is the request body for creating a new toolbox version.
// The toolbox name is provided in the URL path, not in the body.
type CreateToolboxVersionRequest struct {
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Tools       []map[string]any  `json:"tools"`
	Policies    map[string]any    `json:"policies,omitempty"`
}

// ToolboxObject is the lightweight response for a toolbox (no tools list).
type ToolboxObject struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	DefaultVersion string `json:"default_version"`
}

// ToolboxVersionObject is the response for a specific toolbox version.
type ToolboxVersionObject struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description,omitempty"`
	CreatedAt   int64             `json:"created_at"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Tools       []map[string]any  `json:"tools"`
}

// toolboxURL builds the canonical toolboxes URL with the api-version query.
// Path segments are escaped; callers must not pre-escape.
func (c *FoundryToolboxClient) toolboxURL(parts ...string) string {
	escaped := make([]string, len(parts))
	for i, p := range parts {
		escaped[i] = url.PathEscape(p)
	}
	tail := strings.Join(escaped, "/")
	if tail != "" {
		tail = "/" + tail
	}
	return fmt.Sprintf("%s/toolboxes%s?api-version=%s", c.endpoint, tail, toolboxesApiVersion)
}

// CreateToolboxVersion creates a new version of a toolbox.
// If the toolbox does not exist, it will be created automatically.
func (c *FoundryToolboxClient) CreateToolboxVersion(
	ctx context.Context, toolboxName string, request *CreateToolboxVersionRequest,
) (*ToolboxVersionObject, error) {
	target := c.toolboxURL(toolboxName, "versions")
	var result ToolboxVersionObject
	if err := c.doJSON(
		ctx, http.MethodPost, target, request, &result,
		http.StatusOK, http.StatusCreated,
	); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetToolbox retrieves a toolbox by name.
func (c *FoundryToolboxClient) GetToolbox(
	ctx context.Context, toolboxName string,
) (*ToolboxObject, error) {
	var result ToolboxObject
	if err := c.doJSON(
		ctx, http.MethodGet, c.toolboxURL(toolboxName), nil, &result,
	); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteToolbox deletes a toolbox and all its versions.
func (c *FoundryToolboxClient) DeleteToolbox(ctx context.Context, toolboxName string) error {
	return c.doJSON(
		ctx, http.MethodDelete, c.toolboxURL(toolboxName), nil, nil,
		http.StatusOK, http.StatusNoContent,
	)
}

// ListToolboxes returns every toolbox visible on the project endpoint by walking pagination.
func (c *FoundryToolboxClient) ListToolboxes(ctx context.Context) ([]ToolboxObject, error) {
	return listPagedFromClient(
		ctx, c, c.toolboxURL(),
		func(t ToolboxObject) string { return t.ID },
	)
}

// GetToolboxVersion fetches the full version body, including tools[].
func (c *FoundryToolboxClient) GetToolboxVersion(
	ctx context.Context, toolboxName, version string,
) (*ToolboxVersionObject, error) {
	var result ToolboxVersionObject
	if err := c.doJSON(
		ctx, http.MethodGet, c.toolboxURL(toolboxName, "versions", version),
		nil, &result,
	); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListToolboxVersions returns all version summaries for the named toolbox.
func (c *FoundryToolboxClient) ListToolboxVersions(
	ctx context.Context, toolboxName string,
) ([]ToolboxVersionObject, error) {
	return listPagedFromClient(
		ctx, c, c.toolboxURL(toolboxName, "versions"),
		func(v ToolboxVersionObject) string { return v.ID },
	)
}

// DeleteToolboxVersion deletes a single version. Service returns 400 with
// `bad_request` if the version is the current `default_version` and other
// versions exist; the CLI guards this pre-flight.
func (c *FoundryToolboxClient) DeleteToolboxVersion(
	ctx context.Context, toolboxName, version string,
) error {
	return c.doJSON(
		ctx, http.MethodDelete, c.toolboxURL(toolboxName, "versions", version), nil, nil,
		http.StatusOK, http.StatusNoContent,
	)
}

// SetDefaultVersion PATCHes the toolbox to mark a different version as default.
func (c *FoundryToolboxClient) SetDefaultVersion(
	ctx context.Context, toolboxName, version string,
) (*ToolboxObject, error) {
	var result ToolboxObject
	if err := c.doJSON(
		ctx, http.MethodPatch, c.toolboxURL(toolboxName),
		map[string]string{"default_version": version}, &result,
	); err != nil {
		return nil, err
	}
	return &result, nil
}
