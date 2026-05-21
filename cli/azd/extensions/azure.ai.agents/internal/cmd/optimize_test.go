// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

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
	assert.NotNil(t, cmd.Flags().Lookup("target"))
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
	assert.NotEmpty(t, cfg.InlineDataset)
	require.NotNil(t, cfg.Options)
	assert.Equal(t, "gpt-4o", cfg.Options.EvalModel)
	assert.Equal(t, "optimize", cfg.Options.Mode)
	assert.Equal(t, 5, cfg.Options.Budget)
	assert.Contains(t, cfg.Options.TargetAttributes, "instruction")
	assert.Contains(t, cfg.Options.TargetAttributes, "skill")
	require.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "builtin.task_adherence", cfg.Evaluators[0].Name)
}

// ---- LoadOptimizeConfig + reconcileConfigAgentName (--config path) ----

func TestLoadOptimizeConfig_ReconcileAgentName(t *testing.T) {
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

		changed := reconcileConfigAgentName(&cfg.Agent, "env-agent", cfgPath)
		assert.True(t, changed, "should report change when names differ")
		assert.Equal(t, "env-agent", cfg.Agent.Name, "environment name should take precedence")
	})

	t.Run("no change when names match", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfgPath := writeConfigYAML(t, dir, "same-agent")

		cfg, err := LoadOptimizeConfig(cfgPath)
		require.NoError(t, err)

		changed := reconcileConfigAgentName(&cfg.Agent, "same-agent", cfgPath)
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

		changed := reconcileConfigAgentName(&cfg.Agent, "env-agent", cfgPath)
		assert.False(t, changed, "filling empty name is not a 'change' (no conflict)")
		assert.Equal(t, "env-agent", cfg.Agent.Name)
	})

	t.Run("no-op when env name is empty", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfgPath := writeConfigYAML(t, dir, "config-agent")

		cfg, err := LoadOptimizeConfig(cfgPath)
		require.NoError(t, err)

		changed := reconcileConfigAgentName(&cfg.Agent, "", cfgPath)
		assert.False(t, changed)
		assert.Equal(t, "config-agent", cfg.Agent.Name, "original name preserved when env is empty")
	})
}
