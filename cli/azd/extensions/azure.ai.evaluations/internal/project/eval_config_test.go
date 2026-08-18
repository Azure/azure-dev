// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// sampleEvalConfig is the shape the spec documents for evals/eval.yaml: two
// catalogs, then the evals defined over them.
const sampleEvalConfig = `
datasets:
  - name: support-golden
    source: ./datasets/support-golden.jsonl
    version: "1"
  - name: prod-registered

evaluators:
  - name: support-quality
    source: ./evaluators/support-quality.json

evals:
  - name: support-agent-smoke
    description: Quality gate for the support agent
    dataset: support-golden
    evaluation_level: conversation
    max_samples: 100
    evaluators:
      - evaluator: builtin.task_adherence
      - evaluator: support-quality
        name: quality_strict
        initialization_parameters:
          deployment_name: gpt-4.1-nano
    target:
      type: agent
      name: support-agent

  - name: support-agent-trace-eval
    source:
      type: traces
      agent_name: support-agent
      max_traces: 20
    evaluators:
      - evaluator: builtin.task_adherence
`

func loadFromString(t *testing.T, body string) *EvalConfig {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(EvalConfigPath(dir), []byte(body), 0o600))
	cfg, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	return cfg
}

func TestLoadEvalConfig_ParsesAllSections(t *testing.T) {
	cfg := loadFromString(t, sampleEvalConfig)

	require.Len(t, cfg.Datasets, 2)
	require.Equal(t, "support-golden", cfg.Datasets[0].Name)
	require.Equal(t, "./datasets/support-golden.jsonl", cfg.Datasets[0].Source)
	require.Equal(t, "1", cfg.Datasets[0].Version)

	require.Len(t, cfg.Evaluators, 1)
	require.Equal(t, "support-quality", cfg.Evaluators[0].Name)

	require.Equal(t, []string{"support-agent-smoke", "support-agent-trace-eval"}, cfg.EvalNames())
}

// One file holds many evals, and each is selected by its own name.
func TestEval_SelectsByName(t *testing.T) {
	cfg := loadFromString(t, sampleEvalConfig)

	eval, err := cfg.Eval("support-agent-smoke")
	require.NoError(t, err)
	require.Equal(t, "support-golden", eval.Dataset)
	require.Equal(t, "Quality gate for the support agent", eval.Description)
	require.Equal(t, EvaluationLevelConversation, eval.EvaluationLevel)
	require.Equal(t, 100, eval.MaxSamples)
	require.Len(t, eval.Evaluators, 2)
	require.Equal(t, TargetTypeAgent, eval.Target.Type)
	require.Equal(t, "support-agent", eval.Target.Name)
}

// A trace-backed eval invokes nothing, so agent_name filters rather than targets.
func TestEval_TraceSourceHasNoTarget(t *testing.T) {
	cfg := loadFromString(t, sampleEvalConfig)

	eval, err := cfg.Eval("support-agent-trace-eval")
	require.NoError(t, err)
	require.Nil(t, eval.Target)
	require.Equal(t, SourceTypeTraces, eval.Source.Type)
	require.Equal(t, "support-agent", eval.Source.AgentName)
	require.Equal(t, 20, eval.Source.MaxTraces)
}

// An unnamed selection is only answered when the file declares exactly one,
// because guessing which eval a command meant is noticed only after it runs.
func TestEval_UnnamedIsAmbiguousWithSeveral(t *testing.T) {
	cfg := loadFromString(t, sampleEvalConfig)

	_, err := cfg.Eval("")
	require.ErrorContains(t, err, "--eval")
	require.ErrorContains(t, err, "support-agent-trace-eval")

	single := loadFromString(t, "evals:\n  - name: only\n    evaluators:\n      - evaluator: builtin.relevance\n")
	eval, err := single.Eval("")
	require.NoError(t, err)
	require.Equal(t, "only", eval.Name)
}

func TestEval_UnknownNameNamesWhatIsDeclared(t *testing.T) {
	cfg := loadFromString(t, sampleEvalConfig)

	_, err := cfg.Eval("nope")
	require.ErrorContains(t, err, "is not declared")
	require.ErrorContains(t, err, "support-agent-smoke")
}

// HasEval never falls back to "the only one", so a collision check cannot match
// a differently named entry.
func TestHasEvalAndRemoveEval(t *testing.T) {
	cfg := loadFromString(t, sampleEvalConfig)

	require.True(t, cfg.HasEval("support-agent-smoke"))
	require.False(t, cfg.HasEval("nope"))
	require.False(t, cfg.HasEval(""))

	require.True(t, cfg.RemoveEval("support-agent-smoke"))
	require.False(t, cfg.HasEval("support-agent-smoke"))
	require.Equal(t, []string{"support-agent-trace-eval"}, cfg.EvalNames())
	require.False(t, cfg.RemoveEval("support-agent-smoke"))
}

// Only catalog entries carrying a local source are this config's to publish.
// One without a source already exists on the project.
func TestCustomEvaluatorsAndLocalDatasets_OnlyOwnLocalSources(t *testing.T) {
	cfg := loadFromString(t, sampleEvalConfig)

	owned := cfg.CustomEvaluators()
	require.Len(t, owned, 1)
	require.Equal(t, "support-quality", owned[0].Name)
	require.Equal(t, "./evaluators/support-quality.json", owned[0].Source)

	local := cfg.LocalDatasets()
	require.Len(t, local, 1)
	require.Equal(t, "support-golden", local[0].Name,
		"prod-registered has no source, so it is already on the project")
}

func TestDeclarationLookups(t *testing.T) {
	cfg := loadFromString(t, sampleEvalConfig)

	ds, ok := cfg.DatasetDeclaration("support-golden")
	require.True(t, ok)
	require.Equal(t, "./datasets/support-golden.jsonl", ds.Source)

	_, ok = cfg.DatasetDeclaration("missing")
	require.False(t, ok)

	ev, ok := cfg.EvaluatorDeclaration("support-quality")
	require.True(t, ok)
	require.Equal(t, "./evaluators/support-quality.json", ev.Source)
}

// The configuration must survive a write/read cycle, because init and generate
// both append to a file they just read.
func TestEvalConfig_RoundTripsThroughTheStore(t *testing.T) {
	dir := t.TempDir()
	cfg := loadFromString(t, sampleEvalConfig)

	require.NoError(t, SaveEvalConfig(dir, cfg))
	back, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	require.Equal(t, cfg, back)
}

// A missing file is an ordinary state: generate runs before init.
func TestOpenEvalConfig_MissingIsNotAnError(t *testing.T) {
	cfg, err := OpenEvalConfig(t.TempDir())
	require.NoError(t, err)
	require.Nil(t, cfg)
}

// SaveEvalConfig creates the directory, so generate can record an artifact in a
// project that has never run init.
func TestSaveEvalConfig_CreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evals")
	require.NoError(t, SaveEvalConfig(dir, &EvalConfig{
		Datasets: []DatasetDecl{{Name: "generated", Source: "./datasets/generated.jsonl"}},
	}))

	cfg, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)
	require.Empty(t, cfg.Evals, "a generate-only file is inert until init wires an eval")
}

func TestValidate_Accepts(t *testing.T) {
	require.NoError(t, loadFromString(t, sampleEvalConfig).Validate())
}

func TestValidate_Rejects(t *testing.T) {
	const oneEval = "evals:\n  - name: e\n    evaluators:\n      - evaluator: builtin.relevance\n"

	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			// The run path refuses a trace source that does not say whose
			// conversations to read. Accepting it here would deploy a config
			// that cannot run.
			name: "trace source naming no agent",
			body: "evals:\n  - name: e\n    source:\n      type: traces\n" +
				"    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "source.agent_name is required",
		},
		{
			name: "responses source listing no ids",
			body: "evals:\n  - name: e\n    source:\n      type: responses\n" +
				"    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "source.response_ids is required",
		},
		{
			// A window bound the run path cannot parse is dropped by the
			// service, which then grades a default seven days and says nothing.
			name: "window bound that is not a time",
			body: "evals:\n  - name: e\n    source:\n      type: traces\n      agent_name: a\n" +
				"      start_time: yesterday\n    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "which is not a time",
		},
		{
			name: "window ending before it starts",
			body: "evals:\n  - name: e\n    source:\n      type: traces\n      agent_name: a\n" +
				"      start_time: \"2026-08-02T00:00:00Z\"\n      end_time: \"2026-08-01T00:00:00Z\"\n" +
				"    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "holds no traces",
		},
		{
			name: "window declared twice over",
			body: "evals:\n  - name: e\n    source:\n      type: traces\n      agent_name: a\n" +
				"      start_time: \"2026-08-01T00:00:00Z\"\n      lookback_hours: 24\n" +
				"    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "keep one",
		},
		{
			name: "lookback reaching forwards",
			body: "evals:\n  - name: e\n    source:\n      type: traces\n      agent_name: a\n" +
				"      lookback_hours: -24\n    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "cannot reach into the future",
		},
		{
			// Large enough to overflow the nanosecond duration the hours become,
			// which wraps the start bound into the future and reads no traces.
			name: "lookback beyond what a duration holds",
			body: "evals:\n  - name: e\n    source:\n      type: traces\n      agent_name: a\n" +
				"      lookback_hours: 100000000\n    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "beyond the",
		},
		{
			// A window written as a lookback is reported as a lookback: naming
			// start_time sends the reader to a key their file lacks.
			name: "lookback that opens after the window ends",
			body: "evals:\n  - name: e\n    source:\n      type: traces\n      agent_name: a\n" +
				"      lookback_hours: 1\n      end_time: \"2020-01-01T00:00:00Z\"\n" +
				"    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "source.lookback_hours is 1, which opens the window after",
		},
		{
			// Parses, then reads as "no bound" everywhere after, so the bound
			// the file declared would be dropped from the request in silence.
			name: "window bound at the zero time",
			body: "evals:\n  - name: e\n    source:\n      type: traces\n      agent_name: a\n" +
				"      start_time: \"0001-01-01T00:00:00Z\"\n" +
				"    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "not a window any traces fall in",
		},
		{
			name: "negative trace cap",
			body: "evals:\n  - name: e\n    source:\n      type: traces\n      agent_name: a\n" +
				"      max_traces: -5\n    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "source.max_traces",
		},
		{
			name:    "dataset without a name",
			body:    "datasets:\n  - source: ./d.jsonl\n" + oneEval,
			wantErr: "'name' is required",
		},
		{
			name:    "duplicate dataset",
			body:    "datasets:\n  - name: d\n  - name: d\n" + oneEval,
			wantErr: "duplicate dataset name",
		},
		{
			name:    "built-in declared in the catalog",
			body:    "evaluators:\n  - name: builtin.relevance\n" + oneEval,
			wantErr: "needs no catalog entry",
		},
		{
			name:    "version pinned alongside a source",
			body:    "evaluators:\n  - name: q\n    source: ./q.json\n    version: \"3\"\n" + oneEval,
			wantErr: "cannot be set with `source`",
		},
		{
			name: "no evals",
			body: "datasets:\n  - name: d\n",
			// A catalog with no eval is what `generate` leaves behind, so the
			// error has to name the command that declares one.
			wantErr: "azd ai eval init",
		},
		{
			name:    "duplicate eval",
			body:    oneEval + "  - name: e\n    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "duplicate eval name",
		},
		{
			name:    "no evaluators",
			body:    "evals:\n  - name: e\n    evaluators: []\n",
			wantErr: "at least one evaluator is required",
		},
		{
			name: "dataset and source both declared",
			body: "datasets:\n  - name: d\nevals:\n  - name: e\n    dataset: d\n" +
				"    source:\n      type: traces\n    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "declare one",
		},
		{
			name:    "dataset not in the catalog",
			body:    "evals:\n  - name: e\n    dataset: missing\n    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "not in the datasets catalog",
		},
		{
			name:    "evaluator not in the catalog",
			body:    "evals:\n  - name: e\n    evaluators:\n      - evaluator: quality\n",
			wantErr: "not in the evaluators catalog",
		},
		{
			name: "duplicate criterion",
			body: "evals:\n  - name: e\n    evaluators:\n" +
				"      - evaluator: builtin.relevance\n      - evaluator: builtin.relevance\n",
			wantErr: "duplicate criterion",
		},
		{
			name:    "unsupported source type",
			body:    "evals:\n  - name: e\n    source:\n      type: prompt\n    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "is not supported",
		},
		{
			name: "unsupported target type",
			body: "evals:\n  - name: e\n    evaluators:\n      - evaluator: builtin.relevance\n" +
				"    target:\n      type: prompt\n",
			wantErr: "is not supported",
		},
		{
			name: "invalid evaluation level",
			body: "evals:\n  - name: e\n    evaluation_level: sentence\n" +
				"    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "evaluation_level",
		},
		{
			name: "two evals differing only by name",
			body: "evals:\n  - name: a\n    evaluators:\n      - evaluator: builtin.relevance\n" +
				"  - name: b\n    evaluators:\n      - evaluator: builtin.relevance\n",
			wantErr: "identical to",
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

// Two evals that differ only in substance still resolve unambiguously by name,
// and the clash only matters to something about to deploy. Enforcing it on the
// way to a lookup stranded `run list --eval <name>`, which had already been
// told which eval it meant, behind an error whose only escape was hand-editing
// the config.
func TestValidateForLookupAllowsWhatOnlyDeployingCannotTellApart(t *testing.T) {
	body := "evals:\n  - name: a\n    evaluators:\n      - evaluator: builtin.relevance\n" +
		"  - name: b\n    evaluators:\n      - evaluator: builtin.relevance\n"
	cfg := loadFromString(t, body)

	require.NoError(t, cfg.ValidateForLookup())
	require.Error(t, cfg.Validate(), "deploying still cannot tell the two apart")
}

// Lookup still depends on names being unique, so that check stays.
func TestValidateForLookupStillRefusesADuplicateName(t *testing.T) {
	body := "evals:\n  - name: a\n    evaluators:\n      - evaluator: builtin.relevance\n" +
		"  - name: a\n    evaluators:\n      - evaluator: builtin.coherence\n"

	err := loadFromString(t, body).ValidateForLookup()

	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate")
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
