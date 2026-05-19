// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package routines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

const (
	routinesAPIVersion    = "v1"
	routinesPreviewHeader = "Foundry-Features"
	routinesPreviewValue  = "Routines=V1Preview"
)

// Client is the data-plane client for Foundry Routines API operations.
type Client struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewClient creates a new Routines data-plane client.
func NewClient(endpoint string, cred azcore.TokenCredential) *Client {
	clientOptions := &policy.ClientOptions{
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(
				cred,
				[]string{"https://ai.azure.com/.default"},
				nil,
			),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy("azd-ext-azure-ai-routines/0.1.0"),
		},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-routines",
		"v0.1.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &Client{endpoint: strings.TrimRight(endpoint, "/"), pipeline: pipeline}
}

// routineURL returns the URL for a named routine.
func (c *Client) routineURL(name string) string {
	return fmt.Sprintf("%s/routines/%s?api-version=%s", c.endpoint, url.PathEscape(name), routinesAPIVersion)
}

// routinesURL returns the base routines collection URL with optional query parameters.
func (c *Client) routinesURL(extraQuery ...string) string {
	base := fmt.Sprintf("%s/routines?api-version=%s", c.endpoint, routinesAPIVersion)
	if len(extraQuery) > 0 {
		return base + "&" + strings.Join(extraQuery, "&")
	}
	return base
}

// routineActionURL returns the URL for a named routine action route
// (e.g. :dispatch, :dispatchAsync). The action segment is case-sensitive
// and must match the TypeSpec route exactly.
func (c *Client) routineActionURL(name, action string) string {
	return fmt.Sprintf("%s/routines/%s:%s?api-version=%s", c.endpoint, url.PathEscape(name), action, routinesAPIVersion)
}

// routineRunsURL returns the URL for listing routine runs.
func (c *Client) routineRunsURL(routineName string, extraQuery ...string) string {
	base := fmt.Sprintf("%s/routines/%s/runs?api-version=%s", c.endpoint, url.PathEscape(routineName), routinesAPIVersion)
	if len(extraQuery) > 0 {
		return base + "&" + strings.Join(extraQuery, "&")
	}
	return base
}

// addPreviewHeader adds the required Routines preview opt-in header to a request.
func addPreviewHeader(req *policy.Request) {
	req.Raw().Header.Set(routinesPreviewHeader, routinesPreviewValue)
}

// GetRoutine retrieves a routine by name.
func (c *Client) GetRoutine(ctx context.Context, name string) (*Routine, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, c.routineURL(name))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	addPreviewHeader(req)

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	var routine Routine
	if err := decodeJSON(resp.Body, &routine); err != nil {
		return nil, err
	}
	return &routine, nil
}

// ListRoutines retrieves all routines, draining all pages.
func (c *Client) ListRoutines(ctx context.Context) ([]Routine, error) {
	var all []Routine
	nextURL := c.routinesURL()

	for nextURL != "" {
		if err := c.validateSameOrigin(nextURL); err != nil {
			return nil, err
		}

		var page PagedRoutine
		if err := c.getPage(ctx, nextURL, &page); err != nil {
			return nil, err
		}

		all = append(all, page.Value...)
		// The service returns an absolute nextLink URL when more pages exist
		// (Azure.Core.Page<Routine>). We follow it verbatim after a same-origin
		// check rather than re-deriving the continuation query string.
		nextURL = page.NextLink
	}

	return all, nil
}

// getPage performs a paginated GET and decodes the body into out.
// It scopes resp.Body.Close() to a single iteration to avoid file-descriptor
// accumulation when callers loop across many pages.
func (c *Client) getPage(ctx context.Context, pageURL string, out any) error {
	req, err := runtime.NewRequest(ctx, http.MethodGet, pageURL)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	addPreviewHeader(req)

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return runtime.NewResponseError(resp)
	}

	return decodeJSON(resp.Body, out)
}

// PutRoutine creates or replaces a routine (upsert via PUT).
func (c *Client) PutRoutine(ctx context.Context, name string, body *Routine) (*Routine, error) {
	req, err := runtime.NewRequest(ctx, http.MethodPut, c.routineURL(name))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	addPreviewHeader(req)

	if err := setJSONBody(req, body); err != nil {
		return nil, err
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusCreated) {
		return nil, runtime.NewResponseError(resp)
	}

	var result Routine
	if err := decodeJSON(resp.Body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteRoutine deletes a routine by name.
func (c *Client) DeleteRoutine(ctx context.Context, name string) error {
	req, err := runtime.NewRequest(ctx, http.MethodDelete, c.routineURL(name))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	addPreviewHeader(req)

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusNoContent) {
		return runtime.NewResponseError(resp)
	}

	return nil
}

// EnableRoutine flips `enabled` to true via PUT.
// The Foundry Routines API does not expose a dedicated :enable route; the
// client mutates the routine resource directly.
func (c *Client) EnableRoutine(ctx context.Context, name string) (*Routine, error) {
	return c.setEnabled(ctx, name, true)
}

// DisableRoutine flips `enabled` to false via PUT.
func (c *Client) DisableRoutine(ctx context.Context, name string) (*Routine, error) {
	return c.setEnabled(ctx, name, false)
}

// setEnabled performs a GET + PUT to mutate the `enabled` field. It returns
// the current routine without an extra round-trip if the field is already
// at the desired value (idempotent enable/disable).
func (c *Client) setEnabled(ctx context.Context, name string, enabled bool) (*Routine, error) {
	existing, err := c.GetRoutine(ctx, name)
	if err != nil {
		return nil, err
	}
	if existing.Enabled != nil && *existing.Enabled == enabled {
		return existing, nil
	}
	existing.Enabled = &enabled
	return c.PutRoutine(ctx, name, existing)
}

// DispatchRoutineAsync calls the :dispatchAsync action route.
// The action segment is camelCase per TypeSpec; do not change it to
// snake_case without first updating the Foundry Routines spec.
func (c *Client) DispatchRoutineAsync(
	ctx context.Context,
	name string,
	payload *DispatchRoutineRequest,
) (*DispatchRoutineResponse, error) {
	req, err := runtime.NewRequest(ctx, http.MethodPost, c.routineActionURL(name, "dispatchAsync"))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	addPreviewHeader(req)

	if payload != nil {
		if err := setJSONBody(req, payload); err != nil {
			return nil, err
		}
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK, http.StatusAccepted) {
		return nil, runtime.NewResponseError(resp)
	}

	var result DispatchRoutineResponse
	if err := decodeJSON(resp.Body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListRoutineRunsOptions controls optional parameters for listing routine runs.
type ListRoutineRunsOptions struct {
	// Top caps the total number of items returned across all pages (0 = no cap).
	Top    int
	Filter string
}

// ListRoutineRuns retrieves runs for a routine, respecting Top and Filter options.
func (c *Client) ListRoutineRuns(
	ctx context.Context, routineName string, opts ListRoutineRunsOptions,
) ([]RoutineRun, error) {
	var all []RoutineRun

	// baseQuery holds the original filter, preserved across pages. maxResults is
	// only sent on the first page (we cap totals client-side via opts.Top).
	var baseQuery []string
	if opts.Filter != "" {
		baseQuery = append(baseQuery, "filter="+url.QueryEscape(opts.Filter))
	}

	firstPageQuery := slices.Clone(baseQuery)
	if opts.Top > 0 {
		firstPageQuery = append(firstPageQuery, fmt.Sprintf("maxResults=%d", opts.Top))
	}

	nextURL := c.routineRunsURL(routineName, firstPageQuery...)

	for nextURL != "" {
		if err := c.validateSameOrigin(nextURL); err != nil {
			return nil, err
		}

		var page PagedRoutineRun
		if err := c.getPage(ctx, nextURL, &page); err != nil {
			return nil, err
		}

		all = append(all, page.Value...)

		// Respect Top cap across pages.
		if opts.Top > 0 && len(all) >= opts.Top {
			all = all[:opts.Top]
			break
		}

		if page.NextPageToken != "" {
			pageQuery := append(slices.Clone(baseQuery),
				"pageToken="+url.QueryEscape(page.NextPageToken))
			nextURL = c.routineRunsURL(routineName, pageQuery...)
		} else {
			nextURL = ""
		}
	}

	return all, nil
}

// validateSameOrigin ensures a pagination URL has the same origin as the configured endpoint.
func (c *Client) validateSameOrigin(targetURL string) error {
	endpointURL, err := url.Parse(c.endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}

	linkURL, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("invalid pagination URL: %w", err)
	}

	if linkURL.Scheme == "" {
		return fmt.Errorf("pagination URL must have an explicit scheme, got %q", targetURL)
	}

	if !strings.EqualFold(linkURL.Scheme, endpointURL.Scheme) ||
		!strings.EqualFold(linkURL.Host, endpointURL.Host) {
		return fmt.Errorf(
			"pagination URL origin mismatch: expected %s://%s, got %s://%s",
			endpointURL.Scheme, endpointURL.Host, linkURL.Scheme, linkURL.Host,
		)
	}

	return nil
}

// decodeJSON reads and unmarshals a JSON response body.
func decodeJSON(body io.Reader, v any) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return nil
}

// setJSONBody marshals v as JSON and sets it as the request body.
func setJSONBody(req *policy.Request, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}
	req.Raw().Header.Set("Content-Type", "application/json")
	req.Raw().ContentLength = int64(len(data))
	req.Raw().Body = io.NopCloser(bytes.NewReader(data))
	req.Raw().GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return nil
}
