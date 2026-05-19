// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"azure.ai.training/pkg/models"
)

// ListMetrics lists all available metrics for a job.
// POST .../jobs/{id}/metrics/list
func (c *Client) ListMetrics(ctx context.Context, jobID string) (*models.MetricsListResponse, error) {
	reqBody := &models.MetricsListRequest{
		MetricNamespace:   nil,
		ContinuationToken: nil,
	}

	resp, err := c.doDataPlane(ctx, http.MethodPost, fmt.Sprintf("jobs/%s/metrics/list", url.PathEscape(jobID)), reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to list metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.MetricsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode metrics list response: %w", err)
	}

	return &result, nil
}

// GetMetricsFull retrieves full metric data for a specific metric name.
// POST .../jobs/{id}/metrics/full
func (c *Client) GetMetricsFull(
	ctx context.Context, jobID string, metricName string,
) (*models.MetricsFullResponse, error) {
	reqBody := &models.MetricsFullRequest{
		MetricName:        metricName,
		MetricNamespace:   nil,
		ContinuationToken: nil,
		StartTime:         nil,
		EndTime:           nil,
	}

	resp, err := c.doDataPlane(ctx, http.MethodPost, fmt.Sprintf("jobs/%s/metrics/full", url.PathEscape(jobID)), reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, c.HandleError(resp)
	}

	var result models.MetricsFullResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode metrics response: %w", err)
	}

	return &result, nil
}
