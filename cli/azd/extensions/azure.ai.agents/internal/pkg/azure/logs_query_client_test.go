// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testComponentID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/" +
	"providers/microsoft.insights/components/appi"

func TestLogsQueryClient_QueryResource(t *testing.T) {
	start := time.Date(2026, 11, 16, 11, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	t.Run("posts a resource-scoped query and reads partial errors", func(t *testing.T) {
		client := &LogsQueryClient{
			endpoint: logsQueryEndpoint,
			pipeline: newTestPipeline(func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, http.MethodPost, req.Method)
				assert.Equal(t, "https://api.loganalytics.azure.com/v1"+testComponentID+"/query", req.URL.String())
				assert.Equal(t, "wait=120", req.Header.Get("Prefer"))

				var body map[string]string
				require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
				assert.Equal(t, "requests | take 1", body["query"])
				assert.Equal(t, "2026-11-16T11:00:00Z/2026-11-16T12:00:00Z", body["timespan"])

				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(`{
						"tables":[{"name":"PrimaryResult","columns":[{"name":"id","type":"string"}],"rows":[["a"]]}],
						"error":{"code":"PartialError","message":"partial"}}`)),
					Request: req,
				}, nil
			}),
		}

		result, err := client.QueryResource(t.Context(), testComponentID, "requests | take 1", start, end)
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": "a"}}, result.PrimaryTable().Records())
		require.NotNil(t, result.Error)
		assert.Equal(t, "PartialError", result.Error.Code)
	})

	t.Run("returns response errors", func(t *testing.T) {
		client := &LogsQueryClient{
			endpoint: logsQueryEndpoint,
			pipeline: newTestPipeline(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Header:     http.Header{},
					Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"InsufficientAccessError"}}`)),
					Request:    req,
				}, nil
			}),
		}

		_, err := client.QueryResource(t.Context(), testComponentID, "requests", start, end)
		respErr, ok := errors.AsType[*azcore.ResponseError](err)
		require.True(t, ok, "expected ResponseError, got %T", err)
		assert.Equal(t, http.StatusForbidden, respErr.StatusCode)
	})

	t.Run("rejects non Application Insights resource IDs", func(t *testing.T) {
		client := &LogsQueryClient{
			endpoint: logsQueryEndpoint,
			pipeline: newTestPipeline(func(*http.Request) (*http.Response, error) {
				t.Fatal("no request expected")
				return nil, nil
			}),
		}
		for _, id := range []string{
			"",
			"InstrumentationKey=abc",
			testComponentID + "/../../other",
			"/subscriptions/0/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/ws",
		} {
			_, err := client.QueryResource(t.Context(), id, "requests", start, end)
			require.Error(t, err, id)
		}
	})
}
