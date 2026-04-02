// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// MetricsListRequest is the request body for listing available metrics for a job.
type MetricsListRequest struct {
	MetricNamespace   *string `json:"metricNamespace"`
	ContinuationToken *string `json:"continuationToken"`
}

// MetricsListResponse is the response from listing available metrics.
type MetricsListResponse struct {
	Value             []MetricDefinition `json:"value"`
	ContinuationToken *string            `json:"continuationToken,omitempty"`
	NextLink          *string            `json:"nextLink,omitempty"`
}

// MetricDefinition describes a single metric series available for a job.
type MetricDefinition struct {
	MetricKey  map[string]interface{} `json:"metricKey,omitempty"`
	Columns    map[string]string      `json:"columns,omitempty"`
	Properties map[string]string      `json:"properties,omitempty"`
}

// MetricsFullRequest is the request body for retrieving full metric data.
type MetricsFullRequest struct {
	MetricName        string  `json:"metricName"`
	MetricNamespace   *string `json:"metricNamespace"`
	ContinuationToken *string `json:"continuationToken"`
	StartTime         *string `json:"startTime"`
	EndTime           *string `json:"endTime"`
}

// MetricsFullResponse is the response containing full metric data points.
type MetricsFullResponse struct {
	DataContainerID   string                 `json:"dataContainerId,omitempty"`
	Name              string                 `json:"name,omitempty"`
	Columns           map[string]string      `json:"columns,omitempty"`
	Properties        map[string]string      `json:"properties,omitempty"`
	Namespace         *string                `json:"namespace,omitempty"`
	Value             []MetricValue          `json:"value"`
	ContinuationToken *string                `json:"continuationToken,omitempty"`
	NextLink          *string                `json:"nextLink,omitempty"`
}

// MetricValue represents a single metric data point.
type MetricValue struct {
	MetricID   string                 `json:"metricId,omitempty"`
	CreatedUTC string                 `json:"createdUtc,omitempty"`
	Step       int                    `json:"step,omitempty"`
	Data       map[string]interface{} `json:"data,omitempty"`
}
