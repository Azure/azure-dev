// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

const (
	testFeatureId     = "test.ext.feature"
	testFeatureEnvVar = "AZD_TEST_EXT_ENABLE_FEATURE"
)

type featureFixture struct {
	userConfig map[string]string
	dotenv     map[string]string
	services   map[string]map[string]string
	noProject  bool
	noEnv      bool
}

// newFeatureServices builds real user config and environment services backed by temp files.
func newFeatureServices(
	t *testing.T, fixture featureFixture,
) (azdext.UserConfigServiceServer, azdext.EnvironmentServiceServer) {
	t.Helper()
	mockContext := mocks.NewMockContext(t.Context())

	userConfig := config.NewEmptyConfig()
	for key, value := range fixture.userConfig {
		require.NoError(t, userConfig.Set(key, value))
	}
	mockContext.ConfigManager.WithConfig(userConfig)
	userConfigServer, err := NewUserConfigService(config.NewUserConfigManager(mockContext.ConfigManager))
	require.NoError(t, err)

	var environmentServer azdext.EnvironmentServiceServer
	if fixture.noProject {
		environmentServer = NewEnvironmentService(
			lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return nil, azdcontext.ErrNoProject }),
			lazy.NewLazy(func() (environment.Manager, error) { return nil, azdcontext.ErrNoProject }),
		)
	} else {
		azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
		projectConfig := &project.ProjectConfig{Name: "test", Services: map[string]*project.ServiceConfig{}}
		for name, env := range fixture.services {
			values := osutil.ExpandableMap{}
			for key, value := range env {
				values[key] = osutil.NewExpandableString(value)
			}
			projectConfig.Services[name] = &project.ServiceConfig{
				Name: name, Host: project.ContainerAppTarget, Language: project.ServiceLanguagePython,
				RelativePath: name, Environment: values,
			}
		}
		require.NoError(t, project.Save(t.Context(), projectConfig, azdContext.ProjectPath()))

		fileConfigManager := config.NewFileConfigManager(config.NewManager())
		dataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
		envManager, err := environment.NewManager(
			mockContext.Container, azdContext, mockContext.Console, dataStore, nil,
		)
		require.NoError(t, err)
		if !fixture.noEnv {
			env, err := envManager.Create(t.Context(), environment.Spec{Name: "dev"})
			require.NoError(t, err)
			for key, value := range fixture.dotenv {
				env.DotenvSet(key, value)
			}
			require.NoError(t, envManager.Save(t.Context(), env))
			require.NoError(t, azdContext.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "dev"}))
		}
		environmentServer = NewEnvironmentService(lazy.From(azdContext), lazy.From(envManager))
	}

	return userConfigServer, environmentServer
}

// startFeatureServer starts the real gRPC server and returns an SDK client.
func startFeatureServer(t *testing.T, fixture featureFixture) (*azdext.AzdClient, context.Context) {
	t.Helper()
	userConfigServer, environmentServer := newFeatureServices(t, fixture)
	server := NewServer(
		azdext.UnimplementedProjectServiceServer{},
		environmentServer,
		azdext.UnimplementedPromptServiceServer{},
		userConfigServer,
		azdext.UnimplementedDeploymentServiceServer{},
		azdext.UnimplementedEventServiceServer{},
		v1beta.UnimplementedComposeServiceServer{},
		azdext.UnimplementedWorkflowServiceServer{},
		azdext.UnimplementedExtensionServiceServer{},
		azdext.UnimplementedServiceTargetServiceServer{},
		azdext.UnimplementedFrameworkServiceServer{},
		azdext.UnimplementedContainerServiceServer{},
		azdext.UnimplementedAccountServiceServer{},
		azdext.UnimplementedAiModelServiceServer{},
		v1beta.UnimplementedCopilotServiceServer{},
		azdext.UnimplementedProvisioningServiceServer{},
		azdext.UnimplementedValidationServiceServer{},
		azdext.UnimplementedTelemetryServiceServer{},
		v1beta.UnimplementedCommandResultServiceServer{},
	)
	serverInfo, err := server.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Stop()) })

	return dialProjectSDK(t, serverInfo)
}

func TestServerE2E_IsFeatureEnabled(t *testing.T) {
	on := map[string]string{"alpha." + testFeatureId: "on"}
	envTrue := map[string]string{testFeatureEnvVar: "true"}
	envFalse := map[string]string{testFeatureEnvVar: "false"}

	tests := []struct {
		name       string
		fixture    featureFixture
		processEnv string
		want       bool
	}{
		{name: "nothing set", want: false},
		{name: "user config on", fixture: featureFixture{userConfig: on}, want: true},
		{name: "user config alpha.all on", fixture: featureFixture{
			userConfig: map[string]string{"alpha.all": "on"},
		}, want: true},
		{name: "user config true is not on", fixture: featureFixture{
			userConfig: map[string]string{"alpha." + testFeatureId: "true"},
		}, want: false},
		{name: "dotenv only", fixture: featureFixture{dotenv: envTrue}, want: true},
		{name: "service env only", fixture: featureFixture{
			services: map[string]map[string]string{"api": envFalse, "web": envTrue},
		}, want: true},
		{name: "process env only", processEnv: "true", want: true},
		{name: "process false dotenv true", fixture: featureFixture{dotenv: envTrue}, processEnv: "false", want: true},
		{name: "dotenv false process true", fixture: featureFixture{dotenv: envFalse}, processEnv: "true", want: true},
		{name: "user config off service true", fixture: featureFixture{
			userConfig: map[string]string{"alpha." + testFeatureId: "off"},
			services:   map[string]map[string]string{"api": envTrue},
		}, want: true},
		{name: "all false", fixture: featureFixture{
			userConfig: map[string]string{"alpha." + testFeatureId: "off"},
			dotenv:     envFalse,
			services:   map[string]map[string]string{"api": envFalse},
		}, processEnv: "false", want: false},
		{name: "no env selected", fixture: featureFixture{noEnv: true}, want: false},
		{name: "no env selected process true", fixture: featureFixture{noEnv: true}, processEnv: "true", want: true},
		{name: "no project", fixture: featureFixture{noProject: true}, want: false},
		{name: "no project user config on", fixture: featureFixture{noProject: true, userConfig: on}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.processEnv != "" {
				t.Setenv(testFeatureEnvVar, tt.processEnv)
			}
			client, ctx := startFeatureServer(t, tt.fixture)

			enabled, err := client.IsFeatureEnabled(ctx, testFeatureId, testFeatureEnvVar)
			require.NoError(t, err)
			require.Equal(t, tt.want, enabled)
		})
	}
}

func TestServerE2E_IsFeatureEnabled_DefaultEnvNames(t *testing.T) {
	t.Setenv("AZD_ALPHA_ENABLE_TEST_EXT_FEATURE", "true")
	client, ctx := startFeatureServer(t, featureFixture{})

	enabled, err := client.IsFeatureEnabled(ctx, testFeatureId, "")
	require.NoError(t, err)
	require.True(t, enabled)

	enabled, err = client.IsFeatureEnabled(ctx, testFeatureId, testFeatureEnvVar)
	require.NoError(t, err)
	require.False(t, enabled, "an explicit env var replaces the default names")
}

func TestServerE2E_IsFeatureEnabled_Errors(t *testing.T) {
	client, ctx := startFeatureServer(t, featureFixture{})

	_, err := client.IsFeatureEnabled(ctx, " ", "")
	require.Equal(t, codes.InvalidArgument, status.Code(err))

}

func TestBetaUserConfigServiceOverride_EnvName(t *testing.T) {
	userConfigServer, environmentServer := newFeatureServices(t, featureFixture{
		dotenv: map[string]string{testFeatureEnvVar: "true"},
	})
	override := &betaUserConfigServiceOverride{
		userConfig:  userConfigServer.(*userConfigService),
		environment: environmentServer.(*environmentService),
		lookupEnv:   func(string) (string, bool) { return "", false },
	}

	response, err := override.IsFeatureEnabled(t.Context(), &v1beta.IsFeatureEnabledRequest{
		Id: testFeatureId, EnvVar: testFeatureEnvVar, EnvName: "dev",
	})
	require.NoError(t, err)
	require.True(t, response.GetEnabled())

	_, err = override.IsFeatureEnabled(t.Context(), &v1beta.IsFeatureEnabledRequest{
		Id: testFeatureId, EnvVar: testFeatureEnvVar, EnvName: "missing",
	})
	require.ErrorIs(t, err, environment.ErrNotFound, "an explicit environment that does not exist is an error")
}
