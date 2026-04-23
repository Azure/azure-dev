// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// Additional tests for partially-covered functions
// ============================================================

// ---------- Fake ExternalTool that fails CheckInstalled ----------
type failingTool_Cov3 struct {
	toolName   string
	installUrl string
	checkErr   error
}

func (f *failingTool_Cov3) CheckInstalled(_ context.Context) error {
	return f.checkErr
}
func (f *failingTool_Cov3) InstallUrl() string { return f.installUrl }
func (f *failingTool_Cov3) Name() string       { return f.toolName }

// ---------- EnsureServiceTargetTools: Docker missing → suggestRemoteBuild ----------
func Test_projectManager_EnsureServiceTargetTools_DockerMissing_Coverage3(t *testing.T) {
	tmpDir := t.TempDir()
	dockerTool := &failingTool_Cov3{
		toolName: "Docker",
		checkErr: fmt.Errorf("Docker is not installed"),
	}
	sc := makeSvcConfig("api", "api", ContainerAppTarget, ServiceLanguagePython, tmpDir)
	pm := &projectManager{
		importManager: NewImportManager(nil),
		serviceManager: &fakeServiceManager_Cov3{
			serviceTarget: &fakeServiceTarget_Cov3{requiredTools: []tools.ExternalTool{dockerTool}},
		},
	}
	err := pm.EnsureServiceTargetTools(t.Context(), sc.Project, nil)
	require.Error(t, err)
	// suggestRemoteBuild wraps in ErrorWithSuggestion; the Suggestion field has "remoteBuild"
	var errSug *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, err, &errSug)
	assert.Contains(t, errSug.Suggestion, "remoteBuild")
}

// ---------- EnsureAllTools: tool missing (non-Docker) falls through ----------
func Test_projectManager_EnsureAllTools_ToolMissing_Coverage3(t *testing.T) {
	tmpDir := t.TempDir()
	pyTool := &failingTool_Cov3{
		toolName: "Python",
		checkErr: fmt.Errorf("Python is not installed"),
	}
	sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
	pm := &projectManager{
		importManager: NewImportManager(nil),
		serviceManager: &fakeServiceManager_Cov3{
			requiredTools: []tools.ExternalTool{pyTool},
		},
	}
	err := pm.EnsureAllTools(t.Context(), sc.Project, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Python")
}

// ---------- EnsureFrameworkTools: tool missing ----------
func Test_projectManager_EnsureFrameworkTools_ToolMissing_Coverage3(t *testing.T) {
	tmpDir := t.TempDir()
	pyTool := &failingTool_Cov3{
		toolName: "Python",
		checkErr: fmt.Errorf("Python is not installed"),
	}
	innerFw := &fakeFrameworkForTools_Cov3{tools: []tools.ExternalTool{pyTool}}
	sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
	pm := &projectManager{
		importManager: NewImportManager(nil),
		serviceManager: &fakeServiceManager_Cov3{
			frameworkSvc: innerFw,
		},
	}
	err := pm.EnsureFrameworkTools(t.Context(), sc.Project, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Python")
}

// ---------- EnsureRestoreTools: tool missing (dockerProject inner) ----------
func Test_projectManager_EnsureRestoreTools_DockerInner_ToolMissing_Coverage3(t *testing.T) {
	tmpDir := t.TempDir()
	pyTool := &failingTool_Cov3{
		toolName: "Python",
		checkErr: fmt.Errorf("Python is not installed"),
	}
	innerFw := &fakeFrameworkForTools_Cov3{tools: []tools.ExternalTool{pyTool}}
	dp := &dockerProject{framework: innerFw}
	sc := makeSvcConfig("api", "api", ContainerAppTarget, ServiceLanguagePython, tmpDir)
	pm := &projectManager{
		importManager: NewImportManager(nil),
		serviceManager: &fakeServiceManager_Cov3{
			frameworkSvc: dp,
		},
	}
	err := pm.EnsureRestoreTools(t.Context(), sc.Project, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Python")
}

// ---------- fake FrameworkService that returns specific tools ----------
type fakeFrameworkForTools_Cov3 struct {
	noOpProject
	tools []tools.ExternalTool
}

func (f *fakeFrameworkForTools_Cov3) RequiredExternalTools(_ context.Context, _ *ServiceConfig) []tools.ExternalTool {
	return f.tools
}

// ---------- GenerateAllInfrastructure: DotNet CanImport false ----------
func Test_GenerateAllInfrastructure_DotNet_Coverage3(t *testing.T) {
	t.Run("CanImportFalse_FallsThrough", func(t *testing.T) {
		tmpDir := t.TempDir()
		svcDir := filepath.Join(tmpDir, "api")
		require.NoError(t, os.MkdirAll(svcDir, 0o755))

		dotNetImp := &DotNetImporter{hostCheck: map[string]hostCheckResult{}}
		dotNetImp.hostCheck[svcDir] = hostCheckResult{is: false}
		im := NewImportManager(dotNetImp)

		prj := &ProjectConfig{Path: tmpDir, Services: map[string]*ServiceConfig{}}
		sc := &ServiceConfig{
			Name:         "api",
			RelativePath: "api",
			Language:     ServiceLanguageDotNet,
			Host:         AppServiceTarget,
			Project:      prj,
		}
		prj.Services["api"] = sc

		_, err := im.GenerateAllInfrastructure(t.Context(), prj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain any infrastructure")
	})

	t.Run("CanImportError_LogsAndFallsThrough", func(t *testing.T) {
		tmpDir := t.TempDir()
		svcDir := filepath.Join(tmpDir, "api")
		require.NoError(t, os.MkdirAll(svcDir, 0o755))

		dotNetImp := &DotNetImporter{hostCheck: map[string]hostCheckResult{}}
		dotNetImp.hostCheck[svcDir] = hostCheckResult{is: false, err: assert.AnError}
		im := NewImportManager(dotNetImp)

		prj := &ProjectConfig{Path: tmpDir, Services: map[string]*ServiceConfig{}}
		sc := &ServiceConfig{
			Name:         "api",
			RelativePath: "api",
			Language:     ServiceLanguageDotNet,
			Host:         AppServiceTarget,
			Project:      prj,
		}
		prj.Services["api"] = sc

		_, err := im.GenerateAllInfrastructure(t.Context(), prj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain any infrastructure")
	})

	t.Run("WithResources_CallsInfraFsForProject", func(t *testing.T) {
		tmpDir := t.TempDir()
		prj := &ProjectConfig{
			Path:     tmpDir,
			Services: map[string]*ServiceConfig{},
			Resources: map[string]*ResourceConfig{
				"db": {
					Type: ResourceTypeDbPostgres,
					Name: "mydb",
				},
			},
		}
		im := NewImportManager(nil)

		result, err := im.GenerateAllInfrastructure(t.Context(), prj)
		// This calls infraFsForProject which generates Bicep from resources
		require.NoError(t, err)
		assert.NotNil(t, result)
	})
}

// ---------- ProjectInfrastructure additional paths ----------
func Test_ProjectInfrastructure_Coverage3(t *testing.T) {
	t.Run("DefaultPath_NoInfraDir_NoResources", func(t *testing.T) {
		tmpDir := t.TempDir()
		prj := &ProjectConfig{
			Path:     tmpDir,
			Services: map[string]*ServiceConfig{},
		}
		im := NewImportManager(nil)

		infra, err := im.ProjectInfrastructure(t.Context(), prj)
		require.NoError(t, err)
		require.NotNil(t, infra)
		// With no infra dir and no resources, should return default Infra
	})

	t.Run("ExplicitPath_WithBicepFile", func(t *testing.T) {
		tmpDir := t.TempDir()
		infraDir := filepath.Join(tmpDir, "infra")
		require.NoError(t, os.MkdirAll(infraDir, 0o755))
		// Create a main.bicep file
		require.NoError(t, os.WriteFile(
			filepath.Join(infraDir, "main.bicep"),
			[]byte("targetScope = 'subscription'"), 0o600))

		prj := &ProjectConfig{
			Path:     tmpDir,
			Services: map[string]*ServiceConfig{},
		}
		im := NewImportManager(nil)

		infra, err := im.ProjectInfrastructure(t.Context(), prj)
		require.NoError(t, err)
		require.NotNil(t, infra)
		assert.Equal(t, "bicep", string(infra.Options.Provider))
	})

	t.Run("ExplicitPath_WithTerraform", func(t *testing.T) {
		tmpDir := t.TempDir()
		infraDir := filepath.Join(tmpDir, "infra")
		require.NoError(t, os.MkdirAll(infraDir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(infraDir, "main.tf"),
			[]byte("resource \"azurerm_resource_group\" \"rg\" {}"), 0o600))

		prj := &ProjectConfig{
			Path:     tmpDir,
			Services: map[string]*ServiceConfig{},
		}
		im := NewImportManager(nil)

		infra, err := im.ProjectInfrastructure(t.Context(), prj)
		require.NoError(t, err)
		require.NotNil(t, infra)
		assert.Equal(t, "terraform", string(infra.Options.Provider))
	})

	t.Run("WithResources_TempInfra", func(t *testing.T) {
		tmpDir := t.TempDir()
		prj := &ProjectConfig{
			Path:     tmpDir,
			Services: map[string]*ServiceConfig{},
			Resources: map[string]*ResourceConfig{
				"db": {
					Type: ResourceTypeDbPostgres,
					Name: "mydb",
				},
			},
		}
		im := NewImportManager(nil)

		infra, err := im.ProjectInfrastructure(t.Context(), prj)
		require.NoError(t, err)
		require.NotNil(t, infra)
		// Should have a cleanupDir since it generated temp files
		assert.NotEmpty(t, infra.cleanupDir)
		_ = infra.Cleanup()
	})

	t.Run("DotNet_CanImportFalse_FallsThrough", func(t *testing.T) {
		tmpDir := t.TempDir()
		svcDir := filepath.Join(tmpDir, "api")
		require.NoError(t, os.MkdirAll(svcDir, 0o755))

		dotNetImp := &DotNetImporter{hostCheck: map[string]hostCheckResult{}}
		dotNetImp.hostCheck[svcDir] = hostCheckResult{is: false}
		im := NewImportManager(dotNetImp)

		prj := &ProjectConfig{Path: tmpDir, Services: map[string]*ServiceConfig{}}
		sc := &ServiceConfig{
			Name:         "api",
			RelativePath: "api",
			Language:     ServiceLanguageDotNet,
			Host:         AppServiceTarget,
			Project:      prj,
		}
		prj.Services["api"] = sc

		// No infra dir and no resources → default Infra
		infra, err := im.ProjectInfrastructure(t.Context(), prj)
		require.NoError(t, err)
		require.NotNil(t, infra)
	})
}

// ---------- IgnoreFile method coverage for different targets ----------
func Test_ServiceTargetKind_IgnoreFile_Extended_Coverage3(t *testing.T) {
	assert.Equal(t, ".webappignore", AppServiceTarget.IgnoreFile())
	assert.Equal(t, ".funcignore", AzureFunctionTarget.IgnoreFile())
	assert.Equal(t, "", ContainerAppTarget.IgnoreFile())
	assert.Equal(t, "", StaticWebAppTarget.IgnoreFile())
	assert.Equal(t, "", AksTarget.IgnoreFile())
}

// ---------- SupportsDelayedProvisioning ----------
func Test_ServiceTargetKind_SupportsDelayedProvisioning_Extended_Coverage3(t *testing.T) {
	assert.True(t, AksTarget.SupportsDelayedProvisioning())
	assert.False(t, AppServiceTarget.SupportsDelayedProvisioning())
	assert.False(t, ContainerAppTarget.SupportsDelayedProvisioning())
}

// ---------- checkResourceType ----------
func Test_checkResourceType_Coverage3(t *testing.T) {
	t.Run("Match", func(t *testing.T) {
		tr := environment.NewTargetResource("sub", "rg", "myapp", "Microsoft.Web/sites")
		err := checkResourceType(tr, azapi.AzureResourceType("Microsoft.Web/sites"))
		require.NoError(t, err)
	})

	t.Run("Mismatch", func(t *testing.T) {
		tr := environment.NewTargetResource("sub", "rg", "myapp", "Microsoft.Web/sites")
		err := checkResourceType(tr, azapi.AzureResourceType("Microsoft.App/containerApps"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "myapp")
	})
}
