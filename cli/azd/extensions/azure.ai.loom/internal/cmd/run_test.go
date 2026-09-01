// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestRootIncludesExperimentTrackingCommands(t *testing.T) {
	root := NewRootCommand()
	names := map[string]bool{}
	for _, command := range root.Commands() {
		names[command.Name()] = true
	}

	assert.True(t, names["run"])
	assert.False(t, names["ingest"])
	assert.False(t, names["wandb"])
}

func TestRunCommandIncludesAllReadAndAgentSurfaces(t *testing.T) {
	command := newExperimentRunCommand(nil)
	names := map[string]bool{}
	for _, child := range command.Commands() {
		names[child.Name()] = true
	}

	for _, name := range []string{
		"compare",
		"history-keys",
		"list",
		"log-records",
		"logs",
		"metrics",
		"span",
		"summary",
		"system-metrics",
		"trace",
		"ingest",
		"wandb",
	} {
		assert.True(t, names[name], "missing run command %q", name)
	}
}

func TestTraceCommandIncludesListShowAndChat(t *testing.T) {
	command := newRunTraceCommand(nil)
	names := map[string]bool{}
	for _, child := range command.Commands() {
		names[child.Name()] = true
	}

	for _, name := range []string{"list", "show", "chat"} {
		assert.True(t, names[name], "missing trace command %q", name)
	}
}

func TestReadFilterExpressionDefaultsToMatchAll(t *testing.T) {
	filter, err := readFilterExpression("", "")
	require.NoError(t, err)
	assert.JSONEq(t, `{"$expr":true}`, string(filter))
}

func TestReadFilterExpressionFromFile(t *testing.T) {
	filterPath := filepath.Join(t.TempDir(), "filter.json")
	require.NoError(t, os.WriteFile(
		filterPath,
		[]byte(`{
			"$expr": {
				"$eq": [
					{"$getField": "span_name"},
					{"$literal": "chat"}
				]
			}
		}`),
		0o600,
	))

	filter, err := readFilterExpression("", filterPath)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(filter, &parsed))
	require.Contains(t, parsed, "$expr")
}

func TestReadFilterExpressionRejectsConflictingInputs(t *testing.T) {
	_, err := readFilterExpression(`{"$expr":true}`, "filter.json")
	require.Error(t, err)
}

func TestBuildSpanQueryBody(t *testing.T) {
	body := buildSpanQueryBody(
		"my-project",
		json.RawMessage(`{
			"$expr": {
				"$eq": [
					{"$getField": "span_name"},
					{"$literal": "chat"}
				]
			}
		}`),
		true,
		10,
	)

	data, err := json.Marshal(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"project_id": "my-project",
		"query": {
			"$expr": {
				"$eq": [
					{"$getField": "span_name"},
					{"$literal": "chat"}
				]
			}
		},
		"include_details": true,
		"limit": 10
	}`, string(data))
}

func TestReadJSONObjectPreservesLargeInteger(t *testing.T) {
	requestPath := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(
		requestPath,
		[]byte(`{"id":9007199254740993}`),
		0o600,
	))

	body, err := readJSONObject(requestPath)
	require.NoError(t, err)

	id, ok := body["id"].(json.Number)
	require.True(t, ok)
	assert.Equal(t, "9007199254740993", id.String())

	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":9007199254740993}`, string(encoded))
}

func TestReadJSONObjectRejectsMultipleValues(t *testing.T) {
	requestPath := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(
		requestPath,
		[]byte(`{"first":true} {"second":true}`),
		0o600,
	))

	_, err := readJSONObject(requestPath)
	require.Error(t, err)
}

func TestReadNonEmptyExperimentInputRejectsEmptyPayload(t *testing.T) {
	payloadPath := filepath.Join(t.TempDir(), "payload.pb")
	require.NoError(t, os.WriteFile(payloadPath, nil, 0o600))

	_, err := readNonEmptyExperimentInput(payloadPath)
	require.Error(t, err)
}

func TestSetAgentTracesRunIDOverridesPayload(t *testing.T) {
	body := map[string]any{"run_id": "payload-run"}

	setAgentTracesRunID(body, "flag-run")

	assert.Equal(t, "flag-run", body["run_id"])
}

func TestFormatOTLPIngestResponseIncludesPartialSuccess(t *testing.T) {
	tests := []struct {
		name     string
		signal   string
		response proto.Message
		expected string
	}{
		{
			name:   "metrics",
			signal: "metrics",
			response: &collectormetrics.ExportMetricsServiceResponse{
				PartialSuccess: &collectormetrics.ExportMetricsPartialSuccess{
					RejectedDataPoints: 2,
					ErrorMessage:       "two points rejected",
				},
			},
			expected: `{
				"status": "partial_success",
				"partial_success": {
					"rejected_data_points": 2,
					"error_message": "two points rejected"
				}
			}`,
		},
		{
			name:   "logs",
			signal: "logs",
			response: &collectorlogs.ExportLogsServiceResponse{
				PartialSuccess: &collectorlogs.ExportLogsPartialSuccess{
					RejectedLogRecords: 3,
					ErrorMessage:       "three records rejected",
				},
			},
			expected: `{
				"status": "partial_success",
				"partial_success": {
					"rejected_log_records": 3,
					"error_message": "three records rejected"
				}
			}`,
		},
		{
			name:   "traces",
			signal: "traces",
			response: &collectortrace.ExportTraceServiceResponse{
				PartialSuccess: &collectortrace.ExportTracePartialSuccess{
					RejectedSpans: 4,
					ErrorMessage:  "four spans rejected",
				},
			},
			expected: `{
				"status": "partial_success",
				"partial_success": {
					"rejected_spans": 4,
					"error_message": "four spans rejected"
				}
			}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := proto.Marshal(test.response)
			require.NoError(t, err)

			formatted, err := formatOTLPIngestResponse(test.signal, response)
			require.NoError(t, err)
			assert.JSONEq(t, test.expected, string(formatted))
		})
	}
}

func TestFormatOTLPIngestResponseReportsAccepted(t *testing.T) {
	formatted, err := formatOTLPIngestResponse("metrics", json.RawMessage(`{}`))

	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"accepted"}`, string(formatted))

	response, err := proto.Marshal(&collectormetrics.ExportMetricsServiceResponse{
		PartialSuccess: &collectormetrics.ExportMetricsPartialSuccess{},
	})
	require.NoError(t, err)

	formatted, err = formatOTLPIngestResponse("metrics", response)
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"accepted"}`, string(formatted))
}

func TestExperimentCommandsUseJSONOutput(t *testing.T) {
	commands := []*cobra.Command{
		newRunListCommand(nil),
		newRunSpansQueryCommand(nil),
		newOTLPIngestCommand(nil, "metrics"),
		newWandBGraphQLCommand(nil),
	}
	for _, command := range commands {
		assertOutputFlagOptions(t, command, "json", []string{"json"})
	}
}

func assertOutputFlagOptions(t *testing.T, cmd *cobra.Command, wantDefault string, wantAllowed []string) {
	t.Helper()
	require.NotNil(t, cmd.Annotations)
	assert.Equal(t, wantDefault, cmd.Annotations["azdext.default/output"])

	var allowed []string
	require.NoError(t, json.Unmarshal(
		[]byte(cmd.Annotations["azdext.allowed-values/output"]),
		&allowed,
	))
	assert.Equal(t, wantAllowed, allowed)
}
