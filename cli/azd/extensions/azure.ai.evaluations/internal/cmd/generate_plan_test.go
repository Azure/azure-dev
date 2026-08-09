// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/require"
)

// `generate` decides what to submit before it touches the network, so the plan
// it builds — which agent, what model, where the artifact lands — is checkable
// without paying for a generation job. These are the parts that cannot be
// observed afterwards: once the job is submitted, a wrong default is
// indistinguishable from an intended one.

// evalsDir returns flags pointing at an empty eval directory.
func evalsDir(t *testing.T) *generateFlags {
	t.Helper()
	return &generateFlags{path: t.TempDir()}
}

// withEvals writes a configuration into the flags' directory.
func withEvals(t *testing.T, f *generateFlags, evals ...project.Eval) {
	t.Helper()
	require.NoError(t, project.SaveEvalConfig(f.path, &project.EvalConfig{Evals: evals}))
}

// Generation settings are flags only: there is no generate.yaml, because the
// artifact is checked in and regeneration usually wants different settings.
func TestResolvePlan_FromFlagsAlone(t *testing.T) {
	f := evalsDir(t)
	f.target = "shop-agent"
	f.model = "gpt-4o-mini"

	plan, err := resolvePlan(f, "shop-golden", project.DefaultDatasetsDir)
	require.NoError(t, err)

	require.Equal(t, "shop-golden", plan.Name)
	require.Equal(t, "shop-agent", plan.Agent)
	require.Equal(t, "gpt-4o-mini", plan.Model)
	require.Equal(t, "./"+project.DefaultDatasetsDir, plan.OutputDir)
	require.Equal(t, f.path, plan.BaseDir)
}

// Each generate has its own default output directory, so a rubric never lands
// in the datasets folder.
func TestResolvePlan_OutputDirDefaultsPerArtifact(t *testing.T) {
	f := evalsDir(t)
	f.target = "shop-agent"
	f.model = "gpt-4o-mini"

	ds, err := resolvePlan(f, "d", project.DefaultDatasetsDir)
	require.NoError(t, err)
	require.Equal(t, "./"+project.DefaultDatasetsDir, ds.OutputDir)

	ev, err := resolvePlan(f, "r", project.DefaultEvaluatorsDir)
	require.NoError(t, err)
	require.Equal(t, "./"+project.DefaultEvaluatorsDir, ev.OutputDir)

	f.outputDir = "./from-flag"
	override, err := resolvePlan(f, "d", project.DefaultDatasetsDir)
	require.NoError(t, err)
	require.Equal(t, "./from-flag", override.OutputDir)
}

// A named target carries a deployment, and the spec makes it the default, so
// the plan settles without one and lets prepareGeneration read it. Refusing
// here would ask the caller for something the project already knows.
func TestResolvePlan_DefersToTheAgentForTheModel(t *testing.T) {
	f := evalsDir(t)
	f.target = "shop-agent"

	plan, err := resolvePlan(f, "d", project.DefaultDatasetsDir)
	require.NoError(t, err)
	require.Empty(t, plan.Model, "the deployment is read from the agent, not guessed here")
	require.Equal(t, "shop-agent", plan.Agent)
}

// With no target either there is nothing to read a deployment from, so the
// refusal happens before authentication and names the flag that supplies one.
func TestResolvePlan_RequiresAGenerationModelWithNoAgent(t *testing.T) {
	f := evalsDir(t)

	_, err := resolvePlan(f, "d", project.DefaultDatasetsDir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--generation-model")
}

// An input the caller named and got wrong is reported ahead of one they simply
// left out. Both checks are local, so the only thing deciding which the user
// sees is the order they run in — and a missing instruction file is a typo the
// caller can act on, while the model has a documented default path.
func TestResolvePlan_ReportsABadExplicitInputFirst(t *testing.T) {
	f := evalsDir(t)
	f.target = "shop-agent"
	f.instructionFile = filepath.Join(t.TempDir(), "absent.md")

	_, err := resolvePlan(f, "d", project.DefaultDatasetsDir)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--agent-instruction-file",
		"the flag the caller got wrong must win over the one they omitted")
}

// The target is already declared on an eval, so `generate` does not need it
// repeated on every invocation.
func TestResolvePlan_FallsBackToTheDeclaredTarget(t *testing.T) {
	f := evalsDir(t)
	f.model = "gpt-4o"
	withEvals(t, f, project.Eval{
		Name:       "support-agent-eval",
		Evaluators: evalcore.EvaluatorList{{Evaluator: "builtin.relevance"}},
		Target:     &project.Target{Type: project.TargetTypeAgent, Name: "support-agent"},
	})

	plan, err := resolvePlan(f, "d", project.DefaultDatasetsDir)
	require.NoError(t, err)
	require.Equal(t, "support-agent", plan.Agent,
		"the declared target is the agent to generate from")

	// An explicit flag still wins, which is what makes a one-off run possible
	// without editing a file that is checked in.
	f.target = "from-flag"
	plan, err = resolvePlan(f, "d", project.DefaultDatasetsDir)
	require.NoError(t, err)
	require.Equal(t, "from-flag", plan.Agent)
}

// With no configuration at all, generation runs from the instruction alone.
// This is the golden path: both generates precede init.
func TestResolvePlan_NoConfigurationYet(t *testing.T) {
	f := evalsDir(t)
	f.model = "gpt-4o"
	f.instruction = "test refunds and returns"

	plan, err := resolvePlan(f, "d", project.DefaultDatasetsDir)
	require.NoError(t, err)
	require.Empty(t, plan.Agent)
	require.Equal(t, "test refunds and returns", plan.Instruction)
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
