// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mocktools"
	"github.com/stretchr/testify/mock"
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
			Hooks: map[string][]*ext.HookConfig{
				"postprovision": {{Shell: "sh", Run: "echo postprovision", Interactive: true}},
			},
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
	preHandlerCalled, postHandlerCalled, postHookCalled := false, false, false
	require.NoError(t, projectConfig.AddHandler(t.Context(), "preprovision",
		func(context.Context, project.ProjectLifecycleEventArgs) error {
			preHandlerCalled = true
			return nil
		}))
	require.NoError(t, projectConfig.AddHandler(t.Context(), "postprovision",
		func(context.Context, project.ProjectLifecycleEventArgs) error {
			postHandlerCalled = true
			return nil
		}))
	mockContext := mocks.NewMockContext(t.Context())
	mocktools.RegisterHookExecutors(mockContext)
	mockContext.CommandRunner.When(func(_ exec.RunArgs, command string) bool {
		return strings.Contains(command, "azd-postprovision-")
	}).RespondFn(func(exec.RunArgs) (exec.RunResult, error) {
		postHookCalled = true
		return exec.NewRunResult(0, "", ""), nil
	})
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Reload", mock.Anything, env).Return(nil)

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
		envManager:          envManager,
		console:             console,
		commandRunner:       mockContext.CommandRunner,
		serviceLocator:      mockContext.Container,
		formatter:           &output.NoneFormatter{},
		writer:              io.Discard,
		alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		portalUrlBase:       "https://portal.azure.com",
	}

	result, err := action.Run(*mockContext.Context)

	// Must return ErrAbortedByUser (not nil, not a panic)
	require.ErrorIs(t, err, internal.ErrAbortedByUser)
	require.Nil(t, result)

	// Verify project manager was called (action didn't exit prematurely)
	require.Empty(t, serviceManager.initializedServices)
	require.True(t, preHandlerCalled)
	require.False(t, postHandlerCalled)
	require.False(t, postHookCalled)
}

func TestWrapProvisionError_NoPromptValidationFailure(t *testing.T) {
	t.Parallel()
	validationErr := &internal.ErrorWithSuggestion{
		Err:        internal.ErrValidationFailed,
		Message:    "Provisioning was canceled because critical warnings require interactive confirmation.",
		Suggestion: "Resolve the critical warnings, or rerun without --no-prompt.",
	}

	tests := []struct {
		name string
		err  error
	}{
		{name: "failure only", err: validationErr},
		{name: "failure then cancellation", err: errors.Join(validationErr, errValidationCanceledByUser)},
		{name: "cancellation then failure", err: errors.Join(errValidationCanceledByUser, validationErr)},
		{
			name: "wrapped joined failures",
			err:  fmt.Errorf("layer failed: %w", errors.Join(errValidationCanceledByUser, validationErr)),
		},
		{
			name: "provider agnostic cancellation",
			err:  errors.Join(provisioning.ErrProvisionValidationCanceled, validationErr),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			console := mockinput.NewMockConsole()
			err := wrapProvisionError(t.Context(), tt.err, provisionErrorDeps{console: console})
			require.ErrorIs(t, err, internal.ErrValidationFailed)
			require.NotErrorIs(t, err, internal.ErrAbortedByUser)
			code, _ := classify(err)
			require.Equal(t, "error.suggestion", code)
			require.NotContains(t, strings.Join(console.Output(), "\n"), "SUCCESS:")
		})
	}
}

func TestWrapProvisionError_CancellationOnly(t *testing.T) {
	t.Parallel()
	for _, cancellation := range []error{
		errValidationCanceledByUser,
		provisioning.ErrProvisionValidationCanceled,
		fmt.Errorf("layer canceled: %w", errValidationCanceledByUser),
		errors.Join(errValidationCanceledByUser, provisioning.ErrProvisionValidationCanceled),
	} {
		console := mockinput.NewMockConsole()
		err := wrapProvisionError(t.Context(), cancellation, provisionErrorDeps{console: console})
		require.ErrorIs(t, err, internal.ErrAbortedByUser)
		require.Contains(t, strings.Join(console.Output(), "\n"), "SUCCESS: Provisioning was canceled.")
	}
}
