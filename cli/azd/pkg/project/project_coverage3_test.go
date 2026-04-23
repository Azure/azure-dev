// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_StripUTF8BOM(t *testing.T) {
	t.Run("with BOM", func(t *testing.T) {
		data := append([]byte{0xEF, 0xBB, 0xBF}, []byte("hello")...)
		result := stripUTF8BOM(data)
		assert.Equal(t, []byte("hello"), result)
	})

	t.Run("without BOM", func(t *testing.T) {
		data := []byte("hello")
		result := stripUTF8BOM(data)
		assert.Equal(t, []byte("hello"), result)
	})

	t.Run("empty slice", func(t *testing.T) {
		result := stripUTF8BOM([]byte{})
		assert.Empty(t, result)
	})

	t.Run("only BOM", func(t *testing.T) {
		data := []byte{0xEF, 0xBB, 0xBF}
		result := stripUTF8BOM(data)
		assert.Empty(t, result)
	})

	t.Run("partial BOM prefix", func(t *testing.T) {
		data := []byte{0xEF, 0xBB, 0x00}
		result := stripUTF8BOM(data)
		assert.Equal(t, data, result)
	})
}

func Test_MoveFile(t *testing.T) {
	t.Run("successful move", func(t *testing.T) {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "source.txt")
		dstPath := filepath.Join(dir, "destination.txt")

		content := []byte("test content")
		require.NoError(t, os.WriteFile(srcPath, content, 0600))

		err := moveFile(srcPath, dstPath)
		require.NoError(t, err)

		// destination should have the content
		data, err := os.ReadFile(dstPath)
		require.NoError(t, err)
		assert.Equal(t, content, data)
	})

	t.Run("source does not exist", func(t *testing.T) {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "nonexistent.txt")
		dstPath := filepath.Join(dir, "destination.txt")

		err := moveFile(srcPath, dstPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "opening source file")
	})

	t.Run("destination directory does not exist", func(t *testing.T) {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "source.txt")
		require.NoError(t, os.WriteFile(srcPath, []byte("data"), 0600))
		dstPath := filepath.Join(dir, "nonexistent-dir", "destination.txt")

		err := moveFile(srcPath, dstPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "creating destination file")
	})
}

func Test_New_SaveConfig_LoadConfig(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "azure.yaml")

	// Test New
	ctx := t.Context()
	cfg, err := New(ctx, filePath, "my-project")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "my-project", cfg.Name)
	assert.Equal(t, dir, cfg.Path)

	// Verify file was created
	_, err = os.Stat(filePath)
	require.NoError(t, err)
}

func Test_LoadConfig(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "azure.yaml")
	yamlContent := `name: test-project
services:
  web:
    host: appservice
    language: python
    project: ./src/web
`
	require.NoError(t, os.WriteFile(filePath, []byte(yamlContent), 0600))

	ctx := t.Context()
	cfg, err := LoadConfig(ctx, filePath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	raw := cfg.Raw()
	assert.Equal(t, "test-project", raw["name"])
}

func Test_LoadConfig_FileNotFound(t *testing.T) {
	ctx := t.Context()
	_, err := LoadConfig(ctx, filepath.Join(t.TempDir(), "nonexistent.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading project file")
}

func Test_LoadConfig_InvalidYaml(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "azure.yaml")
	require.NoError(t, os.WriteFile(filePath, []byte(":::invalid: yaml: ["), 0600))

	ctx := t.Context()
	_, err := LoadConfig(ctx, filePath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse azure.yaml file")
}

func Test_SaveConfig(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "azure.yaml")

	// First create a valid project file
	yamlContent := `name: save-test
`
	require.NoError(t, os.WriteFile(filePath, []byte(yamlContent), 0600))

	ctx := t.Context()
	cfg, err := LoadConfig(ctx, filePath)
	require.NoError(t, err)

	// Save it back
	outputPath := filepath.Join(dir, "azure-saved.yaml")
	err = SaveConfig(ctx, cfg, outputPath)
	require.NoError(t, err)

	// Verify the output file was created and is valid
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "yaml-language-server")
	assert.Contains(t, string(data), "save-test")
}

func Test_Save(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "azure.yaml")

	prjConfig := &ProjectConfig{
		Name: "test-save",
		Services: map[string]*ServiceConfig{
			"web": {
				RelativePath: "src\\web",
				Host:         AppServiceTarget,
				Language:     ServiceLanguagePython,
				OutputPath:   "dist\\output",
				Infra: provisioning.Options{
					Path: "infra\\web",
				},
			},
		},
		Infra: provisioning.Options{
			Path: "infra\\main",
		},
	}

	ctx := t.Context()
	err := Save(ctx, prjConfig, filePath)
	require.NoError(t, err)

	// Verify file content uses forward slashes
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "yaml-language-server")
	assert.Contains(t, content, "test-save")
	// Path should be set
	assert.Equal(t, dir, prjConfig.Path)
}

func Test_Save_CustomSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "azure.yaml")

	prjConfig := &ProjectConfig{
		Name:              "versioned",
		MetaSchemaVersion: "v1.1",
	}

	ctx := t.Context()
	err := Save(ctx, prjConfig, filePath)
	require.NoError(t, err)

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "schemas/v1.1/azure.yaml.json")
}

func Test_Parse_EmptyContent(t *testing.T) {
	_, err := Parse(t.Context(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "File is empty")
}

func Test_Parse_WhitespaceOnly(t *testing.T) {
	_, err := Parse(t.Context(), "   \n\t  \n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "File is empty")
}

func Test_Parse_InvalidYaml(t *testing.T) {
	_, err := Parse(t.Context(), ":::bad[yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse azure.yaml file")
}

func Test_Parse_ContainerAppNoLanguageNoImage(t *testing.T) {
	yaml := `name: test
services:
  api:
    host: containerapp
    project: ./src/api
`
	_, err := Parse(t.Context(), yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must specify language or image")
}

func Test_Parse_EmptyHost(t *testing.T) {
	yaml := `name: test
services:
  api:
    host: ""
    language: python
    project: ./src/api
`
	_, err := Parse(t.Context(), yaml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host cannot be empty")
}

func Test_Parse_BackslashPathNormalization(t *testing.T) {
	yaml := `name: test
infra:
  path: "infra\\main"
services:
  web:
    host: appservice
    language: python
    project: "src\\web"
    dist: "dist\\out"
`
	cfg, err := Parse(t.Context(), yaml)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	// Paths should be normalized to OS separators (forward slashes on the test or OS-native)
	assert.NotContains(t, cfg.Infra.Path, "\\\\")
}

func Test_Load_FileNotFound(t *testing.T) {
	_, err := Load(t.Context(), filepath.Join(t.TempDir(), "nonexistent.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading project file")
}

func Test_Load_ValidProject(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "azure.yaml")

	yamlContent := `name: load-test
services:
  web:
    host: appservice
    language: python
    project: ./src/web
`
	require.NoError(t, os.WriteFile(filePath, []byte(yamlContent), 0600))

	cfg, err := Load(t.Context(), filePath)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "load-test", cfg.Name)
	assert.Equal(t, dir, cfg.Path)
	require.Contains(t, cfg.Services, "web")
	assert.Equal(t, ServiceLanguagePython, cfg.Services["web"].Language)
}

func Test_HooksFromInfraModule_NoFile(t *testing.T) {
	dir := t.TempDir()
	hooks, err := hooksFromInfraModule(dir, "main")
	require.NoError(t, err)
	assert.Nil(t, hooks)
}

func Test_HooksFromInfraModule_ValidFile(t *testing.T) {
	dir := t.TempDir()
	hooksContent := `preprovision:
  - run: echo hello
    shell: sh
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.hooks.yaml"), []byte(hooksContent), 0600))

	hooks, err := hooksFromInfraModule(dir, "main")
	require.NoError(t, err)
	require.NotNil(t, hooks)
	require.Contains(t, hooks, "preprovision")
}

func Test_HooksFromInfraModule_InvalidYaml(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.hooks.yaml"), []byte(":::invalid"), 0600))

	_, err := hooksFromInfraModule(dir, "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed unmarshalling hooks")
}
