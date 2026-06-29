// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package routines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

const routinesAPIVersion = "v1"

// Client is the data-plane client for Foundry Routines API operations.
type Client struct {
	endpoint string
	pipeline runtime.Pipeline
}

// newHTTPClient returns the *http.Client used by the data-plane pipeline.
//
// The default azcore transport relies on Go's HTTP/2 client, which can wait
// minutes before surfacing a server-side stream reset (RST_STREAM). We set
// explicit response-header and connection-level timeouts so failures surface
// within tens of seconds.
func newHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	return &http.Client{Transport: transport}
}

// NewClient creates a new Routines data-plane client.
func NewClient(endpoint string, cred azcore.TokenCredential) *Client {
	clientOptions := &policy.ClientOptions{
		Transport: newHTTPClient(),
		Logging: policy.LogOptions{
			AllowedHeaders: []string{azsdk.MsCorrelationIdHeader},
		},
		Retry: policy.RetryOptions{
			MaxRetries: 1,
			TryTimeout: 30 * time.Second,
		},
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
// (e.g. :enable, :disable, :dispatch_async).
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


// GetRoutine retrieves a routine by name.
func (c *Client) GetRoutine(ctx context.Context, name string) (*Routine, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, c.routineURL(name))
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
		var page PagedRoutine
		if err := c.getPage(ctx, nextURL, &page); err != nil {
			return nil, err
		}

		all = append(all, page.Value...)
		if page.ContinuationToken == "" {
			break
		}
		nextURL = c.routinesURL("after=" + url.QueryEscape(page.ContinuationToken))
	}

	return all, nil
}

// getPage performs a paginated GET and decodes the body into out.
func (c *Client) getPage(ctx context.Context, pageURL string, out any) error {
	req, err := runtime.NewRequest(ctx, http.MethodGet, pageURL)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

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

// EnableRoutine enables a routine.
func (c *Client) EnableRoutine(ctx context.Context, name string) (*Routine, error) {
	return c.postRoutineAction(ctx, name, "enable")
}

// DisableRoutine disables a routine.
func (c *Client) DisableRoutine(ctx context.Context, name string) (*Routine, error) {
	return c.postRoutineAction(ctx, name, "disable")
}

// postRoutineAction calls a POST :<action> route on a routine and returns the
// updated resource.
func (c *Client) postRoutineAction(ctx context.Context, name, action string) (*Routine, error) {
	req, err := runtime.NewRequest(ctx, http.MethodPost, c.routineActionURL(name, action))
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

	var result Routine
	if err := decodeJSON(resp.Body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DispatchRoutineAsync calls the routine async-dispatch route.
func (c *Client) DispatchRoutineAsync(
	ctx context.Context,
	name string,
	payload *DispatchRoutineRequest,
) (*DispatchRoutineResponse, error) {
	req, err := runtime.NewRequest(ctx, http.MethodPost, c.routineActionURL(name, "dispatch_async"))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

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

	// baseQuery holds the original filter, preserved across pages. limit is
	// only sent on the first page (we cap totals client-side via opts.Top).
	var baseQuery []string
	if opts.Filter != "" {
		baseQuery = append(baseQuery, "filter="+url.QueryEscape(opts.Filter))
	}

	firstPageQuery := slices.Clone(baseQuery)
	if opts.Top > 0 {
		firstPageQuery = append(firstPageQuery, fmt.Sprintf("limit=%d", opts.Top))
	}

	nextURL := c.routineRunsURL(routineName, firstPageQuery...)

	for nextURL != "" {
		var page PagedRoutineRun
		if err := c.getPage(ctx, nextURL, &page); err != nil {
			return nil, err
		}

		all = append(all, page.Value...)

		if opts.Top > 0 && len(all) >= opts.Top {
			all = all[:opts.Top]
			break
		}

		if page.NextPageToken != "" {
			pageQuery := append(slices.Clone(baseQuery),
				"after="+url.QueryEscape(page.NextPageToken))
			nextURL = c.routineRunsURL(routineName, pageQuery...)
		} else {
			nextURL = ""
		}
	}

	return all, nil
}

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
