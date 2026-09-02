// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type capturedExperimentRequest struct {
	method  string
	path    string
	query   url.Values
	headers http.Header
	body    []byte
}

func TestExperimentCommandHTTPContracts(t *testing.T) {
	t.Setenv(experimentAPIKeyEnv, "test-api-key")
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")
	t.Setenv("AZURE_AI_PROJECT_ENDPOINT", "")

	testDir := t.TempDir()
	protobufFile := filepath.Join(testDir, "payload.pb")
	agentTracesFile := filepath.Join(testDir, "agent-traces.json")
	graphQLFile := filepath.Join(testDir, "graphql.json")
	fileStreamFile := filepath.Join(testDir, "file-stream.json")
	require.NoError(t, os.WriteFile(protobufFile, []byte{0x0a, 0x00}, 0o600))
	require.NoError(t, os.WriteFile(
		agentTracesFile,
		[]byte(`{"run_id":"payload-run","resourceSpans":[]}`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		graphQLFile,
		[]byte(`{"query":"query { viewer { id } }"}`),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		fileStreamFile,
		[]byte(`{"files":{"output.log":{"offset":0,"content":["hello"]}}}`),
		0o600,
	))

	tests := []struct {
		name         string
		args         []string
		method       string
		path         string
		query        url.Values
		headers      http.Header
		jsonBody     string
		protobufBody []byte
	}{
		{
			name:   "list runs",
			args:   []string{"ai", "loom", "run", "list", "--take", "5"},
			method: http.MethodGet,
			path:   "/api/projects/project/experiment_tracking/runs",
			query:  url.Values{"take": {"5"}},
		},
		{
			name:   "history keys",
			args:   []string{"ai", "loom", "run", "history-keys", "--run-id", "run-one"},
			method: http.MethodGet,
			path:   "/api/projects/project/experiment_tracking/runs/run-one/history/keys",
		},
		{
			name:   "summary",
			args:   []string{"ai", "loom", "run", "summary", "--run-id", "run-one", "--take", "5"},
			method: http.MethodGet,
			path:   "/api/projects/project/experiment_tracking/runs/run-one/summary",
			query:  url.Values{"take": {"5"}},
		},
		{
			name:    "metrics",
			args:    []string{"ai", "loom", "run", "metrics", "--run-id", "run-one", "--take", "5"},
			method:  http.MethodGet,
			path:    "/api/projects/project/experiment_tracking/runs/run-one/metrics",
			query:   url.Values{"take": {"5"}},
			headers: runContractHeaders("run-one"),
		},
		{
			name: "system metrics",
			args: []string{
				"ai", "loom", "run", "system-metrics",
				"--run-id", "run-one",
				"--name", "cpu",
				"--name", "memory",
				"--take", "5",
			},
			method: http.MethodGet,
			path:   "/api/projects/project/experiment_tracking/runs/run-one/system-metrics",
			query:  url.Values{"names": {"cpu", "memory"}, "take": {"5"}},
		},
		{
			name:   "logs",
			args:   []string{"ai", "loom", "run", "logs", "--run-id", "run-one", "--take", "5"},
			method: http.MethodGet,
			path:   "/api/projects/project/experiment_tracking/runs/run-one/logs",
			query:  url.Values{"take": {"5"}},
		},
		{
			name:   "log records",
			args:   []string{"ai", "loom", "run", "log-records", "--run-id", "run-one", "--take", "5"},
			method: http.MethodGet,
			path:   "/api/projects/project/experiment_tracking/runs/run-one/log-records",
			query:  url.Values{"take": {"5"}},
		},
		{
			name: "compare",
			args: []string{
				"ai", "loom", "run", "compare",
				"--run-id", "run-one",
				"--run-id", "run-two",
				"--metric", "loss",
				"--min", "1",
				"--max", "5",
			},
			method:   http.MethodPost,
			path:     "/api/projects/project/experiment_tracking/runs/compare",
			jsonBody: `{"runIds":["run-one","run-two"],"metricNames":["loss"],"min":1,"max":5}`,
		},
		{
			name:   "list traces",
			args:   []string{"ai", "loom", "run", "trace", "list", "--run-id", "run-one", "--take", "5"},
			method: http.MethodGet,
			path:   "/api/projects/project/experiment_tracking/runs/run-one/traces",
			query:  url.Values{"take": {"5"}},
		},
		{
			name: "show trace",
			args: []string{
				"ai", "loom", "run", "trace", "show",
				"--run-id", "run-one",
				"--trace-id", "trace-one",
			},
			method: http.MethodGet,
			path:   "/api/projects/project/experiment_tracking/runs/run-one/traces/trace-one",
		},
		{
			name: "trace chat",
			args: []string{
				"ai", "loom", "run", "trace", "chat",
				"--run-id", "run-one",
				"--trace-id", "trace-one",
			},
			method:   http.MethodPost,
			path:     "/api/projects/project/experiment_tracking/runs/run-one/agents/traces/chat",
			headers:  runContractHeaders("run-one"),
			jsonBody: `{"project_id":"project","trace_id":"trace-one"}`,
		},
		{
			name: "query spans",
			args: []string{
				"ai", "loom", "run", "span", "query",
				"--run-id", "run-one",
				"--filter", `{"$expr":true}`,
				"--include-details",
				"--limit", "7",
			},
			method:  http.MethodPost,
			path:    "/api/projects/project/experiment_tracking/runs/run-one/agents/spans/query",
			headers: runContractHeaders("run-one"),
			jsonBody: `{
				"project_id":"project",
				"query":{"$expr":true},
				"include_details":true,
				"limit":7
			}`,
		},
		{
			name: "ingest metrics",
			args: []string{
				"ai", "loom", "run", "ingest", "metrics",
				"--run-id", "run-one",
				"--file", protobufFile,
			},
			method:       http.MethodPost,
			path:         "/api/projects/project/experiment_tracking/protocols/otlp/v1/metrics",
			headers:      protobufContractHeaders("run-one"),
			protobufBody: []byte{0x0a, 0x00},
		},
		{
			name: "ingest logs",
			args: []string{
				"ai", "loom", "run", "ingest", "logs",
				"--run-id", "run-one",
				"--file", protobufFile,
			},
			method:       http.MethodPost,
			path:         "/api/projects/project/experiment_tracking/protocols/otlp/v1/logs",
			headers:      protobufContractHeaders("run-one"),
			protobufBody: []byte{0x0a, 0x00},
		},
		{
			name: "ingest traces",
			args: []string{
				"ai", "loom", "run", "ingest", "traces",
				"--run-id", "run-one",
				"--file", protobufFile,
			},
			method:       http.MethodPost,
			path:         "/api/projects/project/experiment_tracking/protocols/otlp/v1/traces",
			headers:      protobufContractHeaders("run-one"),
			protobufBody: []byte{0x0a, 0x00},
		},
		{
			name: "ingest agent traces",
			args: []string{
				"ai", "loom", "run", "ingest", "agent-traces",
				"--run-id", "run-one",
				"--file", agentTracesFile,
			},
			method:   http.MethodPost,
			path:     "/api/projects/project/experiment_tracking/agents/otel/v1/traces",
			jsonBody: `{"run_id":"run-one","resourceSpans":[]}`,
		},
		{
			name: "W&B GraphQL",
			args: []string{
				"ai", "loom", "run", "wandb", "graphql",
				"--file", graphQLFile,
			},
			method:   http.MethodPost,
			path:     "/api/projects/project/experiment_tracking/graphql",
			jsonBody: `{"query":"query { viewer { id } }"}`,
		},
		{
			name: "W&B file stream",
			args: []string{
				"ai", "loom", "run", "wandb", "file-stream",
				"--run-id", "run-one",
				"--entity", "entity-one",
				"--wandb-project", "wandb-project",
				"--file", fileStreamFile,
			},
			method:   http.MethodPost,
			path:     "/api/projects/project/experiment_tracking/files/entity-one/wandb-project/run-one/file_stream",
			jsonBody: `{"files":{"output.log":{"offset":0,"content":["hello"]}}}`,
		},
	}

	originalTransport := http.DefaultTransport
	t.Cleanup(func() {
		http.DefaultTransport = originalTransport
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var captured capturedExperimentRequest
			http.DefaultTransport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				var body []byte
				if request.Body != nil {
					var err error
					body, err = io.ReadAll(request.Body)
					require.NoError(t, err)
				}
				captured = capturedExperimentRequest{
					method:  request.Method,
					path:    request.URL.EscapedPath(),
					query:   request.URL.Query(),
					headers: request.Header.Clone(),
					body:    body,
				}

				responseBody := []byte(`{}`)
				if test.protobufBody != nil {
					responseBody = nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(bytes.NewReader(responseBody)),
					Request:    request,
				}, nil
			})

			command := NewRootCommand()
			command.SetArgs(append(
				test.args[2:],
				"--project-endpoint",
				"https://account.services.ai.azure.com/api/projects/project",
			))
			command.SetOut(&bytes.Buffer{})
			command.SetErr(&bytes.Buffer{})

			require.NoError(t, command.Execute())
			assert.Equal(t, test.method, captured.method)
			assert.Equal(t, test.path, captured.path)

			expectedQuery := make(url.Values, len(test.query)+1)
			for key, values := range test.query {
				expectedQuery[key] = slices.Clone(values)
			}
			expectedQuery.Set("api-version", "v1")
			assert.Equal(t, expectedQuery, captured.query)
			assert.Equal(t, "test-api-key", captured.headers.Get("api-key"))
			assert.Empty(t, captured.headers.Get("Authorization"))

			for key, expected := range test.headers {
				assert.Equal(t, expected, captured.headers.Values(key), "header %s", key)
			}
			if test.jsonBody != "" {
				assert.Equal(t, "application/json", captured.headers.Get("Accept"))
				assert.Equal(t, "application/json", captured.headers.Get("Content-Type"))
				assert.JSONEq(t, test.jsonBody, string(captured.body))
			} else if test.protobufBody != nil {
				assert.Equal(t, "application/x-protobuf", captured.headers.Get("Accept"))
				assert.Equal(t, "application/x-protobuf", captured.headers.Get("Content-Type"))
				assert.Equal(t, test.protobufBody, captured.body)
			} else {
				assert.Equal(t, "application/json", captured.headers.Get("Accept"))
				assert.Empty(t, captured.headers.Get("Content-Type"))
				assert.Empty(t, captured.body)
			}
		})
	}
}

func runContractHeaders(runID string) http.Header {
	return http.Header{
		"X-Wandb-Username":    {"account"},
		"X-Helios-Project-Id": {"project"},
		"X-Helios-Run-Id":     {runID},
	}
}

func protobufContractHeaders(runID string) http.Header {
	headers := runContractHeaders(runID)
	headers.Set("Accept", "application/x-protobuf")
	headers.Set("Content-Type", "application/x-protobuf")
	return headers
}
