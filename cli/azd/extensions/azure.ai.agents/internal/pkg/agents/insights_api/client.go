// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package insights_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"

	"azureaiagent/internal/pkg/useragent"
)

const (
	monitorPath           = "/agent_insight_monitors"
	insightsFeatureHeader = "AgentInsights=V1Preview"
)

type foundryFeaturesPolicy struct{}

func (foundryFeaturesPolicy) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Set("Foundry-Features", insightsFeatureHeader)
	return req.Next()
}

// Client accesses Agent Insights through a Microsoft Foundry project endpoint.
type Client struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewClient creates an authenticated Agent Insights client.
// A nil transport uses the Azure SDK's default HTTP transport.
func NewClient(endpoint string, credential azcore.TokenCredential, transport policy.Transporter) *Client {
	clientOptions := &policy.ClientOptions{
		Transport: transport,
		Logging: policy.LogOptions{
			AllowedHeaders: []string{"X-Ms-Correlation-Request-Id", "X-Request-Id"},
			IncludeBody:    false,
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(credential, []string{"https://ai.azure.com/.default"}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(useragent.Default()),
			foundryFeaturesPolicy{},
		},
	}

	return &Client{
		endpoint: endpoint,
		pipeline: runtime.NewPipeline(
			"azure-ai-agent-insights",
			"v1.0.0",
			runtime.PipelineOptions{},
			clientOptions,
		),
	}
}

// ListMonitors returns monitors matching an exact agent name.
func (c *Client) ListMonitors(
	ctx context.Context,
	agentName string,
	after string,
	limit int,
) (*MonitorPage, error) {
	query := url.Values{}
	query.Set("api-version", APIVersion)
	query.Set("agent_name", agentName)
	query.Set("order", "desc")
	if after != "" {
		query.Set("after", after)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}

	var page MonitorPage
	if err := c.get(ctx, monitorPath, query, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// ListInsights returns one page of insights for a monitor.
func (c *Client) ListInsights(
	ctx context.Context,
	monitorID string,
	options ListInsightOptions,
) (*InsightPage, error) {
	query := url.Values{}
	query.Set("api-version", APIVersion)
	query.Set("include_details", strconv.FormatBool(options.IncludeDetails))
	if options.Category != "" {
		query.Set("category", options.Category)
	}
	if options.Severity != "" {
		query.Set("severity", options.Severity)
	}
	if options.Status != "" {
		query.Set("status", options.Status)
	}
	if options.Order != "" {
		query.Set("order", options.Order)
	}
	if options.After != "" {
		query.Set("after", options.After)
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}

	path := monitorPath + "/" + url.PathEscape(monitorID) + "/insights"
	var page InsightPage
	if err := c.get(ctx, path, query, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, result any) error {
	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return fmt.Errorf("invalid project endpoint: %w", err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return fmt.Errorf("invalid project endpoint: scheme and host are required")
	}
	requestURL := endpoint.Scheme + "://" + endpoint.Host +
		strings.TrimRight(endpoint.EscapedPath(), "/") + path
	if encodedQuery := query.Encode(); encodedQuery != "" {
		requestURL += "?" + encodedQuery
	}

	req, err := runtime.NewRequest(ctx, http.MethodGet, requestURL)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if !runtime.HasStatusCode(resp, http.StatusOK) {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return runtime.NewResponseError(resp)
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}
	return nil
}
