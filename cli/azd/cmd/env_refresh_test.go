// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockEnvRefreshProvider implements provisioning.Provider for env refresh testing.
type mockEnvRefreshProvider struct{}

func (p *mockEnvRefreshProvider) Name() string { return "test" }

func (p *mockEnvRefreshProvider) Initialize(
	_ context.Context, _ string, _ provisioning.Options,
) error {
	return nil
}

func (p *mockEnvRefreshProvider) State(
	_ context.Context, _ *provisioning.StateOptions,
) (*provisioning.StateResult, error) {
	return &provisioning.StateResult{
		State: &provisioning.State{
			Outputs: map[string]provisioning.OutputParameter{
				"WEBSITE_URL": {Type: "string", Value: "https://example.com"},
			},
			Resources: []provisioning.Resource{},
		},
	}, nil
}

func (p *mockEnvRefreshProvider) Deploy(
	_ context.Context,
) (*provisioning.DeployResult, error) {
	return nil, nil
}

func (p *mockEnvRefreshProvider) Preview(
	_ context.Context,
) (*provisioning.DeployPreviewResult, error) {
	return nil, nil
}

func (p *mockEnvRefreshProvider) Destroy(
	_ context.Context, _ provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	return nil, nil
}

func (p *mockEnvRefreshProvider) EnsureEnv(_ context.Context) error {
	return nil
}

func (p *mockEnvRefreshProvider) Parameters(
	_ context.Context,
) ([]provisioning.Parameter, error) {
	return nil, nil
}

func (p *mockEnvRefreshProvider) PlannedOutputs(
	_ context.Context,
) ([]provisioning.PlannedOutput, error) {
	return nil, nil
}

// TestEnvRefreshAction_SucceedsWhenProjectInitFails verifies that env refresh
// completes successfully even when projectManager.Initialize() returns an
// error. This is the key fix for issue #7195 where projects using the
// azure.ai.agent extension would fail because the extension's service target
// initialization could not complete during env refresh.
func TestEnvRefreshAction_SucceedsWhenProjectInitFails(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()

	// Register mock provider in IoC
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterNamedInstance[provisioning.Provider](
		container, string(provisioning.Test), &mockEnvRefreshProvider{},
	)

	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "00000000-0000-0000-0000-000000000000",
		"AZURE_LOCATION":        "eastus2",
	})

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	envManager.On("EnvPath", mock.Anything).
		Return(filepath.Join(projectDir, ".azure", "test-env", ".env"))

	console := mockinput.NewMockConsole()

	provisionMgr := provisioning.NewManager(
		container,
		func() (provisioning.ProviderKind, error) {
			return provisioning.Test, nil
		},
		envManager,
		env,
		console,
		alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		nil,
		cloud.AzurePublic(),
	)

	// Project manager that fails on Initialize (simulating extension failure)
	pm := &mockProjectManager{}
	pm.On("Initialize", mock.Anything, mock.Anything).
		Return(fmt.Errorf("extension service target initialization failed"))

	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Path: projectDir,
		Infra: provisioning.Options{
			Provider: provisioning.Test,
			Path:     "infra",
			Module:   "main",
		},
	}

	action := &envRefreshAction{
		provisionManager:    provisionMgr,
		projectConfig:       projectConfig,
		projectManager:      pm,
		env:                 env,
		envManager:          envManager,
		flags:               &envRefreshFlags{},
		console:             console,
		formatter:           &output.NoneFormatter{},
		writer:              &bytes.Buffer{},
		importManager:       project.NewImportManager(nil),
		alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	result, err := action.Run(t.Context())

	// env refresh should succeed even though project init failed
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "Environment refresh completed", result.Message.Header)

	// Verify that the environment was updated with deployment outputs
	require.Equal(t, "https://example.com", env.Getenv("WEBSITE_URL"))
}

// TestEnvRefreshAction_RaisesServiceEventsOnSuccess verifies that when
// project initialization succeeds, ServiceEventEnvUpdated events are raised
// for each service with the correct deployment outputs.
func TestEnvRefreshAction_RaisesServiceEventsOnSuccess(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()

	// Register mock provider in IoC
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterNamedInstance[provisioning.Provider](
		container, string(provisioning.Test), &mockEnvRefreshProvider{},
	)

	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "00000000-0000-0000-0000-000000000000",
		"AZURE_LOCATION":        "eastus2",
	})

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	envManager.On("EnvPath", mock.Anything).
		Return(filepath.Join(projectDir, ".azure", "test-env", ".env"))

	console := mockinput.NewMockConsole()

	provisionMgr := provisioning.NewManager(
		container,
		func() (provisioning.ProviderKind, error) {
			return provisioning.Test, nil
		},
		envManager,
		env,
		console,
		alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		nil,
		cloud.AzurePublic(),
	)

	// Project manager that succeeds
	pm := &mockProjectManager{}
	pm.On("Initialize", mock.Anything, mock.Anything).Return(nil)

	svcConfig := &project.ServiceConfig{
		Name: "web",
		EventDispatcher: ext.NewEventDispatcher[project.ServiceLifecycleEventArgs](
			project.ServiceEvents...,
		),
	}

	// Register a handler on the service's EventDispatcher to capture the event
	eventFired := false
	var capturedOutputs map[string]provisioning.OutputParameter
	err := svcConfig.AddHandler(t.Context(), project.ServiceEventEnvUpdated,
		func(ctx context.Context, args project.ServiceLifecycleEventArgs) error {
			eventFired = true
			capturedOutputs, _ = args.Args["bicepOutput"].(map[string]provisioning.OutputParameter)
			return nil
		},
	)
	require.NoError(t, err)

	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Path: projectDir,
		Infra: provisioning.Options{
			Provider: provisioning.Test,
			Path:     "infra",
			Module:   "main",
		},
		Services: map[string]*project.ServiceConfig{
			"web": svcConfig,
		},
	}

	action := &envRefreshAction{
		provisionManager:    provisionMgr,
		projectConfig:       projectConfig,
		projectManager:      pm,
		env:                 env,
		envManager:          envManager,
		flags:               &envRefreshFlags{},
		console:             console,
		formatter:           &output.NoneFormatter{},
		writer:              &bytes.Buffer{},
		importManager:       project.NewImportManager(nil),
		alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	result, err := action.Run(t.Context())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "Environment refresh completed", result.Message.Header)

	// Verify the ServiceEventEnvUpdated handler was invoked
	require.True(t, eventFired, "ServiceEventEnvUpdated handler should have been invoked")

	// Verify the handler received the correct deployment outputs
	require.NotNil(t, capturedOutputs)
	require.Contains(t, capturedOutputs, "WEBSITE_URL")
	require.Equal(t, "https://example.com", capturedOutputs["WEBSITE_URL"].Value)
}
