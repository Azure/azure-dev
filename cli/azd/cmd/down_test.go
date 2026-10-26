// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
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

// Test_DownAction_RecordsInfraProvider exercises downAction.Run end-to-end to verify the
// infra.provider telemetry contract on the cmd.down span: the resolved provider(s) are recorded as
// a slice up front, before the destroy loop, so the attribute is present even though provisioning
// subsequently fails. It covers both the all-layers path (multi-provider slice) and a selected
// single layer.
func Test_DownAction_RecordsInfraProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		layers   []provisioning.Options
		expected []string
	}{
		{
			name: "all layers records each distinct provider",
			args: nil,
			layers: []provisioning.Options{
				{Name: "app", Provider: provisioning.Bicep},
				{Name: "data", Provider: provisioning.Terraform},
			},
			expected: []string{"bicep", "terraform"},
		},
		{
			name: "selected layer records only that provider",
			args: []string{"data"},
			layers: []provisioning.Options{
				{Name: "app", Provider: provisioning.Bicep},
				{Name: "data", Provider: provisioning.Terraform},
			},
			expected: []string{"terraform"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sr := tracetest.NewSpanRecorder()
			tp := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(sr))
			ctx, span := tp.Tracer("test").Start(t.Context(), "cmd.down")

			// An empty container has no registered IaC provider, so Manager.Initialize fails cleanly
			// on ResolveNamed inside the destroy loop — after the telemetry attribute is recorded.
			serviceLocator := ioc.NewNestedContainer(nil)
			resolver := func() (provisioning.ProviderKind, error) { return provisioning.Bicep, nil }
			provisionManager := provisioning.NewManager(
				serviceLocator, resolver, nil, nil, mockinput.NewMockConsole(), nil, nil, nil)

			action := &downAction{
				flags:   &downFlags{},
				args:    tt.args,
				console: mockinput.NewMockConsole(),
				projectConfig: &project.ProjectConfig{
					// Path is unused (empty project); a built-in top-level provider skips
					// filesystem-based auto-detection in ProjectInfrastructure.
					Path: t.TempDir(),
					Infra: provisioning.Options{
						Provider: provisioning.Bicep,
						Layers:   tt.layers,
					},
				},
				alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
				importManager:       project.NewImportManager(nil),
				provisionManager:    provisionManager,
			}

			// The empty container makes provisioning fail, so Run returns an error after the
			// telemetry attribute is already recorded — exactly the ordering we want to assert.
			_, err := action.Run(ctx)
			require.Error(t, err)
			span.End()

			ended := sr.Ended()
			require.Len(t, ended, 1)

			var got []string
			var found bool
			for _, attr := range ended[0].Attributes() {
				if attr.Key == fields.InfraProviderKey.Key {
					got = attr.Value.AsStringSlice()
					found = true
				}
			}

			require.True(t, found, "expected infra.provider attribute to be recorded on cmd.down")
			require.ElementsMatch(t, tt.expected, got)
		})
	}
}

// mockDownProvider is a provisioning.Provider with a configurable Destroy result, reusing
// mockRefreshProvider for the remaining interface methods.
type mockDownProvider struct {
	*mockRefreshProvider
	destroyResult *provisioning.DestroyResult
	destroyErr    error
	destroyCalls  int
	failOnCall    int
	skipOnCall    int
}

func (p *mockDownProvider) Destroy(
	_ context.Context, _ provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	p.destroyCalls++
	if p.skipOnCall > 0 {
		return &provisioning.DestroyResult{SkippedDeletion: p.destroyCalls == p.skipOnCall}, p.destroyErr
	}
	if p.failOnCall > p.destroyCalls {
		return p.destroyResult, nil
	}
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
		Name:            "test-project",
		Path:            t.TempDir(),
		EventDispatcher: ext.NewEventDispatcher[project.ProjectLifecycleEventArgs](),
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

func TestDownActionPostdownDispatchBoundary(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                                              string
		selectedLayer, skipped, destroyFailed, hookFailed bool
		layers                                            int
		skipOnCall                                        int
		wantCalls                                         int
	}{
		{name: "completed", layers: 1, wantCalls: 1},
		{name: "all layers completed", layers: 2, wantCalls: 1},
		{name: "destroy failed", layers: 1, destroyFailed: true},
		{name: "later layer failed", layers: 2, destroyFailed: true},
		{name: "preview skipped", layers: 1, skipped: true},
		{name: "later layer skipped", layers: 2, skipOnCall: 2},
		{name: "selected layer only", layers: 2, selectedLayer: true},
		{name: "handler failed", layers: 1, hookFailed: true, wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider := &mockDownProvider{mockRefreshProvider: &mockRefreshProvider{},
				skipOnCall:    test.skipOnCall,
				destroyResult: &provisioning.DestroyResult{SkippedDeletion: test.skipped}}
			action, _, _ := newTestDownAction(t, provider)
			if test.layers == 2 {
				action.projectConfig.Infra.Layers = append(action.projectConfig.Infra.Layers,
					provisioning.Options{Name: "other", Provider: provisioning.Test, Path: "infra", Module: "main"})
			}
			if test.selectedLayer {
				action.args = []string{"infra"}
			}
			calls := 0
			failure := errors.New("postdown state cleanup failed")
			require.NoError(t, action.projectConfig.AddHandler(t.Context(), "postdown",
				func(ctx context.Context, args project.ProjectLifecycleEventArgs) error {
					calls++
					require.Same(t, action.projectConfig, args.Project)
					require.Equal(t, test.layers, provider.destroyCalls,
						"postdown must run only after every selected provider finished")
					if test.hookFailed {
						return failure
					}
					return nil
				}))
			if test.destroyFailed {
				provider.destroyErr = errors.New("destroy failed")
				provider.failOnCall = test.layers
			}
			result, err := action.Run(t.Context())
			assert.Equal(t, test.wantCalls, calls)
			if test.destroyFailed || test.hookFailed {
				require.Error(t, err)
				assert.Nil(t, result, "failed cleanup must not return a success message")
				if test.hookFailed {
					assert.ErrorIs(t, err, failure)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}
