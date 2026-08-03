// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/require"
)

// `generate` decides what to submit before it touches the network, so the plan
// it builds — which agent, what model, where the artifact lands, at what sample
// size — is checkable without paying for a generation job. These are the parts
// that cannot be observed afterwards: once the job is submitted, a wrong
// default is indistinguishable from an intended one.

// evalsDir writes a generation spec and returns the flags pointing at it.
func evalsDir(t *testing.T, generateBody string, files map[string]string) *generateFlags {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "generate.yaml")
	if generateBody != "" {
		require.NoError(t, os.WriteFile(configPath, []byte(generateBody), 0o600))
	}
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
	}
	return &generateFlags{configPath: configPath}
}

func loadSpec(t *testing.T, f *generateFlags) *project.GenerateConfig {
	t.Helper()
	cfg, err := project.LoadGenerateConfig(f.configPath)
	require.NoError(t, err)
	return cfg
}

// A spec is optional, so flags alone are what most callers actually run with.
func TestResolvePlan_FromFlagsAlone(t *testing.T) {
	f := evalsDir(t, "", nil)
	f.target = "shop-agent"
	f.model = "gpt-4o-mini"

	plan, err := resolvePlan(f, loadSpec(t, f), "shop-golden",
		datasetGenEntry(loadSpec(t, f), "shop-golden", 0))
	require.NoError(t, err)

	require.Equal(t, "shop-golden", plan.Name)
	require.Equal(t, "shop-agent", plan.Agent)
	require.Equal(t, "gpt-4o-mini", plan.Model)
	require.Equal(t, "./"+project.DefaultDatasetsDir, plan.OutputDir)
	require.Equal(t, project.DefaultSampleSize, plan.SampleSize)
}

// Without a model there is nothing to bill the job against, and the refusal has
// to name both ways of supplying one.
func TestResolvePlan_RequiresAGenerationModel(t *testing.T) {
	f := evalsDir(t, "", nil)
	f.target = "shop-agent"

	_, err := resolvePlan(f, loadSpec(t, f), "d", genEntry{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--generation-model")
	require.Contains(t, err.Error(), "generationModel")
}

// The spec is read per artifact name, so generating one artifact never picks up
// the other's settings.
func TestResolvePlan_ReadsTheNamedSpecEntry(t *testing.T) {
	f := evalsDir(t, `
generationModel: gpt-4o
dataset:
  spec-dataset:
    sampleSize: 200
    outputDir: ./custom-datasets
    deriveFrom: from-spec
evaluator:
  spec-rubric:
    outputDir: ./custom-evaluators
    deriveFrom: rubric-agent
`, nil)

	cfg := loadSpec(t, f)

	ds, err := resolvePlan(f, cfg, "spec-dataset", datasetGenEntry(cfg, "spec-dataset", 0))
	require.NoError(t, err)
	require.Equal(t, "gpt-4o", ds.Model)
	require.Equal(t, 200, ds.SampleSize)
	require.Equal(t, "./custom-datasets", ds.OutputDir)
	require.Equal(t, "from-spec", ds.Agent)

	ev, err := resolvePlan(f, cfg, "spec-rubric", evaluatorGenEntry(cfg, "spec-rubric", 0))
	require.NoError(t, err)
	require.Equal(t, "./custom-evaluators", ev.OutputDir)
	require.Equal(t, "rubric-agent", ev.Agent)

	// An artifact the spec says nothing about still generates, on the defaults.
	other, err := resolvePlan(f, cfg, "unlisted", datasetGenEntry(cfg, "unlisted", 0))
	require.NoError(t, err)
	require.Equal(t, project.DefaultSampleSize, other.SampleSize)
	require.Equal(t, "./"+project.DefaultDatasetsDir, other.OutputDir)
}

// Flags win over the spec, which is what makes a one-off run possible without
// editing a file that is checked in.
func TestResolvePlan_LayersFlagsOverTheSpec(t *testing.T) {
	f := evalsDir(t, `
generationModel: gpt-4o
dataset:
  spec-dataset:
    sampleSize: 200
    outputDir: ./custom-datasets
    deriveFrom: from-spec
`, nil)
	f.target = "from-flag"
	f.model = "gpt-4o-mini"
	f.outputDir = "./from-flag-dir"

	cfg := loadSpec(t, f)
	plan, err := resolvePlan(f, cfg, "spec-dataset", datasetGenEntry(cfg, "spec-dataset", 500))
	require.NoError(t, err)

	require.Equal(t, "from-flag", plan.Agent)
	require.Equal(t, "gpt-4o-mini", plan.Model)
	require.Equal(t, "./from-flag-dir", plan.OutputDir)
	require.Equal(t, 500, plan.SampleSize)
}

// The target is already declared on the eval, so `generate` does not need it
// repeated on every invocation.
func TestResolvePlan_FallsBackToTheEvalTarget(t *testing.T) {
	f := evalsDir(t, "generationModel: gpt-4o\n", map[string]string{
		"support-agent-smoke.yaml": "evaluators: [builtin.relevance]\n" +
			"target:\n  type: agent\n  name: support-agent\n",
	})

	cfg := loadSpec(t, f)
	plan, err := resolvePlan(f, cfg, "support-agent-smoke",
		datasetGenEntry(cfg, "support-agent-smoke", 0))
	require.NoError(t, err)
	require.Equal(t, "support-agent", plan.Agent,
		"the eval's declared target is the agent to generate from")
}

// With more than one eval the target is ambiguous, so nothing is guessed:
// generation falls back to the instruction alone rather than picking one.
func TestResolvePlan_AmbiguousEvalTargetIsNotGuessed(t *testing.T) {
	f := evalsDir(t, "generationModel: gpt-4o\n", map[string]string{
		"a.yaml": "target:\n  type: agent\n  name: agent-a\n",
		"b.yaml": "target:\n  type: agent\n  name: agent-b\n",
	})

	cfg := loadSpec(t, f)
	plan, err := resolvePlan(f, cfg, "d", datasetGenEntry(cfg, "d", 0))
	require.NoError(t, err)
	require.Empty(t, plan.Agent)

	// Naming one resolves it.
	f.evalName = "b"
	plan, err = resolvePlan(f, cfg, "d", datasetGenEntry(cfg, "d", 0))
	require.NoError(t, err)
	require.Equal(t, "agent-b", plan.Agent)
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
		err := project.ValidateSampleSize(tc.size)
		if tc.allowed {
			require.NoErrorf(t, err, "%d is inside the service's range", tc.size)
			continue
		}
		require.Errorf(t, err, "%d is outside the service's range", tc.size)
		require.Contains(t, err.Error(), "must be between")
	}
}

// Trace days come from the spec, and the flag overrides them.
func TestEvaluatorGenEntry_TraceDays(t *testing.T) {
	cfg := &project.GenerateConfig{
		Evaluator: map[string]project.EvaluatorGenSpec{"r": {TraceDays: 7}},
	}
	require.Equal(t, 7, evaluatorGenEntry(cfg, "r", 0).traceDays)
	require.Equal(t, 30, evaluatorGenEntry(cfg, "r", 30).traceDays)
	require.Zero(t, evaluatorGenEntry(cfg, "absent", 0).traceDays)
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
