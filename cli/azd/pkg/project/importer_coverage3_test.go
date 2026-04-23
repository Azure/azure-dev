// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_DetectProviderFromFiles(t *testing.T) {
	t.Run("empty directory", func(t *testing.T) {
		dir := t.TempDir()
		provider, err := detectProviderFromFiles(dir)
		require.NoError(t, err)
		assert.Empty(t, string(provider))
	})

	t.Run("non-existent directory", func(t *testing.T) {
		provider, err := detectProviderFromFiles(filepath.Join(t.TempDir(), "nonexistent"))
		require.NoError(t, err)
		assert.Empty(t, string(provider))
	})

	t.Run("bicep files only", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.bicep"), []byte("param x string"), 0600))

		provider, err := detectProviderFromFiles(dir)
		require.NoError(t, err)
		assert.Equal(t, "bicep", string(provider))
	})

	t.Run("terraform files only", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("resource {}"), 0600))

		provider, err := detectProviderFromFiles(dir)
		require.NoError(t, err)
		assert.Equal(t, "terraform", string(provider))
	})

	t.Run("both bicep and terraform", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.bicep"), []byte("param x string"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("resource {}"), 0600))

		_, err := detectProviderFromFiles(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "both Bicep and Terraform")
	})

	t.Run("bicepparam files", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.bicepparam"), []byte("param x = 'val'"), 0600))

		provider, err := detectProviderFromFiles(dir)
		require.NoError(t, err)
		assert.Equal(t, "bicep", string(provider))
	})

	t.Run("tfvars files", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dev.tfvars"), []byte("x = \"val\""), 0600))

		provider, err := detectProviderFromFiles(dir)
		require.NoError(t, err)
		assert.Equal(t, "terraform", string(provider))
	})

	t.Run("directories are ignored", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "modules.bicep"), 0755))

		provider, err := detectProviderFromFiles(dir)
		require.NoError(t, err)
		assert.Empty(t, string(provider))
	})
}

func Test_PathHasModule(t *testing.T) {
	t.Run("bicep module exists", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.bicep"), []byte("param x string"), 0600))

		exists, err := pathHasModule(dir, "main")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("terraform module exists", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("resource {}"), 0600))

		exists, err := pathHasModule(dir, "main")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("module does not exist", func(t *testing.T) {
		dir := t.TempDir()
		exists, err := pathHasModule(dir, "main")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("non-existent path", func(t *testing.T) {
		_, err := pathHasModule(filepath.Join(t.TempDir(), "nonexistent"), "main")
		require.Error(t, err)
	})
}

func Test_Infra_Cleanup(t *testing.T) {
	t.Run("with cleanup dir", func(t *testing.T) {
		dir := t.TempDir()
		cleanupDir := filepath.Join(dir, "to-clean")
		require.NoError(t, os.MkdirAll(cleanupDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(cleanupDir, "file.txt"), []byte("data"), 0600))

		infra := &Infra{cleanupDir: cleanupDir}
		err := infra.Cleanup()
		require.NoError(t, err)

		_, err = os.Stat(cleanupDir)
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("without cleanup dir", func(t *testing.T) {
		infra := &Infra{}
		err := infra.Cleanup()
		require.NoError(t, err)
	})
}

func Test_ValidateServiceDependencies(t *testing.T) {
	im := NewImportManager(nil)

	t.Run("no dependencies", func(t *testing.T) {
		prj := &ProjectConfig{Name: "test"}
		services := []*ServiceConfig{
			{Name: "web"},
			{Name: "api"},
		}
		err := im.validateServiceDependencies(services, prj)
		require.NoError(t, err)
	})

	t.Run("valid service dependency", func(t *testing.T) {
		prj := &ProjectConfig{Name: "test"}
		services := []*ServiceConfig{
			{Name: "web", Uses: []string{"api"}},
			{Name: "api"},
		}
		err := im.validateServiceDependencies(services, prj)
		require.NoError(t, err)
	})

	t.Run("valid resource dependency", func(t *testing.T) {
		prj := &ProjectConfig{
			Name: "test",
			Resources: map[string]*ResourceConfig{
				"mydb": {Type: ResourceTypeDbPostgres, Name: "mydb"},
			},
		}
		services := []*ServiceConfig{
			{Name: "web", Uses: []string{"mydb"}},
		}
		err := im.validateServiceDependencies(services, prj)
		require.NoError(t, err)
	})

	t.Run("missing dependency", func(t *testing.T) {
		prj := &ProjectConfig{Name: "test"}
		services := []*ServiceConfig{
			{Name: "web", Uses: []string{"missing-service"}},
		}
		err := im.validateServiceDependencies(services, prj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing-service")
	})
}

func Test_SortServicesByDependencies(t *testing.T) {
	prj := &ProjectConfig{
		Name: "test",
		Services: map[string]*ServiceConfig{
			"web":    {Name: "web", Uses: []string{"api"}},
			"api":    {Name: "api"},
			"worker": {Name: "worker", Uses: []string{"api"}},
		},
	}
	prj.Services["web"].Project = prj
	prj.Services["api"].Project = prj
	prj.Services["worker"].Project = prj

	im := NewImportManager(nil)
	services := []*ServiceConfig{prj.Services["web"], prj.Services["api"], prj.Services["worker"]}

	sorted, err := im.sortServicesByDependencies(services, prj)
	require.NoError(t, err)

	// api should come before web and worker since they depend on it
	apiIdx := -1
	webIdx := -1
	workerIdx := -1
	for i, svc := range sorted {
		switch svc.Name {
		case "api":
			apiIdx = i
		case "web":
			webIdx = i
		case "worker":
			workerIdx = i
		}
	}
	assert.True(t, apiIdx < webIdx, "api should be before web")
	assert.True(t, apiIdx < workerIdx, "api should be before worker")
}
