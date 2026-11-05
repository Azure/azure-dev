// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const skillOptimizeTestConfig = `{
	"display_name":"trace learning",
	"target_configuration":{"type":"foundry_agent","name":"demo-agent","version":"4"},
	"optimization_model_configuration":{"model":"optimizer"},
	"optimization_configuration":{
		"type":"skill_optimization",
		"context":[
			{"type":"agent_traces","trace_window":{"type":"fixed",
				"start_time":1792454400,"end_time":1793059200,"max_traces":10}},
			{"type":"steering_prompt","prompt":"Prefer concise reusable procedures."},
			{"type":"skill_references","skill_references":[]}
		]
	}
}`

const skillOptimizeTestCandidate = `{
	"candidate_id":"candidate-skill","job_id":"job-skill","name":"learned skills","status":"completed","started_at":1,
	"rationale":"Created a grounded procedure and retained the unchanged baseline.",
	"execution_summary":{"updated_at":2,"steps":[
		{"name":"collect_feedback","status":"completed","summary":"Included existing human and rubric feedback.",
			"step_details":{"traces_with_human_annotations_count":1,"traces_with_evaluation_results_count":2}},
		{"name":"publish_skills","status":"completed","step_details":{"published_skills_count":1}}
	]},
	"resolved_context":{"source_trace_ids":["trace-a","trace-b"],"agent_skills":[]},
	"output":{"type":"skill_optimization","skill_results":[
		{"change_type":"create","status":"created",
			"output_skill":{"type":"foundry_skill","name":"triage","version":"3"},
			"rationale":"Repeated evidence supports a diagnostic procedure.",
			"validations":[{"type":"artifact","id":"quality","status":"warning","details":"Limited coverage.","score":0.5}]},
		{"change_type":"no_op","status":"no_change",
			"input_skill":{"type":"foundry_skill","name":"existing","version":"2"},
			"output_skill":{"type":"foundry_skill","name":"existing"}},
		{"change_type":"create","status":"rejected","output_skill":{"type":"foundry_skill","name":"unsupported"},
			"error":{"code":"evidence_provenance","message":"Insufficient evidence."}},
		{"change_type":"create","status":"failed","output_skill":{"type":"foundry_skill","name":"retry"},
			"error":{"code":"quota_exceeded","message":"Model capacity unavailable."}},
		{"change_type":"create","status":"pending","output_skill":{"type":"foundry_skill","name":"pending"}},
		{"change_type":"create","status":"publication_unknown","output_skill":{"type":"foundry_skill","name":"uncertain"},
			"error":{"code":"publication_unknown","message":"The publication receipt is unavailable."}}
	]},
	"files":[{"path":"trace-analysis-test.json","type":"trace_analysis","size_in_bytes":80}],
	"future_service_field":{"preserve":true}
}`

func skillOptimizeRequest(t *testing.T) optimize_api.OptimizeRequest {
	t.Helper()
	var request optimize_api.OptimizeRequest
	require.NoError(t, json.Unmarshal([]byte(skillOptimizeTestConfig), &request))
	return request
}

func TestSkillOptimizeConfigRoundTrip(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"fixed", "trace_ids", "agentless", "evaluation", "generated evaluation", "additional skills"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			request := skillOptimizeRequest(t)
			switch mode {
			case "trace_ids":
				request.OptimizationConfiguration.Context[0].TraceWindow = &optimize_api.SkillTraceWindow{
					Type: "trace_ids", TraceIDs: []string{"trace-a", "trace-b"}, LookbackHours: new(168),
				}
			case "agentless":
				request.TargetConfiguration = nil
				request.OptimizationConfiguration.Context[0].TraceConnectionConfiguration =
					&optimize_api.SkillTraceConnectionConfiguration{
						Type: "application_insights", AgentName: new("namespace/service"),
						ResourceID: "/subscriptions/00000000-0000-0000-0000-000000000000" +
							"/resourceGroups/test/providers/Microsoft.Insights/components/traces",
					}
			case "evaluation", "generated evaluation":
				request.OptimizationConfiguration.EvaluationConfiguration = &optimize_api.EvaluationConfiguration{
					ValidationSet: &optimize_api.SetInput{
						Type:   "target_completion",
						Source: optimize_api.Dataset{Type: "dataset_reference", Name: "validation", Version: "2"},
					},
					Evaluators:      []optimize_api.EvaluatorRef{{Name: "rubric", Version: "1"}},
					EvaluationModel: optimize_api.EvaluationModelConfiguration{Model: "judge"},
				}
				if mode == "generated evaluation" {
					request.OptimizationConfiguration.EvaluationConfiguration.Evaluators = nil
					request.OptimizationConfiguration.EvaluationConfiguration.EvaluatorGenerationConfiguration =
						&optimize_api.EvaluatorGenerationConfiguration{
							Type: "agent_traces", Prompt: new("Check whether responses address the user's request."),
						}
				}
			case "additional skills":
				request.OptimizationConfiguration.AdditionalSkills = []optimize_api.FoundrySkillReference{
					{Type: "foundry_skill", Name: "existing", Version: "2"},
				}
			}
			authored, err := json.Marshal(request)
			require.NoError(t, err)
			configPath := writeTestFile(t, t.TempDir(), "skill.json", string(authored))
			cfg, err := LoadOptimizeConfig(configPath)
			require.NoError(t, err)
			action := &OptimizeAction{flags: &optimizeFlags{}, noPrompt: true}
			require.NoError(t, action.applyOverrides(t.Context(), cfg, t.TempDir()))
			actual, warnings, err := cfg.ToRequest()
			require.NoError(t, err)
			assert.Empty(t, warnings)
			encoded, err := json.Marshal(actual)
			require.NoError(t, err)
			assert.JSONEq(t, string(authored), string(encoded))
			unchanged, err := json.Marshal(cfg.Request)
			require.NoError(t, err)
			assert.JSONEq(t, string(authored), string(unchanged))
			assert.Nil(t, actual.OptimizationConfiguration.BaselineAgentConfiguration)
			assert.Nil(t, actual.OptimizationConfiguration.CandidateSearchConfiguration)
			assert.Empty(t, cfg.DatasetFile)
			if mode != "evaluation" && mode != "generated evaluation" {
				assert.Nil(t, actual.OptimizationConfiguration.EvaluationConfiguration)
			} else {
				assert.NotContains(t, string(encoded), `"training_set"`)
			}
		})
	}
}

func TestSkillOptimizeEvaluatorArrayPresenceOnSubmission(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	for _, tc := range []struct {
		name       string
		evaluation string
	}{
		{"generated evaluator without list", `{
			"evaluation_model":{"model":"judge"},
			"evaluator_generation_configuration":{"type":"agent_traces"}}`},
		{"generated evaluator with empty list", `{
			"evaluation_model":{"model":"judge"},"evaluators":[],
			"evaluator_generation_configuration":{"type":"agent_traces"}}`},
		{"supplied evaluator", `{
			"evaluation_model":{"model":"judge"},
			"evaluators":[{"name":"rubric","version":"1"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authored := strings.Replace(skillOptimizeTestConfig, `"type":"skill_optimization",`,
				`"type":"skill_optimization","evaluation_configuration":`+tc.evaluation+`,`, 1)
			configPath := writeTestFile(t, t.TempDir(), "skill.json", authored)
			cfg, err := LoadOptimizeConfig(configPath)
			require.NoError(t, err)
			require.NoError(t, cfg.Validate())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/agent_optimization_jobs", r.URL.Path)
				var submitted json.RawMessage
				if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&submitted)) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				assert.JSONEq(t, authored, string(submitted))
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"id":"job-skill","status":"queued"}`))
			}))
			defer server.Close()
			action := &OptimizeAction{
				flags: &optimizeFlags{}, client: newTestOptimizeClient(server.URL),
			}
			response, _, err := action.submitJob(t.Context(), &bytes.Buffer{}, server.URL, cfg, "")
			require.NoError(t, err)
			assert.Equal(t, "job-skill", response.ID)
			unchanged, err := json.Marshal(cfg.Request)
			require.NoError(t, err)
			assert.JSONEq(t, authored, string(unchanged))
		})
	}
}

func TestSkillOptimizeInvalidConfigs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*optimize_api.OptimizeRequest)
		want   string
	}{
		{"missing model", func(r *optimize_api.OptimizeRequest) { r.OptimizationModelConfiguration.Model = "" },
			"optimization_model"},
		{"missing contexts", func(r *optimize_api.OptimizeRequest) { r.OptimizationConfiguration.Context = nil },
			"exactly one"},
		{"duplicate trace context", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context = append(r.OptimizationConfiguration.Context, r.OptimizationConfiguration.Context[0])
		}, "exactly one"},
		{"duplicate steering", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context = append(r.OptimizationConfiguration.Context, r.OptimizationConfiguration.Context[1])
		}, "at most one"},
		{"empty guidance", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[1].Prompt = new(" ")
		}, "non-empty prompt"},
		{"oversized guidance", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[1].Prompt = new(strings.Repeat("\u754c", 34134))
		}, "102400 UTF-8 bytes"},
		{"floating target version", func(r *optimize_api.OptimizeRequest) {
			r.TargetConfiguration.Version = "latest"
		}, "optional exact version"},
		{"invalid model", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationModelConfiguration.Model = "models/optimizer"
		}, "optimization_model"},
		{"skill training set", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.EvaluationConfiguration = &optimize_api.EvaluationConfiguration{
				TrainingSet: &optimize_api.SetInput{},
			}
		}, "does not accept training_set"},
		{"evaluation without judge", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.EvaluationConfiguration = &optimize_api.EvaluationConfiguration{}
		}, "evaluation_model.model"},
		{"evaluation without evaluators", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.EvaluationConfiguration = &optimize_api.EvaluationConfiguration{
				EvaluationModel: optimize_api.EvaluationModelConfiguration{Model: "judge"},
			}
		}, "requires evaluators or evaluator_generation_configuration"},
		{"unsupported evaluator generation", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.EvaluationConfiguration = &optimize_api.EvaluationConfiguration{
				EvaluationModel:                  optimize_api.EvaluationModelConfiguration{Model: "judge"},
				EvaluatorGenerationConfiguration: &optimize_api.EvaluatorGenerationConfiguration{Type: "dataset"},
			}
		}, "must be agent_traces"},
		{"unexpected prompt", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].Prompt = new("")
		}, "cannot contain prompt"},
		{"missing trace window", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].TraceWindow = nil
		}, "requires trace_window"},
		{"invalid timestamp", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].TraceWindow.StartTime =
				optimize_api.SkillTraceTimestamp(`"2026-10-20"`)
		}, "integer Unix seconds"},
		{"inverted window", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].TraceWindow.EndTime = optimize_api.SkillTraceTimestamp(`1792368000`)
		}, "end_time after start_time"},
		{"future window", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].TraceWindow.EndTime =
				optimize_api.SkillTraceTimestamp(strconv.FormatInt(time.Now().Add(24*time.Hour).Unix(), 10))
		}, "not in the future"},
		{"zero limit", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].TraceWindow.MaxTraces = new(0)
		}, "between 1 and 100"},
		{"large limit", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].TraceWindow.MaxTraces = new(101)
		}, "between 1 and 100"},
		{"mixed windows", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].TraceWindow.TraceIDs = []string{}
		}, "cannot contain trace_ids"},
		{"duplicate trace IDs", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].TraceWindow = &optimize_api.SkillTraceWindow{
				Type: "trace_ids", TraceIDs: []string{"a", "a"}, LookbackHours: new(1),
			}
		}, "must be unique"},
		{"missing lookback", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].TraceWindow = &optimize_api.SkillTraceWindow{
				Type: "trace_ids", TraceIDs: []string{"a"},
			}
		}, "lookback_hours"},
		{"agentless without source", func(r *optimize_api.OptimizeRequest) { r.TargetConfiguration = nil },
			"trace_connection_configuration"},
		{"legacy trace provider", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].TraceConnectionConfiguration =
				&optimize_api.SkillTraceConnectionConfiguration{
					Type:       "appinsights",
					ResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Insights/components/traces",
				}
		}, "requires type application_insights"},
		{"abbreviated trace provider", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].TraceConnectionConfiguration =
				&optimize_api.SkillTraceConnectionConfiguration{
					Type:       "app_insights",
					ResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Insights/components/traces",
				}
		}, "requires type application_insights"},
		{"target with service name", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.Context[0].TraceConnectionConfiguration =
				&optimize_api.SkillTraceConnectionConfiguration{
					Type: "application_insights", AgentName: new("service"),
					ResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Insights/components/traces",
				}
		}, "cannot be combined"},
		{"unversioned reference", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.AdditionalSkills = []optimize_api.FoundrySkillReference{
				{Type: "foundry_skill", Name: "baseline", Version: "latest"},
			}
		}, "exact version"},
		{"invalid skill name", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.AdditionalSkills = []optimize_api.FoundrySkillReference{
				{Type: "foundry_skill", Name: "Baseline Skill", Version: "1"},
			}
		}, "lowercase hyphenated name"},
		{"agentless wildcard", func(r *optimize_api.OptimizeRequest) {
			r.TargetConfiguration = nil
			r.OptimizationConfiguration.Context[0].TraceConnectionConfiguration =
				&optimize_api.SkillTraceConnectionConfiguration{
					Type: "application_insights", AgentName: new("service*"),
					ResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Insights/components/traces",
				}
		}, "without wildcards or KQL"},
		{"conflicting versions", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.AdditionalSkills = []optimize_api.FoundrySkillReference{
				{Type: "foundry_skill", Name: "baseline", Version: "1"},
				{Type: "foundry_skill", Name: "baseline", Version: "2"},
			}
		}, "conflicting versions"},
		{"agent-only baseline", func(r *optimize_api.OptimizeRequest) {
			r.OptimizationConfiguration.BaselineAgentConfiguration = &optimize_api.BaselineAgentConfiguration{}
		}, "does not support"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := skillOptimizeRequest(t)
			tt.change(&request)
			data, err := json.Marshal(request)
			require.NoError(t, err)
			configPath := writeTestFile(t, t.TempDir(), "invalid.json", string(data))
			cfg, err := LoadOptimizeConfig(configPath)
			if err == nil {
				err = cfg.Validate()
			}
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestSkillOptimizeFlagsFailBeforeAuthentication(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	configPath := writeTestFile(t, t.TempDir(), "skill.json", skillOptimizeTestConfig)
	for _, flag := range []string{"--dataset=golden:1", "--eval-model=judge", "--evaluator=rubric", "--max-candidates=2"} {
		t.Run(flag, func(t *testing.T) {
			cmd := newOptimizeCommand(&azdext.ExtensionContext{NoPrompt: true})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs([]string{"--config", configPath, flag})
			err := cmd.Execute()
			require.ErrorContains(t, err, "cannot be used with a skill_optimization")
			assert.NotContains(t, output.String(), "Job ID")
			assert.NotContains(t, err.Error(), "credential")
		})
	}
	cfg, err := LoadOptimizeConfig(configPath)
	require.NoError(t, err)
	action := &OptimizeAction{flags: &optimizeFlags{optimizationModel: "override"}, noPrompt: true}
	require.NoError(t, action.applyOverrides(t.Context(), cfg, ""))
	request, _, err := cfg.ToRequest()
	require.NoError(t, err)
	assert.Equal(t, "override", request.OptimizationModelConfiguration.Model)
	assert.Equal(t, "optimizer", cfg.Request.OptimizationModelConfiguration.Model)
}

func TestSkillOptimizeAgentlessResolveAndFlagConflict(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	request := skillOptimizeRequest(t)
	request.TargetConfiguration = nil
	request.OptimizationConfiguration.Context[0].TraceConnectionConfiguration =
		&optimize_api.SkillTraceConnectionConfiguration{
			Type: "application_insights", AgentName: new("service"),
			ResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Insights/components/traces",
		}
	data, err := json.Marshal(request)
	require.NoError(t, err)
	file := writeTestFile(t, t.TempDir(), "agentless.json", string(data))
	action := &OptimizeAction{flags: &optimizeFlags{configFile: file}, noPrompt: true}
	cfg, _, project, err := action.resolveConfig(t.Context())
	require.NoError(t, err)
	assert.Empty(t, project)
	assert.Empty(t, cfg.Agent.Name)
	cmd := newOptimizeCommand(&azdext.ExtensionContext{NoPrompt: true})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--config", file, "--agent=override"})
	require.ErrorContains(t, cmd.Execute(), "cannot override an agentless")
}

func TestSkillOptimizeResolvedAgentIdentity(t *testing.T) {
	t.Setenv("AGENT_DEFINITION_PATH", "")
	for _, tc := range []struct {
		name, agent, deployedName, deployedVersion, wantName, wantVersion string
		project, invalid                                                  bool
	}{
		{name: "standalone override", agent: "other-agent", wantName: "other-agent"},
		{name: "padded standalone override", agent: " demo-agent ", invalid: true},
		{name: "project override", agent: "assistant", project: true,
			deployedName: "deployed-agent", deployedVersion: "8", wantName: "deployed-agent", wantVersion: "8"},
		{name: "padded deployed name", agent: "assistant", project: true,
			deployedName: " deployed-agent ", deployedVersion: "8", invalid: true},
		{name: "latest deployed version", agent: "assistant", project: true,
			deployedName: "deployed-agent", deployedVersion: "latest", invalid: true},
		{name: "default deployed version", agent: "assistant", project: true,
			deployedName: "deployed-agent", deployedVersion: "default", invalid: true},
		{name: "authored pin without override", project: true,
			deployedName: "deployed-agent", deployedVersion: "8", wantName: "demo-agent", wantVersion: "4"},
		{name: "ignore unselected deployed alias", project: true,
			deployedName: "deployed-agent", deployedVersion: "latest", wantName: "demo-agent", wantVersion: "4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			projectServer := &recordingProjectServer{nilProject: !tc.project, projectPath: root}
			if tc.project {
				projectServer.existing = map[string]*azdext.ServiceConfig{
					"assistant": optimizeTestDirectService(t, root),
				}
			}
			envServer := &testEnvironmentServiceServer{
				environments: map[string]*azdext.Environment{"dev": {Name: "dev"}},
				values: map[string]map[string]string{"dev": {
					"AGENT_ASSISTANT_NAME": tc.deployedName, "AGENT_ASSISTANT_VERSION": tc.deployedVersion,
				}},
			}
			t.Setenv("AZD_SERVER", newProjectRecorderServer(t, projectServer, envServer))
			var submissions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				submissions.Add(1)
				var submitted optimize_api.OptimizeRequest
				if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&submitted)) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				assert.Equal(t, &optimize_api.AgentIdentifier{
					Type: "foundry_agent", Name: tc.wantName, Version: tc.wantVersion,
				}, submitted.TargetConfiguration)
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"id":"job-skill","status":"queued"}`))
			}))
			defer server.Close()
			action := &OptimizeAction{
				flags: &optimizeFlags{
					configFile: writeTestFile(t, root, "skill.json", skillOptimizeTestConfig),
					agent:      tc.agent, noWait: true,
					optimizeConnectionFlags: optimizeConnectionFlags{endpoint: server.URL},
				},
				envName: "dev", noPrompt: true, client: newTestOptimizeClient(server.URL),
			}
			cfg, _, project, err := action.resolveConfig(t.Context())
			require.NoError(t, err)
			err = action.applyOverrides(t.Context(), cfg, project)
			if tc.invalid {
				require.ErrorContains(t, err, "target_configuration")
			} else {
				require.NoError(t, err)
			}
			request, _, err := cfg.ToRequest()
			if tc.invalid {
				require.ErrorContains(t, err, "target_configuration")
				assert.Nil(t, request)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantName, request.TargetConfiguration.Name)
				assert.Equal(t, tc.wantVersion, request.TargetConfiguration.Version)
			}
			assert.Equal(t, "demo-agent", cfg.Request.TargetConfiguration.Name)
			assert.Equal(t, "4", cfg.Request.TargetConfiguration.Version)
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			err = action.Run(t.Context(), cmd)
			if tc.invalid {
				require.ErrorContains(t, err, "target_configuration")
				assert.Zero(t, submissions.Load())
				assert.NotContains(t, output.String(), "Optimizing")
			} else {
				require.NoError(t, err)
				assert.EqualValues(t, 1, submissions.Load())
			}
		})
	}
}

func TestSkillOptimizeUnknownFieldsAreNotDropped(t *testing.T) {
	t.Parallel()
	input := strings.Replace(skillOptimizeTestConfig, `"max_traces":10`, `"max_traces":10,"misspelled":true`, 1)
	file := writeTestFile(t, t.TempDir(), "unknown.json", input)
	_, err := LoadOptimizeConfig(file)
	require.ErrorContains(t, err, `unknown field "misspelled"`)
}

func TestSkillOptimizeLocalLifecycle(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	var polls atomic.Int32
	var submissions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "v1", r.URL.Query().Get("api-version"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/agent_optimization_jobs":
			submissions.Add(1)
			var request json.RawMessage
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			assert.JSONEq(t, skillOptimizeTestConfig, string(request))
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"job-skill","status":"queued"}`))
		case r.URL.Path == "/agent_optimization_jobs/job-skill":
			status := "in_progress"
			if polls.Add(1) > 1 {
				status = "succeeded"
			}
			_, _ = w.Write([]byte(`{"id":"job-skill","status":"` + status + `",
				"optimization_configuration":{"type":"skill_optimization"},
				"activity_progress":{"activities":[{"title":"Deriving learnings\u001b[2J","status":"in_progress"}]}}`))
		case r.URL.Path == "/agent_optimization_jobs/job-skill/candidates":
			_, _ = w.Write([]byte(`{"data":[{"candidate_id":"candidate-skill","name":"learned skills",
				"status":"completed","output":{"type":"skill_optimization"}}],"has_more":false}`))
		case r.URL.Path == "/agent_optimization_jobs/job-skill/candidates/candidate-skill":
			_, _ = w.Write([]byte(skillOptimizeTestCandidate))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	configPath := writeTestFile(t, t.TempDir(), "skill.json", skillOptimizeTestConfig)
	action := &OptimizeAction{
		flags: &optimizeFlags{configFile: configPath, pollInterval: 1,
			optimizeConnectionFlags: optimizeConnectionFlags{endpoint: server.URL}},
		noPrompt: true, client: newTestOptimizeClient(server.URL),
	}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	require.NoError(t, action.Run(t.Context(), cmd))
	assert.EqualValues(t, 1, submissions.Load())
	assert.EqualValues(t, 2, polls.Load())
	for _, expected := range []string{
		"Deriving learnings", "collect_feedback", "Published version: 3", "Baseline version: 2",
		"Outcome: rejected", "Outcome: failed", "Outcome: pending", "Outcome: publication_unknown",
		"Model capacity unavailable.", "verify the skill before retrying",
		"trace-analysis-test.json", "optimize download", "Limited coverage.",
	} {
		assert.Contains(t, output.String(), expected)
	}
	assert.NotContains(t, output.String(), "Deploy the best candidate")
	assert.NotContains(t, output.String(), "candidate agents as draft")
	assert.NotContains(t, output.String(), "Evaluating")
	assert.Contains(t, output.String(), `Deriving learnings\x1b[2J`)
	assert.NotContains(t, output.String(), "Deriving learnings\x1b[2J")
}

func TestSkillOptimizeDoesNotSaveAgentBaseline(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"job-skill","status":"queued"}`))
	}))
	defer server.Close()
	file := writeTestFile(t, t.TempDir(), "skill.json", skillOptimizeTestConfig)
	cfg, err := LoadOptimizeConfig(file)
	require.NoError(t, err)
	action := &OptimizeAction{flags: &optimizeFlags{}, client: newTestOptimizeClient(server.URL)}
	root := t.TempDir()
	_, _, err = action.submitJob(t.Context(), &bytes.Buffer{}, server.URL, cfg, root)
	require.NoError(t, err)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.Empty(t, cfg.Agent.ConfigFile)
}

func TestSkillOptimizePreservesAgentJobState(t *testing.T) {
	for _, tc := range []struct {
		name        string
		kind        string
		serviceName string
		agentless   bool
	}{
		{"skill service", optimize_api.SkillOptimizationType, "assistant", false},
		{"skill agent fallback", optimize_api.SkillOptimizationType, "", false},
		{"agentless skill", optimize_api.SkillOptimizationType, "assistant", true},
		{"agent optimization", "agent_optimization", "assistant", false},
		{"prompt optimization", "prompt_optimization", "assistant", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultOptimizeConfig("demo-agent")
			cfg.Options.OptimizationModel = "optimizer"
			if tc.kind == optimize_api.SkillOptimizationType {
				request := skillOptimizeRequest(t)
				if tc.agentless {
					request.TargetConfiguration = nil
					request.OptimizationConfiguration.Context[0].TraceConnectionConfiguration =
						&optimize_api.SkillTraceConnectionConfiguration{
							Type: "application_insights", AgentName: new("service"),
							ResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Insights/components/traces",
						}
				}
				cfg = optimizeConfigFromRequest(&request)
			} else {
				cfg.Request = &optimize_api.OptimizeRequest{
					OptimizationConfiguration: optimize_api.OptimizationConfiguration{Type: tc.kind},
				}
			}
			keyName := optimizeEnvKeyName(tc.serviceName, "demo-agent")
			jobKey := optimizeJobIDKeyForAgent(keyName)
			candidateKey := "AGENT_" + toServiceKey(keyName) + "_OPTIMIZATION_CANDIDATE_ID"
			env := &testEnvironmentServiceServer{
				environments: map[string]*azdext.Environment{"dev": {Name: "dev"}},
				values: map[string]map[string]string{"dev": {
					jobKey: "job-agent", candidateKey: "candidate-agent", optimizeLastJobIDKey: "job-agent",
					"AGENT_" + toServiceKey(keyName) + "_NAME":    "demo-agent",
					"AGENT_" + toServiceKey(keyName) + "_VERSION": "7",
				}},
			}
			t.Setenv("AZD_SERVER", newProjectRecorderServer(t, &recordingProjectServer{}, env))
			var promotions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				switch r.URL.Path {
				case "/agent_optimization_jobs":
					var request optimize_api.OptimizeRequest
					if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					assert.Equal(t, tc.kind, request.OptimizationConfiguration.Type)
					if tc.agentless && assert.NotEmpty(t, request.OptimizationConfiguration.Context) {
						connection := request.OptimizationConfiguration.Context[0].TraceConnectionConfiguration
						if assert.NotNil(t, connection) {
							assert.Equal(t, "application_insights", connection.Type)
						}
					}
					w.WriteHeader(http.StatusAccepted)
					_, _ = w.Write([]byte(`{"id":"job-new","status":"queued"}`))
				case "/agent_optimization_jobs/job-agent/candidates/candidate-agent:promote":
					promotions.Add(1)
					w.WriteHeader(http.StatusOK)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			action := &OptimizeAction{
				flags: &optimizeFlags{}, serviceName: tc.serviceName, envName: "dev",
				client: newTestOptimizeClient(server.URL),
			}
			_, _, err := action.submitJob(t.Context(), &bytes.Buffer{}, server.URL, cfg, "")
			require.NoError(t, err)
			assert.Equal(t, "job-new", env.values["dev"][optimizeLastJobIDKey])
			assert.Equal(t, "candidate-agent", env.values["dev"][candidateKey])
			if tc.kind != optimize_api.SkillOptimizationType {
				assert.Equal(t, "job-new", env.values["dev"][jobKey])
				assert.Equal(t, []string{jobKey, optimizeLastJobIDKey}, env.setKeys)
				return
			}
			assert.Equal(t, "job-agent", env.values["dev"][jobKey])
			assert.Equal(t, []string{optimizeLastJobIDKey}, env.setKeys)
			assert.Equal(t, "job-agent", loadOptimizeJobIDForAgent(t.Context(), keyName, "dev"))
			azdClient := newOptimizeTestAzdClient(t, env)
			reportOptimizationDeployments(t.Context(), azdClient,
				[]*azdext.ServiceConfig{{Name: keyName}}, "dev", server.URL, "", newTestOptimizeClient)
			assert.EqualValues(t, 1, promotions.Load())
			assert.Empty(t, env.values["dev"][candidateKey])
			assert.Equal(t, "job-agent", env.values["dev"][jobKey])
		})
	}
}

func TestSkillOptimizeTerminalText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, value, want string
	}{
		{"ordinary prose", "Résumé 中文\n\tSecond line", "Résumé 中文\n\tSecond line"},
		{"cursor control", "before\x1b[2Jafter", `before\x1b[2Jafter`},
		{"hyperlink control", "\x1b]8;;https://example.invalid\aLink\x1b]8;;\x1b\\",
			`\x1b]8;;https://example.invalid\aLink\x1b]8;;\x1b\`},
		{"clipboard control", "\x1b]52;c;payload\a", `\x1b]52;c;payload\a`},
		{"line overwrite", "one\r\ntwo\b", "one\\r\ntwo\\b"},
		{"C1 controls", "\u009b2J\u009d52;c;payload\u009c", `\u009b2J\u009d52;c;payload\u009c`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, optimizeTerminalText(tc.value))
		})
	}
	for r := rune(0); r <= '\u009f'; r++ {
		if !unicode.IsControl(r) || r == '\n' || r == '\t' {
			continue
		}
		assert.Equal(t, -1, strings.IndexFunc(optimizeTerminalText(string(r)), unicode.IsControl),
			"control %s must not reach the terminal", strconv.QuoteRune(r))
	}
}

func TestSkillOptimizeCandidateValidationCoverage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		statuses []string
	}{
		{"unreported", nil},
		{"empty", []string{}},
		{"all passed", []string{"passed", "passed"}},
		{"mixed", []string{"passed", "warning", "failed", "skipped"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var candidate optimize_api.CandidateResult
			require.NoError(t, json.Unmarshal([]byte(skillOptimizeTestCandidate), &candidate))
			candidate.Output.SkillResults[0].Validations = nil
			if tc.statuses != nil {
				candidate.Output.SkillResults[0].Validations = []optimize_api.SkillValidation{}
			}
			for i, status := range tc.statuses {
				candidate.Output.SkillResults[0].Validations = append(candidate.Output.SkillResults[0].Validations,
					optimize_api.SkillValidation{
						Type: "artifact", ID: "check-" + strconv.Itoa(i), Status: status, Details: "Reported coverage.",
					})
			}
			var output bytes.Buffer
			printSkillOptimizeCandidate(&output, "job", candidate)
			assert.Equal(t, len(tc.statuses), strings.Count(output.String(), "    Validation "))
			for i, status := range tc.statuses {
				assert.Contains(t, output.String(),
					"Validation artifact/check-"+strconv.Itoa(i)+": "+status+" - Reported coverage.")
			}
		})
	}
}

func TestSkillOptimizeCandidateEscapesEveryRenderedField(t *testing.T) {
	t.Parallel()
	const value = "before\x1b[2J\a\r\b\x7f\u009bafter"
	for _, tc := range []struct {
		name  string
		field func(*optimize_api.CandidateResult) *string
	}{
		{"candidate name", func(c *optimize_api.CandidateResult) *string { return &c.Name }},
		{"candidate ID", func(c *optimize_api.CandidateResult) *string { return &c.CandidateID }},
		{"candidate status", func(c *optimize_api.CandidateResult) *string { return &c.Status }},
		{"candidate rationale", func(c *optimize_api.CandidateResult) *string { return &c.Rationale }},
		{"candidate error code", func(c *optimize_api.CandidateResult) *string { return &c.Error.Code }},
		{"candidate error message", func(c *optimize_api.CandidateResult) *string { return &c.Error.Message }},
		{"step name", func(c *optimize_api.CandidateResult) *string { return &c.ExecutionSummary.Steps[0].Name }},
		{"step status", func(c *optimize_api.CandidateResult) *string { return &c.ExecutionSummary.Steps[0].Status }},
		{"step summary", func(c *optimize_api.CandidateResult) *string { return &c.ExecutionSummary.Steps[0].Summary }},
		{"step error code", func(c *optimize_api.CandidateResult) *string { return &c.ExecutionSummary.Steps[0].ErrorCode }},
		{"skill name", func(c *optimize_api.CandidateResult) *string { return &c.Output.SkillResults[0].OutputSkill.Name }},
		{"change type", func(c *optimize_api.CandidateResult) *string { return &c.Output.SkillResults[0].ChangeType }},
		{"skill status", func(c *optimize_api.CandidateResult) *string { return &c.Output.SkillResults[0].Status }},
		{"published version", func(c *optimize_api.CandidateResult) *string { return c.Output.SkillResults[0].OutputSkill.Version }},
		{"baseline version", func(c *optimize_api.CandidateResult) *string { return &c.Output.SkillResults[1].InputSkill.Version }},
		{"skill rationale", func(c *optimize_api.CandidateResult) *string { return &c.Output.SkillResults[0].Rationale }},
		{"skill error code", func(c *optimize_api.CandidateResult) *string { return &c.Output.SkillResults[0].Error.Code }},
		{"skill error message", func(c *optimize_api.CandidateResult) *string { return &c.Output.SkillResults[0].Error.Message }},
		{"validation type", func(c *optimize_api.CandidateResult) *string { return &c.Output.SkillResults[0].Validations[0].Type }},
		{"validation ID", func(c *optimize_api.CandidateResult) *string { return &c.Output.SkillResults[0].Validations[0].ID }},
		{"validation status", func(c *optimize_api.CandidateResult) *string { return &c.Output.SkillResults[0].Validations[0].Status }},
		{"validation details", func(c *optimize_api.CandidateResult) *string { return &c.Output.SkillResults[0].Validations[0].Details }},
		{"file path", func(c *optimize_api.CandidateResult) *string { return &c.Files[0].Path }},
		{"file type", func(c *optimize_api.CandidateResult) *string { return &c.Files[0].Type }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var candidate optimize_api.CandidateResult
			require.NoError(t, json.Unmarshal([]byte(skillOptimizeTestCandidate), &candidate))
			candidate.Error = &optimize_api.JobError{Code: "error", Message: "error"}
			candidate.Output.SkillResults[0].Error = &optimize_api.JobError{Code: "error", Message: "error"}
			*tc.field(&candidate) = value
			before, err := json.Marshal(candidate)
			require.NoError(t, err)
			var output bytes.Buffer
			printSkillOptimizeCandidate(&output, "job", candidate)
			assert.Contains(t, output.String(), `before\x1b[2J`)
			for _, r := range output.String() {
				assert.False(t, unicode.IsControl(r) && r != '\n' && r != '\t',
					"rendered field %s contains %s", tc.name, strconv.QuoteRune(r))
			}
			after, err := json.Marshal(candidate)
			require.NoError(t, err)
			assert.Equal(t, before, after, "rendering must not mutate the service result")
		})
	}
}

func TestSkillOptimizeCandidateDownloadSuggestions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, value string
	}{
		{"command substitution", "$(printf substituted)"},
		{"backtick substitution", "`printf substituted`"},
		{"environment substitution", "%PATH%"},
		{"delayed substitution", "!PATH!"},
		{"double quote", `value"quoted`},
		{"single quote", "value'quoted"},
		{"backslash", `reports\file.json`},
		{"command separator", "value;command"},
		{"command chain", "value&command"},
		{"pipeline", "value|command"},
		{"redirection", "value>file"},
		{"newline", "value\ncommand"},
		{"terminal control", "value\x1b[2J"},
		{"smart quote", "value\u2019quoted"},
		{"non-ASCII path", "r\u00e9sum\u00e9.json"},
		{"flag-like value", "--help"},
		{"empty", ""},
	} {
		for _, field := range []string{"job", "candidate", "file"} {
			t.Run(tc.name+"/"+field, func(t *testing.T) {
				t.Parallel()
				var candidate optimize_api.CandidateResult
				require.NoError(t, json.Unmarshal([]byte(skillOptimizeTestCandidate), &candidate))
				jobID := "job-skill"
				switch field {
				case "job":
					jobID = tc.value
				case "candidate":
					candidate.CandidateID = tc.value
				case "file":
					candidate.Files[0].Path = tc.value
				}
				before, err := json.Marshal(candidate)
				require.NoError(t, err)
				var output bytes.Buffer
				printSkillOptimizeCandidate(&output, jobID, candidate)
				assert.NotContains(t, output.String(), "    azd ai agent optimize download ")
				assert.Contains(t, output.String(), "azd ai agent optimize download --help")
				after, err := json.Marshal(candidate)
				require.NoError(t, err)
				assert.Equal(t, before, after, "rendering must not mutate the service result")
			})
		}
	}
}

func TestSkillOptimizeCandidateDownloadSuggestionShellRoundTrip(t *testing.T) {
	t.Parallel()
	for _, shell := range []struct {
		name   string
		args   []string
		prefix string
	}{
		{"sh", []string{"-c"}, `azd() { printf '%s\n' "$@"; }; `},
		{"pwsh", []string{"-NoProfile", "-NonInteractive", "-Command"}, "function azd { $args }; "},
	} {
		t.Run(shell.name, func(t *testing.T) {
			t.Parallel()
			if _, err := exec.LookPath(shell.name); err != nil {
				t.Skip(shell.name + " is not available on PATH")
			}
			for _, path := range []string{"reports/trace-analysis.json", "reports/trace analysis.json"} {
				t.Run(path, func(t *testing.T) {
					t.Parallel()
					var candidate optimize_api.CandidateResult
					require.NoError(t, json.Unmarshal([]byte(skillOptimizeTestCandidate), &candidate))
					candidate.Files[0].Path = path
					var output bytes.Buffer
					printSkillOptimizeCandidate(&output, "job-skill", candidate)
					var suggestion string
					for line := range strings.SplitSeq(output.String(), "\n") {
						if strings.HasPrefix(line, "    azd ai agent optimize download ") {
							suggestion = strings.TrimSpace(line)
							break
						}
					}
					require.NotEmpty(t, suggestion)
					args := append([]string{}, shell.args...)
					args = append(args, shell.prefix+suggestion)
					actual, err := exec.CommandContext(t.Context(), shell.name, args...).Output()
					require.NoError(t, err)
					lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(actual), "\r\n", "\n"), "\n"), "\n")
					assert.Equal(t, []string{
						"ai", "agent", "optimize", "download", "job-skill",
						"--candidate", "candidate-skill", "--file", path,
					}, lines)
				})
			}
		})
	}
}

func TestSkillOptimizeCandidateTerminalControlsPreserveJSON(t *testing.T) {
	t.Parallel()
	var candidate optimize_api.CandidateResult
	require.NoError(t, json.Unmarshal([]byte(skillOptimizeTestCandidate), &candidate))
	candidate.Rationale = "Résumé 中文\n\tbefore\x1b[2Jafter"
	candidate.Error = &optimize_api.JobError{Code: "bad\x1b[2J", Message: "message\r\b\a"}
	body, err := json.Marshal(candidate)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	for _, format := range []string{"json", "table"} {
		t.Run(format, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			var output bytes.Buffer
			cmd.SetOut(&output)
			err := showOptimizeCandidate(cmd, newTestOptimizeClient(server.URL), "job", "candidate", format)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "\x1b")
			assert.NotContains(t, err.Error(), "\r")
			if format == "json" {
				assert.JSONEq(t, string(body), output.String())
			} else {
				assert.Contains(t, output.String(), "Résumé 中文\n\tbefore\\x1b[2Jafter")
				assert.NotContains(t, output.String(), "\x1b")
			}
		})
	}
}

func TestSkillOptimizeJobSummaryTerminalControlsPreserveJSON(t *testing.T) {
	t.Parallel()
	const value = "before\x1b[2Jafter"
	body, err := json.Marshal(map[string]any{
		"id": value, "status": value,
		"target_configuration":       map[string]string{"type": "foundry_agent", "name": value},
		"optimization_configuration": map[string]string{"type": optimize_api.SkillOptimizationType},
		"result":                     map[string]string{"termination_reason": value},
		"error":                      map[string]string{"code": value, "message": value},
		"warnings":                   []string{value},
	})
	require.NoError(t, err)
	var job optimize_api.OptimizeJobStatus
	require.NoError(t, json.Unmarshal(body, &job))
	var output bytes.Buffer
	printOptimizeJobSummary(&output, &job)
	assert.Equal(t, 6, strings.Count(output.String(), `before\x1b[2Jafter`))
	assert.NotContains(t, output.String(), value)
	err = optimizeJobError(&job)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `before\x1b[2Jafter`)
	assert.NotContains(t, err.Error(), value)
	output.Reset()
	printOptimizeResults(t.Context(), &output, &job, false, "")
	assert.Contains(t, output.String(), `before\x1b[2Jafter`)
	assert.NotContains(t, output.String(), value)
	output.Reset()
	require.NoError(t, printOptimizeStatusJSON(&output, &job))
	var roundTrip optimize_api.OptimizeJobStatus
	require.NoError(t, json.Unmarshal(output.Bytes(), &roundTrip))
	assert.Equal(t, value, roundTrip.ID)
	assert.Equal(t, value, roundTrip.Status)
	assert.Equal(t, value, roundTrip.Error.Message)
	assert.Equal(t, []string{value}, roundTrip.Warnings)
}

func TestSkillOptimizeCandidateDetailsPreserveJSON(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/agent_optimization_jobs/job-skill/candidates/candidate-skill", r.URL.Path)
		_, _ = w.Write([]byte(skillOptimizeTestCandidate))
	}))
	defer server.Close()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	var output bytes.Buffer
	cmd.SetOut(&output)
	require.NoError(t, showOptimizeCandidate(cmd, newTestOptimizeClient(server.URL),
		"job-skill", "candidate-skill", "json"))
	assert.JSONEq(t, skillOptimizeTestCandidate, output.String())
}

func TestSkillOptimizeNoChangeDoesNotInventVersion(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	printSkillOptimizeCandidate(&output, "job", optimize_api.CandidateResult{
		CandidateID: "candidate", Status: "completed",
		Output: &optimize_api.CandidateOutput{Type: optimize_api.SkillOptimizationType,
			SkillResults: []optimize_api.SkillResult{{
				ChangeType: "no_op", Status: "no_change",
				InputSkill:  &optimize_api.FoundrySkillReference{Name: "baseline", Version: "8"},
				OutputSkill: optimize_api.FoundrySkillOutputReference{Name: "baseline"},
			}}},
	})
	assert.Contains(t, output.String(), "Outcome: no_change")
	assert.Contains(t, output.String(), "Baseline version: 8")
	assert.NotContains(t, output.String(), "Published version")
}

func TestSkillOptimizeStatusCandidateFlags(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	for _, args := range [][]string{{"job", "--candidate="}, {"job", "--candidate=c", "--watch"}} {
		cmd := newOptimizeStatusCommand(&azdext.ExtensionContext{})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		err := cmd.Execute()
		require.ErrorContains(t, err, "--candidate")
		assert.NotContains(t, err.Error(), "credential")
	}
}

func TestSkillOptimizeYAMLExample(t *testing.T) {
	t.Parallel()
	file := filepath.Join("..", "..", "examples", "skill-optimization.yaml")
	cfg, err := LoadOptimizeConfig(file)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
	assert.True(t, cfg.isSkillOptimization())
}
