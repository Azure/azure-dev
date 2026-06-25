// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/node"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func TestServiceConfigAddHandler(t *testing.T) {
	ctx := t.Context()
	service := getServiceConfig()
	handlerCalled := false

	handler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		handlerCalled = true
		return nil
	}

	err := service.AddHandler(ctx, ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{
		Service:        service,
		ServiceContext: NewServiceContext(),
	})
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestServiceConfigRemoveHandler(t *testing.T) {
	service := getServiceConfig()
	handler1Called := false

	handler1 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		handler1Called = true
		return nil
	}

	// Register handler with a cancellable context
	ctx, cancel := context.WithCancel(t.Context())
	err := service.AddHandler(ctx, ServiceEventDeploy, handler1)
	require.Nil(t, err)

	// Cancel context to trigger removal
	cancel()

	require.Eventually(t, func() bool {
		// Handler should not be called after context cancellation
		handler1Called = false
		err = service.RaiseEvent(t.Context(), ServiceEventDeploy, ServiceLifecycleEventArgs{
			Service:        service,
			ServiceContext: NewServiceContext(),
		})
		return err == nil && !handler1Called
	}, time.Second, 10*time.Millisecond, "Handler should not fire after context cancellation")
}

func TestServiceConfigWithMultipleEventHandlers(t *testing.T) {
	ctx := t.Context()
	service := getServiceConfig()
	handlerCalled1 := false
	handlerCalled2 := false

	handler1 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		require.Equal(t, service.Project, args.Project)
		require.Equal(t, service, args.Service)
		handlerCalled1 = true
		return nil
	}

	handler2 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		require.Equal(t, service.Project, args.Project)
		require.Equal(t, service, args.Service)
		handlerCalled2 = true
		return nil
	}

	err := service.AddHandler(ctx, ServiceEventDeploy, handler1)
	require.Nil(t, err)
	err = service.AddHandler(ctx, ServiceEventDeploy, handler2)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{
		Project:        service.Project,
		Service:        service,
		ServiceContext: NewServiceContext(),
	})
	require.Nil(t, err)
	require.True(t, handlerCalled1)
	require.True(t, handlerCalled2)
}

func TestServiceConfigWithMultipleEvents(t *testing.T) {
	ctx := t.Context()
	service := getServiceConfig()

	provisionHandlerCalled := false
	deployHandlerCalled := false

	provisionHandler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		provisionHandlerCalled = true
		return nil
	}

	deployHandler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		deployHandlerCalled = true
		return nil
	}

	err := service.AddHandler(ctx, ServiceEventPackage, provisionHandler)
	require.Nil(t, err)
	err = service.AddHandler(ctx, ServiceEventDeploy, deployHandler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventPackage, ServiceLifecycleEventArgs{
		Service:        service,
		ServiceContext: NewServiceContext(),
	})
	require.Nil(t, err)

	require.True(t, provisionHandlerCalled)
	require.False(t, deployHandlerCalled)
}

func TestServiceConfigWithEventHandlerErrors(t *testing.T) {
	ctx := t.Context()
	service := getServiceConfig()

	handler1 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		return errors.New("sample error 1")
	}

	handler2 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		return errors.New("sample error 2")
	}

	err := service.AddHandler(ctx, ServiceEventPackage, handler1)
	require.Nil(t, err)
	err = service.AddHandler(ctx, ServiceEventPackage, handler2)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventPackage, ServiceLifecycleEventArgs{
		Service:        service,
		ServiceContext: NewServiceContext(),
	})
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "sample error 1")
	require.Contains(t, err.Error(), "sample error 2")
}

func getServiceConfig() *ServiceConfig {
	const testProj = `
name: test-proj
metadata:
  template: test-proj-template
resourceGroup: rg-test
services:
  api:
    project: src/api
    language: js
    host: containerapp
`

	mockContext := mocks.NewMockContext(context.Background())
	projectConfig, _ := Parse(*mockContext.Context, testProj)

	return projectConfig.Services["api"]
}

func TestServiceConfigRaiseEventWithoutArgs(t *testing.T) {
	ctx := t.Context()
	service := getServiceConfig()
	handlerCalled := false

	handler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		handlerCalled = true
		require.Empty(t, args.Args)
		return nil
	}

	err := service.AddHandler(ctx, ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{
		Service:        service,
		ServiceContext: NewServiceContext(),
	})
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestServiceConfigRaiseEventWithArgs(t *testing.T) {
	ctx := t.Context()
	service := getServiceConfig()
	handlerCalled := false
	eventArgs := ServiceLifecycleEventArgs{
		Service:        service,
		ServiceContext: NewServiceContext(),
		Args:           map[string]any{"foo": "bar"},
	}

	handler := func(ctx context.Context, eventArgs ServiceLifecycleEventArgs) error {
		handlerCalled = true
		require.Equal(t, eventArgs.Args["foo"], "bar")
		return nil
	}

	err := service.AddHandler(ctx, ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, eventArgs)
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestServiceConfigEventHandlerReceivesServiceContext(t *testing.T) {
	ctx := t.Context()
	service := getServiceConfig()
	handlerCalled := false

	// Create a ServiceContext with some test artifacts
	serviceContext := NewServiceContext()

	// Add test artifacts to different lifecycle stages
	restoreArtifact := &Artifact{
		Kind:         ArtifactKindDirectory,
		LocationKind: LocationKindLocal,
		Location:     "/path/to/restored/dependencies",
		Metadata:     map[string]string{"stage": "restore"},
	}

	buildArtifact := &Artifact{
		Kind:         ArtifactKindDirectory,
		LocationKind: LocationKindLocal,
		Location:     "/path/to/build/output",
		Metadata:     map[string]string{"stage": "build"},
	}

	packageArtifact := &Artifact{
		Kind:         ArtifactKindArchive,
		LocationKind: LocationKindLocal,
		Location:     "/path/to/package/app.zip",
		Metadata:     map[string]string{"stage": "package"},
	}

	err := serviceContext.Restore.Add(restoreArtifact)
	require.Nil(t, err)
	err = serviceContext.Build.Add(buildArtifact)
	require.Nil(t, err)
	err = serviceContext.Package.Add(packageArtifact)
	require.Nil(t, err)

	handler := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		handlerCalled = true

		// Verify ServiceContext is available
		require.NotNil(t, args.ServiceContext)

		// Verify all artifacts are accessible in the handler
		require.Len(t, args.ServiceContext.Restore, 1)
		require.Len(t, args.ServiceContext.Build, 1)
		require.Len(t, args.ServiceContext.Package, 1)

		// Verify artifact details
		restoreArtifacts := args.ServiceContext.Restore
		restoreArt, found := restoreArtifacts.FindFirst()
		require.True(t, found)
		require.Equal(t, "/path/to/restored/dependencies", restoreArt.Location)
		require.Equal(t, "restore", restoreArt.Metadata["stage"])

		buildArtifacts := args.ServiceContext.Build
		buildArt, found := buildArtifacts.FindFirst()
		require.True(t, found)
		require.Equal(t, "/path/to/build/output", buildArt.Location)
		require.Equal(t, "build", buildArt.Metadata["stage"])

		packageArtifacts := args.ServiceContext.Package
		packageArt, found := packageArtifacts.FindFirst()
		require.True(t, found)
		require.Equal(t, "/path/to/package/app.zip", packageArt.Location)
		require.Equal(t, "package", packageArt.Metadata["stage"])

		return nil
	}

	err = service.AddHandler(ctx, ServiceEventDeploy, handler)
	require.Nil(t, err)

	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{
		Project:        service.Project,
		Service:        service,
		ServiceContext: serviceContext,
	})
	require.Nil(t, err)
	require.True(t, handlerCalled)
}

func TestServiceConfigConditionEvaluation(t *testing.T) {
	tests := []struct {
		name          string
		condition     string
		envVars       map[string]string
		expectEnabled bool
	}{
		// No condition - should be enabled by default
		{
			name:          "NoCondition",
			condition:     "",
			envVars:       map[string]string{},
			expectEnabled: true,
		},
		// Truthy values
		{
			name:          "ConditionTrue",
			condition:     "true",
			envVars:       map[string]string{},
			expectEnabled: true,
		},
		{
			name:          "ConditionTRUE",
			condition:     "TRUE",
			envVars:       map[string]string{},
			expectEnabled: true,
		},
		{
			name:          "ConditionTrue_MixedCase",
			condition:     "True",
			envVars:       map[string]string{},
			expectEnabled: true,
		},
		{
			name:          "ConditionYes",
			condition:     "yes",
			envVars:       map[string]string{},
			expectEnabled: true,
		},
		{
			name:          "ConditionYES",
			condition:     "YES",
			envVars:       map[string]string{},
			expectEnabled: true,
		},
		{
			name:          "ConditionYes_MixedCase",
			condition:     "Yes",
			envVars:       map[string]string{},
			expectEnabled: true,
		},
		{
			name:          "ConditionOne",
			condition:     "1",
			envVars:       map[string]string{},
			expectEnabled: true,
		},
		// Falsy values
		{
			name:          "ConditionFalse",
			condition:     "false",
			envVars:       map[string]string{},
			expectEnabled: false,
		},
		{
			name:          "ConditionFALSE",
			condition:     "FALSE",
			envVars:       map[string]string{},
			expectEnabled: false,
		},
		{
			name:          "ConditionNo",
			condition:     "no",
			envVars:       map[string]string{},
			expectEnabled: false,
		},
		{
			name:          "ConditionZero",
			condition:     "0",
			envVars:       map[string]string{},
			expectEnabled: false,
		},
		{
			name:          "ConditionRandomString",
			condition:     "random",
			envVars:       map[string]string{},
			expectEnabled: false,
		},
		{
			name:          "ConditionEmptyString",
			condition:     "",
			envVars:       map[string]string{},
			expectEnabled: true, // No condition means enabled
		},
		// Environment variable expansion
		{
			name:          "ConditionFromEnvVarTrue",
			condition:     "${DEPLOY_SERVICE}",
			envVars:       map[string]string{"DEPLOY_SERVICE": "true"},
			expectEnabled: true,
		},
		{
			name:          "ConditionFromEnvVarFalse",
			condition:     "${DEPLOY_SERVICE}",
			envVars:       map[string]string{"DEPLOY_SERVICE": "false"},
			expectEnabled: false,
		},
		{
			name:          "ConditionFromEnvVarOne",
			condition:     "${DEPLOY_SERVICE}",
			envVars:       map[string]string{"DEPLOY_SERVICE": "1"},
			expectEnabled: true,
		},
		{
			name:          "ConditionFromEnvVarZero",
			condition:     "${DEPLOY_SERVICE}",
			envVars:       map[string]string{"DEPLOY_SERVICE": "0"},
			expectEnabled: false,
		},
		{
			name:          "ConditionFromMissingEnvVar",
			condition:     "${DEPLOY_SERVICE}",
			envVars:       map[string]string{},
			expectEnabled: false, // Empty string after expansion is falsy
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &ServiceConfig{
				Name:      "test-service",
				Condition: osutil.NewExpandableString(tt.condition),
			}

			getenv := func(key string) string {
				return tt.envVars[key]
			}

			enabled, err := service.IsEnabled(getenv)
			require.NoError(t, err)
			require.Equal(t, tt.expectEnabled, enabled)
		})
	}
}

func TestServiceConfigConditionMalformed(t *testing.T) {
	service := &ServiceConfig{
		Name:      "test-service",
		Condition: osutil.NewExpandableString("${UNCLOSED"),
	}

	getenv := func(key string) string {
		return ""
	}

	_, err := service.IsEnabled(getenv)
	require.Error(t, err)
	require.Contains(t, err.Error(), "malformed deployment condition template")
}

func TestServiceConfigConditionYamlParsing(t *testing.T) {
	const testProj = `
name: test-proj
services:
  conditionalService:
    project: src/api
    language: js
    host: containerapp
    condition: ${DEPLOY_SERVICE}
  unconditionalService:
    project: src/web
    language: js
    host: appservice
`

	mockContext := mocks.NewMockContext(t.Context())
	projectConfig, err := Parse(*mockContext.Context, testProj)
	require.Nil(t, err)
	require.NotNil(t, projectConfig)

	conditionalService := projectConfig.Services["conditionalService"]
	require.NotNil(t, conditionalService)
	require.False(t, conditionalService.Condition.Empty())

	unconditionalService := projectConfig.Services["unconditionalService"]
	require.NotNil(t, unconditionalService)
	require.True(t, unconditionalService.Condition.Empty())

	// Test with environment variable set to true
	getenvTrue := func(key string) string {
		if key == "DEPLOY_SERVICE" {
			return "true"
		}
		return ""
	}
	enabled, err := conditionalService.IsEnabled(getenvTrue)
	require.NoError(t, err)
	require.True(t, enabled)
	enabled, err = unconditionalService.IsEnabled(getenvTrue)
	require.NoError(t, err)
	require.True(t, enabled)

	// Test with environment variable set to false
	getenvFalse := func(key string) string {
		if key == "DEPLOY_SERVICE" {
			return "false"
		}
		return ""
	}
	enabled, err = conditionalService.IsEnabled(getenvFalse)
	require.NoError(t, err)
	require.False(t, enabled)
	enabled, err = unconditionalService.IsEnabled(getenvFalse)
	require.NoError(t, err)
	require.True(t, enabled)
}

func createTestServiceConfig(path string, host ServiceTargetKind, language ServiceLanguageKind) *ServiceConfig {
	return &ServiceConfig{
		Name:         "api",
		Host:         host,
		Language:     language,
		RelativePath: filepath.Join(path),
		Project: &ProjectConfig{
			Name:            "Test-App",
			Path:            ".",
			EventDispatcher: ext.NewEventDispatcher[ProjectLifecycleEventArgs](),
		},
		EventDispatcher: ext.NewEventDispatcher[ServiceLifecycleEventArgs](),
	}
}

func Test_IsConditionTrue(t *testing.T) {
	tests := []struct {
		value  string
		expect bool
	}{
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"yes", true},
		{"YES", true},
		{"Yes", true},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"no", false},
		{"", false},
		{"random", false},
		{"2", false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			assert.Equal(t, tt.expect, isConditionTrue(tt.value))
		})
	}
}

func Test_ServiceConfig_IsEnabled(t *testing.T) {
	t.Run("no condition always enabled", func(t *testing.T) {
		sc := &ServiceConfig{}
		enabled, err := sc.IsEnabled(func(string) string { return "" })
		require.NoError(t, err)
		assert.True(t, enabled)
	})

	t.Run("condition evaluates to true", func(t *testing.T) {
		sc := &ServiceConfig{
			Condition: osutil.NewExpandableString("${DEPLOY_WEB}"),
		}
		enabled, err := sc.IsEnabled(func(key string) string {
			if key == "DEPLOY_WEB" {
				return "true"
			}
			return ""
		})
		require.NoError(t, err)
		assert.True(t, enabled)
	})

	t.Run("condition evaluates to false", func(t *testing.T) {
		sc := &ServiceConfig{
			Condition: osutil.NewExpandableString("${DEPLOY_WEB}"),
		}
		enabled, err := sc.IsEnabled(func(key string) string {
			if key == "DEPLOY_WEB" {
				return "false"
			}
			return ""
		})
		require.NoError(t, err)
		assert.False(t, enabled)
	})

	t.Run("condition with literal true", func(t *testing.T) {
		sc := &ServiceConfig{
			Condition: osutil.NewExpandableString("1"),
		}
		enabled, err := sc.IsEnabled(func(string) string { return "" })
		require.NoError(t, err)
		assert.True(t, enabled)
	})
}

// Tests for ImportManager.GenerateAllInfrastructure additional branches
func Test_GenerateAllInfrastructure(t *testing.T) {
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

func Test_nodeProject_RequiredExternalTools(t *testing.T) {
	cli := node.NewCli(exec.NewCommandRunner(nil))
	p := NewNodeProject(cli, environment.NewWithValues("test", nil), exec.NewCommandRunner(nil))

	// Provide a ServiceConfig with a valid Project to avoid nil pointer in Path()
	svcConfig := &ServiceConfig{
		Project:      &ProjectConfig{Path: t.TempDir()},
		RelativePath: ".",
	}
	tools := p.RequiredExternalTools(t.Context(), svcConfig)
	require.Len(t, tools, 1)
}

func (f *fakeServiceTargetStub) Initialize(_ context.Context, _ *ServiceConfig) error {
	return nil
}

func (f *fakeServiceTargetStub) RequiredExternalTools(_ context.Context, _ *ServiceConfig) []tools.ExternalTool {
	return nil
}

func (f *fakeServiceTargetStub) Endpoints(
	_ context.Context, _ *ServiceConfig, _ *environment.TargetResource,
) ([]string, error) {
	return f.endpoints, f.endpointsErr
}

// makeSvcConfigWithDispatcher creates a minimal ServiceConfig with an EventDispatcher.
func makeSvcConfigWithDispatcher(
	name string, lang ServiceLanguageKind, host ServiceTargetKind, projPath string,
) *ServiceConfig {
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

func (f *fakeTargetResolver) ResolveTargetResource(
	_ context.Context, _ string, _ *ServiceConfig,
	_ func() (*environment.TargetResource, error),
) (*environment.TargetResource, error) {
	return f.resource, f.err
}

func (f *fakeFrameworkForTools) RequiredExternalTools(_ context.Context, _ *ServiceConfig) []tools.ExternalTool {
	return f.tools
}

// ---------- ProjectInfrastructure additional paths ----------
func Test_ProjectInfrastructure(t *testing.T) {
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

// ---------- ServiceStable: DotNet canImport=true paths ----------
func Test_ServiceStable_DotNet_Errors(t *testing.T) {
	t.Run("NonContainerAppHost_Error", func(t *testing.T) {
		tmpDir := t.TempDir()
		importer := &DotNetImporter{
			hostCheck: map[string]hostCheckResult{
				tmpDir: {is: true},
			},
		}
		im := NewImportManager(importer)

		pc := &ProjectConfig{
			Name: "test",
			Path: tmpDir,
			Services: map[string]*ServiceConfig{
				"api": {
					Name:         "api",
					Host:         AppServiceTarget, // NOT ContainerAppTarget
					Language:     ServiceLanguageDotNet,
					RelativePath: ".",
					Project:      &ProjectConfig{Path: tmpDir},
				},
			},
		}

		_, err := im.ServiceStable(t.Context(), pc)
		require.Error(t, err)
		assert.ErrorIs(t, err, errAppHostMustTargetContainerApp)
	})

	t.Run("MultipleServices_Error", func(t *testing.T) {
		tmpDir := t.TempDir()
		importer := &DotNetImporter{
			hostCheck: map[string]hostCheckResult{
				tmpDir: {is: true},
			},
		}
		im := NewImportManager(importer)

		pc := &ProjectConfig{
			Name: "test",
			Path: tmpDir,
			Services: map[string]*ServiceConfig{
				"api": {
					Name:         "api",
					Host:         ContainerAppTarget,
					Language:     ServiceLanguageDotNet,
					RelativePath: ".",
					Project:      &ProjectConfig{Path: tmpDir},
				},
				"web": {
					Name:         "web",
					Host:         AppServiceTarget,
					Language:     ServiceLanguageJavaScript,
					RelativePath: "web",
					Project:      &ProjectConfig{Path: tmpDir},
				},
			},
		}

		_, err := im.ServiceStable(t.Context(), pc)
		require.Error(t, err)
		assert.ErrorIs(t, err, errNoMultipleServicesWithAppHost)
	})
}

// ---------- ExternalServiceTarget.RequiredExternalTools: trivial empty ----------
func Test_ExternalServiceTarget_RequiredExternalTools(t *testing.T) {
	est := &ExternalServiceTarget{}
	tools := est.RequiredExternalTools(t.Context(), &ServiceConfig{})
	assert.Empty(t, tools)
}

func (f *fakeResourceManager) GetTargetResource(
	_ context.Context, _ string, _ *ServiceConfig,
) (*environment.TargetResource, error) {
	return f.targetResource, f.err
}

func Test_GetTargetResource(t *testing.T) {
	t.Run("DotNetContainerApp_WithServiceProperty", func(t *testing.T) {
		envValues := map[string]string{
			"SERVICE_MYAPP_CONTAINER_ENVIRONMENT_NAME": "my-env",
		}
		env := environment.NewWithValues("test", envValues)

		fakeRM := &fakeResourceManager{
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

		result, err := sm.GetTargetResource(t.Context(), svcConfig, nil)
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

		fakeRM := &fakeResourceManager{
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

		result, err := sm.GetTargetResource(t.Context(), svcConfig, nil)
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

		_, err := sm.GetTargetResource(t.Context(), svcConfig, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not determine container app environment")
	})

	t.Run("DefaultFallback_ToResourceManager", func(t *testing.T) {
		expected := environment.NewTargetResource("sub-id", "rg-name", "res-name", "Microsoft.Web/sites")
		fakeRM := &fakeResourceManager{
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

		result, err := sm.GetTargetResource(t.Context(), svcConfig, nil)
		require.NoError(t, err)
		assert.Equal(t, expected, result)
	})

	t.Run("DotNetContainerApp_WithResourceGroupOverride", func(t *testing.T) {
		envValues := map[string]string{
			"SERVICE_SVC_CONTAINER_ENVIRONMENT_NAME": "env-name",
		}
		env := environment.NewWithValues("test", envValues)

		fakeRM := &fakeResourceManager{
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

		result, err := sm.GetTargetResource(t.Context(), svcConfig, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "env-name", result.ResourceName())
	})

	t.Run("DotNetContainerApp_AspireSkipsMissingEnv", func(t *testing.T) {
		// Aspire services (DotNetContainerApp != nil) don't need AZURE_CONTAINER_APPS_ENVIRONMENT_ID
		env := environment.NewWithValues("test", map[string]string{})

		fakeRM := &fakeResourceManager{
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

		result, err := sm.GetTargetResource(t.Context(), svcConfig, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
		// containerEnvName is "" but no error since DotNetContainerApp != nil
		assert.Equal(t, "", result.ResourceName())
	})
}

func Test_springAppTarget_RequiredExternalTools(t *testing.T) {
	target := NewSpringAppTarget(environment.NewWithValues("test", nil), nil)
	tools := target.RequiredExternalTools(t.Context(), &ServiceConfig{})
	assert.Empty(t, tools)
}

func Test_springAppTarget_Initialize(t *testing.T) {
	target := NewSpringAppTarget(environment.NewWithValues("test", nil), nil)
	err := target.Initialize(t.Context(), &ServiceConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Azure Spring Apps is no longer supported")
}

func Test_springAppTarget_Package(t *testing.T) {
	target := NewSpringAppTarget(environment.NewWithValues("test", nil), nil)
	result, err := target.Package(t.Context(), &ServiceConfig{}, NewServiceContext(), nil)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Azure Spring Apps is no longer supported")
}

func Test_springAppTarget_Publish(t *testing.T) {
	target := NewSpringAppTarget(environment.NewWithValues("test", nil), nil)
	result, err := target.Publish(t.Context(), &ServiceConfig{}, NewServiceContext(), nil, nil, nil)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Azure Spring Apps is no longer supported")
}

func Test_springAppTarget_Deploy(t *testing.T) {
	target := NewSpringAppTarget(environment.NewWithValues("test", nil), nil)
	result, err := target.Deploy(t.Context(), &ServiceConfig{}, NewServiceContext(), nil, nil)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Azure Spring Apps is no longer supported")
}

func Test_springAppTarget_Endpoints(t *testing.T) {
	target := NewSpringAppTarget(environment.NewWithValues("test", nil), nil)
	endpoints, err := target.Endpoints(t.Context(), &ServiceConfig{}, nil)
	assert.Nil(t, endpoints)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Azure Spring Apps is no longer supported")
}

func Test_aiEndpointTarget_Initialize(t *testing.T) {
	target := &aiEndpointTarget{}
	err := target.Initialize(t.Context(), &ServiceConfig{})
	require.NoError(t, err)
}

func Test_aiEndpointTarget_Package(t *testing.T) {
	target := &aiEndpointTarget{}
	result, err := target.Package(t.Context(), &ServiceConfig{}, NewServiceContext(), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_aiEndpointTarget_Publish(t *testing.T) {
	target := &aiEndpointTarget{}
	result, err := target.Publish(t.Context(), &ServiceConfig{}, NewServiceContext(), nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_dotnetContainerAppTarget_RequiredExternalTools(t *testing.T) {
	cli := dotnet.NewCli(exec.NewCommandRunner(nil))
	target := NewDotNetContainerAppTarget(nil, nil, nil, nil, cli, nil, nil, nil, nil, nil, nil, nil)
	tools := target.RequiredExternalTools(t.Context(), &ServiceConfig{})
	require.Len(t, tools, 1)
	assert.Equal(t, cli, tools[0])
}

func Test_dotnetContainerAppTarget_Initialize(t *testing.T) {
	target := NewDotNetContainerAppTarget(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	err := target.Initialize(t.Context(), &ServiceConfig{})
	require.NoError(t, err)
}
