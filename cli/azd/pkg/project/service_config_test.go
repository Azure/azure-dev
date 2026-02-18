// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestServiceConfigAddHandler(t *testing.T) {
	ctx := context.Background()
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
	ctx := context.Background()
	service := getServiceConfig()
	handler1Called := false
	handler2Called := false

	handler1 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		handler1Called = true
		return nil
	}

	handler2 := func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		handler2Called = true
		return nil
	}

	// Only handler 1 was registered
	err := service.AddHandler(ctx, ServiceEventDeploy, handler1)
	require.Nil(t, err)

	err = service.RemoveHandler(ctx, ServiceEventDeploy, handler1)
	require.Nil(t, err)

	// Handler 2 wasn't registered so should error on remove
	err = service.RemoveHandler(ctx, ServiceEventDeploy, handler2)
	require.NotNil(t, err)

	// No events are registered at the time event was raised
	err = service.RaiseEvent(ctx, ServiceEventDeploy, ServiceLifecycleEventArgs{
		Service:        service,
		ServiceContext: NewServiceContext(),
	})
	require.Nil(t, err)
	require.False(t, handler1Called)
	require.False(t, handler2Called)
}

func TestServiceConfigWithMultipleEventHandlers(t *testing.T) {
	ctx := context.Background()
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
	ctx := context.Background()
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
	ctx := context.Background()
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
	ctx := context.Background()
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
	ctx := context.Background()
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
	ctx := context.Background()
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

	mockContext := mocks.NewMockContext(context.Background())
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

func TestExpandEnv_EmptyEnvironment(t *testing.T) {
	service := &ServiceConfig{
		Name:        "api",
		Environment: nil,
	}

	env, err := service.ExpandEnv(func(key string) string {
		return ""
	})

	require.NoError(t, err)
	require.Nil(t, env)
}

func TestExpandEnv_ExpandsVariables(t *testing.T) {
	service := &ServiceConfig{
		Name: "api",
		Environment: osutil.ExpandableMap{
			"API_URL":  osutil.NewExpandableString("${TEST_URL}"),
			"APP_NAME": osutil.NewExpandableString("${TEST_NAME}"),
			"STATIC":   osutil.NewExpandableString("static-value"),
		},
	}

	lookup := func(key string) string {
		switch key {
		case "TEST_URL":
			return "https://example.com"
		case "TEST_NAME":
			return "my-app"
		default:
			return ""
		}
	}

	env, err := service.ExpandEnv(lookup)

	require.NoError(t, err)
	require.NotNil(t, env)

	// Check that service variables are present in the result
	found := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			found[parts[0]] = parts[1]
		}
	}

	require.Equal(t, "https://example.com", found["API_URL"])
	require.Equal(t, "my-app", found["APP_NAME"])
	require.Equal(t, "static-value", found["STATIC"])
}

func TestExpandEnv_ServiceVarsTakePrecedence(t *testing.T) {
	service := &ServiceConfig{
		Name: "api",
		Environment: osutil.ExpandableMap{
			"PATH": osutil.NewExpandableString("custom-path"),
		},
	}

	env, err := service.ExpandEnv(func(key string) string {
		return ""
	})

	require.NoError(t, err)
	require.NotNil(t, env)

	// Find the last occurrence of PATH - service vars are appended so they take precedence
	var lastPathValue string
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			lastPathValue = strings.TrimPrefix(e, "PATH=")
		}
	}

	require.Equal(t, "custom-path", lastPathValue)
}

func TestExpandEnv_MergesWithOSEnvironment(t *testing.T) {
	service := &ServiceConfig{
		Name: "api",
		Environment: osutil.ExpandableMap{
			"CUSTOM_VAR": osutil.NewExpandableString("custom-value"),
		},
	}

	env, err := service.ExpandEnv(func(key string) string {
		return ""
	})

	require.NoError(t, err)
	require.NotNil(t, env)

	// Should have at least the OS environment variables plus the custom one
	require.Greater(t, len(env), 1)

	// Check custom var is present
	found := false
	for _, e := range env {
		if e == "CUSTOM_VAR=custom-value" {
			found = true
			break
		}
	}
	require.True(t, found, "CUSTOM_VAR should be present in environment")
}

func TestExpandEnv_PropagatesErrors(t *testing.T) {
	service := &ServiceConfig{
		Name: "api",
		Environment: osutil.ExpandableMap{
			// Invalid syntax that should cause an error
			"BAD_VAR": osutil.NewExpandableString("${UNCLOSED"),
		},
	}

	_, err := service.ExpandEnv(func(key string) string {
		return "value"
	})

	require.Error(t, err)
}
