// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLooksLikeFoundryAzureYaml(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name: "unified azure.yaml with split foundry hosts",
			content: `name: foundry-simple
services:
  ai-project:
    host: azure.ai.project
  assistant:
    host: azure.ai.agent
    kind: hosted
`,
			want: true,
		},
		{
			name: "legacy microsoft.foundry host",
			content: `name: foundry-legacy
services:
  agents:
    host: microsoft.foundry
`,
			want: true,
		},
		{
			name: "agent manifest with top-level template",
			content: `name: my-agent
template:
  kind: hosted
  name: my-agent
parameters: {}
resources: []
`,
			want: false,
		},
		{
			name: "azure.yaml with only non-foundry services",
			content: `name: web-app
services:
  web:
    host: containerapp
    language: js
`,
			want: false,
		},
		{
			name:    "empty content",
			content: "",
			want:    false,
		},
		{
			name:    "malformed yaml",
			content: "name: [unterminated",
			want:    false,
		},
		{
			name: "services present but not a map",
			content: `name: broken
services: just-a-string
`,
			want: false,
		},
		{
			name: "service without host",
			content: `name: foundry-noisy
services:
  ai-project:
    deployments: []
`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, looksLikeFoundryAzureYaml([]byte(tt.content)))
		})
	}
}

func TestFoundryProjectName(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "name present", content: "name: foundry-simple\nservices: {}\n", want: "foundry-simple"},
		{name: "name with surrounding spaces", content: "name: \"  spaced  \"\n", want: "spaced"},
		{name: "no name", content: "services: {}\n", want: ""},
		{name: "malformed yaml", content: "name: [oops", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, foundryProjectName([]byte(tt.content)))
		})
	}
}

func TestParentDirOf(t *testing.T) {
	tests := []struct {
		filePath string
		want     string
	}{
		{filePath: "azure.yaml", want: ""},
		{filePath: "samples/simple/azure.yaml", want: "samples/simple"},
		{filePath: "a/b/c/azure.yaml", want: "a/b/c"},
	}
	for _, tt := range tests {
		t.Run(tt.filePath, func(t *testing.T) {
			require.Equal(t, tt.want, parentDirOf(tt.filePath))
		})
	}
}

func TestAdoptTargetDir(t *testing.T) {
	t.Run("explicit src wins", func(t *testing.T) {
		dir, display := adoptTargetDir(&initFlags{src: "my-dir"}, "foundry-simple")
		require.Equal(t, "my-dir", dir)
		require.Equal(t, "my-dir", display)
	})

	t.Run("derives folder from project name", func(t *testing.T) {
		dir, display := adoptTargetDir(&initFlags{}, "Foundry Simple")
		require.Equal(t, "foundry-simple", dir)
		require.Equal(t, "foundry-simple", display)
	})

	t.Run("falls back to current dir when name empty", func(t *testing.T) {
		dir, display := adoptTargetDir(&initFlags{}, "")
		require.Equal(t, ".", dir)
		require.Empty(t, display)
	})
}

func TestFolderDisplayIfNew(t *testing.T) {
	t.Run("current dir is never a created folder", func(t *testing.T) {
		require.Empty(t, folderDisplayIfNew("."))
	})

	t.Run("non-existent dir is displayed", func(t *testing.T) {
		require.Equal(t, "brand-new-dir-does-not-exist-xyz", folderDisplayIfNew("brand-new-dir-does-not-exist-xyz"))
	})

	t.Run("existing dir is not displayed", func(t *testing.T) {
		existing := t.TempDir()
		require.Empty(t, folderDisplayIfNew(existing))
	})
}

func TestStagedAzureYamlExists(t *testing.T) {
	t.Run("azure.yaml present", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte("name: x\n"), 0600))
		require.True(t, stagedAzureYamlExists(dir))
	})

	t.Run("azure.yml present", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yml"), []byte("name: x\n"), 0600))
		require.True(t, stagedAzureYamlExists(dir))
	})

	t.Run("absent", func(t *testing.T) {
		require.False(t, stagedAzureYamlExists(t.TempDir()))
	})
}

func TestProjectManifestExists(t *testing.T) {
	t.Run("azure.yaml present", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte("name: x\n"), 0600))
		require.True(t, projectManifestExists(dir))
	})

	t.Run("azure.yml present", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yml"), []byte("name: x\n"), 0600))
		require.True(t, projectManifestExists(dir))
	})

	t.Run("absent", func(t *testing.T) {
		require.False(t, projectManifestExists(t.TempDir()))
	})
}

func TestEnsureStagedAzureYaml_NormalizesAzureYml(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.yml"), []byte("name: foundry-simple\n"), 0600))

	ok, err := ensureStagedAzureYaml(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, fileExists(filepath.Join(dir, "azure.yaml")))
	require.False(t, fileExists(filepath.Join(dir, "azure.yml")))
}

// TestStageAzureYamlTemplate_LocalAzureYaml verifies a local pointer named
// azure.yaml uses its parent directory directly as the template (no temp copy).
func TestStageAzureYamlTemplate_LocalAzureYaml(t *testing.T) {
	sampleDir := t.TempDir()
	azureYaml := filepath.Join(sampleDir, "azure.yaml")
	require.NoError(t, os.WriteFile(azureYaml, []byte("name: foundry-simple\nservices: {}\n"), 0600))

	flags := &initFlags{manifestPointer: azureYaml}
	staging, cleanup, err := stageAzureYamlTemplate(t.Context(), flags, nil, nil)
	require.NoError(t, err)
	defer cleanup()

	require.Equal(t, sampleDir, staging)
	require.True(t, stagedAzureYamlExists(staging))
}

// TestStageAzureYamlTemplate_LocalAzureYmlRenamed verifies azure.yml is staged
// as azure.yaml so azd-core adopts the sample manifest instead of generating a
// default azure.yaml.
func TestStageAzureYamlTemplate_LocalAzureYmlRenamed(t *testing.T) {
	sampleDir := t.TempDir()
	azureYml := filepath.Join(sampleDir, "azure.yml")
	require.NoError(t, os.WriteFile(azureYml, []byte("name: foundry-simple\nservices: {}\n"), 0600))

	flags := &initFlags{manifestPointer: azureYml}
	staging, cleanup, err := stageAzureYamlTemplate(t.Context(), flags, nil, nil)
	require.NoError(t, err)
	defer cleanup()

	require.NotEqual(t, sampleDir, staging)
	require.True(t, fileExists(filepath.Join(staging, "azure.yaml")))
	require.False(t, fileExists(filepath.Join(staging, "azure.yml")))
}

// TestStageAzureYamlTemplate_LocalRenamesToAzureYaml verifies a local pointer
// not named azure.yaml is staged into a temp dir with the manifest written as
// azure.yaml, while sibling files are preserved.
func TestStageAzureYamlTemplate_LocalRenamesToAzureYaml(t *testing.T) {
	sampleDir := t.TempDir()
	pointer := filepath.Join(sampleDir, "sample.yaml")
	require.NoError(t, os.WriteFile(pointer, []byte("name: foundry-simple\nservices: {}\n"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(sampleDir, "agents"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(sampleDir, "agents", "main.py"), []byte("print('x')\n"), 0600))

	flags := &initFlags{manifestPointer: pointer}
	staging, cleanup, err := stageAzureYamlTemplate(t.Context(), flags, nil, nil)
	require.NoError(t, err)
	defer cleanup()

	require.NotEqual(t, sampleDir, staging)
	require.True(t, stagedAzureYamlExists(staging))
	require.False(t, fileExists(filepath.Join(staging, "sample.yaml")))
	// Sibling files are carried into the staging directory.
	require.True(t, fileExists(filepath.Join(staging, "agents", "main.py")))
}
