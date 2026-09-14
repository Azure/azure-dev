// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

type recordingServiceManager struct {
	project.ServiceManager
	initializedServices []string
}

func (m *recordingServiceManager) GetRequiredTools(
	context.Context, *project.ServiceConfig,
) ([]tools.ExternalTool, error) {
	return nil, nil
}

func (m *recordingServiceManager) Initialize(
	_ context.Context, serviceConfig *project.ServiceConfig,
) error {
	m.initializedServices = append(m.initializedServices, serviceConfig.Name)
	return nil
}

// mockProvider implements provisioning.Provider for testing.
type mockProvider struct {
	deployResult *provisioning.DeployResult
	deployErr    error
}

func (p *mockProvider) Name() string { return "test" }

func (p *mockProvider) Initialize(_ context.Context, _ string, _ provisioning.Options) error {
	return nil
}

func (p *mockProvider) State(_ context.Context, _ *provisioning.StateOptions) (*provisioning.StateResult, error) {
	return nil, nil
}

func (p *mockProvider) Deploy(_ context.Context) (*provisioning.DeployResult, error) {
	return p.deployResult, p.deployErr
}

func (p *mockProvider) Preview(_ context.Context) (*provisioning.DeployPreviewResult, error) {
	return nil, nil
}

func (p *mockProvider) Destroy(_ context.Context, _ provisioning.DestroyOptions) (*provisioning.DestroyResult, error) {
	return nil, nil
}

func (p *mockProvider) EnsureEnv(_ context.Context) error { return nil }

func (p *mockProvider) Parameters(_ context.Context) ([]provisioning.Parameter, error) {
	return nil, nil
}

func (p *mockProvider) PlannedOutputs(_ context.Context) ([]provisioning.PlannedOutput, error) {
	return nil, nil
}

// TestProvisionAction_ProvisionValidationCanceled verifies that when the user declines
// provision validation warnings, ProvisionAction.Run returns ErrAbortedByUser and does NOT
// attempt to read deployResult.Deployment.Outputs (which would nil-panic).
//
// Regression test for https://github.com/Azure/azure-dev/issues/7305
func TestProvisionAction_ProvisionValidationCanceled(t *testing.T) {
	t.Parallel()
	// Set up a temp project with a minimal infra directory so ImportManager works.
	projectDir := t.TempDir()
	infraDir := filepath.Join(projectDir, "infra")
	require.NoError(t, os.MkdirAll(infraDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, "main.bicep"), []byte("targetScope = 'subscription'\n"), 0o600))

	// Mock provider that simulates provision validation cancel (user said No).
	provider := &mockProvider{
		deployResult: &provisioning.DeployResult{
			SkippedReason: provisioning.ProvisionValidationCanceledSkipped,
		},
	}

	// Register mock provider in IoC so provisioning.Manager.Initialize can resolve it.
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterNamedInstance[provisioning.Provider](container, string(provisioning.Test), provider)

	env := environment.New("test-env")
	env.SetSubscriptionId("00000000-0000-0000-0000-000000000000")
	env.SetLocation("eastus2")

	console := mockinput.NewMockConsole()

	provisionManager := provisioning.NewManager(
		container,
		func() (provisioning.ProviderKind, error) { return provisioning.Test, nil },
		nil, // envManager — not needed for this test path
		env,
		console,
		alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		nil, // fileShareService
		cloud.AzurePublic(),
	)

	serviceManager := &recordingServiceManager{}
	pm := project.NewProjectManager(nil, serviceManager, project.NewImportManager(nil))

	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Path: projectDir,
		Infra: provisioning.Options{
			Provider: provisioning.Test,
			Path:     "infra",
			Module:   "main",
		},
	}
	connection := &project.ServiceConfig{
		Name:      "connection",
		Host:      project.ServiceTargetKind("azure.ai.connection"),
		Project:   projectConfig,
		Condition: osutil.NewExpandableString("false"),
		AdditionalProperties: map[string]any{
			"$ref": "missing-connection.yaml",
		},
	}
	projectConfig.Services = map[string]*project.ServiceConfig{
		connection.Name: connection,
	}
	projectConfig.EventDispatcher = ext.NewEventDispatcher[project.ProjectLifecycleEventArgs](
		project.ProjectEvents...,
	)

	action := &ProvisionAction{
		flags: &ProvisionFlags{
			global:  &internal.GlobalCommandOptions{},
			EnvFlag: &internal.EnvFlag{},
		},
		provisionManager:    provisionManager,
		projectManager:      pm,
		importManager:       project.NewImportManager(nil),
		projectConfig:       projectConfig,
		env:                 env,
		console:             console,
		formatter:           &output.NoneFormatter{},
		writer:              io.Discard,
		alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		portalUrlBase:       "https://portal.azure.com",
	}

	mockContext := mocks.NewMockContext(t.Context())
	result, err := action.Run(*mockContext.Context)

	// Must return ErrAbortedByUser (not nil, not a panic)
	require.ErrorIs(t, err, internal.ErrAbortedByUser)
	require.Nil(t, result)

	// Verify project manager was called (action didn't exit prematurely)
	require.Empty(t, serviceManager.initializedServices)
}
