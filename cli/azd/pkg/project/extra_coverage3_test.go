// Copyright (c) Microsoft Corporation. Licensed under the MIT License.
package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for ImportManager.GenerateAllInfrastructure additional branches
func Test_GenerateAllInfrastructure_Coverage3(t *testing.T) {
	t.Run("NoServices_NoResources_Error", func(t *testing.T) {
		im := NewImportManager(nil)
		prj := &ProjectConfig{
			Services: map[string]*ServiceConfig{},
		}
		_, err := im.GenerateAllInfrastructure(t.Context(), prj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain any infrastructure")
	})

	t.Run("NonDotNetService_NoResources_Error", func(t *testing.T) {
		tmpDir := t.TempDir()
		prj := &ProjectConfig{
			Path:     tmpDir,
			Services: map[string]*ServiceConfig{},
		}
		sc := &ServiceConfig{
			Name:         "api",
			RelativePath: "api",
			Language:     ServiceLanguagePython,
			Project:      prj,
		}
		prj.Services["api"] = sc

		im := NewImportManager(nil)
		_, err := im.GenerateAllInfrastructure(t.Context(), prj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain any infrastructure")
	})
}

// Tests for containerAppTarget methods at 0%
func Test_containerAppTarget_RequiredExternalTools_Coverage3(t *testing.T) {
	ch := &ContainerHelper{}
	at := &containerAppTarget{containerHelper: ch}
	sc := &ServiceConfig{
		Name:     "api",
		Language: ServiceLanguagePython,
		Project:  &ProjectConfig{},
	}
	toolList := at.RequiredExternalTools(t.Context(), sc)
	// containerHelper.RequiredExternalTools returns docker tool if not remote-build
	// with an empty ServiceConfig Docker section, we get docker
	assert.NotNil(t, toolList)
}

func Test_containerAppTarget_Package_Coverage3(t *testing.T) {
	at := &containerAppTarget{}
	progress := async.NewProgress[ServiceProgress]()
	go func() {
		for range progress.Progress() {
		}
	}()

	result, err := at.Package(t.Context(), nil, nil, progress)
	progress.Done()

	require.NoError(t, err)
	require.NotNil(t, result)
	// containerAppTarget.Package returns empty result
	assert.Empty(t, result.Artifacts)
}

// Tests for Infra.Cleanup
func Test_Infra_Cleanup_Coverage3(t *testing.T) {
	t.Run("NoCleanupDir", func(t *testing.T) {
		infra := &Infra{}
		err := infra.Cleanup()
		require.NoError(t, err)
	})

	t.Run("WithCleanupDir", func(t *testing.T) {
		tmpDir := t.TempDir()
		infra := &Infra{cleanupDir: tmpDir}
		err := infra.Cleanup()
		require.NoError(t, err)
		// The directory should be removed
		assert.NoDirExists(t, tmpDir)
	})
}
