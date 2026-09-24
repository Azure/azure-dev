// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/pkg/dataset_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func simulationRowClient(t *testing.T, rows string) *evalContext {
	t.Helper()
	var server *httptest.Server
	var mu sync.Mutex
	var requests []string
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/credentials"):
			assert.Equal(t, http.MethodPost, r.Method)
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"blobReferenceForConsumption": map[string]any{
					"credential": map[string]any{"sasUri": server.URL + "/rows.jsonl"},
				},
			}))
		case r.Method != http.MethodGet:
			t.Errorf("unexpected service mutation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		case strings.HasSuffix(r.URL.Path, "/versions"):
			_, _ = w.Write([]byte(`{"value":[{"name":"d","version":"1.0"}]}`))
		case strings.HasSuffix(r.URL.Path, "/versions/1.0"):
			_, _ = w.Write([]byte(`{"name":"d","version":"1.0","id":"service-issued-dataset-id"}`))
		case r.URL.Path == "/rows.jsonl":
			_, _ = w.Write([]byte(rows))
		default:
			t.Errorf("unexpected service read: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(func() {
		server.Close()
		mu.Lock()
		defer mu.Unlock()
		assert.Contains(t, requests, "GET /rows.jsonl", "the actual registered rows must be inspected")
	})
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalContext{
		datasetClient: dataset_api.NewDatasetClientFromPipeline(server.URL, pipeline),
	}
}

func TestSimulationRunRejectsMixedTurnSeedRows(t *testing.T) {
	for _, field := range []string{"query", "response"} {
		for _, value := range []any{"text", "", nil} {
			for _, later := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%v/later=%t", field, value, later), func(t *testing.T) {
					row, err := json.Marshal(map[string]any{seedDescriptionField: "A scenario.", field: value})
					require.NoError(t, err)
					rows := string(row)
					index := 1
					if later {
						rows = "{\"test_case_description\":\"A valid scenario.\"}\n" + rows
						index = 2
					}
					ec := simulationRowClient(t, rows)
					source, _, err := ec.buildRunDataSource(t.Context(), runnableSimulation(), "", 0)
					require.ErrorContains(t, err, fmt.Sprintf("row %d carries %q", index, field))
					assert.Nil(t, source, "invalid rows must not produce a run data source")
					assert.Empty(t, ec.state, "validation must not record private state")
				})
			}
		}
	}
}

func TestSimulationRunHonorsPerRowTurnOverride(t *testing.T) {
	ec := simulationRowClient(t, `{"test_case_description":"A longer scenario.",`+
		`"simulation_configuration":{"desired_num_turns":21,"max_num_turns":21}}`)
	group := runnableSimulation()
	group.Simulation.MaxTurns = 0
	source, _, err := ec.buildRunDataSource(t.Context(), group, "", 0)
	require.NoError(t, err)
	require.NotNil(t, source)
	raw, err := json.Marshal(source)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "service-issued-dataset-id")
	assert.NotContains(t, string(raw), "max_turns")
}
