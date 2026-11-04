// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optimize_api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(endpoint string) *OptimizeClient {
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, &policy.ClientOptions{})
	return NewOptimizeClientFromPipeline(endpoint, pipeline)
}

type stubCredential struct{}

func (stubCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "stub"}, nil
}

func TestNewOptimizeClient(t *testing.T) {
	t.Parallel()
	client := NewOptimizeClient("https://example.com", stubCredential{})
	require.NotNil(t, client)
	assert.Equal(t, "https://example.com", client.endpoint)
}

func TestStartOptimize(t *testing.T) {
	t.Parallel()
	request := testGARequest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/agent_optimization_jobs", r.URL.Path)
		assert.Equal(t, "v1", r.URL.Query().Get("api-version"))
		assert.Empty(t, r.Header.Get("Foundry-Features"))
		var actual map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(r.Body).Decode(&actual))
		require.Len(t, actual, 3)
		assert.NotContains(t, actual, "inputs")
		expected, err := json.Marshal(request)
		require.NoError(t, err)
		encoded, err := json.Marshal(actual)
		require.NoError(t, err)
		assert.JSONEq(t, string(expected), string(encoded))
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"job1","status":"queued","run_duration_ms":0,"created_at":1,"updated_at":1}`))
	}))
	defer server.Close()
	job, err := newTestClient(server.URL).StartOptimize(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, "job1", job.ID)
	assert.Equal(t, StatusQueued, job.Status)
}

func TestStartOptimize_DefersValidationToServer(t *testing.T) {
	t.Parallel()
	request := testGARequest()
	request.OptimizationConfiguration.CandidateSearchConfiguration.MaxCandidates = new(0)
	request.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.TestCases = []json.RawMessage{
		json.RawMessage(`{"query":123,"additional_field":"preserved"}`),
	}
	expected, err := json.Marshal(request)
	require.NoError(t, err)
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/agent_optimization_jobs", r.URL.Path)
		var actual json.RawMessage
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&actual)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		assert.JSONEq(t, string(expected), string(actual))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(
			`{"error":{"code":"InvalidOptimizationRequest","message":"Rejected by optimization service"}}`))
	}))
	defer server.Close()

	job, err := newTestClient(server.URL).StartOptimize(t.Context(), request)
	require.Nil(t, job)
	require.Len(t, requests, 1)
	responseErr, ok := errors.AsType[*azcore.ResponseError](err)
	require.True(t, ok, "server validation errors must be returned to the caller")
	assert.Equal(t, http.StatusBadRequest, responseErr.StatusCode)
	assert.Equal(t, "InvalidOptimizationRequest", responseErr.ErrorCode)
	assert.Contains(t, err.Error(), "Rejected by optimization service")
}

func TestGAJobResponsesPreserveJSON(t *testing.T) {
	t.Parallel()
	body := `{"id":"job1","status":"skipped","error":{"code":"no_input","message":"No input"},
		"optimization_configuration":{"type":"agent_optimization","future_field":{"enabled":true}},
		"activity_progress":{"tracking_status":"available","activities":[],"updated_at":null,"observed_at":1},
		"run_duration_ms":0,"created_at":1,"updated_at":2}`
	for _, operation := range []string{"get", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "v1", r.URL.Query().Get("api-version"))
				assert.Empty(t, r.Header.Get("Foundry-Features"))
				if operation == "get" {
					assert.Equal(t, http.MethodGet, r.Method)
					assert.Equal(t, "/agent_optimization_jobs/job1", r.URL.Path)
				} else {
					assert.Equal(t, http.MethodPost, r.Method)
					assert.Equal(t, "/agent_optimization_jobs/job1:cancel", r.URL.Path)
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			client := newTestClient(server.URL)
			var job *OptimizeJobStatus
			var err error
			if operation == "get" {
				job, err = client.GetOptimizeStatus(t.Context(), "job1")
			} else {
				job, err = client.CancelOptimize(t.Context(), "job1")
			}
			require.NoError(t, err)
			assert.True(t, IsTerminal(job.Status))
			encoded, err := json.Marshal(job)
			require.NoError(t, err)
			assert.JSONEq(t, body, string(encoded))
		})
	}
}

func TestListOptimizeJobs(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"", "in_progress"} {
		t.Run(status, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "10", r.URL.Query().Get("limit"))
				assert.Equal(t, status, r.URL.Query().Get("status"))
				_, _ = w.Write([]byte(`{"data":[{"id":"job1","status":"queued","extra":"retained"}],
					"first_id":"job1","last_id":"job1","has_more":false}`))
			}))
			defer server.Close()
			page, err := newTestClient(server.URL).ListOptimizeJobs(t.Context(), 10, status)
			require.NoError(t, err)
			require.Len(t, page.Data, 1)
			encoded, err := json.Marshal(page.Data[0])
			require.NoError(t, err)
			assert.Contains(t, string(encoded), `"extra":"retained"`)
		})
	}
}

func TestOptimizationHTTPErrors(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BadRequest","message":"invalid"}}`))
	}))
	defer server.Close()
	client := newTestClient(server.URL)
	_, err := client.StartOptimize(t.Context(), testGARequest())
	require.ErrorContains(t, err, "400")
	_, err = client.GetOptimizeStatus(t.Context(), "missing")
	require.ErrorContains(t, err, "400")
	_, err = client.CancelOptimize(t.Context(), "missing")
	require.ErrorContains(t, err, "400")
	_, err = client.ListOptimizeJobs(t.Context(), 10, "")
	require.ErrorContains(t, err, "400")
	err = client.ReportDeployment(t.Context(), "j1", &DeploymentReport{CandidateID: "c1"})
	require.ErrorContains(t, err, "400")
}

func TestGetCandidateConfig(t *testing.T) {
	t.Parallel()
	for _, content := range []string{`{"system_prompt":"hello","model":"gpt-4o"}`, "not-json"} {
		t.Run(content, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "v1", r.URL.Query().Get("api-version"))
				switch r.URL.Path {
				case "/agent_optimization_jobs/job1":
					_, _ = w.Write([]byte(`{"optimization_configuration":{}}`))
				case "/agent_optimization_jobs/job1/candidates/c1":
					_, _ = w.Write([]byte(`{"candidate_id":"c1","job_id":"job1","name":"candidate","status":"completed",
						"files":[{"path":"config.json","type":"config","size_in_bytes":50}]}`))
				case "/agent_optimization_jobs/job1/files":
					assert.Equal(t, "c1", r.URL.Query().Get("candidate_id"))
					assert.Equal(t, "config.json", r.URL.Query().Get("path"))
					_, _ = w.Write([]byte(content))
				default:
					t.Errorf("unexpected GA route %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			config, err := newTestClient(server.URL).GetCandidateConfig(t.Context(), "job1", "c1")
			if content == "not-json" {
				require.ErrorContains(t, err, "not valid JSON")
			} else {
				require.NoError(t, err)
				assert.JSONEq(t, content, string(config))
			}
		})
	}
}

func TestGetCandidateConfigRequiresPublishedConfig(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"candidate_id":"c1","files":[]}`))
	}))
	defer server.Close()
	_, err := newTestClient(server.URL).GetCandidateConfig(t.Context(), "j1", "c1")
	require.ErrorContains(t, err, "no published config file")
}

func TestGetCandidateConfigCompletesBaseline(t *testing.T) {
	t.Parallel()
	const baseline = `{
		"system_prompt":"Original instructions","current_model":"baseline-model",
		"skills":[{"name":"original","description":"Original skill","body":"Original body"}],
		"tools":[{"type":"function","function":{"name":"calculate","strict":false,
			"parameters":{"type":"object","properties":{"value":{"const":9007199254740993}}}}},
			{"type":"function","function":{"name":"get_current_time"}}]
	}`
	for _, tt := range []struct {
		name, config, baseline, want, wantErr string
		mutations                             []CandidateMutation
		baselineStatus                        int
	}{
		{
			name:   "instruction-only optimization retains both baseline tools",
			config: `{"name":"agent","instructions":"Artifact instructions","model":"candidate-model","skills":[]}`,
			mutations: []CandidateMutation{
				{Type: "instructions", Value: json.RawMessage(`"Optimized instructions"`)},
			},
			want: `{"name":"agent","instructions":"Optimized instructions","model":"candidate-model","skills":[],
				"tools":[{"type":"function","function":{"name":"calculate","strict":false,
					"parameters":{"type":"object","properties":{"value":{"const":9007199254740993}}}}},
					{"type":"function","function":{"name":"get_current_time"}}]}`,
		},
		{
			name: "all omitted fields are inherited", config: `{}`,
			want: `{"instructions":"Original instructions","model":"baseline-model",
				"skills":[{"name":"original","description":"Original skill","body":"Original body"}],
				"tools":[{"type":"function","function":{"name":"calculate","strict":false,
					"parameters":{"type":"object","properties":{"value":{"const":9007199254740993}}}}},
					{"type":"function","function":{"name":"get_current_time"}}]}`,
		},
		{
			name: "explicit fields and empty arrays win",
			config: `{"system_prompt":"Candidate instructions","model":"candidate-model",
				"skills":[],"tools":[],"temperature":0}`,
			want: `{"system_prompt":"Candidate instructions","model":"candidate-model",
				"skills":[],"tools":[],"temperature":0}`,
		},
		{
			name: "camelCase instructions are not shadowed by baseline",
			config: `{"systemPrompt":"Candidate instructions","model":"candidate-model",
				"skills":[],"tools":[]}`,
			want: `{"systemPrompt":"Candidate instructions","model":"candidate-model",
				"skills":[],"tools":[]}`,
		},
		{
			name: "all mutation types override candidate and baseline",
			config: `{"system_prompt":"Old instructions","systemPrompt":"Also old","instructions":"Old",
				"model":"old-model","skills":[{"name":"old"}],"tools":[{"type":"function"}]}`,
			mutations: []CandidateMutation{
				{Type: "instructions", Value: json.RawMessage(`"Mutated instructions"`)},
				{Type: "model", Value: json.RawMessage(`"mutated-model"`)},
				{Type: "skills", Value: json.RawMessage(`[{"name":"new","description":"New skill","body":"New body"}]`)},
				{Type: "tools", Value: json.RawMessage(`[{"type":"function","function":{"name":"new"}}]`)},
			},
			want: `{"instructions":"Mutated instructions","model":"mutated-model",
				"skills":[{"name":"new","description":"New skill","body":"New body"}],
				"tools":[{"type":"function","function":{"name":"new"}}]}`,
		},
		{
			name: "empty mutations do not restore baseline arrays",
			config: `{"instructions":"Candidate","model":"candidate-model",
				"skills":[{"name":"old"}],"tools":[{"type":"function"}]}`,
			mutations: []CandidateMutation{
				{Type: "skills", Value: json.RawMessage(`[]`)},
				{Type: "tools", Value: json.RawMessage(`[]`)},
			},
			want: `{"instructions":"Candidate","model":"candidate-model","skills":[],"tools":[]}`,
		},
		{
			name: "no submitted baseline leaves optional fields absent", baseline: `null`,
			config: `{"instructions":"Complete instructions","model":"candidate-model"}`,
			want:   `{"instructions":"Complete instructions","model":"candidate-model"}`,
		},
		{
			name: "empty baseline arrays remain explicit", config: `{}`,
			baseline: `{"system_prompt":"Original","current_model":"baseline","skills":[],"tools":[]}`,
			want:     `{"instructions":"Original","model":"baseline","skills":[],"tools":[]}`,
		},
		{
			name: "missing mutation value", config: `{}`,
			mutations: []CandidateMutation{{Type: "instructions"}},
			wantErr:   `mutation "instructions" is missing its value`,
		},
		{
			name: "unsupported mutation", config: `{}`,
			mutations: []CandidateMutation{{Type: "future", Value: json.RawMessage(`true`)}},
			wantErr:   `unsupported candidate mutation "future"`,
		},
		{
			name: "invalid array mutation", config: `{}`,
			mutations: []CandidateMutation{{Type: "tools", Value: json.RawMessage(`null`)}},
			wantErr:   "candidate config tools must be an array",
		},
		{
			name: "invalid string mutation", config: `{}`,
			mutations: []CandidateMutation{{Type: "model", Value: json.RawMessage(`42`)}},
			wantErr:   "candidate config model must be a string",
		},
		{
			name: "null instruction mutation", config: `{}`,
			mutations: []CandidateMutation{{Type: "instructions", Value: json.RawMessage(` null `)}},
			wantErr:   "candidate config instructions must be a string",
		},
		{
			name: "null candidate config", config: `null`,
			wantErr: "candidate config must be a JSON object",
		},
		{
			name: "baseline read failure is not silently ignored", config: `{"instructions":"Candidate"}`,
			baselineStatus: http.StatusForbidden,
			wantErr:        "loading candidate baseline from optimization job",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			snapshot := tt.baseline
			if snapshot == "" {
				snapshot = baseline
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "v1", r.URL.Query().Get("api-version"))
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/agent_optimization_jobs/job1":
					if tt.baselineStatus != 0 {
						w.WriteHeader(tt.baselineStatus)
						return
					}
					_, _ = w.Write([]byte(
						`{"optimization_configuration":{"baseline_agent_configuration":` + snapshot + `}}`))
				case "/agent_optimization_jobs/job1/candidates/c1":
					response := CandidateManifest{
						CandidateID: "c1", JobID: "job1",
						Files:  []CandidateFile{{Type: "config", Path: "config.json"}},
						Output: &CandidateOutput{Type: "agent_optimization", Mutations: tt.mutations},
					}
					assert.NoError(t, json.NewEncoder(w).Encode(response))
				case "/agent_optimization_jobs/job1/files":
					assert.Equal(t, "c1", r.URL.Query().Get("candidate_id"))
					assert.Equal(t, "config.json", r.URL.Query().Get("path"))
					_, _ = w.Write([]byte(tt.config))
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			config, err := newTestClient(server.URL).GetCandidateConfig(t.Context(), "job1", "c1")
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Nil(t, config)
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(config))
			if tt.name == "instruction-only optimization retains both baseline tools" {
				assert.Contains(t, string(config), "9007199254740993")
			}
		})
	}
}

func TestLoadCandidates(t *testing.T) {
	t.Parallel()
	for _, stalled := range []bool{false, true} {
		t.Run(map[bool]string{false: "all pages", true: "stalled cursor"}[stalled], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				assert.Equal(t, "/agent_optimization_jobs/job1/candidates", r.URL.Path)
				assert.Equal(t, "asc", r.URL.Query().Get("order"))
				assert.Equal(t, "100", r.URL.Query().Get("limit"))
				if calls == 1 {
					assert.Empty(t, r.URL.Query().Get("after"))
					_, _ = w.Write([]byte(`{"data":[{"candidate_id":"c1","status":"completed"}],
						"last_id":"c1","has_more":true}`))
				} else {
					assert.Equal(t, "c1", r.URL.Query().Get("after"))
					if stalled {
						_, _ = w.Write([]byte(`{"data":[],"last_id":"c1","has_more":true}`))
					} else {
						_, _ = w.Write([]byte(`{"data":[{"candidate_id":"c2","status":"evaluating"}],
							"last_id":"c2","has_more":false}`))
					}
				}
			}))
			defer server.Close()
			job := &OptimizeJobStatus{ID: "job1"}
			err := newTestClient(server.URL).LoadCandidates(t.Context(), job)
			if stalled {
				require.ErrorContains(t, err, "did not advance")
			} else {
				require.NoError(t, err)
				require.Len(t, job.Candidates(), 2)
				assert.Equal(t, "c2", job.Candidates()[1].CandidateID)
			}
			assert.Equal(t, 2, calls)
		})
	}
}

func TestGetCandidateFile(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/agent_optimization_jobs/job1/files", r.URL.Path)
		assert.Equal(t, "c &1", r.URL.Query().Get("candidate_id"))
		assert.Equal(t, "skills/a b/SKILL.md", r.URL.Query().Get("path"))
		_, _ = w.Write([]byte("# Skill"))
	}))
	defer server.Close()
	content, err := newTestClient(server.URL).GetCandidateFile(t.Context(), "job1", "c &1", "skills/a b/SKILL.md")
	require.NoError(t, err)
	assert.Equal(t, "# Skill", content)
}

func TestReportDeploymentWithHeaders(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/agent_optimization_jobs/j1/candidates/c1:promote", r.URL.Path)
		assert.Equal(t, "true", r.Header.Get(PromotionReportOnlyHeader))
		var actual DeploymentReport
		require.NoError(t, json.NewDecoder(r.Body).Decode(&actual))
		assert.Equal(t, "agent", actual.AgentName)
		assert.Equal(t, "7", actual.AgentVersion)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	require.NoError(t, newTestClient(server.URL).ReportDeploymentWithHeaders(t.Context(), "j1",
		&DeploymentReport{CandidateID: "c1", AgentName: "agent", AgentVersion: "7"},
		map[string]string{PromotionReportOnlyHeader: "true"}))
}
