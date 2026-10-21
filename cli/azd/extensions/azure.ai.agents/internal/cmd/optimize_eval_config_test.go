// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

const gaOptimizationTestConfig = `{
	"display_name":"quality",
	"target_configuration":{"type":"foundry_agent","name":"deployed-agent","version":"4"},
	"optimization_model_configuration":{"model":"optimizer"},
	"optimization_configuration":{
		"type":"agent_optimization",
		"evaluation_configuration":{
			"training_set":{"type":"target_completion","source":{"type":"dataset_reference","name":"golden","version":"2"}},
			"evaluators":[{"name":"builtin.task_adherence","initialization_parameters":{"model":"judge"}}],
			"evaluation_model":{"model":"judge","sampling_params":{"temperature":0.25}},
			"max_concurrent_agent_runs":3
		},
		"candidate_search_configuration":{"max_candidates":7,"candidate_screening_data_sample_size":2},
		"agent_optimization_space":{"target_attributes":["instructions"]},
		"baseline_agent_configuration":{"system_prompt":"Explicit prompt","current_model":"baseline"}
	}
}`

func TestOptimizeGAConfigRoundTripAndAuthority(t *testing.T) {
	root := t.TempDir()
	configPath := writeTestFile(t, root, "optimization.json", gaOptimizationTestConfig)
	svc := optimizeTestDirectService(t, root)
	projectServer := &recordingProjectServer{
		projectPath: root, existing: map[string]*azdext.ServiceConfig{"assistant": svc},
	}
	envServer := &testEnvironmentServiceServer{
		environments: map[string]*azdext.Environment{"dev": {Name: "dev"}},
		values: map[string]map[string]string{
			"dev": {"AGENT_ASSISTANT_NAME": "deployed-agent", "AGENT_ASSISTANT_VERSION": "8"},
		},
	}
	t.Setenv("AZD_SERVER", newProjectRecorderServer(t, projectServer, envServer))
	action := &OptimizeAction{flags: &optimizeFlags{configFile: configPath}, envName: "dev", noPrompt: true}
	cfg, _, project, err := action.resolveConfig(t.Context())
	require.NoError(t, err)
	require.NoError(t, action.applyOverrides(t.Context(), cfg, project))
	request, _, err := cfg.ToRequest()
	require.NoError(t, err)
	data, err := json.Marshal(request)
	require.NoError(t, err)
	assert.JSONEq(t, gaOptimizationTestConfig, string(data),
		"authored identities, versions and nested settings must survive")
	require.Empty(t, envServer.setKeys)
}

func TestOptimizeGAConfigRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	for _, config := range []string{
		strings.Replace(gaOptimizationTestConfig, `"display_name":`, `"unsupported":true,"display_name":`, 1),
		strings.Replace(gaOptimizationTestConfig, `"temperature":0.25`, `"unsupported":true`, 1),
	} {
		path := writeTestFile(t, t.TempDir(), "optimization.json", config)
		_, err := LoadOptimizeConfig(path)
		require.ErrorContains(t, err, "unknown field")
	}
}

func TestOptimizeGAConfigPreservesRawTestCases(t *testing.T) {
	t.Parallel()
	row := `{"query":"question","id":9007199254740993,"candidate_response":"Synthetic answer","metadata":{"extra":true}}`
	config := strings.Replace(gaOptimizationTestConfig, `"type":"dataset_reference","name":"golden","version":"2"`,
		`"type":"inline","test_cases":[`+row+`]`, 1)
	path := writeTestFile(t, t.TempDir(), "optimization.json", config)
	cfg, err := LoadOptimizeConfig(path)
	require.NoError(t, err)
	request, warnings, err := cfg.ToRequest()
	require.NoError(t, err)
	assert.Empty(t, warnings)
	cases := request.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.TestCases
	require.Len(t, cases, 1)
	assert.JSONEq(t, row, string(cases[0]))
	serialized, err := json.Marshal(request)
	require.NoError(t, err)
	assert.JSONEq(t, config, string(serialized))
	assert.Contains(t, string(serialized), `"id":9007199254740993`)
}

func TestOptimizeReusesEvalFileRefs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	evalsDir := filepath.Join(root, "custom", "evaluations")
	dataDir := filepath.Join(evalsDir, "datasets")
	require.NoError(t, os.MkdirAll(dataDir, 0o700))
	rows := "{\"id\":1,\"description\":\"Test objective\",\"query\":\"first\"," +
		"\"candidate_response\":\"Synthetic answer\"}\n" +
		"{\"id\":2,\"query\":\"second\",\"ground_truth\":\"Verified answer\"}\n"
	dataset := writeTestFile(t, dataDir, "rows.jsonl", rows)
	writeTestFile(t, dataDir, "declaration.yaml", "name: golden\nfile: ./rows.jsonl\nversion: '3'\n")
	path := writeTestFile(t, evalsDir, "nightly.yaml", `
datasets:
  - $ref: ./datasets/declaration.yaml
evaluators:
  - name: quality
    version: "6"
    definition: {type: rubric, dimensions: []}
evals:
  - name: nightly
    dataset: golden
    maxSamples: 1
    evaluationLevel: turn
    target: {type: agent, name: assistant}
    evaluators:
      - evaluator: quality
        initializationParameters: {model: judge, threshold: 0.5}
`)
	cfg, err := readOptimizeEvalConfig(path)
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)
	assert.Equal(t, dataset, cfg.Datasets[0].File)
	choices, err := optimizeEvalChoices(cfg, path, "nightly", "deployed-agent", "assistant")
	require.NoError(t, err)
	require.Len(t, choices, 1)
	resolved, err := optimizeConfigFromEval(choices[0], "deployed-agent", "8", "")
	require.NoError(t, err)
	resolved.Options.OptimizationModel = "optimizer"
	request, warnings, err := resolved.ToRequest()
	require.NoError(t, err)
	assert.Empty(t, warnings)
	evaluation := request.OptimizationConfiguration.EvaluationConfiguration
	require.Len(t, evaluation.TrainingSet.Source.TestCases, 1)
	assert.JSONEq(t, strings.Split(rows, "\n")[0], string(evaluation.TrainingSet.Source.TestCases[0]))
	assert.Equal(t, "6", evaluation.Evaluators[0].Version)
	assert.Equal(t, 0.5, evaluation.Evaluators[0].InitializationParameters["threshold"])
	assert.Equal(t, "judge", evaluation.EvaluationModel.Model)
	assert.Equal(t, "8", request.TargetConfiguration.Version)
	serialized, err := json.Marshal(request)
	require.NoError(t, err)
	assert.Contains(t, string(serialized), `"candidate_response":"Synthetic answer"`)
	assert.NotContains(t, string(serialized), "Verified answer", "maxSamples must limit the submitted rows")
	content, err := os.ReadFile(dataset)
	require.NoError(t, err)
	assert.Equal(t, rows, string(content))

	properties, err := structpb.NewStruct(map[string]any{
		"$ref": filepath.Join("custom", "evaluations", "nightly.yaml"),
	})
	require.NoError(t, err)
	project := &azdext.ProjectConfig{Path: root, Services: map[string]*azdext.ServiceConfig{
		"evals": {Host: "azure.ai.eval", AdditionalProperties: properties},
	}}
	projectChoices, err := projectOptimizeEvalChoices(project, "nightly", "deployed-agent", "assistant")
	require.NoError(t, err)
	require.Len(t, projectChoices, 1)
	assert.Equal(t, dataset, projectChoices[0].config.Datasets[0].File)
}

func TestOptimizeEvalUnsupportedInputs(t *testing.T) {
	t.Parallel()
	for _, extra := range []string{
		"source: {}",
		"simulation: {}",
		"evaluationLevel: conversation",
		"evaluation_level: turn",
		"evaluators: [{evaluator: quality, dataMapping: {query: other}}]",
	} {
		t.Run(extra, func(t *testing.T) {
			path := writeTestFile(t, t.TempDir(), "azure.eval.yaml", `
datasets: [{name: golden, version: "2"}]
evals:
  - name: nightly
    dataset: golden
    target: {type: agent, name: assistant}
    `+extra+"\n")
			cfg, err := readOptimizeEvalConfig(path)
			if strings.Contains(extra, "evaluation_level") {
				require.ErrorContains(t, err, "evaluation_level")
				return
			}
			require.NoError(t, err)
			choices, err := optimizeEvalChoices(cfg, path, "", "assistant", "")
			require.NoError(t, err)
			require.Len(t, choices, 1)
			_, err = optimizeConfigFromEval(choices[0], "assistant", "", "judge")
			require.Error(t, err)
		})
	}
}

func TestOptimizeEvalDatasetAndJudgeResolution(t *testing.T) {
	t.Parallel()
	path := writeTestFile(t, t.TempDir(), "azure.eval.yaml", `
datasets: [{name: golden, version: "2"}]
evals:
  - name: nightly
    dataset: golden
    target: {type: agent, name: assistant}
    evaluators:
      - evaluator: builtin.coherence
        initializationParameters: {model: first}
      - evaluator: builtin.relevance
        version: "3"
        initializationParameters: {model: second, deployment_name: second}
`)
	cfg, err := readOptimizeEvalConfig(path)
	require.NoError(t, err)
	choices, err := optimizeEvalChoices(cfg, path, "", "assistant", "")
	require.NoError(t, err)
	require.Len(t, choices, 1)
	_, err = optimizeConfigFromEval(choices[0], "assistant", "", "")
	require.ErrorContains(t, err, "multiple judge models")
	resolved, err := optimizeConfigFromEval(choices[0], "assistant", "", "override")
	require.NoError(t, err)
	assert.Equal(t, "override", resolved.Evaluators[1].InitializationParameters["deployment_name"])
	assert.Equal(t, "second", choices[0].eval.Evaluators[1].InitializationParameters["model"],
		"do not mutate authored config")
	request, _, err := resolved.ToRequest()
	require.NoError(t, err)
	assert.Equal(t, "2", request.OptimizationConfiguration.EvaluationConfiguration.TrainingSet.Source.Version)
	assert.Equal(t, "3", request.OptimizationConfiguration.EvaluationConfiguration.Evaluators[1].Version)
	resolved.MaxSamples = 1
	_, _, err = resolved.ToRequest()
	require.ErrorContains(t, err, "maxSamples requires a local dataset")
}

func TestOptimizeEvalRelativeFileUsesDeclaringDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Mkdir(filepath.Join(root, "evals"), 0o700))
	dataset := writeTestFile(t, filepath.Join(root, "evals"), "data.jsonl", "{\"query\":\"hi\"}\n")
	writeTestFile(t, filepath.Join(root, "evals"), "azure.eval.yaml",
		"datasets: [{name: golden, file: ./data.jsonl}]\n")
	cfg, err := readOptimizeEvalConfig(filepath.Join("evals", "azure.eval.yaml"))
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)
	assert.Equal(t, dataset, cfg.Datasets[0].File)
}

func TestOptimizeRejectsInvalidExplicitFlagsBeforeResolution(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	promptPath := writeTestFile(t, t.TempDir(), "prompt.json", `{
		"target_configuration":{"type":"foundry_agent","name":"a"},
		"optimization_model_configuration":{"model":"optimizer"},
		"optimization_configuration":{"type":"prompt_optimization",
			"context":[{"type":"steering_prompt","prompt":"Be concise"}]}
	}`)
	tests := [][]string{
		{"--config="}, {"--eval-config="}, {"--eval="}, {"--agent="}, {"--dataset="}, {"--evaluator="},
		{"--max-candidates=0"}, {"--poll-interval=0"},
		{"--config", promptPath, "--eval", "nightly"}, {"a", "--agent", "b"},
		{"--config", promptPath, "--dataset", "golden:2"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := newOptimizeCommand(&azdext.ExtensionContext{NoPrompt: true})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs(args)
			err := cmd.Execute()
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "credential")
			assert.Contains(t, err.Error(), "--")
			assert.NotContains(t, output.String(), "Optimizing agent")
		})
	}
	cfg, err := LoadOptimizeConfig(promptPath)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
	request, _, err := cfg.ToRequest()
	require.NoError(t, err)
	assert.Nil(t, request.OptimizationConfiguration.EvaluationConfiguration)
	assert.Equal(t, "Be concise", request.OptimizationConfiguration.Context[0].Prompt)
}

func TestOptimizePromptConfigDefersRequestValidationToServer(t *testing.T) {
	t.Parallel()
	request := &optimize_api.OptimizeRequest{
		TargetConfiguration:            &optimize_api.AgentIdentifier{Type: "foundry_agent", Name: "agent"},
		OptimizationModelConfiguration: optimize_api.ModelConfiguration{Model: "optimizer"},
		OptimizationConfiguration: optimize_api.OptimizationConfiguration{
			Type:                    "prompt_optimization",
			EvaluationConfiguration: &optimize_api.EvaluationConfiguration{},
			Context:                 []optimize_api.OptimizationContext{},
		},
	}
	cfg := optimizeConfigFromRequest(request)

	require.NoError(t, cfg.Validate())
	actual, _, err := cfg.ToRequest()
	require.NoError(t, err)
	assert.Equal(t, request.OptimizationConfiguration, actual.OptimizationConfiguration)
}

func TestOptimizeEvalDoesNotDiscoverLegacyConfig(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "eval.yaml", "invalid: [")
	svc := optimizeTestDirectService(t, root)
	server := &recordingProjectServer{projectPath: root, existing: map[string]*azdext.ServiceConfig{"assistant": svc}}
	env := &testEnvironmentServiceServer{
		environments: map[string]*azdext.Environment{"dev": {Name: "dev"}},
		values: map[string]map[string]string{
			"dev": {"AGENT_ASSISTANT_NAME": "deployed-agent", "AGENT_ASSISTANT_VERSION": "2"},
		},
	}
	t.Setenv("AZD_SERVER", newProjectRecorderServer(t, server, env))
	action := &OptimizeAction{flags: &optimizeFlags{}, noPrompt: true, envName: "dev"}
	cfg, source, _, err := action.resolveConfig(t.Context())
	require.NoError(t, err)
	assert.Empty(t, source)
	assert.Equal(t, "deployed-agent", cfg.Agent.Name)
	assert.Nil(t, cfg.Dataset)
	assert.Nil(t, cfg.TrainingSet)
}

func TestOptimizeMultipleEvalServicesRespectTargetAndSelector(t *testing.T) {
	root := t.TempDir()
	agent := optimizeTestDirectService(t, root)
	makeEvalService := func(target string, names ...string) *azdext.ServiceConfig {
		t.Helper()
		evals := make([]any, 0, len(names))
		for _, name := range names {
			evals = append(evals, map[string]any{
				"name": name, "dataset": "golden",
				"target": map[string]any{"type": "agent", "name": target},
				"evaluators": []any{map[string]any{
					"evaluator":                "builtin.task_adherence",
					"initializationParameters": map[string]any{"model": "judge"},
				}},
			})
		}
		properties, err := structpb.NewStruct(map[string]any{
			"datasets": []any{map[string]any{"name": "golden", "version": "2"}},
			"evals":    evals,
		})
		require.NoError(t, err)
		return &azdext.ServiceConfig{Host: "azure.ai.eval", AdditionalProperties: properties}
	}
	services := map[string]*azdext.ServiceConfig{
		"assistant":        agent,
		"a-other-evals":    makeEvalService("unrelated-agent", "quality"),
		"z-matching-evals": makeEvalService("assistant", "quality", "smoke"),
	}
	server := &recordingProjectServer{projectPath: root, existing: services}
	env := &testEnvironmentServiceServer{
		environments: map[string]*azdext.Environment{"dev": {Name: "dev"}},
		values: map[string]map[string]string{
			"dev": {"AGENT_ASSISTANT_NAME": "deployed-agent", "AGENT_ASSISTANT_VERSION": "2"},
		},
	}
	t.Setenv("AZD_SERVER", newProjectRecorderServer(t, server, env))
	action := &OptimizeAction{flags: &optimizeFlags{}, noPrompt: true, envName: "dev"}
	_, _, _, err := action.resolveConfig(t.Context())
	require.ErrorContains(t, err, "multiple evaluations match")
	action.flags.evalName = "quality"
	cfg, source, _, err := action.resolveConfig(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "z-matching-evals/quality", source)
	assert.Equal(t, "quality", cfg.Name)
	assert.Equal(t, "deployed-agent", cfg.Agent.Name)
}
