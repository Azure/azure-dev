// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

func Test_suggestRemoteBuild(t *testing.T) {
	dockerMissing := &tools.MissingToolErrors{
		ToolNames: []string{"Docker"},
		Errs:      []error{fmt.Errorf("neither docker nor podman is installed")},
	}
	dockerNotRunning := &tools.MissingToolErrors{
		ToolNames: []string{"Docker"},
		Errs:      []error{fmt.Errorf("the Docker service is not running, please start it")},
	}
	bicepMissing := &tools.MissingToolErrors{
		ToolNames: []string{"bicep"},
		Errs:      []error{assert.AnError},
	}

	tests := []struct {
		name           string
		svcTools       []svcToolInfo
		toolErr        *tools.MissingToolErrors
		wantSuggestion bool
		wantContains   string
	}{
		{
			name: "Service_needing_Docker_suggests",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: true},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "api",
		},
		{
			name: "Multiple_services_lists_all",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: true},
				{svc: &ServiceConfig{Name: "web"}, needsDocker: true},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "api, web",
		},
		{
			name: "Service_not_needing_Docker_no_suggestion",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: false},
			},
			toolErr:        dockerMissing,
			wantSuggestion: false,
		},
		{
			name: "Non_Docker_tool_missing_no_suggestion",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: true},
			},
			toolErr:        bicepMissing,
			wantSuggestion: false,
		},
		{
			name: "Mixed_services_only_Docker_ones",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: true},
				{svc: &ServiceConfig{Name: "web"}, needsDocker: false},
				{svc: &ServiceConfig{Name: "worker"}, needsDocker: true},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "api, worker",
		},
		{
			name: "Docker_not_running_suggests_start",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: true},
			},
			toolErr:        dockerNotRunning,
			wantSuggestion: true,
			wantContains:   "start your container runtime",
		},
		{
			name: "Docker_not_installed_suggests_install",
			svcTools: []svcToolInfo{
				{svc: &ServiceConfig{Name: "api"}, needsDocker: true},
			},
			toolErr:        dockerMissing,
			wantSuggestion: true,
			wantContains:   "install Docker",
		},
		{
			name:           "Empty_services_no_suggestion",
			svcTools:       []svcToolInfo{},
			toolErr:        dockerMissing,
			wantSuggestion: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := suggestRemoteBuild(tt.svcTools, tt.toolErr)

			if !tt.wantSuggestion {
				assert.Nil(t, result)
				return
			}

			require.NotNil(t, result)
			assert.Contains(t, result.Suggestion, tt.wantContains)
			assert.Contains(t, result.Suggestion, "remoteBuild")
		})
	}
}

// ---------- fake ServiceManager ----------
type fakeServiceManager struct {
	frameworkSvc               FrameworkService
	serviceTarget              ServiceTarget
	requiredTools              []tools.ExternalTool
	getRequiredToolsErr        error
	getFrameworkErr            error
	getTargetErr               error
	initErr                    error
	initFrameworkErr           error
	initFrameworkErrForService map[string]error
}

func (f *fakeServiceManager) GetRequiredTools(
	ctx context.Context, sc *ServiceConfig,
) ([]tools.ExternalTool, error) {
	return f.requiredTools, f.getRequiredToolsErr
}

func (f *fakeServiceManager) Initialize(ctx context.Context, sc *ServiceConfig) error {
	return f.initErr
}

func (f *fakeServiceManager) InitializeFrameworkService(ctx context.Context, sc *ServiceConfig) error {
	if _, err := f.GetFrameworkService(ctx, sc); err != nil {
		return err
	}
	if err, ok := f.initFrameworkErrForService[sc.Name]; ok {
		return err
	}
	return f.initFrameworkErr
}

func (f *fakeServiceManager) Restore(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, p *async.Progress[ServiceProgress],
) (*ServiceRestoreResult, error) {
	return nil, nil
}

func (f *fakeServiceManager) Build(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, p *async.Progress[ServiceProgress],
) (*ServiceBuildResult, error) {
	return nil, nil
}

func (f *fakeServiceManager) Package(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, p *async.Progress[ServiceProgress], opts *PackageOptions,
) (*ServicePackageResult, error) {
	return nil, nil
}

func (f *fakeServiceManager) Publish(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, p *async.Progress[ServiceProgress], opts *PublishOptions,
) (*ServicePublishResult, error) {
	return nil, nil
}

func (f *fakeServiceManager) Deploy(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, p *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	return nil, nil
}

func (f *fakeServiceManager) GetTargetResource(
	ctx context.Context, sc *ServiceConfig, st ServiceTarget,
) (*environment.TargetResource, error) {
	return nil, nil
}

func (f *fakeServiceManager) GetFrameworkService(
	ctx context.Context, sc *ServiceConfig,
) (FrameworkService, error) {
	return f.frameworkSvc, f.getFrameworkErr
}

func (f *fakeServiceManager) GetServiceTarget(
	ctx context.Context, sc *ServiceConfig,
) (ServiceTarget, error) {
	return f.serviceTarget, f.getTargetErr
}

// ---------- fake ServiceTarget ----------
type fakeConfigurableServiceTarget struct {
	requiredTools []tools.ExternalTool
}

func (f *fakeConfigurableServiceTarget) Initialize(ctx context.Context, sc *ServiceConfig) error {
	return nil
}

func (f *fakeConfigurableServiceTarget) RequiredExternalTools(
	ctx context.Context, sc *ServiceConfig,
) []tools.ExternalTool {
	return f.requiredTools
}

func (f *fakeConfigurableServiceTarget) Package(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, p *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return nil, nil
}

func (f *fakeConfigurableServiceTarget) Publish(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, tr *environment.TargetResource,
	p *async.Progress[ServiceProgress], opts *PublishOptions,
) (*ServicePublishResult, error) {
	return nil, nil
}

func (f *fakeConfigurableServiceTarget) Deploy(
	ctx context.Context, sc *ServiceConfig, sctx *ServiceContext, tr *environment.TargetResource,
	p *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	return nil, nil
}

func (f *fakeConfigurableServiceTarget) Endpoints(
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

func Test_projectManager_Initialize(t *testing.T) {
	t.Run("NoServices", func(t *testing.T) {
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager{frameworkSvc: &noOpProject{}},
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
			serviceManager: &fakeServiceManager{frameworkSvc: &noOpProject{}},
		}
		err := pm.Initialize(t.Context(), sc.Project)
		require.NoError(t, err)
	})

	t.Run("OneService_InitError", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager{initErr: assert.AnError},
		}
		err := pm.Initialize(t.Context(), sc.Project)
		require.Error(t, err)
		require.Contains(t, err.Error(), "initializing service 'api'")
	})
}

func Test_projectManager_InitializeFrameworks(t *testing.T) {
	newProject := func(dir string) *ProjectConfig {
		prj := &ProjectConfig{Path: dir, Services: map[string]*ServiceConfig{}}
		for _, name := range []string{"api", "agent"} {
			prj.Services[name] = &ServiceConfig{
				Name:         name,
				RelativePath: name,
				Host:         AppServiceTarget,
				Language:     ServiceLanguagePython,
				Project:      prj,
			}
		}
		return prj
	}

	t.Run("AllServicesInitialized", func(t *testing.T) {
		prj := newProject(t.TempDir())
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager{frameworkSvc: &noOpProject{}},
		}
		initialized, skipped, err := pm.InitializeFrameworks(t.Context(), prj)
		require.NoError(t, err)
		require.Len(t, initialized, 2)
		require.Empty(t, skipped)
	})

	// A per-service framework initialization failure (for example an extension-provided host that
	// is not loaded) must be skipped, not abort the whole project. The other services still init.
	t.Run("SkipsServiceThatFailsFrameworkInit", func(t *testing.T) {
		prj := newProject(t.TempDir())
		pm := &projectManager{
			importManager: NewImportManager(nil),
			serviceManager: &fakeServiceManager{
				frameworkSvc:               &noOpProject{},
				initFrameworkErrForService: map[string]error{"agent": assert.AnError},
			},
		}
		initialized, skipped, err := pm.InitializeFrameworks(t.Context(), prj)
		require.NoError(t, err)
		require.Len(t, initialized, 1)
		require.Equal(t, "api", initialized[0].Name)
		require.Len(t, skipped, 1)
		require.Equal(t, "agent", skipped[0].Service.Name)
		require.ErrorIs(t, skipped[0].Err, assert.AnError)
	})
}

func Test_projectManager_DefaultServiceFromWd(t *testing.T) {
	t.Run("WdIsProjectDir_ReturnsNil", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Chdir(tmpDir)
		azdCtx := azdcontext.NewAzdContextWithDirectory(tmpDir)
		pm := &projectManager{
			azdContext:     azdCtx,
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager{},
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
			serviceManager: &fakeServiceManager{},
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
			serviceManager: &fakeServiceManager{},
		}
		svc, err := pm.DefaultServiceFromWd(t.Context(), sc.Project)
		require.ErrorIs(t, err, ErrNoDefaultService)
		assert.Nil(t, svc)
	})
}

func Test_projectManager_EnsureAllTools(t *testing.T) {
	t.Run("NoServices", func(t *testing.T) {
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager{},
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
			serviceManager: &fakeServiceManager{requiredTools: nil},
		}
		err := pm.EnsureAllTools(t.Context(), sc.Project, nil)
		require.NoError(t, err)
	})

	t.Run("FilterSkipsService", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager{getRequiredToolsErr: assert.AnError},
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
			serviceManager: &fakeServiceManager{getRequiredToolsErr: assert.AnError},
		}
		err := pm.EnsureAllTools(t.Context(), sc.Project, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "getting service required tools")
	})
}

func Test_projectManager_EnsureFrameworkTools(t *testing.T) {
	t.Run("NoServices", func(t *testing.T) {
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager{},
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
			serviceManager: &fakeServiceManager{frameworkSvc: &noOpProject{}},
		}
		err := pm.EnsureFrameworkTools(t.Context(), sc.Project, nil)
		require.NoError(t, err)
	})

	t.Run("GetFrameworkError", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager{getFrameworkErr: assert.AnError},
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
			serviceManager: &fakeServiceManager{getFrameworkErr: assert.AnError},
		}
		err := pm.EnsureFrameworkTools(t.Context(), sc.Project, func(svc *ServiceConfig) bool { return false })
		require.NoError(t, err)
	})
}

func Test_projectManager_EnsureServiceTargetTools(t *testing.T) {
	t.Run("NoServices", func(t *testing.T) {
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager{},
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
			serviceManager: &fakeServiceManager{serviceTarget: &fakeConfigurableServiceTarget{}},
		}
		err := pm.EnsureServiceTargetTools(t.Context(), sc.Project, nil)
		require.NoError(t, err)
	})

	t.Run("GetTargetError", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager{getTargetErr: assert.AnError},
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
			serviceManager: &fakeServiceManager{getTargetErr: assert.AnError},
		}
		err := pm.EnsureServiceTargetTools(t.Context(), sc.Project, func(svc *ServiceConfig) bool { return false })
		require.NoError(t, err)
	})
}

func Test_projectManager_EnsureRestoreTools(t *testing.T) {
	t.Run("NoServices", func(t *testing.T) {
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager{},
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
			serviceManager: &fakeServiceManager{frameworkSvc: &noOpProject{}},
		}
		err := pm.EnsureRestoreTools(t.Context(), sc.Project, nil)
		require.NoError(t, err)
	})

	t.Run("GetFrameworkError", func(t *testing.T) {
		tmpDir := t.TempDir()
		sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
		pm := &projectManager{
			importManager:  NewImportManager(nil),
			serviceManager: &fakeServiceManager{getFrameworkErr: assert.AnError},
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
			serviceManager: &fakeServiceManager{frameworkSvc: dp},
		}
		err := pm.EnsureRestoreTools(t.Context(), sc.Project, nil)
		require.NoError(t, err)
	})
}

func Test_suggestRemoteBuild_Extended(t *testing.T) {
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

func Test_NewProjectManager(t *testing.T) {
	pm := NewProjectManager(nil, nil, nil)
	require.NotNil(t, pm)
}

// ---------- Fake ExternalTool that fails CheckInstalled ----------
type failingTool struct {
	toolName   string
	installUrl string
	checkErr   error
}

// ---------- EnsureServiceTargetTools: Docker missing → suggestRemoteBuild ----------
func Test_projectManager_EnsureServiceTargetTools_DockerMissing(t *testing.T) {
	tmpDir := t.TempDir()
	dockerTool := &failingTool{
		toolName: "Docker",
		checkErr: fmt.Errorf("Docker is not installed"),
	}
	sc := makeSvcConfig("api", "api", ContainerAppTarget, ServiceLanguagePython, tmpDir)
	pm := &projectManager{
		importManager: NewImportManager(nil),
		serviceManager: &fakeServiceManager{
			serviceTarget: &fakeConfigurableServiceTarget{requiredTools: []tools.ExternalTool{dockerTool}},
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
func Test_projectManager_EnsureAllTools_ToolMissing(t *testing.T) {
	tmpDir := t.TempDir()
	pyTool := &failingTool{
		toolName: "Python",
		checkErr: fmt.Errorf("Python is not installed"),
	}
	sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
	pm := &projectManager{
		importManager: NewImportManager(nil),
		serviceManager: &fakeServiceManager{
			requiredTools: []tools.ExternalTool{pyTool},
		},
	}
	err := pm.EnsureAllTools(t.Context(), sc.Project, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Python")
}

// ---------- EnsureFrameworkTools: tool missing ----------
func Test_projectManager_EnsureFrameworkTools_ToolMissing(t *testing.T) {
	tmpDir := t.TempDir()
	pyTool := &failingTool{
		toolName: "Python",
		checkErr: fmt.Errorf("Python is not installed"),
	}
	innerFw := &fakeFrameworkForTools{tools: []tools.ExternalTool{pyTool}}
	sc := makeSvcConfig("api", "api", AppServiceTarget, ServiceLanguagePython, tmpDir)
	pm := &projectManager{
		importManager: NewImportManager(nil),
		serviceManager: &fakeServiceManager{
			frameworkSvc: innerFw,
		},
	}
	err := pm.EnsureFrameworkTools(t.Context(), sc.Project, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Python")
}

// ---------- EnsureRestoreTools: tool missing (dockerProject inner) ----------
func Test_projectManager_EnsureRestoreTools_DockerInner_ToolMissing(t *testing.T) {
	tmpDir := t.TempDir()
	pyTool := &failingTool{
		toolName: "Python",
		checkErr: fmt.Errorf("Python is not installed"),
	}
	innerFw := &fakeFrameworkForTools{tools: []tools.ExternalTool{pyTool}}
	dp := &dockerProject{framework: innerFw}
	sc := makeSvcConfig("api", "api", ContainerAppTarget, ServiceLanguagePython, tmpDir)
	pm := &projectManager{
		importManager: NewImportManager(nil),
		serviceManager: &fakeServiceManager{
			frameworkSvc: dp,
		},
	}
	err := pm.EnsureRestoreTools(t.Context(), sc.Project, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Python")
}
