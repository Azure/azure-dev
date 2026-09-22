// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func reconcileArtifactConfig(
	t *testing.T, caller string, ec *evalContext, cfg *project.EvalConfig, dir string,
) error {
	t.Helper()
	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	// Exercise the real include/strict loader, not only direct struct validation.
	loaded, err := project.LoadEvalConfig(path)
	require.NoError(t, err)
	if caller == "up" {
		_, err := deployValidationFixture(t, t.Context(), ec, loaded, dir)
		return err
	}
	cmd := jsonCmd(t, "json")
	cmd.SetContext(t.Context())
	cmd.SetOut(new(bytes.Buffer))
	return (&evalCreateAction{cmd: cmd}).create(ec, loaded, &loaded.Evals[0], path)
}

func TestReconciliationRejectsUnusableLocalDatasetRows(t *testing.T) {
	tests := []struct {
		name      string
		rows      string
		target    string
		simulated bool
		want      string
	}{
		{"agent missing query", `{"response":"answer"}`, project.TargetTypeAgent, false, `"query"`},
		{"model missing query", `{"response":"answer"}`, project.TargetTypeModel, false, `"query"`},
		{"missing description", `{"category":"orders"}`, project.TargetTypeAgent, true, `no "test_case_description"`},
		{"empty description", `{"test_case_description":""}`, project.TargetTypeAgent, true, "empty or non-text"},
		{
			"whitespace description", `{"test_case_description":" \t\r\n "}`,
			project.TargetTypeAgent, true, "empty or non-text",
		},
		{"nontext description", `{"test_case_description":42}`, project.TargetTypeAgent, true, "empty or non-text"},
		{"null description", `{"test_case_description":null}`, project.TargetTypeAgent, true, "empty or non-text"},
		{
			"mixed conversation", "{\"test_case_description\":\"valid\"}\n{\"messages\":[]}",
			project.TargetTypeAgent, true, `row 2 carries "messages"`,
		},
		{
			"seed and messages in one row", `{"test_case_description":"valid","messages":[]}`,
			project.TargetTypeAgent, true, `carries "messages"`,
		},
		{
			"fractional turns", `{"test_case_description":"valid","desired_num_turns":1.5}`,
			project.TargetTypeAgent, true, "not a positive whole number",
		},
		{
			"zero turns", `{"test_case_description":"valid","desired_num_turns":0}`,
			project.TargetTypeAgent, true, "not a positive whole number",
		},
		{
			"negative turns", `{"test_case_description":"valid","desired_num_turns":-1}`,
			project.TargetTypeAgent, true, "not a positive whole number",
		},
		{
			"text turns", `{"test_case_description":"valid","desired_num_turns":"2"}`,
			project.TargetTypeAgent, true, "not a positive whole number",
		},
		{
			"null turns", `{"test_case_description":"valid","desired_num_turns":null}`,
			project.TargetTypeAgent, true, "not a positive whole number",
		},
		{
			"over cap", `{"test_case_description":"valid","desired_num_turns":6}`,
			project.TargetTypeAgent, true, "simulation.max_turns is 5",
		},
		{
			"later over cap", "\uFEFF{\"test_case_description\":\"valid\",\"desired_num_turns\":5}\n\n" +
				`{"test_case_description":"invalid","desired_num_turns":6}`,
			project.TargetTypeAgent, true, "row 2 asks for 6 turns",
		},
	}
	for _, caller := range []string{"create", "up"} {
		for _, tt := range tests {
			t.Run(caller+"/"+tt.name, func(t *testing.T) {
				ec, env, service, cfg, dir := validationFixture(t)
				// An evaluator need not require query: the target invocation does.
				service.definition = `{"definition":{"data_schema":{"properties":{}}}}`
				group := &cfg.Evals[0]
				group.Target = &project.Target{Name: "target", Type: tt.target}
				if tt.simulated {
					group.EvaluationLevel = project.EvaluationLevelConversation
					group.Simulation = &project.Simulation{Model: "simulator", MaxTurns: 5}
				}
				require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(tt.rows), 0o600))
				err := reconcileArtifactConfig(t, caller, ec, cfg, dir)
				require.ErrorContains(t, err, tt.want)
				assert.Empty(t, service.requests, "invalid local rows must fail before service work")
				assert.Empty(t, env.config, "invalid local rows must not record private state")
				assert.Empty(t, env.values)
				_, err = os.Stat(filepath.Join(dir, ".azure"))
				assert.ErrorIs(t, err, os.ErrNotExist)
			})
		}
	}
}

func TestReconciliationAcceptsUsableLocalDatasetModes(t *testing.T) {
	tests := []struct {
		name      string
		rows      string
		target    string
		simulated bool
	}{
		{"agent query", `{"query":"hello"}`, project.TargetTypeAgent, false},
		{"model query", `{"query":"hello"}`, project.TargetTypeModel, false},
		{"sparse target inputs", "{\"query\":\"hi\"}\n{\"response\":\"answer\"}", project.TargetTypeAgent, false},
		{"static completed conversation", `{"messages":[{"role":"user","content":"hello"}]}`, "", false},
		{"seed without optional turns", `{"test_case_description":"A delayed order."}`, project.TargetTypeAgent, true},
		{
			"seed at configured cap",
			"\uFEFF{\"test_case_description\":\"A delayed order.\",\"desired_num_turns\":5}\n\n",
			project.TargetTypeAgent, true,
		},
	}
	for _, caller := range []string{"create", "up"} {
		for _, tt := range tests {
			t.Run(caller+"/"+tt.name, func(t *testing.T) {
				ec, env, service, cfg, dir := validationFixture(t)
				service.definition = `{"definition":{"data_schema":{"properties":{}}}}`
				group := &cfg.Evals[0]
				if tt.target != "" {
					group.Target = &project.Target{Name: "target", Type: tt.target}
				}
				if tt.simulated {
					group.EvaluationLevel = project.EvaluationLevelConversation
					group.Simulation = &project.Simulation{Model: "simulator", MaxTurns: 5}
				}
				require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(tt.rows), 0o600))
				for range 2 {
					require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
				}
				assert.Equal(t, 1, service.createCount)
				assert.Equal(t, "1.0", env.stored(t, versionKey("dataset", "turn-tests")))
			})
		}
	}
}

func TestReconciliationRejectsInvalidRubricBeforePublishingDataset(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, source := range []string{"bare file", "full document", "inline"} {
			for _, parameters := range []string{
				`{"dimensions":[{"id":"clarity","weight":0}]}`,
				`{"dimensions":[{"id":"clarity","weight":11}]}`,
				`{"dimensions":[{"id":"clarity","weight":1.5}]}`,
				`{"dimensions":[{"id":"clarity","weight":null}]}`,
				`{"dimensions":[{"id":"clarity","weight":"5"}]}`,
				`{"dimensions":[{"id":"clarity","weight":5}],"pass_threshold":1.1}`,
				`{"dimensions":[{"id":"clarity","weight":5}],"pass_threshold":-0.1}`,
			} {
				t.Run(caller+"/"+source+"/"+parameters, func(t *testing.T) {
					ec, env, service, cfg, dir := validationFixture(t)
					decl := project.EvaluatorDecl{Name: "local"}
					switch source {
					case "inline":
						require.NoError(t, json.Unmarshal([]byte(parameters), &decl.Definition))
					default:
						decl.Source = "rubric.json"
						body := parameters
						if source == "full document" {
							body = `{"name":"local","definition":` + parameters + `}`
						}

						require.NoError(t, os.WriteFile(filepath.Join(dir, decl.Source), []byte(body), 0o600))
					}
					cfg.Evaluators = []project.EvaluatorDecl{decl}
					cfg.Evals[0].Evaluators = evalcore.EvaluatorList{{Evaluator: "local"}}
					err := reconcileArtifactConfig(t, caller, ec, cfg, dir)
					require.Error(t, err)
					assert.Contains(t, err.Error(), "definition.")
					assert.Empty(t, service.requests)
					assert.Empty(t, env.config)
					assert.Empty(t, env.values)
				})
			}
		}
	}
}

func TestCreateDoesNotValidateUnselectedDatasetModes(t *testing.T) {
	ec, env, service, cfg, dir := validationFixture(t)
	unrelated := *runnableSimulation()
	unrelated.Name = "unrelated-simulation"
	unrelated.Dataset = "unrelated-seeds"
	unrelated.Evaluators = evalcore.EvaluatorList{{Evaluator: "builtin.valid"}}
	cfg.Evals = append(cfg.Evals, unrelated)
	cfg.Datasets = append(cfg.Datasets, project.DatasetDecl{Name: unrelated.Dataset, File: "invalid-seeds.jsonl"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "invalid-seeds.jsonl"),
		[]byte(`{"test_case_description":" "}`), 0o600))
	require.NoError(t, reconcileArtifactConfig(t, "create", ec, cfg, dir))
	assert.Equal(t, 1, service.createCount)
	assert.Empty(t, env.stored(t, versionKey("dataset", unrelated.Dataset)))
	assert.Empty(t, env.stored(t, idKey("eval", unrelated.Name)))
}
