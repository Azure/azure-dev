// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/opteval"
	"azureaiagent/internal/pkg/agents/optimize_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- newOptimizeApplyCommand — command shape ----

func TestNewOptimizeApplyCommand_UseString(t *testing.T) {
	t.Parallel()
	cmd := newOptimizeApplyCommand(&azdext.ExtensionContext{})
	assert.Equal(t, "apply", cmd.Use)
}

func TestNewOptimizeApplyCommand_Flags(t *testing.T) {
	t.Parallel()
	cmd := newOptimizeApplyCommand(&azdext.ExtensionContext{})

	require.NotNil(t, cmd.Flags().Lookup("candidate"))
	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("endpoint"))
	require.NotNil(t, cmd.Flags().Lookup("project-endpoint"))
}

func TestNewOptimizeApplyCommand_CandidateIsRequired(t *testing.T) {
	t.Parallel()
	cmd := newOptimizeApplyCommand(&azdext.ExtensionContext{})
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "candidate")
}

// ---- printPreviewLines ----

func TestPrintPreviewLines(t *testing.T) {
	t.Parallel()

	// Disable color output so assertions don't need ANSI codes.
	color.NoColor = true

	tests := []struct {
		name   string
		lines  []string
		prefix string
		want   []string // substrings expected in output
	}{
		{
			"fewer lines than limit",
			[]string{"line1", "line2"},
			"+ ",
			[]string{"+ line1", "+ line2"},
		},
		{
			"exactly at limit",
			[]string{"a", "b", "c", "d"},
			"- ",
			[]string{"- a", "- b", "- c", "- d"},
		},
		{
			"exceeds limit shows truncation",
			[]string{"a", "b", "c", "d", "e", "f"},
			"+ ",
			[]string{"+ a", "+ b", "+ c", "+ d", "... (2 more lines)"},
		},
		{
			"empty lines",
			[]string{},
			"- ",
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			c := color.New(color.FgWhite)
			printPreviewLines(&buf, tt.lines, tt.prefix, c)
			out := buf.String()
			for _, s := range tt.want {
				assert.Contains(t, out, s)
			}
			if tt.want == nil {
				assert.Empty(t, out)
			}
		})
	}
}

// ---- printPromptDiff ----

func TestPrintPromptDiff(t *testing.T) {
	t.Parallel()

	color.NoColor = true

	t.Run("shows diff when baseline and candidate have instructions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		// Set up baseline with metadata that points to an instruction file.
		baselineDir := filepath.Join(dir, agentConfigsDir, opteval.BaselineDir)
		require.NoError(t, os.MkdirAll(baselineDir, 0750))
		require.NoError(t, os.WriteFile(
			filepath.Join(baselineDir, opteval.InstructionFile),
			[]byte("You are a baseline assistant.\nLine two."),
			0600,
		))
		require.NoError(t, os.WriteFile(
			filepath.Join(baselineDir, opteval.MetadataFile),
			[]byte("instruction_file: instructions.md\nmodel: gpt-4o\n"),
			0600,
		))

		candidateConfig := map[string]any{
			"systemPrompt": "You are an optimized assistant.\nNew line two.\nNew line three.",
		}

		var buf bytes.Buffer
		printPromptDiff(&buf, dir, "cand1", candidateConfig)
		out := buf.String()

		assert.Contains(t, out, "Instruction diff")
		assert.Contains(t, out, "Baseline")
		assert.Contains(t, out, "Optimized")
		assert.Contains(t, out, "You are a baseline assistant.")
		assert.Contains(t, out, "You are an optimized assistant.")
	})

	t.Run("no output when candidate has no instructions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		candidateConfig := map[string]any{"model": "gpt-4o"}

		var buf bytes.Buffer
		printPromptDiff(&buf, dir, "cand1", candidateConfig)
		assert.Empty(t, buf.String())
	})

	t.Run("no output when baseline config missing", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		candidateConfig := map[string]any{"systemPrompt": "optimized"}

		var buf bytes.Buffer
		printPromptDiff(&buf, dir, "cand1", candidateConfig)
		assert.Empty(t, buf.String())
	})

	t.Run("no output when baseline has no instruction file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		// Write metadata without instruction_file.
		baselineDir := filepath.Join(dir, agentConfigsDir, opteval.BaselineDir)
		require.NoError(t, os.MkdirAll(baselineDir, 0750))
		require.NoError(t, os.WriteFile(
			filepath.Join(baselineDir, opteval.MetadataFile),
			[]byte("model: gpt-4o\n"),
			0600,
		))

		candidateConfig := map[string]any{"systemPrompt": "optimized"}

		var buf bytes.Buffer
		printPromptDiff(&buf, dir, "cand1", candidateConfig)
		assert.Empty(t, buf.String())
	})
}

// ---- extractInstructions ----

func TestExtractInstructions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		config any
		want   string
	}{
		{
			"systemPrompt field",
			map[string]any{"systemPrompt": "You are a helpful assistant."},
			"You are a helpful assistant.",
		},
		{
			"instructions field",
			map[string]any{"instructions": "Follow the rules."},
			"Follow the rules.",
		},
		{
			"systemPrompt takes precedence",
			map[string]any{
				"systemPrompt": "From systemPrompt",
				"instructions": "From instructions",
			},
			"From systemPrompt",
		},
		{"nil config", nil, ""},
		{"non-map config", "just a string", ""},
		{"empty map", map[string]any{}, ""},
		{"non-string value", map[string]any{"systemPrompt": 42}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, extractInstructions(tt.config))
		})
	}
}

// ---- agentConfigMetadata.resolveInstructions ----

func TestAgentConfigMetadata_ResolveInstructions(t *testing.T) {
	t.Parallel()
	t.Run("reads instruction file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "instructions.md"), []byte("Be helpful."), 0600))

		meta := &agentConfigMetadata{InstructionFile: "instructions.md"}
		assert.Equal(t, "Be helpful.", meta.resolveInstructions(dir))
	})

	t.Run("returns empty when no file set", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{}
		assert.Empty(t, meta.resolveInstructions(t.TempDir()))
	})

	t.Run("returns empty when file missing", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{InstructionFile: "nonexistent.md"}
		assert.Empty(t, meta.resolveInstructions(t.TempDir()))
	})
}

// ---- agentConfigMetadata.resolveSkillDir ----

func TestAgentConfigMetadata_ResolveSkillDir(t *testing.T) {
	t.Parallel()
	t.Run("returns empty when not set", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{}
		assert.Empty(t, meta.resolveSkillDir("/some/dir"))
	})

	t.Run("resolves relative path", func(t *testing.T) {
		t.Parallel()
		meta := &agentConfigMetadata{SkillDir: "skills"}
		result := meta.resolveSkillDir("/project/config")
		assert.Equal(t, filepath.Join("/project/config", "skills"), result)
	})

	t.Run("preserves absolute path", func(t *testing.T) {
		t.Parallel()
		abs := filepath.Join(os.TempDir(), "absolute-skills")
		meta := &agentConfigMetadata{SkillDir: abs}
		assert.Equal(t, abs, meta.resolveSkillDir("/any/dir"))
	})
}

// ---- writeAgentConfigFromCandidate ----

func TestWriteAgentConfigFromCandidate(t *testing.T) {
	t.Parallel()
	t.Run("writes metadata and instructions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		config := map[string]any{
			"name":         "test-agent",
			"model":        "gpt-4o",
			"systemPrompt": "Test prompt.",
		}

		err := writeAgentConfigFromCandidate(dir, config)
		require.NoError(t, err)

		assert.FileExists(t, filepath.Join(dir, opteval.MetadataFile))
		assert.FileExists(t, filepath.Join(dir, opteval.InstructionFile))

		content, err := os.ReadFile(filepath.Join(dir, opteval.InstructionFile))
		require.NoError(t, err)
		assert.Equal(t, "Test prompt.", string(content))
	})

	t.Run("writes inline skills", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		config := map[string]any{
			"systemPrompt": "prompt",
			"skills": []any{
				map[string]any{
					"name":        "search",
					"description": "Search the web",
					"body":        "Search content here.",
				},
			},
		}

		err := writeAgentConfigFromCandidate(dir, config)
		require.NoError(t, err)

		skillFile := filepath.Join(dir, opteval.SkillsDir, "search", "SKILL.md")
		assert.FileExists(t, skillFile)
	})

	t.Run("handles nil config gracefully", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		err := writeAgentConfigFromCandidate(dir, nil)
		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(dir, opteval.MetadataFile))
	})
}

// ---- cleanOtherCandidates ----

func TestCleanOtherCandidates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create baseline, current candidate, and old candidate directories.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, opteval.BaselineDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cand_current"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cand_old"), 0750))

	var buf bytes.Buffer
	cleanOtherCandidates(dir, "cand_current", &buf)

	// baseline and cand_current should remain; cand_old should be removed.
	assert.DirExists(t, filepath.Join(dir, opteval.BaselineDir))
	assert.DirExists(t, filepath.Join(dir, "cand_current"))
	assert.NoDirExists(t, filepath.Join(dir, "cand_old"))
}

// ---- isSkillFile ----

func TestIsSkillFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file optimize_api.CandidateFile
		want bool
	}{
		{"skill type", optimize_api.CandidateFile{Type: "skill", Path: "foo.md"}, true},
		{"skills path prefix", optimize_api.CandidateFile{Type: "file", Path: "skills/search/SKILL.md"}, true},
		{"other type and path", optimize_api.CandidateFile{Type: "file", Path: "config.yaml"}, false},
		{"empty", optimize_api.CandidateFile{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isSkillFile(tt.file))
		})
	}
}

// ---- isReservedEnvVarError ----

func TestIsReservedEnvVarError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"reserved for platform use", fmt.Errorf("variable is reserved for platform use"), true},
		{"AGENT_* variables", fmt.Errorf("AGENT_* variables are reserved"), true},
		{"unrelated error", fmt.Errorf("connection refused"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isReservedEnvVarError(tt.err))
		})
	}
}
