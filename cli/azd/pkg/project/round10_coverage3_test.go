// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================== fakes for Round 10 ==================

// fakeTarget_r10 implements ServiceTarget with configurable results.
type fakeTarget_r10 struct {
	packageResult *ServicePackageResult
	packageErr    error
	publishResult *ServicePublishResult
	publishErr    error
	deployResult  *ServiceDeployResult
	deployErr     error
	endpoints     []string
	endpointsErr  error
}

func (f *fakeTarget_r10) Initialize(_ context.Context, _ *ServiceConfig) error {
	return nil
}

func (f *fakeTarget_r10) RequiredExternalTools(_ context.Context, _ *ServiceConfig) []tools.ExternalTool {
	return nil
}

func (f *fakeTarget_r10) Package(
	_ context.Context, _ *ServiceConfig, _ *ServiceContext, _ *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return f.packageResult, f.packageErr
}

func (f *fakeTarget_r10) Publish(
	_ context.Context, _ *ServiceConfig, _ *ServiceContext, _ *environment.TargetResource,
	_ *async.Progress[ServiceProgress], _ *PublishOptions,
) (*ServicePublishResult, error) {
	return f.publishResult, f.publishErr
}

func (f *fakeTarget_r10) Deploy(
	_ context.Context, _ *ServiceConfig, _ *ServiceContext, _ *environment.TargetResource,
	_ *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	return f.deployResult, f.deployErr
}

func (f *fakeTarget_r10) Endpoints(_ context.Context, _ *ServiceConfig, _ *environment.TargetResource) ([]string, error) {
	return f.endpoints, f.endpointsErr
}

// fakeLocator_r10 resolves FrameworkService and ServiceTarget.
// Supports optional composite framework service.
type fakeLocator_r10 struct {
	framework FrameworkService
	target    ServiceTarget
	composite CompositeFrameworkService
}

func (f *fakeLocator_r10) ResolveNamed(name string, o any) error {
	switch ptr := o.(type) {
	case *FrameworkService:
		if f.framework != nil {
			*ptr = f.framework
			return nil
		}
		return &UnsupportedServiceHostError{Host: name}
	case *ServiceTarget:
		if f.target != nil {
			*ptr = f.target
			return nil
		}
		// Return ioc.ErrResolveInstance to trigger the suggestion path
		return fmt.Errorf("%w: no target %s", ioc.ErrResolveInstance, name)
	case *CompositeFrameworkService:
		if f.composite != nil {
			*ptr = f.composite
			return nil
		}
		return &UnsupportedServiceHostError{Host: name}
	}
	return nil
}

func (f *fakeLocator_r10) Resolve(_ any) error { return nil }
func (f *fakeLocator_r10) Invoke(_ any) error  { return nil }

// helper to create a progress channel and drain it to avoid blocking.
func newDrainedProgress_r10() *async.Progress[ServiceProgress] {
	p := async.NewProgress[ServiceProgress]()
	go func() { for range p.Progress() {} }()
	return p
}

// helper to create a minimal ServiceConfig with EventDispatcher.
func makeSvcConfig_r10(name string, lang ServiceLanguageKind, host ServiceTargetKind, projPath string) *ServiceConfig {
	proj := &ProjectConfig{
		Name: "testproj",
		Path: projPath,
	}
	return &ServiceConfig{
		Name:            name,
		Language:        lang,
		Host:            host,
		Project:         proj,
		EventDispatcher: ext.NewEventDispatcher[ServiceLifecycleEventArgs](),
	}
}

// helper to build a serviceManager for round 10 tests.
func makeServiceManager_r10(
	env *environment.Environment,
	locator ioc.ServiceLocator,
	resMgr ResourceManager,
) *serviceManager {
	return &serviceManager{
		env:                 env,
		serviceLocator:      locator,
		resourceManager:     resMgr,
		operationCache:      ServiceOperationCache{},
		alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(nil),
	}
}

// ================== Package tests ==================

func Test_ServiceManager_Package_HappyPath_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeTarget_r10{
		packageResult: &ServicePackageResult{},
	}
	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework, target: target}
	resMgr := &fakeResourceManager_Cov3{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager_r10(env, locator, resMgr)

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress_r10()
	defer progress.Done()

	result, err := sm.Package(t.Context(), svcConfig, nil, progress, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_ServiceManager_Package_CacheHit_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeTarget_r10{packageResult: &ServicePackageResult{}}
	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework, target: target}
	resMgr := &fakeResourceManager_Cov3{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager_r10(env, locator, resMgr)
	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	// Pre-populate cache
	cached := &ServicePackageResult{}
	sm.setOperationResult(svcConfig, ServiceEventPackage, cached)

	progress := newDrainedProgress_r10()
	defer progress.Done()

	result, err := sm.Package(t.Context(), svcConfig, nil, progress, nil)
	require.NoError(t, err)
	require.Same(t, cached, result) // should return cached instance
}

func Test_ServiceManager_Package_FrameworkError_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	// No framework registered → error
	locator := &fakeLocator_r10{}
	resMgr := &fakeResourceManager_Cov3{}
	sm := makeServiceManager_r10(env, locator, resMgr)

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress_r10()
	defer progress.Done()

	_, err := sm.Package(t.Context(), svcConfig, nil, progress, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting framework service")
}

func Test_ServiceManager_Package_TargetError_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	framework := NewNoOpProject(env)
	// No target registered → error from IoC
	locator := &fakeLocator_r10{framework: framework}
	resMgr := &fakeResourceManager_Cov3{}
	sm := makeServiceManager_r10(env, locator, resMgr)

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress_r10()
	defer progress.Done()

	_, err := sm.Package(t.Context(), svcConfig, nil, progress, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting service target")
}

func Test_ServiceManager_Package_OutputPath_File_Coverage3(t *testing.T) {
	tmpDir := t.TempDir()
	env := environment.NewWithValues("test", map[string]string{})

	// Create a fake package artifact file
	pkgFile := filepath.Join(tmpDir, "app.zip")
	require.NoError(t, os.WriteFile(pkgFile, []byte("zip-content"), 0600))

	target := &fakeTarget_r10{
		packageResult: &ServicePackageResult{
			Artifacts: ArtifactCollection{
				&Artifact{
					Kind:         ArtifactKindArchive,
					LocationKind: LocationKindLocal,
					Location:     pkgFile,
				},
			},
		},
	}
	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework, target: target}
	resMgr := &fakeResourceManager_Cov3{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager_r10(env, locator, resMgr)
	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress_r10()
	defer progress.Done()

	// File path output
	outDir := filepath.Join(tmpDir, "out")
	outFile := filepath.Join(outDir, "result.zip")
	result, err := sm.Package(t.Context(), svcConfig, nil, progress, &PackageOptions{OutputPath: outFile})
	require.NoError(t, err)
	require.NotNil(t, result)

	// The file should have been moved
	_, err = os.Stat(outFile)
	assert.NoError(t, err, "output file should exist")
}

func Test_ServiceManager_Package_OutputPath_Dir_Coverage3(t *testing.T) {
	tmpDir := t.TempDir()
	env := environment.NewWithValues("test", map[string]string{})

	// Create a fake package artifact file
	pkgFile := filepath.Join(tmpDir, "app.zip")
	require.NoError(t, os.WriteFile(pkgFile, []byte("zip-content"), 0600))

	target := &fakeTarget_r10{
		packageResult: &ServicePackageResult{
			Artifacts: ArtifactCollection{
				&Artifact{
					Kind:         ArtifactKindArchive,
					LocationKind: LocationKindLocal,
					Location:     pkgFile,
				},
			},
		},
	}
	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework, target: target}
	resMgr := &fakeResourceManager_Cov3{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager_r10(env, locator, resMgr)
	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress_r10()
	defer progress.Done()

	// Directory path output (no extension → treated as directory)
	outDir := filepath.Join(tmpDir, "outdir")
	result, err := sm.Package(t.Context(), svcConfig, nil, progress, &PackageOptions{OutputPath: outDir})
	require.NoError(t, err)
	require.NotNil(t, result)

	// The file should have been moved to outdir/app.zip
	_, err = os.Stat(filepath.Join(outDir, "app.zip"))
	assert.NoError(t, err, "output file should exist in directory")
}

// ================== Deploy tests ==================

func Test_ServiceManager_Deploy_HappyPath_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeTarget_r10{
		packageResult: &ServicePackageResult{},
		publishResult: &ServicePublishResult{},
		deployResult:  &ServiceDeployResult{},
	}
	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework, target: target}
	resMgr := &fakeResourceManager_Cov3{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager_r10(env, locator, resMgr)
	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress_r10()
	defer progress.Done()

	result, err := sm.Deploy(t.Context(), svcConfig, nil, progress)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_ServiceManager_Deploy_CacheHit_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeTarget_r10{deployResult: &ServiceDeployResult{}}
	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework, target: target}
	resMgr := &fakeResourceManager_Cov3{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager_r10(env, locator, resMgr)
	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	// Pre-populate deploy cache
	cached := &ServiceDeployResult{}
	sm.setOperationResult(svcConfig, ServiceEventDeploy, cached)

	progress := newDrainedProgress_r10()
	defer progress.Done()

	result, err := sm.Deploy(t.Context(), svcConfig, nil, progress)
	require.NoError(t, err)
	require.Same(t, cached, result)
}

func Test_ServiceManager_Deploy_WithOverriddenEndpoints_Coverage3(t *testing.T) {
	// Set SERVICE_WEB_ENDPOINTS in dotenv
	env := environment.NewWithValues("test", map[string]string{
		"SERVICE_WEB_ENDPOINTS": `["http://example.com","http://other.com"]`,
	})

	target := &fakeTarget_r10{
		packageResult: &ServicePackageResult{},
		publishResult: &ServicePublishResult{},
		deployResult:  &ServiceDeployResult{},
	}
	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework, target: target}
	resMgr := &fakeResourceManager_Cov3{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager_r10(env, locator, resMgr)
	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress_r10()
	defer progress.Done()

	result, err := sm.Deploy(t.Context(), svcConfig, nil, progress)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Overridden endpoints should be added as artifacts
	assert.GreaterOrEqual(t, len(result.Artifacts), 2)
}

func Test_ServiceManager_Deploy_TargetError_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeTarget_r10{
		packageResult: &ServicePackageResult{},
		publishResult: &ServicePublishResult{},
		deployErr:     errors.New("deploy-failed"),
	}
	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework, target: target}
	resMgr := &fakeResourceManager_Cov3{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager_r10(env, locator, resMgr)
	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress_r10()
	defer progress.Done()

	_, err := sm.Deploy(t.Context(), svcConfig, nil, progress)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed deploying service")
}

// ================== Publish tests ==================

func Test_ServiceManager_Publish_HappyPath_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeTarget_r10{
		packageResult: &ServicePackageResult{},
		publishResult: &ServicePublishResult{},
	}
	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework, target: target}
	resMgr := &fakeResourceManager_Cov3{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager_r10(env, locator, resMgr)
	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress_r10()
	defer progress.Done()

	result, err := sm.Publish(t.Context(), svcConfig, nil, progress, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_ServiceManager_Publish_CacheHit_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeTarget_r10{publishResult: &ServicePublishResult{}}
	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework, target: target}
	resMgr := &fakeResourceManager_Cov3{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager_r10(env, locator, resMgr)
	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	cached := &ServicePublishResult{}
	sm.setOperationResult(svcConfig, ServiceEventPublish, cached)

	progress := newDrainedProgress_r10()
	defer progress.Done()

	result, err := sm.Publish(t.Context(), svcConfig, nil, progress, nil)
	require.NoError(t, err)
	require.Same(t, cached, result)
}

// ================== Build tests ==================

func Test_ServiceManager_Build_HappyPath_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework}
	resMgr := &fakeResourceManager_Cov3{}

	sm := makeServiceManager_r10(env, locator, resMgr)
	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress_r10()
	defer progress.Done()

	result, err := sm.Build(t.Context(), svcConfig, nil, progress)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_ServiceManager_Build_CacheHit_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework}
	resMgr := &fakeResourceManager_Cov3{}

	sm := makeServiceManager_r10(env, locator, resMgr)
	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	cached := &ServiceBuildResult{}
	sm.setOperationResult(svcConfig, ServiceEventBuild, cached)

	progress := newDrainedProgress_r10()
	defer progress.Done()

	result, err := sm.Build(t.Context(), svcConfig, nil, progress)
	require.NoError(t, err)
	require.Same(t, cached, result)
}

// ================== GetFrameworkService tests ==================

func Test_ServiceManager_GetFrameworkService_ImageOverride_Coverage3(t *testing.T) {
	// When Language==None and Image is set, it should override to Docker
	env := environment.NewWithValues("test", map[string]string{})
	framework := NewNoOpProject(env)
	locator := &fakeLocator_r10{framework: framework}
	sm := makeServiceManager_r10(env, locator, &fakeResourceManager_Cov3{})

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageNone, ContainerAppTarget, t.TempDir())
	svcConfig.Image = osutil.NewExpandableString("myregistry.azurecr.io/myapp:latest")

	fs, err := sm.GetFrameworkService(t.Context(), svcConfig)
	require.NoError(t, err)
	require.NotNil(t, fs)
	// After the call, Language should be overridden to Docker
	assert.Equal(t, ServiceLanguageDocker, svcConfig.Language)
}

func Test_ServiceManager_GetFrameworkService_CompositeWrap_Coverage3(t *testing.T) {
	// When host.RequiresContainer() && language != Docker/None → wrap with composite
	env := environment.NewWithValues("test", map[string]string{})
	framework := NewNoOpProject(env)
	composite := &fakeCompositeFramework_Cov3{}
	locator := &fakeLocator_r10{
		framework: framework,
		composite: composite,
	}
	sm := makeServiceManager_r10(env, locator, &fakeResourceManager_Cov3{})

	// ContainerAppTarget.RequiresContainer() == true, language Python != Docker
	svcConfig := makeSvcConfig_r10("web", ServiceLanguagePython, ContainerAppTarget, t.TempDir())

	fs, err := sm.GetFrameworkService(t.Context(), svcConfig)
	require.NoError(t, err)
	require.NotNil(t, fs)
	// Should have wrapped with composite and set source
	assert.Equal(t, framework, composite.source)
}

func Test_ServiceManager_GetFrameworkService_ResolveError_Coverage3(t *testing.T) {
	// When language resolution fails with non-ErrResolveInstance error
	env := environment.NewWithValues("test", map[string]string{})
	locator := &fakeLocator_r10{} // no framework → UnsupportedServiceHostError
	sm := makeServiceManager_r10(env, locator, &fakeResourceManager_Cov3{})

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	_, err := sm.GetFrameworkService(t.Context(), svcConfig)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve language")
}

// ================== GetServiceTarget tests ==================

func Test_ServiceManager_GetServiceTarget_IoC_Error_Coverage3(t *testing.T) {
	// When target resolution fails with ioc.ErrResolveInstance → ErrorWithSuggestion
	env := environment.NewWithValues("test", map[string]string{})
	locator := &fakeLocator_r10{} // target is nil → returns ioc.ErrResolveInstance
	sm := makeServiceManager_r10(env, locator, &fakeResourceManager_Cov3{})

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	_, err := sm.GetServiceTarget(t.Context(), svcConfig)
	require.Error(t, err)
	// Should contain suggestion about supported hosts
	assert.Contains(t, err.Error(), "appservice")
}

func Test_ServiceManager_GetServiceTarget_HappyPath_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})
	target := &fakeTarget_r10{}
	locator := &fakeLocator_r10{target: target}
	sm := makeServiceManager_r10(env, locator, &fakeResourceManager_Cov3{})

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	result, err := sm.GetServiceTarget(t.Context(), svcConfig)
	require.NoError(t, err)
	assert.Same(t, target, result)
}

// ================== GetTargetResource tests ==================

func Test_ServiceManager_GetTargetResource_Default_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})
	expected := environment.NewTargetResource("sub", "rg", "myres", "type")
	resMgr := &fakeResourceManager_Cov3{targetResource: expected}
	sm := makeServiceManager_r10(env, &fakeLocator_r10{}, resMgr)

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	target := &fakeTarget_r10{}

	result, err := sm.GetTargetResource(t.Context(), svcConfig, target)
	require.NoError(t, err)
	assert.Equal(t, expected, result)
}

func Test_ServiceManager_GetTargetResource_DotNetContainerApp_WithAspire_Coverage3(t *testing.T) {
	// DotNetContainerAppTarget with DotNetContainerApp set (Aspire path)
	// Should use resourceGroupName from config + containerEnvName from env
	env := environment.NewWithValues("test", map[string]string{
		"SERVICE_WEB_CONTAINER_ENVIRONMENT_NAME": "myenv",
	})

	resMgr := &fakeResourceManager_Cov3{resourceGroupName: "my-rg"}
	sm := makeServiceManager_r10(env, &fakeLocator_r10{}, resMgr)

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageCsharp, DotNetContainerAppTarget, t.TempDir())
	svcConfig.DotNetContainerApp = &DotNetContainerAppOptions{}

	result, err := sm.GetTargetResource(t.Context(), svcConfig, &fakeTarget_r10{})
	require.NoError(t, err)
	assert.Equal(t, "myenv", result.ResourceName())
	assert.Equal(t, "my-rg", result.ResourceGroupName())
}

func Test_ServiceManager_GetTargetResource_DotNetContainerApp_NoEnvName_Coverage3(t *testing.T) {
	// DotNetContainerAppTarget without DotNetContainerApp and no container env name → error
	env := environment.NewWithValues("test", map[string]string{})
	resMgr := &fakeResourceManager_Cov3{resourceGroupName: "rg"}
	sm := makeServiceManager_r10(env, &fakeLocator_r10{}, resMgr)

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageCsharp, DotNetContainerAppTarget, t.TempDir())

	_, err := sm.GetTargetResource(t.Context(), svcConfig, &fakeTarget_r10{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not determine container app environment")
}

func Test_ServiceManager_GetTargetResource_DotNetContainerApp_FromGlobalEnv_Coverage3(t *testing.T) {
	// DotNetContainerAppTarget using AZURE_CONTAINER_APPS_ENVIRONMENT_ID (global fallback)
	env := environment.NewWithValues("test", map[string]string{
		"AZURE_CONTAINER_APPS_ENVIRONMENT_ID": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/managedEnvironments/myenv",
	})
	resMgr := &fakeResourceManager_Cov3{resourceGroupName: "rg"}
	sm := makeServiceManager_r10(env, &fakeLocator_r10{}, resMgr)

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageCsharp, DotNetContainerAppTarget, t.TempDir())

	result, err := sm.GetTargetResource(t.Context(), svcConfig, &fakeTarget_r10{})
	require.NoError(t, err)
	// Should extract last segment from the env ID
	assert.Equal(t, "myenv", result.ResourceName())
}

func Test_ServiceManager_GetTargetResource_TargetResourceResolver_Coverage3(t *testing.T) {
	// Target that implements TargetResourceResolver
	env := environment.NewWithValues("test", map[string]string{})
	expected := environment.NewTargetResource("sub2", "rg2", "custom", "type2")

	resolver := &fakeTargetResolver_r10{resource: expected}
	resMgr := &fakeResourceManager_Cov3{}
	sm := makeServiceManager_r10(env, &fakeLocator_r10{}, resMgr)

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	result, err := sm.GetTargetResource(t.Context(), svcConfig, resolver)
	require.NoError(t, err)
	assert.Equal(t, expected, result)
}

// fakeTargetResolver_r10 implements both ServiceTarget and TargetResourceResolver.
type fakeTargetResolver_r10 struct {
	fakeTarget_r10
	resource *environment.TargetResource
	err      error
}

func (f *fakeTargetResolver_r10) ResolveTargetResource(
	_ context.Context, _ string, _ *ServiceConfig,
	_ func() (*environment.TargetResource, error),
) (*environment.TargetResource, error) {
	return f.resource, f.err
}

// ================== GetRequiredTools tests ==================

func Test_ServiceManager_GetRequiredTools_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})
	framework := NewNoOpProject(env)
	target := &fakeTarget_r10{}
	locator := &fakeLocator_r10{framework: framework, target: target}
	sm := makeServiceManager_r10(env, locator, &fakeResourceManager_Cov3{})

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	tools, err := sm.GetRequiredTools(t.Context(), svcConfig)
	require.NoError(t, err)
	assert.Empty(t, tools)
}

// ================== OverriddenEndpoints tests ==================

func Test_OverriddenEndpoints_ValidJSON_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{
		"SERVICE_MYAPP_ENDPOINTS": `["http://a.com","http://b.com"]`,
	})
	svcConfig := &ServiceConfig{Name: "myapp"}

	endpoints := OverriddenEndpoints(context.Background(), svcConfig, env)
	assert.Equal(t, []string{"http://a.com", "http://b.com"}, endpoints)
}

func Test_OverriddenEndpoints_InvalidJSON_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{
		"SERVICE_MYAPP_ENDPOINTS": `not-json`,
	})
	svcConfig := &ServiceConfig{Name: "myapp"}

	endpoints := OverriddenEndpoints(context.Background(), svcConfig, env)
	assert.Nil(t, endpoints)
}

func Test_OverriddenEndpoints_Empty_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})
	svcConfig := &ServiceConfig{Name: "myapp"}

	endpoints := OverriddenEndpoints(context.Background(), svcConfig, env)
	assert.Nil(t, endpoints)
}

// ================== GetTargetResource with ResourceGroupName override ==================

func Test_ServiceManager_GetTargetResource_DotNetContainerApp_RgOverride_Coverage3(t *testing.T) {
	// Test with ResourceGroupName set on ServiceConfig
	env := environment.NewWithValues("test", map[string]string{
		"SERVICE_WEB_CONTAINER_ENVIRONMENT_NAME": "myenv",
	})
	resMgr := &fakeResourceManager_Cov3{resourceGroupName: "override-rg"}
	sm := makeServiceManager_r10(env, &fakeLocator_r10{}, resMgr)

	svcConfig := makeSvcConfig_r10("web", ServiceLanguageCsharp, DotNetContainerAppTarget, t.TempDir())
	svcConfig.ResourceGroupName = osutil.NewExpandableString("my-custom-rg")

	result, err := sm.GetTargetResource(t.Context(), svcConfig, &fakeTarget_r10{})
	require.NoError(t, err)
	assert.Equal(t, "myenv", result.ResourceName())
}
