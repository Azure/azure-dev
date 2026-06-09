// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/opt_eval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- relativeDisplay ----

func TestRelativeDisplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		absPath    string
		projectDir string
		want       string
	}{
		{"relative path", filepath.Join("/project", "sub", "file.yaml"), "/project", filepath.Join("sub", "file.yaml")},
		{"same dir", filepath.Join("/project", "file.yaml"), "/project", "file.yaml"},
		{"empty absPath", "", "/project", ""},
		{"empty projectDir", "/project/file.yaml", "", "/project/file.yaml"},
		{"both empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := relativeDisplay(tt.absPath, tt.projectDir)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---- reconcileConfigAgent ----

func TestReconcileConfigAgent(t *testing.T) {
	t.Parallel()
	t.Run("no change when names match", func(t *testing.T) {
		t.Parallel()
		agent := &opt_eval.AgentRef{Name: "my-agent"}
		changed := reconcileConfigAgent(io.Discard, agent, "my-agent", "", "config.yaml")
		assert.False(t, changed)
		assert.Equal(t, "my-agent", agent.Name)
	})

	t.Run("sets name when agent name is empty", func(t *testing.T) {
		t.Parallel()
		agent := &opt_eval.AgentRef{}
		changed := reconcileConfigAgent(io.Discard, agent, "env-agent", "", "config.yaml")
		assert.False(t, changed)
		assert.Equal(t, "env-agent", agent.Name)
	})

	t.Run("overrides when names differ", func(t *testing.T) {
		t.Parallel()
		agent := &opt_eval.AgentRef{Name: "config-agent"}
		changed := reconcileConfigAgent(io.Discard, agent, "env-agent", "", "config.yaml")
		assert.True(t, changed)
		assert.Equal(t, "env-agent", agent.Name)
	})

	t.Run("no change when envName is empty", func(t *testing.T) {
		t.Parallel()
		agent := &opt_eval.AgentRef{Name: "my-agent"}
		changed := reconcileConfigAgent(io.Discard, agent, "", "", "config.yaml")
		assert.False(t, changed)
		assert.Equal(t, "my-agent", agent.Name)
	})

	t.Run("clears stale version when env has none", func(t *testing.T) {
		t.Parallel()
		agent := &opt_eval.AgentRef{Name: "a", Version: "old-v"}
		changed := reconcileConfigAgent(io.Discard, agent, "a", "", "config.yaml")
		assert.True(t, changed)
		assert.Empty(t, agent.Version)
	})

	t.Run("env version overrides config version", func(t *testing.T) {
		t.Parallel()
		agent := &opt_eval.AgentRef{Name: "a", Version: "old-v"}
		changed := reconcileConfigAgent(io.Discard, agent, "a", "new-v", "config.yaml")
		assert.True(t, changed)
		assert.Equal(t, "new-v", agent.Version)
	})
}

// ---- statusLabelAndColor ----

func TestStatusLabelAndColor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status    string
		wantLabel string
	}{
		{"completed", "Completed"},
		{"succeeded", "Succeeded"},
		{"failed", "Failed"},
		{"cancelled", "Cancelled"},
		{"canceled", "Cancelled"},
		{"running", "Running"},
		{"in_progress", "Running"},
		{"partial", "Partial"},
		{"", "No runs"},
		{"unknown_status", "unknown_status"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			t.Parallel()
			label, colorFn := statusLabelAndColor(tt.status)
			assert.Equal(t, tt.wantLabel, label)
			assert.NotNil(t, colorFn)
		})
	}
}

func TestColorizeStatus(t *testing.T) {
	t.Parallel()
	// colorizeStatus should return a non-empty string for any input.
	assert.NotEmpty(t, colorizeStatus("completed"))
	assert.NotEmpty(t, colorizeStatus("failed"))
	assert.NotEmpty(t, colorizeStatus(""))
	assert.NotEmpty(t, colorizeStatus("unknown"))
}

func TestPadColorizedStatus(t *testing.T) {
	t.Parallel()
	// padColorizedStatus should return a non-empty string for any input.
	result := padColorizedStatus("completed")
	assert.NotEmpty(t, result)
	// The padded string should be longer than the label due to padding + ANSI.
	assert.Contains(t, result, "Completed")
}

// ---- writeBaselineConfig ----

func TestWriteBaselineConfig(t *testing.T) {
	t.Parallel()
	t.Run("writes metadata and instruction file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		err := writeBaselineConfig(dir, baselineParams{
			Model:       "gpt-4o",
			Instruction: "You are a helpful assistant.",
		})
		require.NoError(t, err)

		metaPath := filepath.Join(dir, opt_eval.AgentConfigsDir, opt_eval.BaselineDir, opt_eval.MetadataFile)
		assert.FileExists(t, metaPath)

		instrPath := filepath.Join(dir, opt_eval.AgentConfigsDir, opt_eval.BaselineDir, opt_eval.InstructionFile)
		assert.FileExists(t, instrPath)
		content, err := os.ReadFile(instrPath) //nolint:gosec // test file path
		require.NoError(t, err)
		assert.Equal(t, "You are a helpful assistant.", string(content))
	})

	t.Run("writes metadata without instruction", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		err := writeBaselineConfig(dir, baselineParams{
			Model: "gpt-4o",
		})
		require.NoError(t, err)

		metaPath := filepath.Join(dir, opt_eval.AgentConfigsDir, opt_eval.BaselineDir, opt_eval.MetadataFile)
		assert.FileExists(t, metaPath)

		instrPath := filepath.Join(dir, opt_eval.AgentConfigsDir, opt_eval.BaselineDir, opt_eval.InstructionFile)
		assert.NoFileExists(t, instrPath)
	})

	t.Run("auto-detects skill dir", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "skills"), 0750))

		err := writeBaselineConfig(dir, baselineParams{
			Instruction: "test",
		})
		require.NoError(t, err)

		metaPath := filepath.Join(dir, opt_eval.AgentConfigsDir, opt_eval.BaselineDir, opt_eval.MetadataFile)
		data, err := os.ReadFile(metaPath) //nolint:gosec // test file path
		require.NoError(t, err)
		assert.Contains(t, string(data), "skill_dir")
	})
}

// ---- writeBaselineIfNeeded ----

func TestWriteBaselineIfNeeded(t *testing.T) {
	t.Parallel()
	t.Run("creates baseline when none exists", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		result := writeBaselineIfNeeded(dir, "test instruction")
		assert.NotEmpty(t, result)
		assert.FileExists(t, filepath.Join(dir, result))
	})

	t.Run("skips when baseline already exists", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Create existing baseline.
		absPath := filepath.Join(dir, opt_eval.BaselineConfigRelPath())
		require.NoError(t, os.MkdirAll(filepath.Dir(absPath), 0750))
		require.NoError(t, os.WriteFile(absPath, []byte("existing"), 0600))

		result := writeBaselineIfNeeded(dir, "test instruction")
		assert.Empty(t, result)
	})

	t.Run("returns empty for empty inputs", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, writeBaselineIfNeeded("", "instruction"))
		assert.Empty(t, writeBaselineIfNeeded("/some/dir", ""))
	})
}
