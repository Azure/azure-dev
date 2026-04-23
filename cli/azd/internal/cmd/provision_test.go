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
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockProjectManager implements project.ProjectManager for testing.
type mockProjectManager struct {
	mock.Mock
}

func (m *mockProjectManager) Initialize(ctx context.Context, projectConfig *project.ProjectConfig) error {
	return m.Called(ctx, projectConfig).Error(0)
}

func (m *mockProjectManager) EnsureAllTools(
	ctx context.Context, projectConfig *project.ProjectConfig, _ project.ServiceFilterPredicate,
) error {
	return m.Called(ctx, projectConfig).Error(0)
}

func (m *mockProjectManager) DefaultServiceFromWd(
	ctx context.Context, projectConfig *project.ProjectConfig,
) (*project.ServiceConfig, error) {
	args := m.Called(ctx, projectConfig)
	return args.Get(0).(*project.ServiceConfig), args.Error(1)
}

func (m *mockProjectManager) EnsureFrameworkTools(
	ctx context.Context, projectConfig *project.ProjectConfig, _ project.ServiceFilterPredicate,
) error {
	return m.Called(ctx, projectConfig).Error(0)
}

func (m *mockProjectManager) EnsureServiceTargetTools(
	ctx context.Context, projectConfig *project.ProjectConfig, _ project.ServiceFilterPredicate,
) error {
	return m.Called(ctx, projectConfig).Error(0)
}

func (m *mockProjectManager) EnsureRestoreTools(
	ctx context.Context, projectConfig *project.ProjectConfig, _ project.ServiceFilterPredicate,
) error {
	return m.Called(ctx, projectConfig).Error(0)
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

// TestProvisionAction_PreflightAborted verifies that when the user declines
// preflight warnings, ProvisionAction.Run returns ErrAbortedByUser and does NOT
// attempt to read deployResult.Deployment.Outputs (which would nil-panic).
//
// Regression test for https://github.com/Azure/azure-dev/issues/7305
func TestProvisionAction_PreflightAborted(t *testing.T) {
	t.Parallel()
	// Set up a temp project with a minimal infra directory so ImportManager works.
	projectDir := t.TempDir()
	infraDir := filepath.Join(projectDir, "infra")
	require.NoError(t, os.MkdirAll(infraDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, "main.bicep"), []byte("targetScope = 'subscription'\n"), 0o600))

	// Mock provider that simulates preflight abort (user said No).
	provider := &mockProvider{
		deployResult: &provisioning.DeployResult{
			SkippedReason: provisioning.PreflightAbortedSkipped,
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

	pm := &mockProjectManager{}
	pm.On("Initialize", mock.Anything, mock.Anything).Return(nil)
	pm.On("EnsureAllTools", mock.Anything, mock.Anything).Return(nil)

	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Path: projectDir,
		Infra: provisioning.Options{
			Provider: provisioning.Test,
			Path:     "infra",
			Module:   "main",
		},
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

	mockContext := mocks.NewMockContext(context.Background())
	result, err := action.Run(*mockContext.Context)

	// Must return ErrAbortedByUser (not nil, not a panic)
	require.ErrorIs(t, err, internal.ErrAbortedByUser)
	require.Nil(t, result)

	// Verify project manager was called (action didn't exit prematurely)
	pm.AssertExpectations(t)
}
