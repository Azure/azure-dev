// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func Test_NewDownAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &downFlags{}
	console := mockinput.NewMockConsole()
	a := newDownAction(nil, flags, nil, nil, nil, nil, console, nil, nil)
	da := a.(*downAction)
	require.Same(t, flags, da.flags)
}

func Test_NewDownCmd(t *testing.T) {
	t.Parallel()
	cmd := newDownCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "down [<layer>]", cmd.Use)
}

func Test_NewDownFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newDownFlags(cmd, global)
	require.NotNil(t, flags)
}

// mockDownProvider is a provisioning.Provider with a configurable Destroy result, reusing
// mockRefreshProvider for the remaining interface methods.
type mockDownProvider struct {
	*mockRefreshProvider
	destroyResult *provisioning.DestroyResult
	destroyErr    error
}

func (p *mockDownProvider) Destroy(
	_ context.Context, _ provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	return p.destroyResult, p.destroyErr
}

// newTestDownAction wires a downAction against a real provisioning.Manager backed by the given
// mock provider, mirroring newTestEnvRefreshAction.
func newTestDownAction(
	t *testing.T,
	provider provisioning.Provider,
) (*downAction, *mockinput.MockConsole, *mockenv.MockEnvManager) {
	t.Helper()

	container := ioc.NewNestedContainer(nil)
	ioc.RegisterNamedInstance(container, string(provisioning.Test), provider)

	env := environment.New("test-env")
	env.SetSubscriptionId("00000000-0000-0000-0000-000000000000")
	env.SetLocation("eastus2")

	console := mockinput.NewMockConsole()

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, mock.Anything).Return(nil)
	envManager.On("InvalidateEnvCache", mock.Anything, mock.Anything).Return(nil)

	provisionManager := provisioning.NewManager(
		container,
		func() (provisioning.ProviderKind, error) { return provisioning.Test, nil },
		envManager,
		env,
		console,
		alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		nil, // fileShareService
		cloud.AzurePublic(),
	)

	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Path: t.TempDir(),
		Infra: provisioning.Options{
			Provider: provisioning.Test,
			Path:     "infra",
			Module:   "main",
			// Set Layers so ProjectInfrastructure resolves without requiring infra files on disk.
			Layers: []provisioning.Options{
				{Name: "infra", Provider: provisioning.Test, Path: "infra", Module: "main"},
			},
		},
	}

	action := &downAction{
		flags:               &downFlags{global: &internal.GlobalCommandOptions{}},
		provisionManager:    provisionManager,
		env:                 env,
		envManager:          envManager,
		console:             console,
		projectConfig:       projectConfig,
		importManager:       project.NewImportManager(nil),
		alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
	}

	return action, console, envManager
}

// Test_DownAction_Run_SkippedDeletion verifies that when the provider reports SkippedDeletion (e.g.
// a --no-prompt CI preview), azd down does not emit the full-teardown success header. See #4317.
func Test_DownAction_Run_SkippedDeletion(t *testing.T) {
	provider := &mockDownProvider{
		mockRefreshProvider: &mockRefreshProvider{},
		destroyResult:       &provisioning.DestroyResult{SkippedDeletion: true},
	}
	action, _, envManager := newTestDownAction(t, provider)

	result, err := action.Run(t.Context())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.Message, "no success message expected when deletion was skipped")
	// Cache is still invalidated so azd show refreshes.
	envManager.AssertCalled(t, "InvalidateEnvCache", mock.Anything, mock.Anything)
}

// Test_DownAction_Run_Deleted verifies that a normal destroy still reports the teardown success header.
func Test_DownAction_Run_Deleted(t *testing.T) {
	provider := &mockDownProvider{
		mockRefreshProvider: &mockRefreshProvider{},
		destroyResult:       &provisioning.DestroyResult{},
	}
	action, _, _ := newTestDownAction(t, provider)

	result, err := action.Run(t.Context())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Message)
	require.Contains(t, result.Message.Header, "Your application was removed")
}
