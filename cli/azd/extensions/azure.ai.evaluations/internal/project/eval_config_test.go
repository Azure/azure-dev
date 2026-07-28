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

const sampleDeployConfig = `
evaluators:
  - name: support-quality
    source: ./evaluators/support-quality/rubric_dimensions.json
  - name: safety-check
    source: ./evaluators/safety-check.json

datasets:
  - name: support-golden
    source: ./datasets/support-golden.jsonl
    version: "1"

evalGroups:
  - name: pr-gate
    description: Quality gate for the support agent
    dataset: support-golden
    evaluators:
      - builtin.task_adherence
      - { name: support-quality, threshold: 4.0 }
      - safety-check
    target:
      type: agent
      name: support-agent
    options:
      eval_model: gpt-4.1-nano
      max_samples: 100
      evaluation_level: conversation
`

func loadFromString(t *testing.T, body string) *EvalConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "azure.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err)
	return cfg
}

func TestLoadEvalConfig_ParsesAllSections(t *testing.T) {
	cfg := loadFromString(t, sampleDeployConfig)

	require.Len(t, cfg.Evaluators, 2)
	require.Len(t, cfg.Datasets, 1)
	require.Len(t, cfg.EvalGroups, 1)

	ds, ok := cfg.Dataset("support-golden")
	require.True(t, ok)
	require.Equal(t, "./datasets/support-golden.jsonl", ds.Source)
	require.Equal(t, "1", ds.Version)

	g, ok := cfg.Group("pr-gate")
	require.True(t, ok)
	require.Equal(t, "support-golden", g.Dataset)
	require.Equal(t, TargetTypeAgent, g.Target.Type)
	require.Equal(t, "support-agent", g.Target.Name)
	require.Equal(t, EvaluationLevelConversation, g.Options.EvaluationLevel)
}

// Evaluator entries accept a bare string or a mapping carrying a threshold.
func TestEvaluatorList_MixedForms(t *testing.T) {
	cfg := loadFromString(t, sampleDeployConfig)
	g, ok := cfg.Group("pr-gate")
	require.True(t, ok)
	require.Len(t, g.Evaluators, 3)

	require.Equal(t, "builtin.task_adherence", g.Evaluators[0].Name)
	require.True(t, g.Evaluators[0].IsBuiltin())
	require.Equal(t, "task_adherence", g.Evaluators[0].APIName(),
		"the builtin prefix must be stripped before it reaches the service")
	require.Nil(t, g.Evaluators[0].Threshold)

	require.Equal(t, "support-quality", g.Evaluators[1].Name)
	require.False(t, g.Evaluators[1].IsBuiltin())
	require.NotNil(t, g.Evaluators[1].Threshold)
	require.InDelta(t, 4.0, *g.Evaluators[1].Threshold, 0.0001)

	require.Equal(t, "safety-check", g.Evaluators[2].Name)
	require.Nil(t, g.Evaluators[2].Threshold)
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

func TestValidate_Accepts(t *testing.T) {
	require.NoError(t, loadFromString(t, sampleDeployConfig).Validate())
}

func TestValidate_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "dataset referenced but not declared",
			body:    "evalGroups:\n  - name: g\n    dataset: missing\n    evaluators: [builtin.relevance]\n",
			wantErr: "is not declared in datasets",
		},
		{
			name: "custom evaluator referenced but not declared",
			body: "datasets:\n  - name: d\n" +
				"evalGroups:\n  - name: g\n    dataset: d\n    evaluators: [not-declared]\n",
			wantErr: "is not declared in evaluators",
		},
		{
			name:    "built-in declared as a custom evaluator",
			body:    "evaluators:\n  - name: builtin.relevance\n",
			wantErr: "must not be declared",
		},
		{
			name:    "group without evaluators",
			body:    "evalGroups:\n  - name: g\n    evaluators: []\n",
			wantErr: "at least one evaluator is required",
		},
		{
			name:    "unsupported target type",
			body:    "evalGroups:\n  - name: g\n    evaluators: [builtin.relevance]\n    target:\n      type: prompt\n",
			wantErr: "is not supported",
		},
		{
			name: "invalid evaluation level",
			body: "evalGroups:\n  - name: g\n    evaluators: [builtin.relevance]\n" +
				"    options:\n      evaluation_level: sentence\n",
			wantErr: "evaluation_level",
		},
		{
			name:    "duplicate dataset",
			body:    "datasets:\n  - name: d\n  - name: d\n",
			wantErr: "duplicate dataset name",
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

func TestResolveGroup(t *testing.T) {
	single := loadFromString(t, sampleDeployConfig)

	t.Run("only group is used when unnamed", func(t *testing.T) {
		g, err := single.ResolveGroup("")
		require.NoError(t, err)
		require.Equal(t, "pr-gate", g.Name)
	})

	t.Run("named group", func(t *testing.T) {
		g, err := single.ResolveGroup("pr-gate")
		require.NoError(t, err)
		require.Equal(t, "pr-gate", g.Name)
	})

	t.Run("unknown name is an error", func(t *testing.T) {
		_, err := single.ResolveGroup("nope")
		require.ErrorContains(t, err, "is not declared")
	})

	t.Run("ambiguous without a name", func(t *testing.T) {
		multi := loadFromString(t,
			"evalGroups:\n  - name: pr-gate\n    evaluators: [builtin.relevance]\n"+
				"  - name: nightly\n    evaluators: [builtin.relevance]\n")
		_, err := multi.ResolveGroup("")
		require.ErrorContains(t, err, "--eval-group")
		require.ErrorContains(t, err, "nightly")
	})

	t.Run("empty config", func(t *testing.T) {
		_, err := (&EvalConfig{}).ResolveGroup("")
		require.ErrorContains(t, err, "no eval groups")
	})
}

// local_dir accepts a directory or an explicit file path.
func TestArtifactPath(t *testing.T) {
	cases := []struct {
		name     string
		localDir string
		resource string
		ext      string
		want     string
	}{
		{"directory derives the file name", "datasets", "support-golden", ".jsonl",
			filepath.Join("base", "datasets", "support-golden.jsonl")},
		{"explicit file path is used as-is", "generated/datasets/support-golden.jsonl", "ignored", ".jsonl",
			filepath.Join("base", "generated", "datasets", "support-golden.jsonl")},
		{"empty local_dir falls back to the base", "", "support-quality", ".json",
			filepath.Join("base", "support-quality.json")},
		{"yaml rubric file path", "generated/rubrics/quality.yaml", "ignored", ".json",
			filepath.Join("base", "generated", "rubrics", "quality.yaml")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ArtifactPath("base", tc.localDir, tc.resource, tc.ext))
		})
	}
}
