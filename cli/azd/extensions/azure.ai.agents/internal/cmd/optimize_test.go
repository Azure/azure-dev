// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptimizeCommand_HasExpectedSubCommands(t *testing.T) {
	cmd := newOptimizeCommand(&azdext.ExtensionContext{})

	expected := []string{"status", "list", "cancel", "deploy"}
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
	assert.NotNil(t, cmd.Flags().Lookup("endpoint"))
	assert.NotNil(t, cmd.Flags().Lookup("agent"))
	assert.NotNil(t, cmd.Flags().Lookup("strategy"))
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
