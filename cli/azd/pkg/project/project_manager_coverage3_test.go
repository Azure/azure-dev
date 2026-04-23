// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- fake ServiceManager ----------
type fakeServiceManager_Cov3 struct {
	frameworkSvc        FrameworkService
	serviceTarget       ServiceTarget
	requiredTools       []tools.ExternalTool
	getRequiredToolsErr error
	getFrameworkErr     error
	getTargetErr        error
	initErr             error
}

func (f *fakeServiceManager_Cov3) GetRequiredTools(
	ctx context.Context, sc *ServiceConfig,
) ([]tools.ExternalTool, error) {
	return f.requiredTools, f.getRequiredToolsErr
}

func (f *fakeServiceManager_Cov3) Initialize(ctx context.Context, sc *ServiceConfig) error {
	return f.initErr
}

func (f *fakeServiceManager_Cov3) Restore(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, p *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	return nil, nil
}

func (f *fakeServiceManager_Cov3) Build(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, p *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	return nil, nil
}

func (f *fakeServiceManager_Cov3) Package(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, p *async.Progress[ServiceProgress], opts *PackageOptions,
) (*ServicePackageResult, error) {
	return nil, nil
}

func (f *fakeServiceManager_Cov3) Publish(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, p *async.Progress[ServiceProgress], opts *PublishOptions,
) (*ServicePublishResult, error) {
	return nil, nil
}

func (f *fakeServiceManager_Cov3) Deploy(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, p *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	return nil, nil
}

func (f *fakeServiceManager_Cov3) GetTargetResource(
	ctx context.Context, sc *ServiceConfig, st ServiceTarget,
) (*environment.TargetResource, error) {
	return nil, nil
}

func (f *fakeServiceManager_Cov3) GetFrameworkService(
	ctx context.Context, sc *ServiceConfig,
) (FrameworkService, error) {
	return f.frameworkSvc, f.getFrameworkErr
}

func (f *fakeServiceManager_Cov3) GetServiceTarget(
	ctx context.Context, sc *ServiceConfig,
) (ServiceTarget, error) {
	return f.serviceTarget, f.getTargetErr
}

// ---------- fake ServiceTarget ----------
type fakeServiceTarget_Cov3 struct {
	requiredTools []tools.ExternalTool
}

func (f *fakeServiceTarget_Cov3) Initialize(ctx context.Context, sc *ServiceConfig) error {
	return nil
}

func (f *fakeServiceTarget_Cov3) RequiredExternalTools(
	ctx context.Context, sc *ServiceConfig,
) []tools.ExternalTool {
	return f.requiredTools
}

func (f *fakeServiceTarget_Cov3) Package(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, p *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return nil, nil
}

func (f *fakeServiceTarget_Cov3) Publish(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, tr *environment.TargetResource,
	p *async.Progress[ServiceProgress], opts *PublishOptions,
) (*ServicePublishResult, error) {
	return nil, nil
}

func (f *fakeServiceTarget_Cov3) Deploy(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, tr *environment.TargetResource,
	p *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	return nil, nil
}

func (f *fakeServiceTarget_Cov3) Endpoints(
	ctx context.Context, sc *ServiceConfig, tr *environment.TargetResource,
) ([]string, error) {
	return nil, nil
}

// ---------- helper ----------
func makeSvcConfig(name, relPath string, host ServiceTargetKind, lang ServiceLanguageKind, projDir string) *ServiceConfig {
	prj := &ProjectConfig{Path: projDir, Services: map[string]*ServiceConfig{}}
	sc := &ServiceConfig{
		Name:         name,
		RelativePath: relPath,
		Host:         host,
		Language:     lang,
		Project:      prj,
	}
	prj.Services[name] = sc
	return sc
}

// ============================================================
// Tests
// ============================================================

func Test_projectManager_Initialize_Coverage3(t *testing.T) {
	t.Run("NoServices", func(t *testing.T) {
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{frameworkSvc: &noOpProject{}},
		}
		prj := &ProjectConfig{Services: map[string]*ServiceConfig{}}
		err := pm.Initialize(t.Context(), prj)
		require.NoError(t, err)
	})

	t.Run("OneService_Success", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{frameworkSvc: &noOpProject{}},
		}
		err := pm.Initialize(t.Context(), sc.Project)
		require.NoError(t, err)
	})

	t.Run("OneService_InitError", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{initErr: assert.AnError},
		}
		err := pm.Initialize(t.Context(), sc.Project)
		require.Error(t, err)
		require.Contains(t, err.Error(), "initializing service 'api'")
	})
}

func Test_projectManager_DefaultServiceFromWd_Coverage3(t *testing.T) {
	t.Run("WdIsProjectDir_ReturnsNil", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Chdir(tmpDir)
		azdCtx := azdcontext.NewAzdContextWithDirectory(tmpDir)
		pm := &projectManager{
			azdContext:     azdCtx,
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{},
		}
		prj := &ProjectConfig{Path: tmpDir, Services: map[string]*ServiceConfig{}}
		svc, err := pm.DefaultServiceFromWd(t.Context(), prj)
		require.NoError(t, err)
		assert.Nil(t, svc)
	})

	t.Run("WdMatchesService", func(t *testing.T) {
		tmpDir := t.TempDir()
		svcDir := filepath.Join(tmpDir, "api")
		require.NoError(t, os.MkdirAll(svcDir, 0o755))
		t.Chdir(svcDir)

		azdCtx := azdcontext.NewAzdContextWithDirectory(tmpDir)
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			azdContext:     azdCtx,
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{},
		}
		svc, err := pm.DefaultServiceFromWd(t.Context(), sc.Project)
		require.NoError(t, err)
		require.NotNil(t, svc)
		assert.Equal(t, "api", svc.Name)
	})

	t.Run("WdNoMatch_ReturnsError", func(t *testing.T) {
		tmpDir := t.TempDir()
		otherDir := filepath.Join(tmpDir, "unrelated")
		require.NoError(t, os.MkdirAll(otherDir, 0o755))
		t.Chdir(otherDir)

		azdCtx := azdcontext.NewAzdContextWithDirectory(tmpDir)
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			azdContext:     azdCtx,
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{},
		}
		svc, err := pm.DefaultServiceFromWd(t.Context(), sc.Project)
		require.ErrorIs(t, err, ErrNoDefaultService)
		assert.Nil(t, svc)
	})
}

func Test_projectManager_EnsureAllTools_Coverage3(t *testing.T) {
	t.Run("NoServices", func(t *testing.T) {
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{},
		}
		prj := &ProjectConfig{Services: map[string]*ServiceConfig{}}
		err := pm.EnsureAllTools(t.Context(), prj, nil)
		require.NoError(t, err)
	})

	t.Run("WithService_NoTools", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{requiredTools: nil},
		}
		err := pm.EnsureAllTools(t.Context(), sc.Project, nil)
		require.NoError(t, err)
	})

	t.Run("FilterSkipsService", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{getRequiredToolsErr: assert.AnError},
		}
		// Filter rejects all services → loop body never executes → no error
		err := pm.EnsureAllTools(t.Context(), sc.Project, func(svc *ServiceConfig) bool { return false })
		require.NoError(t, err)
	})

	t.Run("GetRequiredToolsError", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{getRequiredToolsErr: assert.AnError},
		}
		err := pm.EnsureAllTools(t.Context(), sc.Project, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "getting service required tools")
	})
}

func Test_projectManager_EnsureFrameworkTools_Coverage3(t *testing.T) {
	t.Run("NoServices", func(t *testing.T) {
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{},
		}
		prj := &ProjectConfig{Services: map[string]*ServiceConfig{}}
		err := pm.EnsureFrameworkTools(t.Context(), prj, nil)
		require.NoError(t, err)
	})

	t.Run("WithService_Success", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{frameworkSvc: &noOpProject{}},
		}
		err := pm.EnsureFrameworkTools(t.Context(), sc.Project, nil)
		require.NoError(t, err)
	})

	t.Run("GetFrameworkError", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{getFrameworkErr: assert.AnError},
		}
		err := pm.EnsureFrameworkTools(t.Context(), sc.Project, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "getting framework service")
	})

	t.Run("FilterSkipsService", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{getFrameworkErr: assert.AnError},
		}
		err := pm.EnsureFrameworkTools(t.Context(), sc.Project, func(svc *ServiceConfig) bool { return false })
		require.NoError(t, err)
	})
}

func Test_projectManager_EnsureServiceTargetTools_Coverage3(t *testing.T) {
	t.Run("NoServices", func(t *testing.T) {
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{},
		}
		prj := &ProjectConfig{Services: map[string]*ServiceConfig{}}
		err := pm.EnsureServiceTargetTools(t.Context(), prj, nil)
		require.NoError(t, err)
	})

	t.Run("WithService_NoTools", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{serviceTarget: &fakeServiceTarget_Cov3{}},
		}
		err := pm.EnsureServiceTargetTools(t.Context(), sc.Project, nil)
		require.NoError(t, err)
	})

	t.Run("GetTargetError", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{getTargetErr: assert.AnError},
		}
		err := pm.EnsureServiceTargetTools(t.Context(), sc.Project, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "getting service target")
	})

	t.Run("FilterSkipsService", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{getTargetErr: assert.AnError},
		}
		err := pm.EnsureServiceTargetTools(t.Context(), sc.Project, func(svc *ServiceConfig) bool { return false })
		require.NoError(t, err)
	})
}

func Test_projectManager_EnsureRestoreTools_Coverage3(t *testing.T) {
	t.Run("NoServices", func(t *testing.T) {
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{},
		}
		prj := &ProjectConfig{Services: map[string]*ServiceConfig{}}
		err := pm.EnsureRestoreTools(t.Context(), prj, nil)
		require.NoError(t, err)
	})

	t.Run("WithService_NonDocker", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{frameworkSvc: &noOpProject{}},
		}
		err := pm.EnsureRestoreTools(t.Context(), sc.Project, nil)
		require.NoError(t, err)
	})

	t.Run("GetFrameworkError", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{getFrameworkErr: assert.AnError},
		}
		err := pm.EnsureRestoreTools(t.Context(), sc.Project, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "getting framework service")
	})

	t.Run("DockerProject_DelegatesInner", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", ContainerAppTarget, ServiceLanguagePython, tmpDir)
		inner := &noOpProject{}
		dp := &dockerProject{framework: inner}
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager_Cov3{frameworkSvc: dp},
		}
		err := pm.EnsureRestoreTools(t.Context(), sc.Project, nil)
		require.NoError(t, err)
	})
}

func Test_suggestRemoteBuild_Extended_Coverage3(t *testing.T) {
	t.Run("NonDockerTool_ReturnsNil", func(t *testing.T) {
		toolErr := &tools.MissingToolErrors{ToolNames: []string{"Python"}}
		result := suggestRemoteBuild(nil, toolErr)
		assert.Nil(t, result)
	})

	t.Run("DockerMissing_NoRemoteBuildCapable_ReturnsNil", func(t *testing.T) {
		toolErr := &tools.MissingToolErrors{ToolNames: []string{"Docker"}}
		infos := []svcToolInfo{{svc: &ServiceConfig{Name: "web"}, needsDocker: false}}
		result := suggestRemoteBuild(infos, toolErr)
		assert.Nil(t, result)
	})

	t.Run("DockerMissing_HasRemoteBuildCapable_Install", func(t *testing.T) {
		toolErr := &tools.MissingToolErrors{
			ToolNames: []string{"Docker"},
		}
		infos := []svcToolInfo{{svc: &ServiceConfig{Name: "api"}, needsDocker: true}}
		result := suggestRemoteBuild(infos, toolErr)
		require.NotNil(t, result)
		assert.Contains(t, result.Suggestion, "api")
		assert.Contains(t, result.Suggestion, "remoteBuild")
		assert.Contains(t, result.Suggestion, "install Docker")
	})

	t.Run("DockerNotRunning_Suggestion", func(t *testing.T) {
		toolErr := &tools.MissingToolErrors{
			ToolNames: []string{"Docker"},
			Errs:      []error{&notRunningErr{}},
		}
		infos := []svcToolInfo{{svc: &ServiceConfig{Name: "api"}, needsDocker: true}}
		result := suggestRemoteBuild(infos, toolErr)
		require.NotNil(t, result)
		assert.Contains(t, result.Suggestion, "start your container runtime")
	})
}

// notRunningErr makes Error() contain "is not running" for suggestRemoteBuild.
type notRunningErr struct{}

func (e *notRunningErr) Error() string {
	return "Docker is not running"
}
