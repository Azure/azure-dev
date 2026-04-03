// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package project

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResourceManager_Cov3 implements ResourceManager for testing
type fakeResourceManager_Cov3 struct {
	resourceGroupName string
	targetResource    *environment.TargetResource
	err               error
}

func (f *fakeResourceManager_Cov3) GetResourceGroupName(
	_ context.Context, _ string, _ osutil.ExpandableString,
) (string, error) {
	return f.resourceGroupName, f.err
}

func (f *fakeResourceManager_Cov3) GetTargetResource(
	_ context.Context, _ string, _ *ServiceConfig,
) (*environment.TargetResource, error) {
	return f.targetResource, f.err
}

func (f *fakeResourceManager_Cov3) GetServiceResources(
	_ context.Context, _ string, _ string, _ *ServiceConfig,
) ([]*azapi.ResourceExtended, error) {
	return nil, f.err
}

func (f *fakeResourceManager_Cov3) GetServiceResource(
	_ context.Context, _ string, _ string, _ *ServiceConfig, _ string,
) (*azapi.ResourceExtended, error) {
	return nil, f.err
}

// fakeCompositeFramework_Cov3 implements CompositeFrameworkService for testing
type fakeCompositeFramework_Cov3 struct {
	noOpProject
	source FrameworkService
}

func (f *fakeCompositeFramework_Cov3) SetSource(source FrameworkService) {
	f.source = source
}

func Test_GetFrameworkService_Coverage3(t *testing.T) {
	t.Run("LanguageNone_WithImage_SetsDocker", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
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

		result, err := sm.GetFrameworkService(context.Background(), svcConfig)
		require.NoError(t, err)
		require.NotNil(t, result)
		// Language should have been changed to Docker
		assert.Equal(t, ServiceLanguageDocker, svcConfig.Language)
	})

	t.Run("ResolveSuccess_SimpleLanguage", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
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

		result, err := sm.GetFrameworkService(context.Background(), svcConfig)
		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("ResolveFailure_UnsupportedLanguage", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
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

		_, err := sm.GetFrameworkService(context.Background(), svcConfig)
		require.Error(t, err)
	})

	t.Run("RequiresContainer_WrapsWithComposite", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Container.MustRegisterNamedTransient(string(ServiceLanguagePython), func() FrameworkService {
			return &noOpProject{}
		})
		mockContext.Container.MustRegisterNamedTransient(string(ServiceLanguageDocker), func() CompositeFrameworkService {
			return &fakeCompositeFramework_Cov3{}
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

		result, err := sm.GetFrameworkService(context.Background(), svcConfig)
		require.NoError(t, err)
		require.NotNil(t, result)
	})
}

func Test_GetTargetResource_Coverage3(t *testing.T) {
	t.Run("DotNetContainerApp_WithServiceProperty", func(t *testing.T) {
		envValues := map[string]string{
			"SERVICE_MYAPP_CONTAINER_ENVIRONMENT_NAME": "my-env",
		}
		env := environment.NewWithValues("test", envValues)

		fakeRM := &fakeResourceManager_Cov3{
			resourceGroupName: "my-rg",
		}
		sm := &serviceManager{
			env:             env,
			resourceManager: fakeRM,
			initialized:     map[*ServiceConfig]map[any]bool{},
		}

		svcConfig := &ServiceConfig{
			Name:    "myapp",
			Host:    DotNetContainerAppTarget,
			Project: &ProjectConfig{},
		}

		result, err := sm.GetTargetResource(context.Background(), svcConfig, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "my-env", result.ResourceName())
		assert.Equal(t, "my-rg", result.ResourceGroupName())
	})

	t.Run("DotNetContainerApp_FallbackToEnvVar", func(t *testing.T) {
		envValues := map[string]string{
			"AZURE_CONTAINER_APPS_ENVIRONMENT_ID": "/subscriptions/sub/resourceGroups/rg" +
				"/providers/Microsoft.App/managedEnvironments/my-fallback-env",
		}
		env := environment.NewWithValues("test", envValues)

		fakeRM := &fakeResourceManager_Cov3{
			resourceGroupName: "fallback-rg",
		}
		sm := &serviceManager{
			env:             env,
			resourceManager: fakeRM,
			initialized:     map[*ServiceConfig]map[any]bool{},
		}

		svcConfig := &ServiceConfig{
			Name:    "svc",
			Host:    DotNetContainerAppTarget,
			Project: &ProjectConfig{},
		}

		result, err := sm.GetTargetResource(context.Background(), svcConfig, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		// Should extract last segment from ID
		assert.Equal(t, "my-fallback-env", result.ResourceName())
	})

	t.Run("DotNetContainerApp_MissingEnv_Error", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{})

		sm := &serviceManager{
			env:         env,
			initialized: map[*ServiceConfig]map[any]bool{},
		}

		svcConfig := &ServiceConfig{
			Name:    "svc",
			Host:    DotNetContainerAppTarget,
			Project: &ProjectConfig{},
		}

		_, err := sm.GetTargetResource(context.Background(), svcConfig, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not determine container app environment")
	})

	t.Run("DefaultFallback_ToResourceManager", func(t *testing.T) {
		expected := environment.NewTargetResource("sub-id", "rg-name", "res-name", "Microsoft.Web/sites")
		fakeRM := &fakeResourceManager_Cov3{
			targetResource: expected,
		}
		env := environment.NewWithValues("test", map[string]string{})
		sm := &serviceManager{
			env:             env,
			resourceManager: fakeRM,
			initialized:     map[*ServiceConfig]map[any]bool{},
		}

		svcConfig := &ServiceConfig{
			Name:    "web",
			Host:    AppServiceTarget,
			Project: &ProjectConfig{},
		}

		result, err := sm.GetTargetResource(context.Background(), svcConfig, nil)
		require.NoError(t, err)
		assert.Equal(t, expected, result)
	})

	t.Run("DotNetContainerApp_WithResourceGroupOverride", func(t *testing.T) {
		envValues := map[string]string{
			"SERVICE_SVC_CONTAINER_ENVIRONMENT_NAME": "env-name",
		}
		env := environment.NewWithValues("test", envValues)

		fakeRM := &fakeResourceManager_Cov3{
			resourceGroupName: "override-rg",
		}
		sm := &serviceManager{
			env:             env,
			resourceManager: fakeRM,
			initialized:     map[*ServiceConfig]map[any]bool{},
		}

		svcConfig := &ServiceConfig{
			Name:              "svc",
			Host:              DotNetContainerAppTarget,
			ResourceGroupName: osutil.NewExpandableString("custom-rg"),
			Project:           &ProjectConfig{},
		}

		result, err := sm.GetTargetResource(context.Background(), svcConfig, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "env-name", result.ResourceName())
	})

	t.Run("DotNetContainerApp_AspireSkipsMissingEnv", func(t *testing.T) {
		// Aspire services (DotNetContainerApp != nil) don't need AZURE_CONTAINER_APPS_ENVIRONMENT_ID
		env := environment.NewWithValues("test", map[string]string{})

		fakeRM := &fakeResourceManager_Cov3{
			resourceGroupName: "aspire-rg",
		}
		sm := &serviceManager{
			env:             env,
			resourceManager: fakeRM,
			initialized:     map[*ServiceConfig]map[any]bool{},
		}

		svcConfig := &ServiceConfig{
			Name:               "svc",
			Host:               DotNetContainerAppTarget,
			DotNetContainerApp: &DotNetContainerAppOptions{},
			Project:            &ProjectConfig{},
		}

		result, err := sm.GetTargetResource(context.Background(), svcConfig, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		// containerEnvName is "" but no error since DotNetContainerApp != nil
		assert.Equal(t, "", result.ResourceName())
	})
}

func Test_GetFrameworkService_DockerLanguage_Coverage3(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
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

	result, err := sm.GetFrameworkService(context.Background(), svcConfig)
	require.NoError(t, err)
	require.NotNil(t, result)
}
