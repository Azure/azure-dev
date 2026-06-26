// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func Test_NewExternalFrameworkService(t *testing.T) {
	svc := NewExternalFrameworkService("test-lang", ServiceLanguageCustom, nil, nil, nil)
	require.NotNil(t, svc)
}

// Unknown language passes through

// fakeServiceLocator resolves FrameworkService and ServiceTarget.
// Supports optional composite framework service.
type fakeServiceLocator struct {
	framework FrameworkService
	target    ServiceTarget
	composite CompositeFrameworkService
}

func (f *fakeServiceLocator) ResolveNamed(name string, o any) error {
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

func (f *fakeServiceLocator) Resolve(_ any) error { return nil }

func (f *fakeServiceLocator) Invoke(_ any) error { return nil }

func Test_ServiceManager_Package_HappyPath(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	target := &fakeServiceTargetStub{
		packageResult: &ServicePackageResult{},
	}
	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework, target: target}
	resMgr := &fakeResourceManager{
		targetResource: environment.NewTargetResource("sub", "rg", "res", "type"),
	}

	sm := makeServiceManager(env, locator, resMgr)

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress()
	defer progress.Done()

	result, err := sm.Package(t.Context(), svcConfig, nil, progress, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_ServiceManager_Package_FrameworkError(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	// No framework registered → error
	locator := &fakeServiceLocator{}
	resMgr := &fakeResourceManager{}
	sm := makeServiceManager(env, locator, resMgr)

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress()
	defer progress.Done()

	_, err := sm.Package(t.Context(), svcConfig, nil, progress, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting framework service")
}

func Test_ServiceManager_Package_TargetError(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	framework := NewNoOpProject(env)
	// No target registered → error from IoC
	locator := &fakeServiceLocator{framework: framework}
	resMgr := &fakeResourceManager{}
	sm := makeServiceManager(env, locator, resMgr)

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress()
	defer progress.Done()

	_, err := sm.Package(t.Context(), svcConfig, nil, progress, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting service target")
}

func Test_ServiceManager_Build_HappyPath(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})

	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework}
	resMgr := &fakeResourceManager{}

	sm := makeServiceManager(env, locator, resMgr)
	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	progress := newDrainedProgress()
	defer progress.Done()

	result, err := sm.Build(t.Context(), svcConfig, nil, progress)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_ServiceManager_GetFrameworkService_ImageOverride(t *testing.T) {
	// When Language==None and Image is set, it should override to Docker
	env := environment.NewWithValues("test", map[string]string{})
	framework := NewNoOpProject(env)
	locator := &fakeServiceLocator{framework: framework}
	sm := makeServiceManager(env, locator, &fakeResourceManager{})

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageNone, ContainerAppTarget, t.TempDir())
	svcConfig.Image = osutil.NewExpandableString("myregistry.azurecr.io/myapp:latest")

	fs, err := sm.GetFrameworkService(t.Context(), svcConfig)
	require.NoError(t, err)
	require.NotNil(t, fs)
	// After the call, Language should be overridden to Docker
	assert.Equal(t, ServiceLanguageDocker, svcConfig.Language)
}

func Test_ServiceManager_GetFrameworkService_CompositeWrap(t *testing.T) {
	// When host.RequiresContainer() && language != Docker/None → wrap with composite
	env := environment.NewWithValues("test", map[string]string{})
	framework := NewNoOpProject(env)
	composite := &fakeCompositeFramework{}
	locator := &fakeServiceLocator{
		framework: framework,
		composite: composite,
	}
	sm := makeServiceManager(env, locator, &fakeResourceManager{})

	// ContainerAppTarget.RequiresContainer() == true, language Python != Docker
	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguagePython, ContainerAppTarget, t.TempDir())

	fs, err := sm.GetFrameworkService(t.Context(), svcConfig)
	require.NoError(t, err)
	require.NotNil(t, fs)
	// Should have wrapped with composite and set source
	assert.Equal(t, framework, composite.source)
}

func Test_ServiceManager_GetFrameworkService_ResolveError(t *testing.T) {
	// When language resolution fails with non-ErrResolveInstance error
	env := environment.NewWithValues("test", map[string]string{})
	locator := &fakeServiceLocator{} // no framework → UnsupportedServiceHostError
	sm := makeServiceManager(env, locator, &fakeResourceManager{})

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	_, err := sm.GetFrameworkService(t.Context(), svcConfig)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve language")
}

func Test_ServiceManager_GetTargetResource_Default(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})
	expected := environment.NewTargetResource("sub", "rg", "myres", "type")
	resMgr := &fakeResourceManager{targetResource: expected}
	sm := makeServiceManager(env, &fakeServiceLocator{}, resMgr)

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())
	target := &fakeServiceTargetStub{}

	result, err := sm.GetTargetResource(t.Context(), svcConfig, target)
	require.NoError(t, err)
	assert.Equal(t, expected, result)
}

func Test_ServiceManager_GetTargetResource_DotNetContainerApp_WithAspire(t *testing.T) {
	// DotNetContainerAppTarget with DotNetContainerApp set (Aspire path)
	// Should use resourceGroupName from config + containerEnvName from env
	env := environment.NewWithValues("test", map[string]string{
		"SERVICE_WEB_CONTAINER_ENVIRONMENT_NAME": "myenv",
	})

	resMgr := &fakeResourceManager{resourceGroupName: "my-rg"}
	sm := makeServiceManager(env, &fakeServiceLocator{}, resMgr)

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageCsharp, DotNetContainerAppTarget, t.TempDir())
	svcConfig.DotNetContainerApp = &DotNetContainerAppOptions{}

	result, err := sm.GetTargetResource(t.Context(), svcConfig, &fakeServiceTargetStub{})
	require.NoError(t, err)
	assert.Equal(t, "myenv", result.ResourceName())
	assert.Equal(t, "my-rg", result.ResourceGroupName())
}

func Test_ServiceManager_GetTargetResource_DotNetContainerApp_NoEnvName(t *testing.T) {
	// DotNetContainerAppTarget without DotNetContainerApp and no container env name → error
	env := environment.NewWithValues("test", map[string]string{})
	resMgr := &fakeResourceManager{resourceGroupName: "rg"}
	sm := makeServiceManager(env, &fakeServiceLocator{}, resMgr)

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageCsharp, DotNetContainerAppTarget, t.TempDir())

	_, err := sm.GetTargetResource(t.Context(), svcConfig, &fakeServiceTargetStub{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not determine container app environment")
}

func Test_ServiceManager_GetTargetResource_DotNetContainerApp_FromGlobalEnv(t *testing.T) {
	// DotNetContainerAppTarget using AZURE_CONTAINER_APPS_ENVIRONMENT_ID (global fallback)
	env := environment.NewWithValues("test", map[string]string{
		"AZURE_CONTAINER_APPS_ENVIRONMENT_ID": "/subscriptions/sub/resourceGroups/rg/" +
			"providers/Microsoft.App/managedEnvironments/myenv",
	})
	resMgr := &fakeResourceManager{resourceGroupName: "rg"}
	sm := makeServiceManager(env, &fakeServiceLocator{}, resMgr)

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageCsharp, DotNetContainerAppTarget, t.TempDir())

	result, err := sm.GetTargetResource(t.Context(), svcConfig, &fakeServiceTargetStub{})
	require.NoError(t, err)
	// Should extract last segment from the env ID
	assert.Equal(t, "myenv", result.ResourceName())
}

func Test_ServiceManager_GetTargetResource_TargetResourceResolver(t *testing.T) {
	// Target that implements TargetResourceResolver
	env := environment.NewWithValues("test", map[string]string{})
	expected := environment.NewTargetResource("sub2", "rg2", "custom", "type2")

	resolver := &fakeTargetResolver{resource: expected}
	resMgr := &fakeResourceManager{}
	sm := makeServiceManager(env, &fakeServiceLocator{}, resMgr)

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageJavaScript, AppServiceTarget, t.TempDir())

	result, err := sm.GetTargetResource(t.Context(), svcConfig, resolver)
	require.NoError(t, err)
	assert.Equal(t, expected, result)
}

// fakeTargetResolver implements both ServiceTarget and TargetResourceResolver.
type fakeTargetResolver struct {
	fakeServiceTargetStub
	resource *environment.TargetResource
	err      error
}

func Test_ServiceManager_GetTargetResource_DotNetContainerApp_RgOverride(t *testing.T) {
	// Test with ResourceGroupName set on ServiceConfig
	env := environment.NewWithValues("test", map[string]string{
		"SERVICE_WEB_CONTAINER_ENVIRONMENT_NAME": "myenv",
	})
	resMgr := &fakeResourceManager{resourceGroupName: "override-rg"}
	sm := makeServiceManager(env, &fakeServiceLocator{}, resMgr)

	svcConfig := makeSvcConfigWithDispatcher("web", ServiceLanguageCsharp, DotNetContainerAppTarget, t.TempDir())
	svcConfig.ResourceGroupName = osutil.NewExpandableString("my-custom-rg")

	result, err := sm.GetTargetResource(t.Context(), svcConfig, &fakeServiceTargetStub{})
	require.NoError(t, err)
	assert.Equal(t, "myenv", result.ResourceName())
}

// ---------- fakeSimpleServiceLocator for serviceManager tests ----------
type fakeSimpleServiceLocator struct {
	framework FrameworkService
	target    ServiceTarget
}

func newFakeLocator(framework FrameworkService, target ServiceTarget) *fakeSimpleServiceLocator {
	return &fakeSimpleServiceLocator{framework: framework, target: target}
}

// fakeResourceManager implements ResourceManager for testing
type fakeResourceManager struct {
	resourceGroupName string
	targetResource    *environment.TargetResource
	err               error
}

// fakeCompositeFramework implements CompositeFrameworkService for testing
type fakeCompositeFramework struct {
	noOpProject
	source FrameworkService
}

func (f *fakeCompositeFramework) SetSource(source FrameworkService) {
	f.source = source
}

func Test_GetFrameworkService(t *testing.T) {
	t.Run("LanguageNone_WithImage_SetsDocker", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Container.MustRegisterNamedTransient(string(ServiceLanguageDocker), func() FrameworkService {
			return &noOpProject{}
		})

		env := environment.NewWithValues("test", map[string]string{})
		sm := &serviceManager{
			env:            env,
			serviceLocator: mockContext.Container,
			initialized:    map[*ServiceConfig]map[any]bool{},
		}

		svcConfig := &ServiceConfig{
			Name:     "test-svc",
			Language: ServiceLanguageNone,
			Image:    osutil.NewExpandableString("myimage:latest"),
			Host:     AppServiceTarget,
			Project:  &ProjectConfig{Path: t.TempDir()},
		}

		result, err := sm.GetFrameworkService(t.Context(), svcConfig)
		require.NoError(t, err)
		require.NotNil(t, result)
		// Language should have been changed to Docker
		assert.Equal(t, ServiceLanguageDocker, svcConfig.Language)
	})

	t.Run("ResolveSuccess_SimpleLanguage", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Container.MustRegisterNamedTransient(string(ServiceLanguagePython), func() FrameworkService {
			return &noOpProject{}
		})

		env := environment.NewWithValues("test", map[string]string{})
		sm := &serviceManager{
			env:            env,
			serviceLocator: mockContext.Container,
			initialized:    map[*ServiceConfig]map[any]bool{},
		}

		svcConfig := &ServiceConfig{
			Name:     "api",
			Language: ServiceLanguagePython,
			Host:     AppServiceTarget,
			Project:  &ProjectConfig{Path: t.TempDir()},
		}

		result, err := sm.GetFrameworkService(t.Context(), svcConfig)
		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("ResolveFailure_UnsupportedLanguage", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		// Don't register anything for "unknown-lang"

		env := environment.NewWithValues("test", map[string]string{})
		sm := &serviceManager{
			env:            env,
			serviceLocator: mockContext.Container,
			initialized:    map[*ServiceConfig]map[any]bool{},
		}

		svcConfig := &ServiceConfig{
			Name:     "svc",
			Language: ServiceLanguageKind("unknown-lang"),
			Host:     AppServiceTarget,
			Project:  &ProjectConfig{Path: t.TempDir()},
		}

		_, err := sm.GetFrameworkService(t.Context(), svcConfig)
		require.Error(t, err)
	})

	t.Run("RequiresContainer_WrapsWithComposite", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Container.MustRegisterNamedTransient(string(ServiceLanguagePython), func() FrameworkService {
			return &noOpProject{}
		})
		mockContext.Container.MustRegisterNamedTransient(string(ServiceLanguageDocker), func() CompositeFrameworkService {
			return &fakeCompositeFramework{}
		})

		env := environment.NewWithValues("test", map[string]string{})
		sm := &serviceManager{
			env:            env,
			serviceLocator: mockContext.Container,
			initialized:    map[*ServiceConfig]map[any]bool{},
		}

		svcConfig := &ServiceConfig{
			Name:     "api",
			Language: ServiceLanguagePython,
			Host:     ContainerAppTarget, // RequiresContainer = true
			Project:  &ProjectConfig{Path: t.TempDir()},
		}

		result, err := sm.GetFrameworkService(t.Context(), svcConfig)
		require.NoError(t, err)
		require.NotNil(t, result)
	})
}

func Test_GetFrameworkService_DockerLanguage(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Container.MustRegisterNamedTransient(string(ServiceLanguageDocker), func() FrameworkService {
		return &noOpProject{}
	})

	env := environment.NewWithValues("test", map[string]string{})
	afm := alpha.NewFeaturesManagerWithConfig(nil)
	sm := NewServiceManager(env, nil, mockContext.Container, ServiceOperationCache{}, afm)

	svcConfig := &ServiceConfig{
		Name:     "docker-svc",
		Language: ServiceLanguageDocker,
		Host:     ContainerAppTarget,
		Project:  &ProjectConfig{Path: t.TempDir()},
	}

	result, err := sm.GetFrameworkService(t.Context(), svcConfig)
	require.NoError(t, err)
	require.NotNil(t, result)
}
