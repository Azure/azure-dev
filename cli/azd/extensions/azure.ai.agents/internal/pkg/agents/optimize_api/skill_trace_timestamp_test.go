// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optimize_api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillTraceTimestampRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		wire string
		utc  string
	}{
		{`0`, "1970-01-01T00:00:00Z"},
		{`1792454400`, "2026-10-20T00:00:00Z"},
		{`-62135596800`, "0001-01-01T00:00:00Z"},
		{`253402300799`, "9999-12-31T23:59:59Z"},
		{`"2026-10-20T00:00:00Z"`, "2026-10-20T00:00:00Z"},
		{`"2026-10-20T01:00:00.1234567+01:00"`, "2026-10-20T00:00:00.1234567Z"},
	} {
		t.Run(tt.wire, func(t *testing.T) {
			t.Parallel()
			var value SkillTraceTimestamp
			require.NoError(t, json.Unmarshal([]byte(tt.wire), &value))
			parsed, err := value.Time()
			require.NoError(t, err)
			assert.Equal(t, tt.utc, parsed.UTC().Format(time.RFC3339Nano))
			actual, err := json.Marshal(value)
			require.NoError(t, err)
			assert.JSONEq(t, tt.wire, string(actual))
		})
	}
}

func TestSkillTraceTimestampRejectsInvalidValues(t *testing.T) {
	t.Parallel()
	for _, wire := range []string{
		`null`, `true`, `[]`, `{}`, `""`, `"1792454400"`, `"2026-10-20"`,
		`"2026-10-20T00:00:00"`, `"0000-01-01T00:00:00Z"`,
		`1792454400.5`, `1792454400.0`, `1e3`, `1792454400000`,
		`253402300800`, `-62135596801`, `9223372036854775808`, `-9223372036854775809`,
	} {
		t.Run(wire, func(t *testing.T) {
			t.Parallel()
			var value SkillTraceTimestamp
			require.ErrorContains(t, json.Unmarshal([]byte(wire), &value), "trace timestamps")
			assert.Empty(t, value)
		})
	}
}

func TestSkillTraceWindowKeepsEpochZeroAndOmittedBoundsDistinct(t *testing.T) {
	t.Parallel()
	for _, wire := range []string{
		`{"type":"fixed","start_time":0,"end_time":1}`,
		`{"type":"trace_ids","trace_ids":["trace-a"],"lookback_hours":1}`,
		`{"type":"fixed","start_time":1792454400,"end_time":"2026-10-27T00:00:00Z"}`,
	} {
		var window SkillTraceWindow
		require.NoError(t, json.Unmarshal([]byte(wire), &window))
		actual, err := json.Marshal(window)
		require.NoError(t, err)
		assert.JSONEq(t, wire, string(actual))
	}
	var missing SkillTraceTimestamp
	_, err := missing.Time()
	require.Error(t, err)
}

func TestSkillTraceTimestampJobLifecycleAndMixedLists(t *testing.T) {
	t.Parallel()
	jobBody := func(start, end string) string {
		return fmt.Sprintf(`{"id":"job","status":"queued","created_at":1,"updated_at":2,
			"optimization_configuration":{"type":"skill_optimization","context":[
			{"type":"agent_traces","trace_window":{"type":"fixed","start_time":%s,"end_time":%s}}]},
			"future_service_field":{"preserve":true}}`, start, end)
	}
	numeric := jobBody("1792454400", "1793059200")
	iso := jobBody(`"2026-10-20T00:00:00Z"`, `"2026-10-27T00:00:00Z"`)
	for _, tt := range []struct {
		name string
		body string
	}{
		{"unix", numeric},
		{"iso", iso},
		{"mixed", jobBody("1792454400", `"2026-10-27T00:00:00Z"`)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var request OptimizeRequest
			require.NoError(t, json.Unmarshal([]byte(numeric), &request))
			request.TargetConfiguration = &AgentIdentifier{Type: "foundry_agent", Name: "demo-agent", Version: "4"}
			request.OptimizationModelConfiguration = ModelConfiguration{Model: "optimizer"}
			expected, err := json.Marshal(request)
			require.NoError(t, err)
			listBody := `{"data":[` + iso + `,` + numeric + `],"has_more":false}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "v1", r.URL.Query().Get("api-version"))
				if r.URL.Path == "/agent_optimization_jobs" {
					if r.Method == http.MethodGet {
						_, _ = w.Write([]byte(listBody))
						return
					}
					assert.Equal(t, http.MethodPost, r.Method)
					var actual json.RawMessage
					if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&actual)) {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					assert.JSONEq(t, string(expected), string(actual))
					assert.Contains(t, string(actual), `"start_time":1792454400`)
					w.WriteHeader(http.StatusAccepted)
				} else {
					assert.True(t, r.URL.Path == "/agent_optimization_jobs/job" ||
						strings.HasSuffix(r.URL.Path, "/job:cancel"))
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := newTestClient(server.URL)
			created, err := client.StartOptimize(t.Context(), &request)
			require.NoError(t, err)
			got, err := client.GetOptimizeStatus(t.Context(), "job")
			require.NoError(t, err)
			cancelled, err := client.CancelOptimize(t.Context(), "job")
			require.NoError(t, err)
			for _, job := range []*OptimizeJobStatus{created, got, cancelled} {
				assert.EqualValues(t, 1, job.CreatedAt)
				assert.EqualValues(t, 2, job.UpdatedAt)
				start, err := job.OptimizationConfiguration.Context[0].TraceWindow.StartTime.Time()
				require.NoError(t, err)
				assert.EqualValues(t, 1792454400, start.Unix())
				encoded, err := json.Marshal(job)
				require.NoError(t, err)
				assert.JSONEq(t, tt.body, string(encoded))
			}
			page, err := client.ListOptimizeJobs(t.Context(), 20, "")
			require.NoError(t, err)
			require.Len(t, page.Data, 2)
			for _, job := range page.Data {
				start, err := job.OptimizationConfiguration.Context[0].TraceWindow.StartTime.Time()
				require.NoError(t, err)
				assert.EqualValues(t, 1792454400, start.Unix())
			}
			encoded, err := json.Marshal(page)
			require.NoError(t, err)
			assert.JSONEq(t, listBody, string(encoded))
		})
	}
}
