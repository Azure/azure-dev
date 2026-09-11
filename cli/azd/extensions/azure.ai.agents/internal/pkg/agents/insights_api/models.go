// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package insights_api contains the Agent Insights data-plane client and contracts.
package insights_api

import "encoding/json"

// APIVersion is the Agent Insights public data-plane API version.
const APIVersion = "v1"

// Monitor is the subset of an Agent Insights monitor needed to locate an agent's insights.
type Monitor struct {
	ID        string `json:"id"`
	AgentName string `json:"agent_name"`
}

// MonitorPage is a cursor-paginated monitor response.
type MonitorPage struct {
	Data    []Monitor `json:"data"`
	LastID  string    `json:"last_id"`
	HasMore bool      `json:"has_more"`
}

// InsightPage is a cursor-paginated insight response.
//
// Insights remain raw JSON so exports preserve fields introduced by newer service versions.
type InsightPage struct {
	Data    []json.RawMessage `json:"data"`
	LastID  string            `json:"last_id"`
	HasMore bool              `json:"has_more"`
}

// ListInsightOptions controls server-side filtering and cursor pagination.
type ListInsightOptions struct {
	Category       string
	Severity       string
	Status         string
	IncludeDetails bool
	Order          string
	After          string
	Limit          int
}
