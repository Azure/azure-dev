// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package experimenttracking provides an authenticated client for Foundry
// experiment-tracking APIs.
package experimenttracking

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
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

const (
	defaultAPIVersion = "v1"
	foundryScope      = "https://ai.azure.com/.default"
	maxResponseBytes  = 64 << 20
	defaultTimeout    = 30 * time.Second
	ingestionTimeout  = 5 * time.Minute
)

// ErrTokenAcquisition identifies failures acquiring a Foundry access token.
var ErrTokenAcquisition = errors.New("acquire Foundry access token")

// Client calls the experiment-tracking APIs for a Foundry project.
type Client struct {
	projectEndpoint string
	projectID       string
	accountID       string
	apiVersion      string
	credential      azcore.TokenCredential
	apiKey          string
	httpClient      *http.Client
}

// NewClient creates an experiment-tracking client.
func NewClient(
	projectEndpoint string,
	projectIDOverride string,
	apiVersion string,
	credential azcore.TokenCredential,
) (*Client, error) {
	return newClient(
		projectEndpoint,
		projectIDOverride,
		apiVersion,
		credential,
		"",
		newHTTPClient(),
	)
}

// NewClientWithAPIKey creates an experiment-tracking client that authenticates
// with a Foundry project API key.
func NewClientWithAPIKey(
	projectEndpoint string,
	projectIDOverride string,
	apiVersion string,
	apiKey string,
) (*Client, error) {
	return newClient(
		projectEndpoint,
		projectIDOverride,
		apiVersion,
		nil,
		apiKey,
		newHTTPClient(),
	)
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func newClient(
	projectEndpoint string,
	projectIDOverride string,
	apiVersion string,
	credential azcore.TokenCredential,
	apiKey string,
	httpClient *http.Client,
) (*Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	if credential == nil && apiKey == "" {
		return nil, fmt.Errorf("credential or API key must be provided")
	}

	parsed, err := url.Parse(projectEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse project endpoint: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("project endpoint must use http or https")
	}

	projectID, err := projectIDFromEndpoint(parsed)
	if err != nil && strings.TrimSpace(projectIDOverride) == "" {
		return nil, err
	}
	if override := strings.TrimSpace(projectIDOverride); override != "" {
		projectID = override
	}
	if strings.ContainsAny(projectID, `/\`) {
		return nil, fmt.Errorf("project ID must not contain path separators")
	}

	accountID := strings.Split(parsed.Hostname(), ".")[0]
	if accountID == "" {
		return nil, fmt.Errorf("derive account ID from project endpoint")
	}

	if apiVersion == "" {
		apiVersion = defaultAPIVersion
	}

	return &Client{
		projectEndpoint: strings.TrimRight(projectEndpoint, "/"),
		projectID:       projectID,
		accountID:       accountID,
		apiVersion:      apiVersion,
		credential:      credential,
		apiKey:          apiKey,
		httpClient:      httpClient,
	}, nil
}

func projectIDFromEndpoint(endpoint *url.URL) (string, error) {
	const prefix = "/api/projects/"

	escapedPath := strings.TrimRight(endpoint.EscapedPath(), "/")
	if !strings.HasPrefix(escapedPath, prefix) {
		return "", fmt.Errorf("project endpoint path must match %s<project-id>", prefix)
	}

	escapedID := strings.TrimPrefix(escapedPath, prefix)
	if escapedID == "" || strings.Contains(escapedID, "/") {
		return "", fmt.Errorf("project endpoint must contain exactly one project ID path segment")
	}

	projectID, err := url.PathUnescape(escapedID)
	if err != nil {
		return "", fmt.Errorf("decode project ID: %w", err)
	}
	return projectID, nil
}

// ProjectID returns the project ID derived from the endpoint or explicit override.
func (c *Client) ProjectID() string {
	return c.projectID
}

// AccountID returns the account ID derived from the endpoint host.
func (c *Client) AccountID() string {
	return c.accountID
}

// RunHeaders returns compatibility headers required by selected run APIs.
func (c *Client) RunHeaders(runID string) http.Header {
	headers := make(http.Header)
	headers.Set("X-WANDB-USERNAME", c.accountID)
	headers.Set("X-Helios-Project-Id", c.projectID)
	headers.Set("x-helios-run-id", runID)
	return headers
}

// DoJSON sends an authenticated JSON request and returns its response body.
func (c *Client) DoJSON(
	ctx context.Context,
	method string,
	apiPath string,
	query url.Values,
	headers http.Header,
	body any,
) (json.RawMessage, error) {
	return c.doJSON(ctx, method, apiPath, query, headers, body, c.httpClient)
}

// DoJSONIngestion sends an authenticated JSON ingestion request with the longer upload timeout.
func (c *Client) DoJSONIngestion(
	ctx context.Context,
	method string,
	apiPath string,
	query url.Values,
	headers http.Header,
	body any,
) (json.RawMessage, error) {
	return c.doJSON(ctx, method, apiPath, query, headers, body, c.httpClientWithTimeout(ingestionTimeout))
}

func (c *Client) doJSON(
	ctx context.Context,
	method string,
	apiPath string,
	query url.Values,
	headers http.Header,
	body any,
	httpClient *http.Client,
) (json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("Accept", "application/json")
	if body != nil {
		headers.Set("Content-Type", "application/json")
	}

	return c.doWithHTTPClient(ctx, method, apiPath, query, headers, reader, httpClient)
}

// DoBytes sends an authenticated request with an arbitrary content type.
func (c *Client) DoBytes(
	ctx context.Context,
	method string,
	apiPath string,
	query url.Values,
	headers http.Header,
	contentType string,
	body []byte,
) (json.RawMessage, error) {
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("Accept", contentType)
	headers.Set("Content-Type", contentType)
	return c.doWithHTTPClient(
		ctx,
		method,
		apiPath,
		query,
		headers,
		bytes.NewReader(body),
		c.httpClientWithTimeout(ingestionTimeout),
	)
}

func (c *Client) httpClientWithTimeout(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport:     c.httpClient.Transport,
		CheckRedirect: c.httpClient.CheckRedirect,
		Jar:           c.httpClient.Jar,
		Timeout:       timeout,
	}
}

func (c *Client) doWithHTTPClient(
	ctx context.Context,
	method string,
	apiPath string,
	query url.Values,
	headers http.Header,
	body io.Reader,
	httpClient *http.Client,
) (json.RawMessage, error) {
	requestURL, err := c.requestURL(apiPath, query)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header = headers.Clone()
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	} else {
		token, tokenErr := c.credential.GetToken(ctx, policy.TokenRequestOptions{
			Scopes: []string{foundryScope},
		})
		if tokenErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrTokenAcquisition, tokenErr)
		}
		req.Header.Set("Authorization", "Bearer "+token.Token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, newLimitedResponseError(resp, maxResponseBytes)
	}

	data, truncated, err := readLimitedBody(resp.Body, maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if truncated {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxResponseBytes)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return json.RawMessage(`{}`), nil
	}

	return json.RawMessage(data), nil
}

func newLimitedResponseError(resp *http.Response, maxBytes int64) error {
	data, truncated, err := readLimitedBody(resp.Body, maxBytes)
	if err != nil {
		return fmt.Errorf("read error response body: %w", err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	responseErr := runtime.NewResponseError(resp)
	if truncated {
		return fmt.Errorf("error response body exceeds %d bytes: %w", maxBytes, responseErr)
	}
	return responseErr
}

func readLimitedBody(reader io.Reader, maxBytes int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > maxBytes {
		return data[:maxBytes], true, nil
	}
	return data, false, nil
}

func (c *Client) requestURL(apiPath string, query url.Values) (string, error) {
	base, err := url.Parse(c.projectEndpoint)
	if err != nil {
		return "", fmt.Errorf("parse project endpoint: %w", err)
	}

	escapedAPIPath := strings.TrimPrefix(apiPath, "/")
	segments := strings.Split(escapedAPIPath, "/")
	for i, segment := range segments {
		switch segment {
		case ".":
			segments[i] = "%2E"
		case "..":
			segments[i] = "%2E%2E"
		}
	}
	rawPath := strings.TrimRight(base.EscapedPath(), "/") +
		"/experiment_tracking/" + strings.Join(segments, "/")
	decodedPath, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", fmt.Errorf("decode request path: %w", err)
	}
	base.Path = decodedPath
	base.RawPath = rawPath
	values := base.Query()
	for key, entries := range query {
		for _, value := range entries {
			values.Add(key, value)
		}
	}
	values.Set("api-version", c.apiVersion)
	base.RawQuery = values.Encode()
	return base.String(), nil
}
