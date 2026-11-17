// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"

	"azureaiagent/internal/pkg/useragent"
)

const (
	logsQueryEndpoint = "https://api.loganalytics.azure.com/v1"
	logsQueryScope    = "https://api.loganalytics.io/.default"
)

// appInsightsResourceIDPattern matches an Application Insights component resource ID.
// Resource IDs come from service responses, so they are validated before being
// placed in a request URL.
var appInsightsResourceIDPattern = regexp.MustCompile(
	`(?i)^/subscriptions/[0-9a-f-]+/resourceGroups/[\w.()-]+/providers/microsoft\.insights/components/[\w.()-]+$`,
)

// IsAppInsightsResourceID reports whether id is an Application Insights component resource ID.
func IsAppInsightsResourceID(id string) bool {
	return appInsightsResourceIDPattern.MatchString(id)
}

// LogsQueryClient runs KQL queries against an Azure resource through the Azure
// Monitor Log Analytics query API.
type LogsQueryClient struct {
	endpoint string
	pipeline runtime.Pipeline
}

// NewLogsQueryClient creates a client for the Log Analytics query API.
func NewLogsQueryClient(cred azcore.TokenCredential) *LogsQueryClient {
	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{azsdk.MsCorrelationIdHeader},
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{logsQueryScope}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(useragent.Default()),
		},
	}

	return &LogsQueryClient{
		endpoint: logsQueryEndpoint,
		pipeline: runtime.NewPipeline("azure-ai-agents", "v1.0.0", runtime.PipelineOptions{}, clientOptions),
	}
}

// LogsQueryColumn describes one column of a query result table.
type LogsQueryColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// LogsQueryTable is one table of a query result.
type LogsQueryTable struct {
	Name    string            `json:"name"`
	Columns []LogsQueryColumn `json:"columns"`
	Rows    [][]any           `json:"rows"`
}

// Records returns each row keyed by column name.
func (t LogsQueryTable) Records() []map[string]any {
	records := make([]map[string]any, 0, len(t.Rows))
	for _, row := range t.Rows {
		record := make(map[string]any, len(t.Columns))
		for i, column := range t.Columns {
			if i < len(row) {
				record[column.Name] = row[i]
			}
		}
		records = append(records, record)
	}
	return records
}

// LogsQueryError is the error object of a query response. The service returns
// a 200 response with code PartialError when some data could not be read.
type LogsQueryError struct {
	Code    string           `json:"code"`
	Message string           `json:"message"`
	Details []LogsQueryError `json:"details,omitempty"`
}

// LogsQueryResult is the response of a Log Analytics query.
type LogsQueryResult struct {
	Tables []LogsQueryTable `json:"tables"`
	Error  *LogsQueryError  `json:"error,omitempty"`
}

// PrimaryTable returns the first result table, or an empty table.
func (r *LogsQueryResult) PrimaryTable() LogsQueryTable {
	if r == nil || len(r.Tables) == 0 {
		return LogsQueryTable{}
	}
	return r.Tables[0]
}

// QueryResource runs a KQL query scoped to resourceID over the [start, end] window.
// The resource must be an Application Insights component.
func (c *LogsQueryClient) QueryResource(
	ctx context.Context,
	resourceID string,
	query string,
	start time.Time,
	end time.Time,
) (*LogsQueryResult, error) {
	if !IsAppInsightsResourceID(resourceID) {
		return nil, fmt.Errorf("%q is not an Application Insights resource ID", resourceID)
	}

	payload, err := json.Marshal(map[string]string{
		"query":    query,
		"timespan": start.UTC().Format(time.RFC3339) + "/" + end.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode query: %w", err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPost, c.endpoint+resourceID+"/query")
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Raw().Header.Set("Prefer", "wait=120")
	if err := req.SetBody(streaming.NopCloser(bytes.NewReader(payload)), "application/json"); err != nil {
		return nil, fmt.Errorf("failed to set request body: %w", err)
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

	var result LogsQueryResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal query response: %w", err)
	}
	return &result, nil
}
