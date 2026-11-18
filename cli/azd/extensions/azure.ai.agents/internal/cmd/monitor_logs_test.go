// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var logQueryColumns = []string{
	"timestamp", "record_type", "severity_level", "message", "exception_type", "agent_version", "session_id",
	"trace_id", "span_id", "logger", "role_instance", "item_count", "attributes",
}

// Rows follow the shape of real hosted-agent records, newest first as the query returns them.
func testLogRows() [][]any {
	return [][]any{
		{"2026-11-16T11:50:00Z", "exception", 3.0, "Exception occurred while pinging live metrics.",
			"ServiceRequestError", "1", "session-a", "00000000000000000000000000000000", "0000000000000000",
			"azure.monitor.opentelemetry.exporter._quickpulse._exporter", "instance-1", 1.0,
			map[string]any{"gen_ai.agent.name": "support", "microsoft.session.id": "session-a"}},
		{"2026-11-16T11:45:00Z", "log", 2.0, "Retrying due to server request error",
			"", "1", "session-a", "7ce8546a1be04f0d94c30247b5e0f596", "53bfe56de8e09581",
			"azure.monitor.opentelemetry.exporter.export._base", "instance-1", 2.0,
			map[string]any{"gen_ai.agent.name": "support", "authorization": "Bearer secret"}},
		{"2026-11-16T11:40:00Z", "log", 1.0, "AgentServerHost started",
			"", "1", "session-b", "", "", "azure.ai.agentserver", "instance-1", 1.0,
			map[string]any{"gen_ai.agent.name": "support"}},
	}
}

func testLogOptions(output string) *logQueryOptions {
	return &logQueryOptions{since: time.Hour, sinceLabel: "1h", limit: 10, output: output}
}

func TestBuildLogQuery(t *testing.T) {
	start := testTraceNow.Add(-time.Hour)
	query := buildLogQuery("sup'port", testFoundryProjectID, &logQueryOptions{limit: 20}, start, testTraceNow)
	assert.Contains(t, query, `let agentName = 'sup\'port';`)
	assert.Contains(t, query, "(traces | extend record_type = 'log'")
	assert.Contains(t, query, "(exceptions | extend record_type = 'exception'")
	assert.Contains(t, query, "datetime(2026-11-16T11:00:00Z) .. datetime(2026-11-16T12:00:00Z)")
	// Records are isolated to the selected agent; records without agent identity never match.
	assert.Contains(t, query, "| where iff(isnotempty(agent_name_attr), agent_name_attr =~ agentName,")
	assert.Contains(t, query, "let projectId = '"+testFoundryProjectID+"';")
	assert.Contains(t, query, "isnotempty(foundry_project) or isnotempty(gen_ai_project)")
	assert.Contains(t, query, "isempty(foundry_project) or foundry_project =~ projectId")
	assert.Contains(t, query, "isempty(gen_ai_project) or gen_ai_project =~ projectId")
	assert.Contains(t, query, "| order by timestamp desc\n| take 21")
	assert.NotContains(t, query, "session_id ==")
	assert.NotContains(t, query, "severity_level >=")
	assert.NotContains(t, query, " contains ")

	filtered := buildLogQuery("support", testFoundryProjectID, &logQueryOptions{
		limit: 5, sessionID: "session-a", minSeverity: 2, search: `it's "slow"`,
	}, start, testTraceNow)
	assert.Contains(t, filtered, "| where session_id == 'session-a'")
	assert.Contains(t, filtered, "| where severity_level >= 2")
	assert.Contains(t, filtered, `| where message contains 'it\'s "slow"' or exception_type contains 'it\'s "slow"'`)
}

func TestRunMonitorLogQuery_JSON(t *testing.T) {
	logs := &fakeTraceQuerier{result: queryResult(logQueryColumns, testLogRows()...)}
	deps, agents, _ := newFakeTraceDeps(logs)
	var out, errOut bytes.Buffer
	opts := testLogOptions("json")
	opts.limit = 2
	opts.level = "warning"
	opts.minSeverity = 2

	require.NoError(t, runMonitorLogQuery(t.Context(), testTraceTarget(), opts, deps, &out, &errOut))
	assert.Equal(t, "support", agents.gotName)
	assert.Equal(t, testAppInsightsID, logs.gotResource)
	assert.Contains(t, logs.gotQuery, "| take 3")

	var result logQueryResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	assert.True(t, result.Truncated)
	assert.Equal(t, "warning", result.Filters.Level)
	assert.Equal(t, "console", result.Filters.Type)
	assert.Equal(t, []string{"traces", "exceptions"}, result.Coverage.Tables)
	assert.True(t, result.Coverage.Sampled)
	require.Len(t, result.Data, 2)

	// The newest records are kept and shown oldest first.
	assert.Equal(t, "2026-11-16T11:45:00Z", result.Data[0].Timestamp)
	assert.Equal(t, "warning", result.Data[0].Severity)
	assert.Equal(t, "7ce8546a1be04f0d94c30247b5e0f596", result.Data[0].TraceID)
	assert.Equal(t, "53bfe56de8e09581", result.Data[0].SpanID)
	assert.Equal(t, redactedValue, result.Data[0].Attributes["authorization"])
	assert.Equal(t, "exception", result.Data[1].RecordType)
	assert.Equal(t, "error", result.Data[1].Severity)
	assert.Equal(t, "ServiceRequestError", result.Data[1].ExceptionType)
	assert.Equal(t, "session-a", result.Data[1].SessionID)
	assert.Equal(t, "1", result.Data[1].AgentVersion)
	assert.Empty(t, result.Data[1].TraceID, "all-zero IDs mean no trace context")

	assert.Contains(t, errOut.String(), "WARNING: more log records matched; showing the newest 2")
	assert.NotContains(t, errOut.String(), "sampled", "sampling is reported in JSON only")
	assert.NotContains(t, out.String(), "Querying logs")
}

func TestRunMonitorLogQuery_Table(t *testing.T) {
	deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(logQueryColumns, testLogRows()...)})
	var out, errOut bytes.Buffer
	opts := testLogOptions("table")
	opts.utc = true

	require.NoError(t, runMonitorLogQuery(t.Context(), testTraceTarget(), opts, deps, &out, &errOut))
	text := out.String()
	assert.Contains(t, text, "TIME")
	assert.Contains(t, text, "ServiceRequestError: Exception occurred while pinging live metrics.")
	assert.Less(t, bytes.Index(out.Bytes(), []byte("AgentServerHost started")),
		bytes.Index(out.Bytes(), []byte("ServiceRequestError")), "records print oldest first")
	assert.NotContains(t, errOut.String(), "WARNING")
}

func TestLogTableEscapesTerminalControls(t *testing.T) {
	assert.Equal(t, `hello\x1b[2J \u202e`, singleLine("hello\x1b[2J\n\u202e"))
}

func TestRunMonitorLogQuery_Empty(t *testing.T) {
	t.Run("json keeps stdout parseable", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(logQueryColumns)})
		var out, errOut bytes.Buffer
		opts := testLogOptions("json")
		opts.search = "timeout"
		opts.sessionID = "session-a"

		require.NoError(t, runMonitorLogQuery(t.Context(), testTraceTarget(), opts, deps, &out, &errOut))
		var result map[string]any
		require.NoError(t, json.Unmarshal(out.Bytes(), &result))
		assert.Equal(t, []any{}, result["data"])
		assert.Contains(t, errOut.String(),
			`No log records found for agent "support" in the last 1h containing "timeout", in session "session-a".`)
	})

	t.Run("table", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(logQueryColumns)})
		var out, errOut bytes.Buffer

		require.NoError(t, runMonitorLogQuery(t.Context(), testTraceTarget(), testLogOptions("table"), deps, &out, &errOut))
		assert.Contains(t, out.String(), `No log records found for agent "support" in the last 1h.`)
	})

	t.Run("partial error", func(t *testing.T) {
		result := queryResult(logQueryColumns)
		result.Error = &azure.LogsQueryError{Code: "PartialError", Message: "some data was not read"}
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: result})
		var out, errOut bytes.Buffer

		require.NoError(t, runMonitorLogQuery(t.Context(), testTraceTarget(), testLogOptions("json"), deps, &out, &errOut))
		var listed logQueryResult
		require.NoError(t, json.Unmarshal(out.Bytes(), &listed))
		assert.Equal(t, "some data was not read", listed.PartialError)
		assert.Contains(t, errOut.String(), "results may be incomplete")
	})
}

func TestRunMonitorLogQuery_Failures(t *testing.T) {
	t.Run("agent not found", func(t *testing.T) {
		logs := &fakeTraceQuerier{}
		deps, agents, connections := newFakeTraceDeps(logs)
		agents.agent = nil
		agents.err = newTestResponseError(t, http.StatusNotFound)

		err := runMonitorLogQuery(t.Context(), testTraceTarget(), testLogOptions("json"), deps,
			&bytes.Buffer{}, &bytes.Buffer{})
		requireLocalErrorCode(t, err, exterrors.CodeAgentNotFound)
		assert.Zero(t, connections.calls)
		assert.Zero(t, logs.calls)
	})

	t.Run("prompt agent", func(t *testing.T) {
		logs := &fakeTraceQuerier{}
		deps, agents, _ := newFakeTraceDeps(logs)
		agents.agent = agentWithKind("support", "prompt")

		err := runMonitorLogQuery(t.Context(), testTraceTarget(), testLogOptions("json"), deps,
			&bytes.Buffer{}, &bytes.Buffer{})
		requireLocalErrorCode(t, err, exterrors.CodeUnsupportedAgentKind)
		assert.Zero(t, logs.calls)
	})

	t.Run("no telemetry connection", func(t *testing.T) {
		logs := &fakeTraceQuerier{}
		deps, _, connections := newFakeTraceDeps(logs)
		connections.connections = nil

		err := runMonitorLogQuery(t.Context(), testTraceTarget(), testLogOptions("json"), deps,
			&bytes.Buffer{}, &bytes.Buffer{})
		requireLocalErrorCode(t, err, exterrors.CodeTracingNotConfigured)
		assert.Zero(t, logs.calls)
	})

	t.Run("telemetry resource not found", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{err: newTestResponseError(t, http.StatusNotFound)})
		err := runMonitorLogQuery(t.Context(), testTraceTarget(), testLogOptions("json"), deps,
			&bytes.Buffer{}, &bytes.Buffer{})
		requireLocalErrorCode(t, err, exterrors.CodeTelemetryResourceNotFound)
	})

	t.Run("access denied", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{err: newTestResponseError(t, http.StatusForbidden)})
		err := runMonitorLogQuery(t.Context(), testTraceTarget(), testLogOptions("json"), deps,
			&bytes.Buffer{}, &bytes.Buffer{})
		serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
		require.True(t, ok, "expected ServiceError, got %T: %v", err, err)
		assert.Contains(t, serviceErr.ErrorCode, exterrors.OpQueryLogs)
		assert.Contains(t, serviceErr.Suggestion, "Log Analytics Reader")
	})
}
