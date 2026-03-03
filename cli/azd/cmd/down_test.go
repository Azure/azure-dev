// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestProvisionManager(
	mockContext *mocks.MockContext,
	lazyEnv *lazy.Lazy[*environment.Environment],
	envManager environment.Manager,
) *provisioning.Manager {
	alphaManager := alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig())
	return provisioning.NewManager(
		mockContext.Container,
		func() (provisioning.ProviderKind, error) { return provisioning.Bicep, nil },
		envManager,
		lazyEnv,
		mockContext.Console,
		alphaManager,
		nil,
		cloud.AzurePublic(),
	)
}

func newTestDownAction(
	t *testing.T,
	mockContext *mocks.MockContext,
	envManager *mockenv.MockEnvManager,
	lazyEnv *lazy.Lazy[*environment.Environment],
	provisionManager *provisioning.Manager,
) *downAction {
	t.Helper()
	alphaManager := alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig())
	action := newDownAction(
		[]string{},
		&downFlags{},
		provisionManager,
		lazyEnv,
		envManager,
		&project.ProjectConfig{},
		mockContext.Console,
		alphaManager,
		project.NewImportManager(nil),
	)
	return action.(*downAction)
}

func Test_DownAction_NoEnvironments_ReturnsError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	envManager := &mockenv.MockEnvManager{}

	// lazyEnv must NOT be called when no env exists and it returns ErrNameNotSpecified
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return nil, environment.ErrNameNotSpecified
	})
	provisionManager := newTestProvisionManager(mockContext, lazyEnv, envManager)

	action := newTestDownAction(t, mockContext, envManager, lazyEnv, provisionManager)

	_, err := action.Run(*mockContext.Context)
	require.Error(t, err)

	var suggestionErr *internal.ErrorWithSuggestion
	require.True(t, errors.As(err, &suggestionErr))
	require.Contains(t, suggestionErr.Error(), "no environment selected")
	require.Contains(t, suggestionErr.Suggestion, "azd env new")
}

func Test_DownAction_EnvironmentNotFound_ReturnsError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	envManager := &mockenv.MockEnvManager{}

	// Simulate -e flag pointing to a missing environment
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return nil, fmt.Errorf("'missing-env': %w", environment.ErrNotFound)
	})
	provisionManager := newTestProvisionManager(mockContext, lazyEnv, envManager)

	action := newTestDownAction(t, mockContext, envManager, lazyEnv, provisionManager)

	_, err := action.Run(*mockContext.Context)
	require.Error(t, err)

	var suggestionErr *internal.ErrorWithSuggestion
	require.True(t, errors.As(err, &suggestionErr))
	require.Contains(t, suggestionErr.Error(), "environment not found")
	require.Contains(t, suggestionErr.Suggestion, "azd env list")
}

func Test_DownAction_NoDefaultEnvironment_ReturnsError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	envManager := &mockenv.MockEnvManager{}

	// No -e flag and no default environment set
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return nil, environment.ErrNameNotSpecified
	})
	provisionManager := newTestProvisionManager(mockContext, lazyEnv, envManager)

	action := newTestDownAction(t, mockContext, envManager, lazyEnv, provisionManager)

	_, err := action.Run(*mockContext.Context)
	require.Error(t, err)

	var suggestionErr *internal.ErrorWithSuggestion
	require.True(t, errors.As(err, &suggestionErr))
	require.Contains(t, suggestionErr.Error(), "no environment selected")
	require.Contains(t, suggestionErr.Suggestion, "azd env select")
}

func Test_DownAction_EnvironmentExists_ProceedsToProvisioning(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	envManager := &mockenv.MockEnvManager{}
	envManager.On("InvalidateEnvCache", mock.Anything, mock.Anything).Return(nil)

	env := environment.NewWithValues("test-env", nil)
	lazyEnv := lazy.From(env)
	provisionManager := newTestProvisionManager(mockContext, lazyEnv, envManager)

	action := newTestDownAction(t, mockContext, envManager, lazyEnv, provisionManager)

	_, err := action.Run(*mockContext.Context)
	// The action must get past the env check and reach provisioning.
	// It will fail on Initialize (no IaC provider in mock container), which is expected.
	// The key assertion is: the error is NOT an env-check error.
	require.Error(t, err)
	var suggestionErr *internal.ErrorWithSuggestion
	require.False(t, errors.As(err, &suggestionErr), "Expected a provisioning error, not an env-check error")
}
