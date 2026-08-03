// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// sampleEvalConfig is the shape the spec documents for evals/<eval-name>.yaml.
const sampleEvalConfig = `
description: Quality gate for the support agent

dataset:
  name: support-golden
  source: ./datasets/support-golden.jsonl
  version: "1"

evaluators:
  - builtin.task_adherence
  - name: support-quality
    source: ./evaluators/support-quality.json
    threshold: 4.0
    initialization_parameters:
      deployment_name: gpt-4.1-nano
  - safety-check

target:
  type: agent
  name: support-agent

options:
  max_samples: 100
  evaluation_level: conversation
`

func loadFromString(t *testing.T, body string) *EvalConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "support-agent-smoke.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err)
	return cfg
}

func TestLoadEvalConfig_ParsesAllSections(t *testing.T) {
	cfg := loadFromString(t, sampleEvalConfig)

	require.NotNil(t, cfg.Dataset)
	require.Equal(t, "support-golden", cfg.Dataset.Name)
	require.Equal(t, "./datasets/support-golden.jsonl", cfg.Dataset.Source)
	require.Equal(t, "1", cfg.Dataset.Version)

	require.Len(t, cfg.Evaluators, 3)
	require.Equal(t, TargetTypeAgent, cfg.Target.Type)
	require.Equal(t, "support-agent", cfg.Target.Name)
	require.Equal(t, EvaluationLevelConversation, cfg.Options.EvaluationLevel)
	require.Equal(t, 100, cfg.Options.MaxSamples)
}

// The eval takes its name from the service entry that pulled the file in, so
// the body never repeats it.
func TestEval_TakesNameFromTheService(t *testing.T) {
	cfg := loadFromString(t, sampleEvalConfig)

	eval := cfg.Eval("support-agent-smoke")
	require.Equal(t, "support-agent-smoke", eval.Name)
	require.Equal(t, "support-golden", eval.Dataset)
	require.Equal(t, "Quality gate for the support agent", eval.Description)
	require.Len(t, eval.Evaluators, 3)
	require.Same(t, cfg.Target, eval.Target)
}

// Only the referenced evaluators carrying a local source are this config's to
// publish. A built-in needs nothing, and one without a source already exists.
func TestCustomEvaluators_OnlyOwnsLocalSources(t *testing.T) {
	cfg := loadFromString(t, sampleEvalConfig)

	owned := cfg.CustomEvaluators()
	require.Len(t, owned, 1)
	require.Equal(t, "support-quality", owned[0].Name)
	require.Equal(t, "./evaluators/support-quality.json", owned[0].Source)
}

// Evaluator entries accept a bare string or a mapping carrying the rest of the
// declaration.
func TestEvaluatorList_MixedForms(t *testing.T) {
	cfg := loadFromString(t, sampleEvalConfig)
	require.Len(t, cfg.Evaluators, 3)

	require.Equal(t, "builtin.task_adherence", cfg.Evaluators[0].Name)
	require.True(t, cfg.Evaluators[0].IsBuiltin())
	require.Equal(t, "task_adherence", cfg.Evaluators[0].APIName(),
		"the builtin prefix must be stripped before it reaches the service")
	require.Nil(t, cfg.Evaluators[0].Threshold)

	require.Equal(t, "support-quality", cfg.Evaluators[1].Name)
	require.False(t, cfg.Evaluators[1].IsBuiltin())
	require.NotNil(t, cfg.Evaluators[1].Threshold)
	require.InDelta(t, 4.0, *cfg.Evaluators[1].Threshold, 0.0001)
	require.Equal(t, "gpt-4.1-nano",
		cfg.Evaluators[1].InitializationParameters["deployment_name"],
		"the judge deployment is declared per evaluator, not once per eval")

	require.Equal(t, "safety-check", cfg.Evaluators[2].Name)
	require.Nil(t, cfg.Evaluators[2].Threshold)
}

// Round-tripping must not rewrite bare names into mappings.
func TestEvaluatorList_RoundTripKeepsCompactForm(t *testing.T) {
	threshold := 4.0
	list := evalcore.EvaluatorList{
		{Name: "builtin.relevance"},
		{Name: "support-quality", Threshold: &threshold},
	}

	out, err := yaml.Marshal(list)
	require.NoError(t, err)

	var back evalcore.EvaluatorList
	require.NoError(t, yaml.Unmarshal(out, &back))
	require.Len(t, back, 2)
	require.Equal(t, "builtin.relevance", back[0].Name)
	require.Nil(t, back[0].Threshold)
	require.NotNil(t, back[1].Threshold)
	require.Contains(t, string(out), "- builtin.relevance",
		"an evaluator with only a name should stay a plain string")
}

// An evaluator carrying a source must not be flattened to its name, or the
// declaration that says what to publish is lost on the next write.
func TestEvaluatorList_RoundTripKeepsSource(t *testing.T) {
	list := evalcore.EvaluatorList{
		{Name: "support-quality", Source: "./evaluators/support-quality.json"},
		{Name: "builtin.task_adherence",
			InitializationParameters: map[string]any{"deployment_name": "gpt-4.1-nano"}},
	}

	out, err := yaml.Marshal(list)
	require.NoError(t, err)

	var back evalcore.EvaluatorList
	require.NoError(t, yaml.Unmarshal(out, &back))
	require.Len(t, back, 2)
	require.Equal(t, "./evaluators/support-quality.json", back[0].Source)
	require.Equal(t, "gpt-4.1-nano", back[1].InitializationParameters["deployment_name"])
}

func TestValidate_Accepts(t *testing.T) {
	require.NoError(t, loadFromString(t, sampleEvalConfig).Validate())
}

func TestValidate_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "dataset without a name",
			body:    "dataset:\n  source: ./d.jsonl\nevaluators: [builtin.relevance]\n",
			wantErr: "'name' is required",
		},
		{
			name:    "no evaluators",
			body:    "evaluators: []\n",
			wantErr: "at least one evaluator is required",
		},
		{
			name:    "built-in with a source to publish",
			body:    "evaluators:\n  - name: builtin.relevance\n    source: ./x.json\n",
			wantErr: "has no source to publish",
		},
		{
			name:    "duplicate evaluator",
			body:    "evaluators: [builtin.relevance, builtin.relevance]\n",
			wantErr: "duplicate evaluator name",
		},
		{
			name:    "version pinned alongside a source",
			body:    "evaluators:\n  - name: q\n    source: ./q.json\n    version: \"3\"\n",
			wantErr: "cannot be set with `source`",
		},
		{
			name:    "unsupported target type",
			body:    "evaluators: [builtin.relevance]\ntarget:\n  type: prompt\n",
			wantErr: "is not supported",
		},
		{
			name: "invalid evaluation level",
			body: "evaluators: [builtin.relevance]\n" +
				"options:\n  evaluation_level: sentence\n",
			wantErr: "evaluation_level",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadFromString(t, tc.body).Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// One file is one eval, named after the file, so the directory listing is the
// list of evals a project declares.
func TestResolveEvalConfigPath(t *testing.T) {
	write := func(t *testing.T, dir string, names ...string) {
		t.Helper()
		for _, n := range names {
			require.NoError(t, os.WriteFile(
				filepath.Join(dir, n), []byte("evaluators: [builtin.relevance]\n"), 0o600))
		}
	}

	t.Run("the only eval is used when unnamed", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "pr-gate.yaml", "generate.yaml")

		path, err := ResolveEvalConfigPath(dir, "")
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "pr-gate.yaml"), path,
			"the generation spec shares the directory and is not an eval")
	})

	t.Run("named eval", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "pr-gate.yaml", "nightly.yaml")

		path, err := ResolveEvalConfigPath(dir, "nightly")
		require.NoError(t, err)
		require.Equal(t, filepath.Join(dir, "nightly.yaml"), path)
	})

	t.Run("unknown name is an error", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "pr-gate.yaml")

		_, err := ResolveEvalConfigPath(dir, "nope")
		require.ErrorContains(t, err, "is not declared")
	})

	t.Run("ambiguous without a name", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "pr-gate.yaml", "nightly.yaml")

		_, err := ResolveEvalConfigPath(dir, "")
		require.ErrorContains(t, err, "--eval")
		require.ErrorContains(t, err, "nightly")
	})

	t.Run("empty directory", func(t *testing.T) {
		_, err := ResolveEvalConfigPath(t.TempDir(), "")
		require.ErrorContains(t, err, "no evals")
	})
}

// outputDir accepts a directory or an explicit file path.
func TestArtifactPath(t *testing.T) {
	cases := []struct {
		name      string
		outputDir string
		resource  string
		ext       string
		want      string
	}{
		{"directory derives the file name", "datasets", "support-golden", ".jsonl",
			filepath.Join("base", "datasets", "support-golden.jsonl")},
		{"explicit file path is used as-is", "generated/datasets/support-golden.jsonl", "ignored", ".jsonl",
			filepath.Join("base", "generated", "datasets", "support-golden.jsonl")},
		{"empty outputDir falls back to the base", "", "support-quality", ".json",
			filepath.Join("base", "support-quality.json")},
		{"yaml rubric file path", "generated/rubrics/quality.yaml", "ignored", ".json",
			filepath.Join("base", "generated", "rubrics", "quality.yaml")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ArtifactPath("base", tc.outputDir, tc.resource, tc.ext))
		})
	}
}
