// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/optconfig"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestRelativeDisplay(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, path, projectDir, want string
	}{
		{"relative", filepath.Join("project", "sub", "file.yaml"), "project", filepath.Join("sub", "file.yaml")},
		{"same directory", filepath.Join("project", "file.yaml"), "project", "file.yaml"},
		{"empty path", "", "project", ""},
		{"empty project", "file.yaml", "", "file.yaml"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, relativeDisplay(tt.path, tt.projectDir))
		})
	}
}

func TestWriteBaselineConfig(t *testing.T) {
	t.Parallel()
	for _, instruction := range []string{"", "You are a helpful assistant."} {
		t.Run(instruction, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "skills"), 0750))
			tools := filepath.Join(dir, "tools.json")
			require.NoError(t, os.WriteFile(tools, []byte("[]"), 0600))
			require.NoError(t, writeBaselineConfig(dir, baselineParams{
				Model: "gpt-4o", Instruction: instruction, ToolsFile: tools,
			}))
			data, err := os.ReadFile(filepath.Join(dir, optconfig.BaselineConfigRelPath()))
			require.NoError(t, err)
			var metadata map[string]any
			require.NoError(t, yaml.Unmarshal(data, &metadata))
			assert.Equal(t, "../../tools.json", metadata["tool_file"])
			assert.NotContains(t, metadata, "tools_file")
			ref := optconfig.AgentRef{ConfigFile: optconfig.BaselineConfigRelPath()}
			resolved := ref.ResolveConfig(dir)
			require.NotNil(t, resolved)
			assert.Equal(t, "gpt-4o", resolved.Model)
			assert.Equal(t, filepath.Join(dir, "skills"), resolved.SkillDir)
			assert.Equal(t, tools, resolved.ToolsFile)
			if instruction == "" {
				assert.Empty(t, resolved.InstructionFile)
			} else {
				content, err := os.ReadFile(resolved.InstructionFile)
				require.NoError(t, err)
				assert.Equal(t, instruction, string(content))
			}
		})
	}
}

func TestMergeAgentBaseline_RuntimeToolsFile(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name          string
		configFile    string
		toolsOverride bool
	}{
		{name: "default baseline"},
		{name: "explicit metadata", configFile: filepath.Join("custom", optconfig.MetadataFile)},
		{name: "preserves explicit tools", toolsOverride: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			configFile := tt.configFile
			if configFile == "" {
				configFile = optconfig.BaselineConfigRelPath()
			}
			configDir := filepath.Join(dir, filepath.Dir(configFile))
			require.NoError(t, os.MkdirAll(configDir, 0750))
			require.NoError(t, os.WriteFile(
				filepath.Join(dir, configFile), []byte("tool_file: tools.json\n"), 0600))
			toolsPath := filepath.Join(configDir, "tools.json")
			require.NoError(t, os.WriteFile(toolsPath, []byte("[]"), 0600))
			cfg := &OptimizeConfig{
				Config: optconfig.Config{Agent: optconfig.AgentRef{ConfigFile: tt.configFile}},
			}
			if tt.toolsOverride {
				toolsPath = filepath.Join(dir, "custom-tools.json")
				cfg.ToolsFile = toolsPath
			}

			mergeAgentBaseline(cfg, dir)

			assert.Equal(t, configFile, cfg.Agent.ConfigFile)
			assert.Equal(t, toolsPath, cfg.ToolsFile)
		})
	}
}

func TestIsDatasetName(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		value string
		want  bool
	}{
		{"dataset:2", true},
		{"my-dataset.v2", true},
		{"data.jsonl", false},
		{"DATA.JSONL", false},
		{"data.json", false},
		{"data.csv", false},
		{`data\train.jsonl`, false},
		{"./data.jsonl", false},
		{"", false},
	} {
		t.Run(tt.value, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isDatasetName(tt.value))
		})
	}
}

func TestResolveOptimizeDatasetInput(t *testing.T) {
	t.Run("versioned registered dataset", func(t *testing.T) {
		file, ref, err := resolveOptimizeDatasetInput("quality:2", "")
		require.NoError(t, err)
		assert.Empty(t, file)
		assert.Equal(t, &optconfig.DatasetRef{Name: "quality", Version: "2"}, ref)
	})
	for _, value := range []string{"quality", "quality:", ":2"} {
		t.Run(value, func(t *testing.T) {
			_, _, err := resolveOptimizeDatasetInput(value, "")
			require.ErrorContains(t, err, "name:version")
		})
	}
	t.Run("local paths resolve against cwd", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		path := filepath.Join(dir, "train.jsonl")
		require.NoError(t, os.WriteFile(path, []byte("{\"query\":\"hi\"}\n"), 0600))
		file, ref, err := resolveOptimizeDatasetInput("train.jsonl", filepath.Join(dir, "agent"))
		require.NoError(t, err)
		assert.Equal(t, path, file)
		assert.Nil(t, ref)
		assert.Equal(t, path, resolveCwdRelative(path))
		resolved, err := resolveLocalDatasetFile("train.jsonl", dir)
		require.NoError(t, err)
		assert.Equal(t, path, resolved)
	})
	t.Run("missing file", func(t *testing.T) {
		_, _, err := resolveOptimizeDatasetInput(filepath.Join(t.TempDir(), "missing.jsonl"), "")
		require.ErrorContains(t, err, "not accessible")
	})
}

func TestBuildModelSelectionChoices(t *testing.T) {
	t.Parallel()
	choices := buildModelSelectionChoices("")
	require.Len(t, choices, 1)
	assert.Equal(t, selectOtherDeploymentValue, choices[0].Value)
	choices = buildModelSelectionChoices("my-deployment")
	require.Len(t, choices, 2)
	assert.Equal(t, "my-deployment", choices[0].Value)
	assert.Contains(t, choices[0].Label, "(deployed)")
	assert.Equal(t, selectOtherDeploymentValue, choices[1].Value)
}

func TestFormatOptimizeTimestamp(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		value any
		want  string
	}{
		{int64(1), "1970-01-01 00:00:01 UTC"},
		{float64(1), "1970-01-01 00:00:01 UTC"},
		{1, "1970-01-01 00:00:01 UTC"},
		{"2026-10-20T10:00:00-07:00", "2026-10-20 17:00:00 UTC"},
		{"unknown", "unknown"},
		{int64(0), ""},
		{0, ""},
		{float64(0), ""},
		{"", ""},
		{nil, ""},
	} {
		assert.Equal(t, tt.want, formatOptimizeTimestamp(tt.value))
	}
}
