// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// canonicalDoc is one configuration that uses every camelCase key.
const canonicalDoc = `evaluators:
  - name: quality
    source: ./evaluators/quality.json
    displayName: Quality
    categories: [quality]
    supportedEvaluationLevels: [turn, conversation]
evals:
  - name: turns
    dataset: golden
    evaluationLevel: turn
    maxSamples: 5
    evaluators:
      - evaluator: builtin.task_completion
        initializationParameters:
          model: gpt-5.6-luna
        dataMapping:
          query: "{{item.q}}"
    target:
      type: agent
      name: support-agent
  - name: traces
    source:
      type: traces
      lookbackHours: 24
      maxTraces: 20
      agentName: support-agent
      agentVersion: "3"
      maxTurns: 6
    evaluators:
      - evaluator: builtin.coherence
  - name: windowed
    source:
      type: traces
      agentName: support-agent
      startTime: "2026-01-01T00:00:00Z"
      endTime: "2026-01-02T00:00:00Z"
    evaluators:
      - evaluator: builtin.coherence
  - name: responses
    source:
      type: responses
      responseIds: [resp_1, resp_2]
    evaluators:
      - evaluator: builtin.coherence
  - name: simulated
    dataset: seeds
    evaluationLevel: conversation
    target:
      type: agent
      name: support-agent
    simulation:
      model: connection/simulator
      numConversations: 3
      maxTurns: 8
    evaluators:
      - evaluator: builtin.task_completion
`

func TestEveryCamelCaseKeyIsReadAsWritten(t *testing.T) {
	cfg, err := DecodeEvalConfig([]byte(canonicalDoc), "azure.eval.yaml")
	require.NoError(t, err)
	assert.Equal(t, "turn", cfg.Evals[0].EvaluationLevel)
	assert.Equal(t, 5, cfg.Evals[0].MaxSamples)
	assert.Equal(t, "gpt-5.6-luna", cfg.Evals[0].Evaluators[0].InitializationParameters["model"])
	assert.Equal(t, "{{item.q}}", cfg.Evals[0].Evaluators[0].DataMapping["query"])
	source := cfg.Evals[1].Source
	assert.Equal(t, 24, source.LookbackHours)
	assert.Equal(t, 20, source.MaxTraces)
	assert.Equal(t, "support-agent", source.AgentName)
	assert.Equal(t, "3", source.AgentVersion)
	assert.Equal(t, 6, source.MaxTurns)
	assert.Equal(t, "2026-01-01T00:00:00Z", cfg.Evals[2].Source.StartTime)
	assert.Equal(t, "2026-01-02T00:00:00Z", cfg.Evals[2].Source.EndTime)
	assert.Equal(t, []string{"resp_1", "resp_2"}, cfg.Evals[3].Source.ResponseIDs)
	assert.Equal(t, 3, cfg.Evals[4].Simulation.NumConversations)
	assert.Equal(t, 8, cfg.Evals[4].Simulation.MaxTurns)
	assert.Equal(t, "Quality", cfg.Evaluators[0].DisplayName)
	assert.Equal(t, []string{"turn", "conversation"}, cfg.Evaluators[0].SupportedEvaluationLevels)
}

// camelCase is the only spelling. A key written the old way is an unknown key
// like any other, and the suggestion names the one to use.
const sourceKeys = "lookback_hours max_traces agent_name agent_version max_turns start_time end_time response_ids"

func TestASnakeCaseKeyIsAnUnknownKeyThatNamesTheCamelCaseOne(t *testing.T) {
	for snake, camel := range map[string]string{
		"evaluation_level": "evaluationLevel", "max_samples": "maxSamples",
		"lookback_hours": "lookbackHours", "max_traces": "maxTraces", "agent_name": "agentName",
		"agent_version": "agentVersion", "max_turns": "maxTurns", "start_time": "startTime",
		"end_time": "endTime", "response_ids": "responseIds",
	} {
		t.Run(snake, func(t *testing.T) {
			scope := "evals:\n  - name: e\n    " + snake + ": 1\n"
			if strings.Contains(sourceKeys, snake) {
				scope = "evals:\n  - name: e\n    source:\n      type: traces\n      " + snake + ": 1\n"
			}
			_, err := DecodeEvalConfig([]byte(scope), "azure.eval.yaml")
			require.Error(t, err)
			assert.Contains(t, err.Error(), `unknown key "`+snake+`"`)
			assert.Contains(t, err.Error(), `did you mean "`+camel+`"?`)
		})
	}
	const reference = "evals:\n  - name: e\n    evaluators:\n      - evaluator: x\n"
	const simulation = "evals:\n  - name: e\n    simulation:\n      model: c/m\n"
	for name, body := range map[string]string{
		"initialization_parameters":   reference + "        initialization_parameters: {}\n",
		"data_mapping":                reference + "        data_mapping: {}\n",
		"num_conversations":           simulation + "      num_conversations: 2\n",
		"display_name":                "evaluators:\n  - name: e\n    display_name: A\n",
		"supported_evaluation_levels": "evaluators:\n  - name: e\n    supported_evaluation_levels: [turn]\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeEvalConfig([]byte(body), "azure.eval.yaml")
			require.Error(t, err)
			assert.Contains(t, err.Error(), `unknown key "`+name+`"`)
		})
	}
}

// What the extension writes uses the camelCase keys.
func TestWhatTheExtensionWritesUsesCamelCaseKeys(t *testing.T) {
	cfg := &EvalConfig{Evals: []Eval{{
		Name: "e", Dataset: "d", EvaluationLevel: "turn", MaxSamples: 2,
		Evaluators: evalcore.EvaluatorList{{
			Evaluator:                "builtin.task_completion",
			InitializationParameters: map[string]any{"model": "m"},
			DataMapping:              map[string]string{"query": "{{item.q}}"},
		}},
	}}}
	path := filepath.Join(t.TempDir(), EvalConfigBase)
	require.NoError(t, SaveEvalConfigTo(path, cfg))
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, key := range []string{"evaluationLevel:", "maxSamples:", "initializationParameters:", "dataMapping:"} {
		assert.Contains(t, string(body), key)
	}
	for _, old := range []string{"evaluation_level", "max_samples", "initialization_parameters", "data_mapping"} {
		assert.NotContains(t, string(body), old)
	}
	reread, err := DecodeEvalConfig(body, path)
	require.NoError(t, err)
	assert.Equal(t, cfg, reread)
}

func TestScaffoldingAnEvalWritesCamelCaseKeys(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, ApplyScaffold(dir, ScaffoldWrite{Evals: []Eval{{
		Name: "new", Dataset: "golden", EvaluationLevel: "conversation", MaxSamples: 9,
		Source: nil,
	}}}))
	body, err := os.ReadFile(filepath.Join(dir, EvalConfigBase))
	require.NoError(t, err)
	assert.Contains(t, string(body), "evaluationLevel: conversation")
	assert.Contains(t, string(body), "maxSamples: 9")
	assert.NotContains(t, string(body), "evaluation_level")
}

// An eval's fingerprint is recorded in the azd environment and decides whether
// an immutable eval is recreated. It is computed from the json encoding of the
// struct, which is an internal encoding and not the file's keys, so it keeps its
// names: renaming them would make every eval already deployed look changed and
// be recreated on the next deploy, for no change anyone can see. The values below
// were computed before the file's keys were renamed.
func TestFingerprintsAreTheOnesComputedBeforeTheKeysWereRenamed(t *testing.T) {
	full := Eval{
		Name: "full", ID: "id-1", Description: "d", Dataset: "ds",
		EvaluationLevel: "conversation", MaxSamples: 7,
		Evaluators: evalcore.EvaluatorList{{
			Evaluator: "builtin.task_completion", Name: "tc", Version: "2",
			InitializationParameters: map[string]any{"model": "gpt-5.6-luna", "threshold": 3},
			DataMapping:              map[string]string{"query": "{{item.q}}"},
		}},
		Target:     &Target{Type: "agent", Name: "support-agent"},
		Simulation: &Simulation{Model: "conn/dep", NumConversations: 2, MaxTurns: 4},
	}
	traces := Eval{
		Name: "traces", Source: &SourceDecl{
			Type: "traces", LookbackHours: 24, MaxTraces: 20, AgentName: "a", AgentVersion: "3", MaxTurns: 5,
			StartTime: "2026-01-01T00:00:00Z", EndTime: "2026-01-02T00:00:00Z",
		},
		Evaluators: evalcore.EvaluatorList{{Evaluator: "builtin.coherence"}},
	}
	for name, tc := range map[string]struct {
		group             Eval
		fingerprint, defn string
	}{
		"full": {
			full, "308ccb40a74492b1aba8ae75a7e257343ad8f23fc843a994d09e4efb1de2996a",
			"7982867f9052d25b947dfb8d7e24a3cc8b9f1d143b3d6d7daef17a32d9657eff",
		},
		"traces": {
			traces, "c3b12fd84539260130c311fffe80ecc244440023a3c5a58589e0f39839c084cf",
			"3f8ac550e01fef8d3545514240ef97a9ca0c2582b51425b0adf00e2369eb729d",
		},
	} {
		group, err := FingerprintGroup(tc.group)
		require.NoError(t, err)
		definition, err := FingerprintDefinition(tc.group)
		require.NoError(t, err)
		assert.Equal(t, tc.fingerprint, group, name+" group fingerprint")
		assert.Equal(t, tc.defn, definition, name+" definition fingerprint")
	}
}

func compileEvalSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	body, err := os.ReadFile("../../schemas/azure.ai.eval.json")
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(body, &document))
	const uri = "https://example.test/eval.schema.json"
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource(uri, document))
	schema, err := compiler.Compile(uri)
	require.NoError(t, err)
	return schema
}

// validate reads a configuration as the editor does: as plain data.
func validate(t *testing.T, schema *jsonschema.Schema, body string) error {
	t.Helper()
	var value any
	require.NoError(t, yaml.Unmarshal([]byte(body), &value))
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	var plain any
	require.NoError(t, json.Unmarshal(encoded, &plain))
	return schema.Validate(plain)
}

func TestTheSchemaDescribesTheCamelCaseKeysAndOnlyThose(t *testing.T) {
	schema := compileEvalSchema(t)
	require.NoError(t, validate(t, schema, canonicalDoc))
	for name, body := range map[string]string{
		"evaluation_level": "evals:\n  - name: e\n    evaluation_level: turn\n",
		"max_samples":      "evals:\n  - name: e\n    max_samples: 1\n",
		"source key":       "evals:\n  - name: e\n    source:\n      type: traces\n      max_traces: 1\n",
		"simulation key":   "evals:\n  - name: e\n    simulation:\n      model: c/m\n      max_turns: 1\n",
		"reference key":    "evals:\n  - name: e\n    evaluators:\n      - evaluator: x\n        data_mapping: {}\n",
		"catalog key":      "evaluators:\n  - name: e\n    display_name: A\n",
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, validate(t, schema, body), "the CLI refuses it, so the editor has to as well")
		})
	}
}

func TestTheSchemaChecksFileReferenceOverlays(t *testing.T) {
	schema := compileEvalSchema(t)
	for _, tc := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "dataset", body: `datasets: [{$ref: ./dataset.yaml, name: golden, version: "2"}]`},
		{name: "eval", body: `evals: [{$ref: ./eval.yaml, maxSamples: 5}]`},
		{name: "eval name", body: `evals: [{$ref: ./eval.yaml, name: quality}]`},
		{name: "simulation eval overlay", body: `evals: [{$ref: ./eval.yaml, simulation: {model: c/m}}]`},
		{name: "source", body: `evals: [{name: quality, source: {$ref: ./source.yaml, lookbackHours: 24}}]`},
		{name: "response source", body: `evals: [{name: quality, source: {$ref: ./source.yaml, type: responses}}]`},
		{name: "target", body: `evals: [{name: quality, target: {$ref: ./target.yaml, type: agent}}]`},
		{name: "evaluator catalog", body: `evaluators: [{$ref: ./evaluator.yaml, displayName: Quality}]`},
		{name: "evaluator reference",
			body: `evals: [{name: quality, evaluators: [{$ref: ./reference.yaml, initializationParameters: {model: m}}]}]`},
		{name: "rubric service keys",
			body: `evaluators: [{name: quality, definition: {$ref: ./rubric.json, service_owned_key: true}}]`},
		{name: "snake eval", body: `evals: [{$ref: ./eval.yaml, max_samples: 5}]`, wantErr: true},
		{name: "snake source",
			body: `evals: [{name: quality, source: {$ref: ./source.yaml, lookback_hours: 24}}]`, wantErr: true},
		{name: "snake catalog",
			body: `evaluators: [{$ref: ./evaluator.yaml, display_name: Quality}]`, wantErr: true},
		{name: "snake evaluator reference",
			body: `evals: [{name: quality, evaluators: [{$ref: ./reference.yaml, data_mapping: {}}]}]`, wantErr: true},
		{name: "unknown dataset", body: `datasets: [{$ref: ./dataset.yaml, typo: true}]`, wantErr: true},
		{name: "unknown target",
			body: `evals: [{name: quality, target: {$ref: ./target.yaml, typo: true}}]`, wantErr: true},
		{name: "invalid cap type", body: `evals: [{$ref: ./eval.yaml, maxSamples: five}]`, wantErr: true},
		{name: "invalid source type",
			body: `evals: [{name: quality, source: {$ref: ./source.yaml, type: unsupported}}]`, wantErr: true},
		{name: "missing inline name", body: `evals: [{maxSamples: 5}]`, wantErr: true},
		{name: "missing inline source type",
			body: `evals: [{name: quality, source: {lookbackHours: 24}}]`, wantErr: true},
		{name: "missing inline response IDs",
			body: `evals: [{name: quality, source: {type: responses}}]`, wantErr: true},
		{name: "missing inline evaluator", body: `evals: [{name: quality, evaluators: [{}]}]`, wantErr: true},
		{name: "invalid reference type", body: `evals: [{$ref: 1}]`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validate(t, schema, tc.body)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFileReferenceOverlayKeysAgreeWithRuntime(t *testing.T) {
	schema := compileEvalSchema(t)
	for _, tc := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "eval cap", body: `evals: [{$ref: ./quality.yaml, maxSamples: 5}]`},
		{name: "source window",
			body: `evals: [{name: quality, source: {$ref: ./source.yaml, lookbackHours: 48}}]`},
		{name: "snake eval", body: `evals: [{$ref: ./quality.yaml, max_samples: 5}]`, wantErr: true},
		{name: "snake source",
			body: `evals: [{name: quality, source: {$ref: ./source.yaml, lookback_hours: 48}}]`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "quality.yaml"),
				[]byte("name: quality\ndataset: golden\nmaxSamples: 2\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "source.yaml"),
				[]byte("type: traces\nagentName: support-agent\nlookbackHours: 24\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase), []byte(tc.body), 0o600))
			schemaErr := validate(t, schema, tc.body)
			cfg, runtimeErr := OpenEvalConfig(dir)
			if tc.wantErr {
				assert.Error(t, schemaErr)
				require.Error(t, runtimeErr)
				assert.Contains(t, runtimeErr.Error(), "unknown key")
				return
			}
			require.NoError(t, schemaErr)
			require.NoError(t, runtimeErr)
			require.Len(t, cfg.Evals, 1)
			if cfg.Evals[0].Source != nil {
				assert.Equal(t, 48, cfg.Evals[0].Source.LookbackHours)
				assert.Equal(t, "support-agent", cfg.Evals[0].Source.AgentName)
			} else {
				assert.Equal(t, 5, cfg.Evals[0].MaxSamples)
				assert.Equal(t, "golden", cfg.Evals[0].Dataset)
			}
		})
	}
}
