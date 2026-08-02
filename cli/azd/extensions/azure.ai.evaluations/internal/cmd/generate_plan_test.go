// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/require"
)

// `generate` decides what to submit before it touches the network, so the plan
// it builds — which artifacts, from what instruction, at what sample size — is
// checkable without paying for a generation job. These are the parts that
// cannot be observed afterwards: once the jobs are submitted, a wrong default
// is indistinguishable from an intended one.

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "eval_generate.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// A spec is optional, so the defaults are what most callers actually run with.
func TestResolveGenerateConfigDefaultsFromFlagsAlone(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent.yaml")

	cfg, err := resolveGenerateConfig(absent, "shop-agent", "gpt-4o-mini", "", 0, 0)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	require.Equal(t, "shop-agent", cfg.Agent.Name)
	require.NotNil(t, cfg.Generate.Rubric)
	require.Equal(t, "shop-agent-quality", cfg.Generate.Rubric.Name)
	require.Equal(t, "gpt-4o-mini", cfg.Generate.Rubric.Model)
	require.Equal(t, "./"+project.DefaultEvaluatorsDir, cfg.Generate.Rubric.LocalDir)

	require.NotNil(t, cfg.Generate.Dataset)
	require.Equal(t, "shop-agent-golden", cfg.Generate.Dataset.Name)
	require.Equal(t, project.StrategySynthetic, cfg.Generate.Dataset.Strategy)
	require.Equal(t, project.DefaultSampleSize, cfg.Generate.Dataset.SampleSize)
	require.Equal(t, "./"+project.DefaultDatasetsDir, cfg.Generate.Dataset.LocalDir)
}

// Without a target there is nothing to generate from, and the refusal has to
// name the flag rather than a config field the caller may not have.
func TestResolveGenerateConfigRequiresATarget(t *testing.T) {
	_, err := resolveGenerateConfig(
		filepath.Join(t.TempDir(), "absent.yaml"), "", "gpt-4o-mini", "", 0, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--target")
}

func TestResolveGenerateConfigReadsTheSpec(t *testing.T) {
	path := writeConfig(t, `
agent:
  name: from-spec
  context:
    instructions: ./instructions.md
    traces:
      window: 7d
      source: ignored-today
      sample: 5
generate:
  rubric:
    name: spec-rubric
    model: gpt-4o
    local_dir: ./custom-evaluators
  dataset:
    name: spec-dataset
    strategy: synthetic
    sampleSize: 200
    local_dir: ./custom-datasets
`)

	cfg, err := resolveGenerateConfig(path, "", "", "", 0, 0)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	require.Equal(t, "from-spec", cfg.Agent.Name)
	require.Equal(t, "./instructions.md", cfg.Agent.Context.Instructions)
	require.Equal(t, "spec-rubric", cfg.Generate.Rubric.Name)
	require.Equal(t, "gpt-4o", cfg.Generate.Rubric.Model)
	require.Equal(t, "./custom-evaluators", cfg.Generate.Rubric.LocalDir)
	require.Equal(t, "spec-dataset", cfg.Generate.Dataset.Name)
	require.Equal(t, 200, cfg.Generate.Dataset.SampleSize)

	require.NotNil(t, cfg.Agent.Context.Traces)
	require.Equal(t, "7d", cfg.Agent.Context.Traces.Window)
	require.Equal(t, 5, cfg.Agent.Context.Traces.Sample)
}

// Flags win over the spec, which is what makes a one-off run possible without
// editing a file that is checked in.
func TestResolveGenerateConfigLayersFlagsOverTheSpec(t *testing.T) {
	path := writeConfig(t, `
agent:
  name: from-spec
generate:
  rubric:
    name: spec-rubric
    model: gpt-4o
  dataset:
    name: spec-dataset
    sampleSize: 200
`)

	cfg, err := resolveGenerateConfig(path, "from-flag", "gpt-4o-mini", "", 500, 14)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	require.Equal(t, "from-flag", cfg.Agent.Name)
	require.Equal(t, "gpt-4o-mini", cfg.Generate.Rubric.Model)
	require.Equal(t, 500, cfg.Generate.Dataset.SampleSize)
	require.Equal(t, "14d", cfg.Agent.Context.Traces.Window,
		"--trace-days must reach the spec as a window, since that is the only "+
			"trace field the generation API takes")

	// The rubric name is not derived when the spec named one, so a --target
	// override must not silently rename an artifact the spec author declared.
	require.Equal(t, "spec-rubric", cfg.Generate.Rubric.Name)
}

// A spec that declares a dataset without a size still has to submit a legal
// job, so the default is applied rather than left at zero.
func TestResolveGenerateConfigFillsAMissingSampleSize(t *testing.T) {
	path := writeConfig(t, `
agent:
  name: sized
generate:
  dataset:
    name: no-size
`)

	cfg, err := resolveGenerateConfig(path, "", "", "", 0, 0)
	require.NoError(t, err)
	require.Equal(t, project.DefaultSampleSize, cfg.Generate.Dataset.SampleSize)
	require.NoError(t, cfg.Validate())
}

// The bounds are the service's, and the boundaries themselves have to be
// accepted: a check that rejected 15 or 1000 would be indistinguishable from
// one that is simply too strict.
func TestGenerateSampleSizeBounds(t *testing.T) {
	for _, tc := range []struct {
		size    int
		allowed bool
	}{
		{project.MinSampleSize - 1, false},
		{project.MinSampleSize, true},
		{project.DefaultSampleSize, true},
		{project.MaxSampleSize, true},
		{project.MaxSampleSize + 1, false},
	} {
		cfg, err := resolveGenerateConfig(
			filepath.Join(t.TempDir(), "absent.yaml"),
			"bounded", "gpt-4o-mini", "", tc.size, 0)
		require.NoError(t, err)
		require.Equal(t, tc.size, cfg.Generate.Dataset.SampleSize)

		err = cfg.Validate()
		if tc.allowed {
			require.NoErrorf(t, err, "%d is inside the service's range", tc.size)
			continue
		}
		require.Errorf(t, err, "%d is outside the service's range", tc.size)
		require.Contains(t, err.Error(), "sampleSize")
	}
}

// --dataset and --evaluator both mean "use this one". Only the dataset side is
// resolved here; the evaluator side is decided in the command body, so it is
// covered by the CLI test that watches for the skip message.
func TestResolveGenerateConfigSkipsTheDatasetWhenOneIsSupplied(t *testing.T) {
	cfg, err := resolveGenerateConfig(
		filepath.Join(t.TempDir(), "absent.yaml"),
		"supplied", "gpt-4o-mini", "prod-sample", 0, 0)
	require.NoError(t, err)
	require.Nil(t, cfg.Generate.Dataset, "a supplied dataset must not be generated")
	require.NotNil(t, cfg.Generate.Rubric)
	require.NoError(t, cfg.Validate())
}

// Both jobs bill against one deployment, so a spec with no rubric has no model
// to run either of them.
func TestGenerationModelComesFromTheRubricSpec(t *testing.T) {
	require.Equal(t, "", generationModel(&project.GenerateConfig{}))

	cfg := &project.GenerateConfig{}
	cfg.Generate.Rubric = &project.RubricSpec{Name: "r", Model: "gpt-4o"}
	require.Equal(t, "gpt-4o", generationModel(cfg))
}

func TestResolveInstruction(t *testing.T) {
	dir := t.TempDir()
	filled := filepath.Join(dir, "instruction.md")
	require.NoError(t, os.WriteFile(filled, []byte("  test refunds and returns\n\n"), 0o600))
	blank := filepath.Join(dir, "blank.md")
	require.NoError(t, os.WriteFile(blank, []byte("   \n"), 0o600))

	t.Run("inline is returned as given", func(t *testing.T) {
		got, err := resolveInstruction("inline text", "")
		require.NoError(t, err)
		require.Equal(t, "inline text", got)
	})

	t.Run("a file is read and trimmed", func(t *testing.T) {
		got, err := resolveInstruction("", filled)
		require.NoError(t, err)
		require.Equal(t, "test refunds and returns", got)
	})

	// A whitespace-only file would otherwise generate from nothing, which
	// produces a rubric with no relation to the agent.
	t.Run("an empty file is refused", func(t *testing.T) {
		_, err := resolveInstruction("", blank)
		require.Error(t, err)
		require.Contains(t, err.Error(), "is empty")
	})

	t.Run("a missing file names the flag", func(t *testing.T) {
		_, err := resolveInstruction("", filepath.Join(dir, "absent.md"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "--agent-instruction-file")
	})
}

// The generation API takes a day window and nothing else, so the two fields it
// drops are reported rather than silently discarded.
func TestWarnIgnoredTraceFields(t *testing.T) {
	cfg := &project.GenerateConfig{}
	cfg.Agent.Context.Traces = &project.TraceSpec{Window: "7d", Source: "some-source", Sample: 5}

	var out strings.Builder
	warnIgnoredTraceFields(cfg, &out)
	require.Contains(t, out.String(), "agent.context.traces.source")
	require.Contains(t, out.String(), "agent.context.traces.sample")
	require.NotContains(t, out.String(), "agent.context.traces.window",
		"the window is the one trace field the API takes, so it is not a no-op")

	// A window on its own is fully supported and must not produce a warning.
	quiet := &project.GenerateConfig{}
	quiet.Agent.Context.Traces = &project.TraceSpec{Window: "7d"}
	out.Reset()
	warnIgnoredTraceFields(quiet, &out)
	require.Empty(t, out.String())
}
