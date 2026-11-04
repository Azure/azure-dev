// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package optconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatasetReferences(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name          string
		ref           *DatasetRef
		local, remote bool
		wantLocalPath string
	}{
		{name: "unset"},
		{name: "empty", ref: &DatasetRef{}},
		{name: "local", ref: &DatasetRef{LocalURI: "train.jsonl"}, local: true, wantLocalPath: "train.jsonl"},
		{name: "registered", ref: &DatasetRef{Name: "data", Version: "2"}, remote: true},
		{name: "registered with cache", ref: &DatasetRef{Name: "data", LocalURI: "cache.jsonl"}, remote: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.local, tt.ref.IsLocal())
			assert.Equal(t, tt.remote, tt.ref.IsRemote())
			cfg := &Config{Dataset: tt.ref}
			assert.Equal(t, tt.wantLocalPath, cfg.LocalDatasetPath())
			if tt.remote {
				assert.Same(t, tt.ref, cfg.RemoteDatasetReference())
			} else {
				assert.Nil(t, cfg.RemoteDatasetReference())
			}
			cfg.DatasetFile = "override.jsonl"
			assert.Equal(t, "override.jsonl", cfg.LocalDatasetPath())
		})
	}
}

func TestSafePath(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	path, err := SafePath(base, "skills/tool.md")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(base, "skills", "tool.md"), path)
	for _, value := range []string{"..", "../outside", filepath.Join("..", "outside")} {
		_, err := SafePath(base, value)
		require.ErrorContains(t, err, "escapes base directory")
	}
}

func TestAgentConfiguration(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, AgentConfigsDir, BaselineDir)
	require.NoError(t, os.MkdirAll(dir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, MetadataFile), []byte(
		"model: agent-model\ninstruction_file: instructions.md\nskill_dir: skills\ntool_file: tools.json\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, InstructionFile), []byte("baseline instructions"), 0600))
	ref := &AgentRef{ConfigFile: BaselineConfigRelPath()}
	cfg := ref.ResolveConfig(root)
	require.NotNil(t, cfg)
	assert.Equal(t, "agent-model", cfg.Model)
	assert.Equal(t, filepath.Join(dir, InstructionFile), cfg.InstructionFile)
	assert.Equal(t, filepath.Join(dir, SkillsDir), cfg.SkillDir)
	assert.Equal(t, filepath.Join(dir, ToolsFile), cfg.ToolsFile)
	ref.Instruction = InstructionRef{File: cfg.InstructionFile}
	assert.Equal(t, "baseline instructions", ref.ResolvedSystemPrompt())
	ref.Instruction = InstructionRef{Value: "inline instructions"}
	assert.Equal(t, "inline instructions", ref.ResolvedSystemPrompt())
	assert.False(t, ref.Instruction.IsEmpty())
	ref.Instruction = InstructionRef{}
	assert.True(t, ref.Instruction.IsEmpty())
	assert.Nil(t, (&AgentRef{}).ResolveConfig(root))
}

func TestAgentConfiguration_ToolsMetadata(t *testing.T) {
	t.Parallel()
	absoluteTools := filepath.Join(t.TempDir(), "absolute-tools.json")
	for _, tt := range []struct {
		name, metadata, want string
	}{
		{"current key", "tool_file: tools.json\n", "tools.json"},
		{"legacy key", "tools_file: tools.json\n", "tools.json"},
		{"current key wins", "tool_file: current.json\ntools_file: previous.json\n", "current.json"},
		{"empty current key falls back", "tool_file: ''\ntools_file: previous.json\n", "previous.json"},
		{"absolute legacy path", "tools_file: " + absoluteTools + "\n", absoluteTools},
		{"empty keys", "tool_file: ''\ntools_file: ''\n", ""},
		{"missing keys", "model: agent-model\n", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, AgentConfigsDir, BaselineDir)
			require.NoError(t, os.MkdirAll(dir, 0750))
			metadataPath := filepath.Join(dir, MetadataFile)
			require.NoError(t, os.WriteFile(metadataPath, []byte(tt.metadata), 0600))
			ref := &AgentRef{ConfigFile: BaselineConfigRelPath()}

			cfg := ref.ResolveConfig(root)

			require.NotNil(t, cfg)
			want := tt.want
			if want != "" && !filepath.IsAbs(want) {
				want = filepath.Join(dir, want)
			}
			assert.Equal(t, want, cfg.ToolsFile)
			data, err := os.ReadFile(metadataPath)
			require.NoError(t, err)
			assert.Equal(t, tt.metadata, string(data), "reading must not rewrite the metadata")
		})
	}
}
