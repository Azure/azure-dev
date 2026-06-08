// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/opt_eval"
	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptimizeCommand_HasExpectedSubCommands(t *testing.T) {
	cmd := newOptimizeCommand(&azdext.ExtensionContext{})

	expected := []string{"status", "list", "cancel", "deploy", "apply"}
	var actual []string
	for _, sub := range cmd.Commands() {
		actual = append(actual, sub.Name())
	}

	for _, name := range expected {
		assert.Contains(t, actual, name, "optimize should have sub-command %q", name)
	}
	assert.NotContains(t, actual, "run", "optimize should not have 'run' sub-command (merged into root)")
}

func TestOptimizeCommand_AcceptsPositionalArg(t *testing.T) {
	cmd := newOptimizeCommand(&azdext.ExtensionContext{})

	err := cmd.Args(cmd, []string{"my-agent"})
	assert.NoError(t, err)

	err = cmd.Args(cmd, []string{})
	assert.NoError(t, err)

	err = cmd.Args(cmd, []string{"my-agent", "extra"})
	assert.Error(t, err)
}

func TestOptimizeCommand_AcceptsConfigFlag(t *testing.T) {
	cmd := newOptimizeCommand(&azdext.ExtensionContext{})

	f := cmd.Flags().Lookup("config")
	require.NotNil(t, f, "--config flag should be registered")
	assert.Equal(t, "c", f.Shorthand, "--config should have -c shorthand")

	assert.NotNil(t, cmd.Flags().Lookup("poll-interval"))
}

func TestOptimizeCommand_DatasetFlag(t *testing.T) {
	t.Parallel()
	cmd := newOptimizeCommand(&azdext.ExtensionContext{})

	f := cmd.Flags().Lookup("dataset")
	require.NotNil(t, f, "--dataset flag should be registered")
	assert.Equal(t, "d", f.Shorthand, "--dataset should have -d shorthand")
	assert.Equal(t, "", f.DefValue, "--dataset default should be empty")
}

func TestOptimizeCommand_DefaultFlags(t *testing.T) {
	cmd := newOptimizeCommand(&azdext.ExtensionContext{})

	pollVal, err := cmd.Flags().GetInt("poll-interval")
	require.NoError(t, err)
	assert.Equal(t, 5, pollVal, "--poll-interval should default to 5")
}

func TestIsTerminal_ViaOptimizeAPI(t *testing.T) {
	assert.True(t, optimize_api.IsTerminal(optimize_api.StatusCompleted))
	assert.True(t, optimize_api.IsTerminal(optimize_api.StatusFailed))
	assert.True(t, optimize_api.IsTerminal(optimize_api.StatusCancelled))
	assert.False(t, optimize_api.IsTerminal(optimize_api.StatusRunning))
	assert.False(t, optimize_api.IsTerminal(optimize_api.StatusPending))
	assert.False(t, optimize_api.IsTerminal(""))
}

func TestTruncateString(t *testing.T) {
	assert.Equal(t, "abc", truncateString("abc", 10))
	assert.Equal(t, "abcdefg...", truncateString("abcdefghijk", 10))
	assert.Equal(t, "ab", truncateString("abcdef", 2))
}

func TestFormatOptimizeStatus(t *testing.T) {
	assert.NotEmpty(t, formatOptimizeStatus(optimize_api.StatusCompleted))
	assert.NotEmpty(t, formatOptimizeStatus(optimize_api.StatusFailed))
	assert.NotEmpty(t, formatOptimizeStatus(optimize_api.StatusCancelled))
	assert.NotEmpty(t, formatOptimizeStatus(optimize_api.StatusRunning))
	assert.NotEmpty(t, formatOptimizeStatus("unknown"))
}

// ---- defaultOptimizeConfig ----

func TestDefaultOptimizeConfig(t *testing.T) {
	t.Parallel()
	cfg := defaultOptimizeConfig("my-agent")

	assert.Equal(t, "my-agent", cfg.Agent.Name)
	require.NotNil(t, cfg.Options)
	assert.Empty(t, cfg.Options.EvalModel)
	require.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "builtin.task_adherence", cfg.Evaluators[0].Name)
}

// ---- LoadOptimizeConfig + reconcileConfigAgent (--config path) ----

func TestLoadOptimizeConfig_ReconcileAgent(t *testing.T) {
	t.Parallel()

	writeConfigYAML := func(t *testing.T, dir, agentName string) string {
		t.Helper()
		content := "agent:\n  name: " + agentName + "\noptions:\n  eval_model: gpt-4o\n  mode: optimize\n"
		path := filepath.Join(dir, "spec.yaml")
		require.NoError(t, os.WriteFile(path, []byte(content), 0600))
		return path
	}

	t.Run("env overrides config when names differ", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfgPath := writeConfigYAML(t, dir, "config-agent")

		cfg, err := LoadOptimizeConfig(cfgPath)
		require.NoError(t, err)
		assert.Equal(t, "config-agent", cfg.Agent.Name)

		changed := reconcileConfigAgent(&cfg.Agent, "env-agent", "", cfgPath)
		assert.True(t, changed, "should report change when names differ")
		assert.Equal(t, "env-agent", cfg.Agent.Name, "environment name should take precedence")
	})

	t.Run("no change when names match", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfgPath := writeConfigYAML(t, dir, "same-agent")

		cfg, err := LoadOptimizeConfig(cfgPath)
		require.NoError(t, err)

		changed := reconcileConfigAgent(&cfg.Agent, "same-agent", "", cfgPath)
		assert.False(t, changed)
		assert.Equal(t, "same-agent", cfg.Agent.Name)
	})

	t.Run("sets name when config has empty agent name", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		content := "agent:\n  kind: hosted\noptions:\n  eval_model: gpt-4o\n"
		cfgPath := filepath.Join(dir, "spec.yaml")
		require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0600))

		cfg, err := LoadOptimizeConfig(cfgPath)
		require.NoError(t, err)
		assert.Empty(t, cfg.Agent.Name)

		changed := reconcileConfigAgent(&cfg.Agent, "env-agent", "", cfgPath)
		assert.False(t, changed, "filling empty name is not a 'change' (no conflict)")
		assert.Equal(t, "env-agent", cfg.Agent.Name)
	})

	t.Run("no-op when env name is empty", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfgPath := writeConfigYAML(t, dir, "config-agent")

		cfg, err := LoadOptimizeConfig(cfgPath)
		require.NoError(t, err)

		changed := reconcileConfigAgent(&cfg.Agent, "", "", cfgPath)
		assert.False(t, changed)
		assert.Equal(t, "config-agent", cfg.Agent.Name, "original name preserved when env is empty")
	})
}

// ---- applyOverrides: --dataset flag ----

func TestApplyOverrides_DatasetFlag_LocalFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "data.jsonl")
	require.NoError(t, os.WriteFile(dataFile, []byte(`{"input":"hi"}`+"\n"), 0600))

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent: opt_eval.AgentRef{Name: "a", Instruction: opt_eval.InstructionRef{Value: "test"}},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o"},
	}

	action := &OptimizeAction{
		flags:    &optimizeFlags{dataset: dataFile},
		noPrompt: true,
	}

	err := action.applyOverrides(t.Context(), cfg, dir)
	require.NoError(t, err)
	assert.Equal(t, dataFile, cfg.DatasetFile)
	assert.Nil(t, cfg.DatasetReference)
}

func TestApplyOverrides_DatasetFlag_RegisteredName(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent: opt_eval.AgentRef{Name: "a", Instruction: opt_eval.InstructionRef{Value: "test"}},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o"},
	}

	action := &OptimizeAction{
		flags:    &optimizeFlags{dataset: "my-golden-dataset"},
		noPrompt: true,
	}

	err := action.applyOverrides(t.Context(), cfg, "")
	require.NoError(t, err)
	assert.Empty(t, cfg.DatasetFile)
	require.NotNil(t, cfg.DatasetReference)
	assert.Equal(t, "my-golden-dataset", cfg.DatasetReference.Name)
}

func TestApplyOverrides_DatasetFlag_OverridesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "new.jsonl")
	require.NoError(t, os.WriteFile(dataFile, []byte(`{"input":"hi"}`+"\n"), 0600))

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:            opt_eval.AgentRef{Name: "a", Instruction: opt_eval.InstructionRef{Value: "test"}},
			DatasetReference: &opt_eval.DatasetRef{Name: "old-ref"},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o"},
	}

	action := &OptimizeAction{
		flags:    &optimizeFlags{dataset: dataFile},
		noPrompt: true,
	}

	err := action.applyOverrides(t.Context(), cfg, dir)
	require.NoError(t, err)
	assert.Equal(t, dataFile, cfg.DatasetFile, "file should replace ref")
	assert.Nil(t, cfg.DatasetReference, "ref should be cleared")
}

// ---- eval.yaml auto-use in --no-prompt mode ----

func TestLoadOptimizeConfig_EvalYAML_WithDatasetFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Simulate an eval.yaml with dataset_file — the format generated by "azd ai agent eval init".
	dataFile := filepath.Join(dir, "data.jsonl")
	require.NoError(t, os.WriteFile(dataFile, []byte(`{"input":"hello"}`+"\n"), 0600))

	evalYAML := fmt.Sprintf(`agent:
  name: travel-agent
dataset_file: %s
evaluators:
  - name: builtin.task_adherence
options:
  eval_model: gpt-4o
`, dataFile)
	evalPath := filepath.Join(dir, "eval.yaml")
	require.NoError(t, os.WriteFile(evalPath, []byte(evalYAML), 0600))

	cfg, err := LoadOptimizeConfig(evalPath)
	require.NoError(t, err)
	assert.Equal(t, "travel-agent", cfg.Agent.Name)
	assert.Equal(t, dataFile, cfg.DatasetFile, "dataset_file from eval.yaml should be loaded")
	assert.Nil(t, cfg.DatasetReference)
	assert.Equal(t, "gpt-4o", cfg.Options.EvalModel)
}

func TestLoadOptimizeConfig_EvalYAML_WithDatasetReference(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	evalYAML := `agent:
  name: travel-agent
dataset_reference:
  name: golden-dataset
  version: "2"
evaluators:
  - name: builtin.task_adherence
options:
  eval_model: gpt-4o
`
	evalPath := filepath.Join(dir, "eval.yaml")
	require.NoError(t, os.WriteFile(evalPath, []byte(evalYAML), 0600))

	cfg, err := LoadOptimizeConfig(evalPath)
	require.NoError(t, err)
	assert.Equal(t, "travel-agent", cfg.Agent.Name)
	assert.Empty(t, cfg.DatasetFile)
	require.NotNil(t, cfg.DatasetReference, "dataset_reference from eval.yaml should be loaded")
	assert.Equal(t, "golden-dataset", cfg.DatasetReference.Name)
	assert.Equal(t, "2", cfg.DatasetReference.Version)
}

func TestApplyOverrides_NoPrompt_EvalYAML_WithDataset_Succeeds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write a real dataset file so validation passes.
	dataFile := filepath.Join(dir, "data.jsonl")
	require.NoError(t, os.WriteFile(dataFile, []byte(`{"input":"hi"}`+"\n"), 0600))

	// Config as if loaded from eval.yaml (has dataset already).
	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent:       opt_eval.AgentRef{Name: "travel-agent", Instruction: opt_eval.InstructionRef{Value: "You are a travel agent."}},
			DatasetFile: dataFile,
			Evaluators:  opt_eval.EvaluatorList{{Name: "builtin.task_adherence"}},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o"},
	}

	action := &OptimizeAction{
		flags:    &optimizeFlags{},
		noPrompt: true,
	}

	err := action.applyOverrides(t.Context(), cfg, "")
	require.NoError(t, err)
	assert.Equal(t, dataFile, cfg.DatasetFile, "dataset from eval.yaml should be preserved")
}

func TestResolveOptimizeDataset_NoPrompt_ReturnsError(t *testing.T) {
	t.Parallel()

	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent: opt_eval.AgentRef{Name: "a"},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o"},
	}

	err := resolveOptimizeDataset(t.Context(), nil, cfg, "", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a dataset is required")
}

func TestApplyOverrides_NoPrompt_NoDataset_ReturnsError(t *testing.T) {
	t.Parallel()

	// Config without dataset — simulates using defaultOptimizeConfig in --no-prompt.
	cfg := &OptimizeConfig{
		Config: opt_eval.Config{
			Agent: opt_eval.AgentRef{Name: "a", Instruction: opt_eval.InstructionRef{Value: "test"}},
		},
		Options: &opt_eval.Options{EvalModel: "gpt-4o"},
	}

	action := &OptimizeAction{
		flags:    &optimizeFlags{},
		noPrompt: true,
	}

	err := action.applyOverrides(t.Context(), cfg, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a dataset is required")
}

// ---- printOptimizeResults — table format ----

func TestPrintOptimizeResults_TableHasCandidateScorePass(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{
		Candidates: []optimize_api.CandidateResult{
			{Name: "baseline", AvgScore: 0.91, PassRate: 1.0},
			{Name: "candidate_1", AvgScore: 0.95, PassRate: 1.0},
		},
		Best: &optimize_api.CandidateResult{Name: "candidate_1"},
	}

	var buf strings.Builder
	printOptimizeResults(t.Context(), &buf, status, false, "")
	out := buf.String()

	// Verify header columns.
	assert.Contains(t, out, "Candidate")
	assert.Contains(t, out, "Score")
	assert.Contains(t, out, "Pass")

	// Verify no removed columns.
	assert.NotContains(t, out, "Strategies")
	assert.NotContains(t, out, "Tokens")
	assert.NotContains(t, out, "Optimal")

	// Verify candidate data.
	assert.Contains(t, out, "baseline")
	assert.Contains(t, out, "candidate_1")
	assert.Contains(t, out, "0.91")
	assert.Contains(t, out, "0.95")
	assert.Contains(t, out, "100%")
}

func TestPrintOptimizeResults_BestMarkedWithStar(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{
		Candidates: []optimize_api.CandidateResult{
			{Name: "baseline", AvgScore: 0.80, PassRate: 0.7},
			{Name: "candidate_1", AvgScore: 0.95, PassRate: 1.0},
		},
		Best: &optimize_api.CandidateResult{Name: "candidate_1"},
	}

	var buf strings.Builder
	printOptimizeResults(t.Context(), &buf, status, false, "")

	assert.Contains(t, buf.String(), "candidate_1 ★")
}

func TestPrintOptimizeResults_NoCandidates(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{}
	var buf strings.Builder
	printOptimizeResults(t.Context(), &buf, status, false, "")

	// Should print nothing for an empty candidates list.
	assert.Empty(t, buf.String())
}

func TestPrintOptimizeResults_ShowsCandidateIDs(t *testing.T) {
	t.Parallel()

	status := &optimize_api.OptimizeJobStatus{
		Candidates: []optimize_api.CandidateResult{
			{Name: "candidate_1", AvgScore: 0.95, PassRate: 1.0, CandidateID: "abc-123"},
		},
		Best: &optimize_api.CandidateResult{Name: "candidate_1", CandidateID: "abc-123"},
	}

	var buf strings.Builder
	printOptimizeResults(t.Context(), &buf, status, true, "")
	out := buf.String()

	assert.Contains(t, out, "Candidate IDs")
	assert.Contains(t, out, "abc-123")
	assert.Contains(t, out, "optimize apply")
}
