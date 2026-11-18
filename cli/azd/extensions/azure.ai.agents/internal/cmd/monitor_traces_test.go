// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAppInsightsID      = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/microsoft.insights/components/appi-one"
	testOtherAppInsightsID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/microsoft.insights/components/appi-two"
	testFoundryProjectID   = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account/projects/project"
)

type fakeConnectionLister struct {
	connections []azure.Connection
	err         error
	calls       int
}

func (f *fakeConnectionLister) GetAllConnections(context.Context) ([]azure.Connection, error) {
	f.calls++
	return f.connections, f.err
}

type fakeTraceQuerier struct {
	result      *azure.LogsQueryResult
	err         error
	calls       int
	gotResource string
	gotQuery    string
}

func (f *fakeTraceQuerier) QueryResource(
	_ context.Context, resourceID, query string, _, _ time.Time,
) (*azure.LogsQueryResult, error) {
	f.calls++
	f.gotResource = resourceID
	f.gotQuery = query
	return f.result, f.err
}

func appInsightsConnection(name, resourceID string, isDefault bool) azure.Connection {
	return azure.Connection{
		Name:      name,
		ID:        testFoundryProjectID + "/connections/" + name,
		Type:      azure.ConnectionTypeAppInsights,
		Target:    resourceID,
		IsDefault: isDefault,
	}
}

func queryResult(columns []string, rows ...[]any) *azure.LogsQueryResult {
	if len(columns) > 0 && columns[len(columns)-1] == "agent_match_count" {
		matches := 0
		for _, row := range rows {
			if row[1] != "exception" && genAISpanMatchesAgent(recordDynamic(map[string]any{"attributes": row[14]}, "attributes"), "support") {
				matches++
			}
		}
		for i := range rows {
			rows[i] = append(rows[i], float64(matches))
		}
	}
	table := azure.LogsQueryTable{Name: "PrimaryResult", Rows: rows}
	for _, column := range columns {
		table.Columns = append(table.Columns, azure.LogsQueryColumn{Name: column})
	}
	return &azure.LogsQueryResult{Tables: []azure.LogsQueryTable{table}}
}

func genAISpanMatchesAgent(attributes map[string]any, agentName string) bool {
	operation, _ := attributes["gen_ai.operation.name"].(string)
	if operation == "" {
		return false
	}
	name, _ := attributes["gen_ai.agent.name"].(string)
	id, _ := attributes["gen_ai.agent.id"].(string)
	if name != "" {
		return strings.EqualFold(name, agentName)
	}
	prefix := agentName + ":"
	return strings.EqualFold(id, agentName) || len(id) > len(prefix) && strings.EqualFold(id[:len(prefix)], prefix)
}

var testTraceNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func newFakeTraceDeps(logs *fakeTraceQuerier) (*traceDeps, *fakeRemoteAgentReader, *fakeConnectionLister) {
	agents := &fakeRemoteAgentReader{agent: agentWithKind("support", "hosted")}
	connections := &fakeConnectionLister{
		connections: []azure.Connection{appInsightsConnection("appi", testAppInsightsID, true)},
	}
	return &traceDeps{
		agents:      agents,
		connections: connections,
		logs:        logs,
		now:         func() time.Time { return testTraceNow },
	}, agents, connections
}

func testTraceTarget() *AgentContext {
	return &AgentContext{ProjectEndpoint: testProjectEndpoint, Name: "support"}
}

func TestParseTraceWindow(t *testing.T) {
	valid := map[string]time.Duration{
		"30m":  30 * time.Minute,
		"6h":   6 * time.Hour,
		"7d":   7 * 24 * time.Hour,
		"730d": maxTraceWindow,
	}
	for raw, want := range valid {
		got, err := parseTraceWindow(raw)
		require.NoError(t, err, raw)
		assert.Equal(t, want, got, raw)
	}

	for _, raw := range []string{"", "0d", "-1h", "731d", "213504d", "1w", "abc"} {
		_, err := parseTraceWindow(raw)
		requireLocalErrorCode(t, err, exterrors.CodeInvalidParameter)
	}
}

func TestRedactTelemetryURLs(t *testing.T) {
	values := map[string]any{
		"url.full": "https://user:password@example.com/blob?sig=secret#fragment",
		"nested":   map[string]any{"http.url": "https://example.com/path?token=secret"},
		"array":    []any{"https://example.com/path?sig=secret"},
		"prompt":   "keep this prompt",
	}
	data, err := json.Marshal(redactTraceAttributes(values))
	require.NoError(t, err)
	assert.NotContains(t, string(data), "secret")
	assert.NotContains(t, string(data), "password")
	assert.NotContains(t, string(data), "fragment")
	assert.Contains(t, string(data), "https://example.com/blob")
	assert.Contains(t, string(data), "keep this prompt")
}

func TestWriteTraceJSONFileReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.json")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644)) // #nosec G306 -- test replacing a readable file
	require.NoError(t, writeTraceJSONFile(path, map[string]string{"value": "new"}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"new"`)
	if os.PathSeparator != '\\' {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestMillisecondsPreserveSubmillisecondPrecision(t *testing.T) {
	assert.Equal(t, 400*time.Microsecond, msDuration(0.4))
	assert.Equal(t, 1600*time.Microsecond, msDuration(1.6))
}

func TestResolveTracingSource(t *testing.T) {
	t.Run("no connection", func(t *testing.T) {
		lister := &fakeConnectionLister{connections: []azure.Connection{
			{Name: "search", Type: azure.ConnectionTypeCognitiveSearch, Target: "https://search"},
		}}
		_, err := resolveTracingSource(t.Context(), lister, testProjectEndpoint)
		requireLocalErrorCode(t, err, exterrors.CodeTracingNotConfigured)
	})

	t.Run("connection without resource ID", func(t *testing.T) {
		lister := &fakeConnectionLister{connections: []azure.Connection{
			appInsightsConnection("appi", "InstrumentationKey=abc", false),
		}}
		_, err := resolveTracingSource(t.Context(), lister, testProjectEndpoint)
		requireLocalErrorCode(t, err, exterrors.CodeTracingNotConfigured)
	})

	t.Run("resource ID from metadata", func(t *testing.T) {
		conn := appInsightsConnection("appi", "InstrumentationKey=abc", false)
		conn.Metadata = map[string]string{"ResourceId": testAppInsightsID}
		lister := &fakeConnectionLister{connections: []azure.Connection{conn}}
		source, err := resolveTracingSource(t.Context(), lister, testProjectEndpoint)
		require.NoError(t, err)
		assert.Equal(t, traceTelemetrySource{ConnectionName: "appi", ResourceID: testAppInsightsID, ProjectID: testFoundryProjectID}, *source)
	})

	t.Run("connections to the same resource", func(t *testing.T) {
		lister := &fakeConnectionLister{connections: []azure.Connection{
			appInsightsConnection("first", testAppInsightsID, false),
			appInsightsConnection("second", strings.ToUpper(testAppInsightsID), false),
		}}
		source, err := resolveTracingSource(t.Context(), lister, testProjectEndpoint)
		require.NoError(t, err)
		assert.Equal(t, "first", source.ConnectionName)
	})

	t.Run("default connection wins", func(t *testing.T) {
		lister := &fakeConnectionLister{connections: []azure.Connection{
			appInsightsConnection("first", testAppInsightsID, false),
			appInsightsConnection("second", testOtherAppInsightsID, true),
		}}
		source, err := resolveTracingSource(t.Context(), lister, testProjectEndpoint)
		require.NoError(t, err)
		assert.Equal(t, testOtherAppInsightsID, source.ResourceID)
	})

	t.Run("invalid default is not replaced by a nondefault", func(t *testing.T) {
		lister := &fakeConnectionLister{connections: []azure.Connection{
			appInsightsConnection("valid", testAppInsightsID, false),
			appInsightsConnection("default", "InstrumentationKey=secret", true),
		}}
		_, err := resolveTracingSource(t.Context(), lister, testProjectEndpoint)
		requireLocalErrorCode(t, err, exterrors.CodeTracingNotConfigured)
	})

	t.Run("missing project identity cannot scope telemetry", func(t *testing.T) {
		conn := appInsightsConnection("appi", testAppInsightsID, true)
		conn.ID = ""
		_, err := resolveTracingSource(t.Context(), &fakeConnectionLister{connections: []azure.Connection{conn}}, testProjectEndpoint)
		requireLocalErrorCode(t, err, exterrors.CodeTracingNotConfigured)
	})

	t.Run("ambiguous connections", func(t *testing.T) {
		lister := &fakeConnectionLister{connections: []azure.Connection{
			appInsightsConnection("first", testAppInsightsID, false),
			appInsightsConnection("second", testOtherAppInsightsID, false),
		}}
		_, err := resolveTracingSource(t.Context(), lister, testProjectEndpoint)
		localErr := requireLocalErrorCode(t, err, exterrors.CodeAmbiguousTracingConnection)
		assert.Contains(t, localErr.Message, "first, second")
	})

	t.Run("same resource with conflicting project identities is ambiguous", func(t *testing.T) {
		first := appInsightsConnection("first", testAppInsightsID, false)
		second := appInsightsConnection("second", testAppInsightsID, false)
		second.ID = strings.Replace(second.ID, "/projects/project/", "/projects/other/", 1)
		_, err := resolveTracingSource(t.Context(),
			&fakeConnectionLister{connections: []azure.Connection{first, second}}, testProjectEndpoint)
		requireLocalErrorCode(t, err, exterrors.CodeAmbiguousTracingConnection)
	})

	t.Run("access denied", func(t *testing.T) {
		lister := &fakeConnectionLister{err: newTestResponseError(t, http.StatusForbidden)}
		_, err := resolveTracingSource(t.Context(), lister, testProjectEndpoint)
		serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
		require.True(t, ok, "expected ServiceError, got %T: %v", err, err)
		assert.Contains(t, serviceErr.Suggestion, "read connections")
	})
}

func TestBuildTraceQueries(t *testing.T) {
	assert.Equal(t, `'a\'b\\c'`, kqlString(`a'b\c`))

	start := testTraceNow.Add(-time.Hour)
	list := buildTraceListQuery("sup'port", testFoundryProjectID, traceStatusFailed, start, testTraceNow, 10)
	assert.Contains(t, list, `let agentName = 'sup\'port';`)
	assert.Contains(t, list, "| where failed_span_count > 0")
	assert.Contains(t, list, "| take 11")
	assert.Contains(t, list, "datetime(2026-11-16T11:00:00Z) .. datetime(2026-11-16T12:00:00Z)")
	assert.Contains(t, list, "| where isnotempty(tostring(customDimensions['gen_ai.operation.name']))")
	assert.Contains(t, list, "arg_min(timestamp, agent_span_name) by operation_Id")
	assert.Contains(t, list, "| extend root_span_name = coalesce(root_span_name, agent_span_name)")
	assert.Contains(t, list, "coalesce(tostring(customDimensions['gen_ai.agent.version'])")
	assert.Contains(t, list, "let projectId = '"+testFoundryProjectID+"';")
	assert.Contains(t, list, "isnotempty(foundry_project) or isnotempty(gen_ai_project)")
	assert.Contains(t, list, "isempty(foundry_project) or foundry_project =~ projectId")
	assert.Contains(t, list, "isempty(gen_ai_project) or gen_ai_project =~ projectId")
	assert.Contains(t, list, "iff(isnotempty(agent_name_attr), agent_name_attr =~ agentName,")

	assert.Contains(t, buildTraceListQuery("support", testFoundryProjectID, traceStatusSucceeded, start, testTraceNow, 5),
		"| where failed_span_count == 0")
	assert.NotContains(t, buildTraceListQuery("support", testFoundryProjectID, traceStatusAll, start, testTraceNow, 5),
		"failed_span_count >")

	export := buildTraceExportQuery("trace-1", "support", testFoundryProjectID, start, testTraceNow, 100)
	assert.Contains(t, export, "let traceId = 'trace-1';")
	assert.Contains(t, export, "| take 101")
	assert.Contains(t, export, "isempty(foundry_project) or foundry_project =~ projectId")
	assert.Contains(t, export, "isempty(gen_ai_project) or gen_ai_project =~ projectId")
	assert.Contains(t, export, "iff(isnotempty(agent_name_attr), agent_name_attr =~ agentName,")
	assert.Less(t, strings.Index(export, "let agentMatches"), strings.Index(export, "| take 101"))
}

var traceListColumns = []string{
	"trace_id", "start_time", "end_time", "span_count", "failed_span_count", "sampled",
	"root_span_name", "agent_version", "conversation_id",
}

func TestRunMonitorTraces_List(t *testing.T) {
	rows := [][]any{
		{"t2", "2026-11-16T11:50:00Z", "2026-11-16T11:50:02.5Z", 4.0, 1.0, 0.0, "invoke_agent", "3", "conv-2"},
		{"t1", "2026-11-16T11:40:00Z", "2026-11-16T11:40:01Z", 2.0, 0.0, 1.0, "invoke_agent", "3", ""},
	}

	t.Run("json truncated", func(t *testing.T) {
		logs := &fakeTraceQuerier{result: queryResult(traceListColumns, rows...)}
		deps, agents, _ := newFakeTraceDeps(logs)
		var out, errOut bytes.Buffer
		opts := &traceOptions{since: time.Hour, sinceLabel: "1h", status: traceStatusAll, limit: 1, output: "json"}

		require.NoError(t, runMonitorTraces(t.Context(), testTraceTarget(), opts, deps, &out, &errOut))
		assert.Equal(t, "support", agents.gotName)
		assert.Equal(t, testAppInsightsID, logs.gotResource)

		var result traceListResult
		require.NoError(t, json.Unmarshal(out.Bytes(), &result))
		require.Len(t, result.Data, 1)
		assert.True(t, result.Truncated)
		assert.Equal(t, "t2", result.Data[0].TraceID)
		assert.Equal(t, "failed", result.Data[0].Status)
		assert.Equal(t, int64(2500), result.Data[0].DurationMs)
		assert.Equal(t, "conv-2", result.Data[0].ConversationID)
		assert.Contains(t, errOut.String(), "WARNING: more traces matched")
		assert.NotContains(t, out.String(), "Querying traces")
	})

	t.Run("json partial error", func(t *testing.T) {
		result := queryResult(traceListColumns, rows[1])
		result.Error = &azure.LogsQueryError{Code: "PartialError", Message: "some data was not read"}
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: result})
		var out, errOut bytes.Buffer
		opts := &traceOptions{since: time.Hour, sinceLabel: "1h", status: traceStatusAll, limit: 10, output: "json"}

		require.NoError(t, runMonitorTraces(t.Context(), testTraceTarget(), opts, deps, &out, &errOut))
		var listed traceListResult
		require.NoError(t, json.Unmarshal(out.Bytes(), &listed))
		assert.Equal(t, "some data was not read", listed.PartialError)
		assert.True(t, listed.Data[0].Sampled)
		assert.Contains(t, errOut.String(), "results may be incomplete")
	})

	t.Run("empty json keeps stdout parseable", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(traceListColumns)})
		var out, errOut bytes.Buffer
		opts := &traceOptions{since: time.Hour, sinceLabel: "1h", status: traceStatusAll, limit: 10, output: "json"}

		require.NoError(t, runMonitorTraces(t.Context(), testTraceTarget(), opts, deps, &out, &errOut))
		var listed map[string]any
		require.NoError(t, json.Unmarshal(out.Bytes(), &listed))
		assert.Equal(t, []any{}, listed["data"])
		assert.Contains(t, errOut.String(), `No traces found for agent "support" in the last 1h.`)
	})

	t.Run("empty table", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(traceListColumns)})
		var out, errOut bytes.Buffer
		opts := &traceOptions{since: time.Hour, sinceLabel: "1h", status: traceStatusAll, limit: 10, output: "table"}

		require.NoError(t, runMonitorTraces(t.Context(), testTraceTarget(), opts, deps, &out, &errOut))
		assert.Contains(t, out.String(), `No traces found for agent "support" in the last 1h.`)
	})
}

func TestRunMonitorTraces_FailsBeforeQuery(t *testing.T) {
	t.Run("agent not found", func(t *testing.T) {
		logs := &fakeTraceQuerier{}
		deps, agents, connections := newFakeTraceDeps(logs)
		agents.agent = nil
		agents.err = newTestResponseError(t, http.StatusNotFound)

		err := runMonitorTraces(t.Context(), testTraceTarget(), &traceOptions{limit: 10}, deps,
			&bytes.Buffer{}, &bytes.Buffer{})
		requireLocalErrorCode(t, err, exterrors.CodeAgentNotFound)
		assert.Zero(t, connections.calls)
		assert.Zero(t, logs.calls)
	})

	t.Run("tracing not configured", func(t *testing.T) {
		logs := &fakeTraceQuerier{}
		deps, _, connections := newFakeTraceDeps(logs)
		connections.connections = nil

		err := runMonitorTraces(t.Context(), testTraceTarget(), &traceOptions{limit: 10}, deps,
			&bytes.Buffer{}, &bytes.Buffer{})
		requireLocalErrorCode(t, err, exterrors.CodeTracingNotConfigured)
		assert.Zero(t, logs.calls)
	})

	t.Run("telemetry resource not found", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{err: newTestResponseError(t, http.StatusNotFound)})
		err := runMonitorTraces(t.Context(), testTraceTarget(), &traceOptions{limit: 10}, deps,
			&bytes.Buffer{}, &bytes.Buffer{})
		requireLocalErrorCode(t, err, exterrors.CodeTelemetryResourceNotFound)
	})

	t.Run("logs access denied", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{err: newTestResponseError(t, http.StatusForbidden)})
		err := runMonitorTraces(t.Context(), testTraceTarget(), &traceOptions{limit: 10}, deps,
			&bytes.Buffer{}, &bytes.Buffer{})
		serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
		require.True(t, ok, "expected ServiceError, got %T: %v", err, err)
		assert.Contains(t, serviceErr.Suggestion, "Log Analytics Reader")
	})
}

var traceExportColumns = []string{
	"timestamp", "span_kind", "span_id", "parent_id", "name", "duration_ms", "success",
	"result_code", "type", "target", "role", "item_count", "message", "problem_id", "attributes", "agent_match_count",
}

func exportRow(kind, spanID, parentID, name string, success any, itemCount float64, attributes string) []any {
	return []any{
		"2026-11-16T11:00:00Z", kind, spanID, parentID, name, 250.0, success,
		"200", "", "", "support", itemCount, "", "", attributes,
	}
}

const supportAgentAttributes = `{"gen_ai.agent.name":"support","gen_ai.agent.id":"support:3","gen_ai.operation.name":"invoke_agent"}`

func TestAssessTraceCompleteness_ReportsAbsentSpansWithoutGuessingCause(t *testing.T) {
	at := func(seconds int) time.Time { return time.Date(2026, 11, 16, 22, 55, seconds, 0, time.UTC) }
	span := func(id, parent, role string, start time.Time, items int64) traceSpan {
		return traceSpan{SpanID: id, ParentSpanID: parent, Role: role, ItemCount: items, start: start}
	}

	t.Run("hosted agent trace without recorded root or entry parents", func(t *testing.T) {
		// Sanitized shape of a real hosted-agent trace: the earliest span of the agentsv2
		// and responsesapi roles each reference a parent that is not in the telemetry.
		spans := []traceSpan{
			span("agentsvc-request", "absent-parent-a", "agentsv2", at(56), 1),
			span("agent-token", "agentsvc-request", "hosted-agent", at(63), 1),
			span("agent-invoke", "agentsvc-request", "hosted-agent", at(64), 3),
			span("agent-chat", "agent-invoke", "hosted-agent", at(64), 4),
			span("responses-invoke", "absent-parent-b", "responsesapi", at(66), 1),
			span("responses-chat", "responses-invoke", "responsesapi", at(67), 1),
		}
		result := assessTraceCompleteness("trace-1", spans, nil, false, "")
		assert.False(t, result.Complete)
		assert.False(t, result.RootSpanFound)
		assert.Equal(t, []string{"absent-parent-a", "absent-parent-b"}, result.MissingParentSpanIDs)
		assert.True(t, result.Sampled)
		require.Len(t, result.Notes, 3)
		assert.Contains(t, result.Notes[0], "no root span (a span without a parent) was found in the queried telemetry")
		assert.Contains(t, result.Notes[1], "2 parent span ID(s) referenced by recorded spans or exceptions were not found")
		for _, note := range result.Notes[:2] {
			assert.Contains(t, note, "does not mean the operation failed")
			assert.Contains(t, note, "the telemetry does not show why")
		}
		assert.Contains(t, result.Notes[2], "2 span record(s) have item_count > 1")
	})

	t.Run("earliest span of a role with an absent parent is still reported", func(t *testing.T) {
		// The absent parent can be a sampled or filtered span of the same service, so
		// being the first span of its role does not make the parent external.
		spans := []traceSpan{
			span("root", "", "agentsv2", at(55), 1),
			span("agent-invoke", "lost-span", "hosted-agent", at(56), 1),
			span("agent-chat", "agent-invoke", "hosted-agent", at(57), 1),
		}
		result := assessTraceCompleteness("trace-1", spans, nil, false, "")
		assert.False(t, result.Complete)
		assert.True(t, result.RootSpanFound)
		assert.Equal(t, []string{"lost-span"}, result.MissingParentSpanIDs)
		require.Len(t, result.Notes, 1)
		assert.Contains(t, result.Notes[0], "1 parent span ID(s)")
	})

	t.Run("recorded root and parents are complete", func(t *testing.T) {
		spans := []traceSpan{
			span("root", "trace-1", "agentsv2", at(55), 1),
			span("agent-invoke", "root", "hosted-agent", at(56), 1),
		}
		result := assessTraceCompleteness("trace-1", spans, nil, false, "")
		assert.True(t, result.Complete)
		assert.Empty(t, result.MissingParentSpanIDs)
		assert.Empty(t, result.Notes)
	})

	t.Run("missing exception parent makes trace incomplete", func(t *testing.T) {
		spans := []traceSpan{span("root", "", "agent", at(55), 1)}
		exceptions := []traceException{{ParentSpanID: "missing"}}
		result := assessTraceCompleteness("trace-1", spans, exceptions, false, "")
		assert.False(t, result.Complete)
		assert.Equal(t, []string{"missing"}, result.MissingParentSpanIDs)
	})
}

func TestGenAISpanMatchesAgent(t *testing.T) {
	tests := []struct {
		name       string
		attributes string
		want       bool
	}{
		{
			name: "hosted invocation with UUID agent ID",
			attributes: `{"gen_ai.agent.name":"support","gen_ai.agent.id":"00000000-0000-0000-0000-000000000001",
				"gen_ai.agent.version":"1","gen_ai.operation.name":"invoke_agent"}`,
			want: true,
		},
		{
			name:       "versioned agent ID without name",
			attributes: `{"gen_ai.agent.id":"support:3","gen_ai.operation.name":"invoke_agent"}`,
			want:       true,
		},
		{
			name:       "case insensitive agent identity",
			attributes: `{"gen_ai.agent.name":"SUPPORT","gen_ai.operation.name":"invoke_agent"}`,
			want:       true,
		},
		{
			name:       "partial trace with model operation",
			attributes: `{"gen_ai.agent.name":"support","gen_ai.operation.name":"chat"}`,
			want:       true,
		},
		{
			name:       "tool operation",
			attributes: `{"gen_ai.agent.name":"support","gen_ai.operation.name":"execute_tool"}`,
			want:       true,
		},
		{
			name: "inherited agent identity on infrastructure span",
			attributes: `{"gen_ai.agent.name":"support","gen_ai.agent.id":"00000000-0000-0000-0000-000000000001",
				"gen_ai.agent.version":"1","_MS.ProcessedByMetricExtractors":"True"}`,
		},
		{
			name:       "empty operation",
			attributes: `{"gen_ai.agent.name":"support","gen_ai.operation.name":""}`,
		},
		{
			name:       "invalid operation attribute type",
			attributes: `{"gen_ai.agent.name":"support","gen_ai.operation.name":42}`,
		},
		{
			name:       "GenAI span from another agent",
			attributes: `{"gen_ai.agent.name":"support-other","gen_ai.agent.id":"support-other:1","gen_ai.operation.name":"invoke_agent"}`,
		},
		{
			name:       "conflicting agent name takes precedence over agent ID",
			attributes: `{"gen_ai.agent.name":"other","gen_ai.agent.id":"support:1","gen_ai.operation.name":"invoke_agent"}`,
		},
		{
			name:       "role without agent identity",
			attributes: `{"service.name":"support","gen_ai.operation.name":"invoke_agent"}`,
		},
		{
			name:       "empty version does not identify agent",
			attributes: `{"gen_ai.agent.id":"support:","gen_ai.operation.name":"invoke_agent"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attributes := recordDynamic(map[string]any{"attributes": tt.attributes}, "attributes")
			assert.Equal(t, tt.want, genAISpanMatchesAgent(attributes, "support"))
		})
	}
}

func exportOptions() *traceOptions {
	return &traceOptions{
		traceID: "trace-1", since: defaultTraceExportWindow, sinceLabel: "30d", output: "json",
	}
}

func TestRunMonitorTraces_Export(t *testing.T) {
	t.Run("agent span after export limit still establishes ownership", func(t *testing.T) {
		rows := make([][]any, 0, maxTraceExportSpans+1)
		for range maxTraceExportSpans + 1 {
			rows = append(rows, exportRow("dependency", "child", "", "dependency", true, 1, `{}`))
		}
		result := queryResult(traceExportColumns, rows...)
		// The scalar count covers the full window, including a match beyond the returned rows.
		for _, row := range result.Tables[0].Rows {
			row[len(row)-1] = float64(1)
		}
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: result})
		var out, errOut bytes.Buffer
		require.NoError(t, runMonitorTraces(t.Context(), testTraceTarget(), exportOptions(), deps, &out, &errOut))
		var export traceExport
		require.NoError(t, json.Unmarshal(out.Bytes(), &export))
		assert.True(t, export.Completeness.Truncated)
		assert.Len(t, export.Spans, maxTraceExportSpans)
	})
	t.Run("complete trace with redaction", func(t *testing.T) {
		logs := &fakeTraceQuerier{result: queryResult(traceExportColumns,
			exportRow("request", "root", "trace-1", "invoke_agent", true, 1, supportAgentAttributes),
			exportRow("dependency", "child", "root", "chat", false, 1,
				`{"http.request.header.authorization":"Bearer abc","gen_ai.prompt":"hello","x":"bearer token"}`),
			exportRow("exception", "", "child", "", nil, 1, `{"api_key":"k"}`),
		)}
		deps, _, _ := newFakeTraceDeps(logs)
		var out, errOut bytes.Buffer

		require.NoError(t, runMonitorTraces(t.Context(), testTraceTarget(), exportOptions(), deps, &out, &errOut))
		assert.Contains(t, logs.gotQuery, "let traceId = 'trace-1';")
		assert.Empty(t, errOut.String())

		var export traceExport
		require.NoError(t, json.Unmarshal(out.Bytes(), &export))
		assert.Equal(t, traceExportSchemaVersion, export.SchemaVersion)
		assert.False(t, export.Completeness.Complete)
		assert.True(t, export.Completeness.RootSpanFound)
		require.Len(t, export.Spans, 2)
		assert.Equal(t, "ok", export.Spans[0].Status)
		assert.Equal(t, "error", export.Spans[1].Status)
		assert.Equal(t, "root", export.Spans[1].ParentSpanID)
		assert.Equal(t, "2026-11-16T11:00:00.25Z", export.Spans[0].EndTime)
		assert.Equal(t, redactedValue, export.Spans[1].Attributes["http.request.header.authorization"])
		assert.Equal(t, redactedValue, export.Spans[1].Attributes["x"])
		assert.Equal(t, "hello", export.Spans[1].Attributes["gen_ai.prompt"])
		require.Len(t, export.Exceptions, 1)
		assert.Equal(t, redactedValue, export.Exceptions[0].Attributes["api_key"])
	})

	t.Run("not found", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(traceExportColumns)})
		err := runMonitorTraces(t.Context(), testTraceTarget(), exportOptions(), deps,
			&bytes.Buffer{}, &bytes.Buffer{})
		requireLocalErrorCode(t, err, exterrors.CodeTraceNotFound)
	})

	t.Run("trace from another agent", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(traceExportColumns,
			exportRow("request", "root", "", "invoke_agent", true, 1,
				`{"gen_ai.agent.name":"support-other","gen_ai.agent.id":"support-other:1","gen_ai.operation.name":"invoke_agent"}`),
		)})
		var out bytes.Buffer
		err := runMonitorTraces(t.Context(), testTraceTarget(), exportOptions(), deps, &out, &bytes.Buffer{})
		requireLocalErrorCode(t, err, exterrors.CodeTraceAgentMismatch)
		assert.Empty(t, out.String())
	})

	t.Run("infrastructure-only traces are rejected", func(t *testing.T) {
		for _, name := range []string{
			"GET /metadata/instance/compute", "GET /msi/token",
			"GET /AzMonSDKDynamicConfiguration", "GET /AzMonSDKDynamicConfigurationChanges",
		} {
			t.Run(name, func(t *testing.T) {
				deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(traceExportColumns,
					exportRow("dependency", "infra", "trace-1", name, true, 1,
						`{"gen_ai.agent.name":"support","gen_ai.agent.id":"00000000-0000-0000-0000-000000000001","gen_ai.agent.version":"1"}`),
				)})
				var out bytes.Buffer
				opts := exportOptions()
				opts.outFile = filepath.Join(t.TempDir(), "trace.json")
				err := runMonitorTraces(t.Context(), testTraceTarget(), opts, deps, &out, &bytes.Buffer{})
				requireLocalErrorCode(t, err, exterrors.CodeTraceAgentMismatch)
				assert.Empty(t, out.String())
				assert.NoFileExists(t, opts.outFile)
			})
		}
	})

	t.Run("operation and selected agent must occur on same span", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(traceExportColumns,
			exportRow("dependency", "infra", "", "GET /msi/token", true, 1,
				`{"gen_ai.agent.name":"support"}`),
			exportRow("dependency", "other", "infra", "invoke_agent other", true, 1,
				`{"gen_ai.agent.name":"other","gen_ai.operation.name":"invoke_agent"}`),
		)})
		var out bytes.Buffer
		err := runMonitorTraces(t.Context(), testTraceTarget(), exportOptions(), deps, &out, &bytes.Buffer{})
		requireLocalErrorCode(t, err, exterrors.CodeTraceAgentMismatch)
		assert.Empty(t, out.String())
	})

	t.Run("exception attributes cannot qualify a trace", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(traceExportColumns,
			exportRow("exception", "", "missing-parent", "", nil, 1, supportAgentAttributes),
		)})
		err := runMonitorTraces(t.Context(), testTraceTarget(), exportOptions(), deps, &bytes.Buffer{}, &bytes.Buffer{})
		requireLocalErrorCode(t, err, exterrors.CodeTraceAgentMismatch)
	})

	t.Run("hosted trace retains infrastructure children", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(traceExportColumns,
			exportRow("request", "invoke", "missing-parent", "invoke_agent", true, 1,
				`{"gen_ai.agent.name":"support","gen_ai.agent.id":"00000000-0000-0000-0000-000000000001",
				"gen_ai.operation.name":"invoke_agent","gen_ai.agent.version":"1"}`),
			exportRow("dependency", "token", "invoke", "GET /msi/token", true, 1,
				`{"gen_ai.agent.name":"support","gen_ai.agent.id":"00000000-0000-0000-0000-000000000001"}`),
		)})
		var out, errOut bytes.Buffer
		require.NoError(t, runMonitorTraces(t.Context(), testTraceTarget(), exportOptions(), deps, &out, &errOut))
		var export traceExport
		require.NoError(t, json.Unmarshal(out.Bytes(), &export))
		require.Len(t, export.Spans, 2)
		assert.Equal(t, "GET /msi/token", export.Spans[1].Name)
		assert.Equal(t, "invoke", export.Spans[1].ParentSpanID)
		assert.False(t, export.Completeness.Complete)
		assert.False(t, export.Completeness.RootSpanFound)
		assert.Equal(t, []string{"missing-parent"}, export.Completeness.MissingParentSpanIDs)
		assert.Equal(t, "missing-parent", export.Spans[0].ParentSpanID)
		assert.Empty(t, errOut.String())
	})

	t.Run("missing parent and sampling stay in JSON only", func(t *testing.T) {
		later := exportRow("dependency", "child", "missing-parent", "chat", true, 4, supportAgentAttributes)
		later[0] = "2026-11-16T11:00:01Z"
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(traceExportColumns,
			exportRow("request", "root", "", "invoke_agent", true, 1, supportAgentAttributes),
			later,
		)})
		var out, errOut bytes.Buffer
		require.NoError(t, runMonitorTraces(t.Context(), testTraceTarget(), exportOptions(), deps, &out, &errOut))

		var export traceExport
		require.NoError(t, json.Unmarshal(out.Bytes(), &export))
		assert.False(t, export.Completeness.Complete)
		assert.True(t, export.Completeness.RootSpanFound)
		assert.True(t, export.Completeness.Sampled)
		assert.Equal(t, []string{"missing-parent"}, export.Completeness.MissingParentSpanIDs)
		assert.Contains(t, export.Completeness.Notes[0],
			"1 parent span ID(s) referenced by recorded spans or exceptions were not found in the queried telemetry")
		assert.Contains(t, export.Completeness.Notes[1], "1 span record(s) have item_count > 1")
		assert.Empty(t, errOut.String())
	})

	t.Run("out file keeps missing root parents and sampling in JSON only", func(t *testing.T) {
		deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: queryResult(traceExportColumns,
			exportRow("request", "invoke", "missing-parent", "invoke_agent", true, 4, supportAgentAttributes),
		)})
		opts := exportOptions()
		opts.output = "table"
		opts.outFile = filepath.Join(t.TempDir(), "trace.json")
		var out, errOut bytes.Buffer

		require.NoError(t, runMonitorTraces(t.Context(), testTraceTarget(), opts, deps, &out, &errOut))
		assert.Empty(t, out.String())
		assert.Equal(t, "Wrote trace trace-1 (1 spans, 0 exceptions) to "+opts.outFile+"\n", errOut.String())

		data, err := os.ReadFile(opts.outFile)
		require.NoError(t, err)
		var export traceExport
		require.NoError(t, json.Unmarshal(data, &export))
		assert.Equal(t, "trace-1", export.TraceID)
		assert.False(t, export.Completeness.Complete)
		assert.False(t, export.Completeness.RootSpanFound)
		assert.True(t, export.Completeness.Sampled)
		assert.Equal(t, []string{"missing-parent"}, export.Completeness.MissingParentSpanIDs)
		require.Len(t, export.Completeness.Notes, 4)
		require.Len(t, export.Spans, 1)
		assert.Equal(t, "missing-parent", export.Spans[0].ParentSpanID)
	})
}

func TestRunMonitorTraces_ExportQueryWarnings(t *testing.T) {
	for _, tt := range []struct {
		name         string
		truncated    bool
		partialError string
	}{
		{name: "successful query", truncated: false},
		{name: "truncated query", truncated: true},
		{name: "partial query", partialError: "some data was not read"},
		{name: "truncated partial query", truncated: true, partialError: "some data was not read"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rows := [][]any{
				exportRow("request", "invoke", "missing-parent", "invoke_agent", true, 4, supportAgentAttributes),
			}
			if tt.truncated {
				for range maxTraceExportSpans {
					rows = append(rows, exportRow("dependency", "", "invoke", "chat", true, 1, `{}`))
				}
			}
			result := queryResult(traceExportColumns, rows...)
			if tt.partialError != "" {
				result.Error = &azure.LogsQueryError{Code: "PartialError", Message: tt.partialError}
			}

			for _, format := range []string{"json", "table", "file"} {
				t.Run(format, func(t *testing.T) {
					deps, _, _ := newFakeTraceDeps(&fakeTraceQuerier{result: result})
					opts := exportOptions()
					opts.output = format
					if format == "file" {
						opts.outFile = filepath.Join(t.TempDir(), "trace.json")
					}
					var out, errOut bytes.Buffer
					require.NoError(t, runMonitorTraces(t.Context(), testTraceTarget(), opts, deps, &out, &errOut))

					warningCount := 0
					if tt.truncated {
						warningCount++
						assert.Contains(t, errOut.String(), "only the first 5000 were exported")
					}
					if tt.partialError != "" {
						warningCount++
						assert.Contains(t, errOut.String(), "query returned a partial error: "+tt.partialError)
					}
					assert.Equal(t, warningCount, strings.Count(errOut.String(), "WARNING:"))
					assert.NotContains(t, errOut.String(), "no root span")
					assert.NotContains(t, errOut.String(), "parent span ID(s)")
					assert.NotContains(t, errOut.String(), "telemetry is sampled")
					assert.NotContains(t, errOut.String(), "may not contain every span")

					if format == "table" {
						assert.Contains(t, out.String(), "Complete: no")
						return
					}
					data := out.Bytes()
					if format == "file" {
						assert.Empty(t, out.String())
						assert.Contains(t, errOut.String(), "Wrote trace trace-1")
						var err error
						data, err = os.ReadFile(opts.outFile)
						require.NoError(t, err)
					}
					var export traceExport
					require.NoError(t, json.Unmarshal(data, &export))
					assert.False(t, export.Completeness.Complete)
					assert.False(t, export.Completeness.RootSpanFound)
					assert.True(t, export.Completeness.Sampled)
					assert.Equal(t, []string{"missing-parent"}, export.Completeness.MissingParentSpanIDs)
					assert.Equal(t, tt.truncated, export.Completeness.Truncated)
					assert.Equal(t, tt.partialError, export.Completeness.PartialError)
					assert.Len(t, export.Completeness.Notes, 4+warningCount)
					assert.Equal(t, "missing-parent", export.Spans[0].ParentSpanID)
				})
			}
		})
	}
}
