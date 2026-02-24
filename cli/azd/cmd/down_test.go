// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// failServiceLocator is a test helper that fails the test if any IoC resolution is attempted.
type failServiceLocator struct {
	t *testing.T
}

func (f *failServiceLocator) Resolve(instance any) error {
	f.t.Fatal("serviceLocator.Resolve should not be called")
	return nil
}

func (f *failServiceLocator) ResolveNamed(name string, instance any) error {
	f.t.Fatal("serviceLocator.ResolveNamed should not be called")
	return nil
}

func (f *failServiceLocator) Invoke(resolver any) error {
	f.t.Fatal("serviceLocator.Invoke should not be called")
	return nil
}

var _ ioc.ServiceLocator = (*failServiceLocator)(nil)

func newTestDownAction(
	t *testing.T,
	mockContext *mocks.MockContext,
	envManager *mockenv.MockEnvManager,
	lazyEnv *lazy.Lazy[*environment.Environment],
	serviceLocator ioc.ServiceLocator,
) *downAction {
	t.Helper()
	if serviceLocator == nil {
		serviceLocator = &failServiceLocator{t: t}
	}
	alphaManager := alpha.NewFeaturesManagerWithConfig(nil)
	action := newDownAction(
		[]string{},
		&downFlags{},
		serviceLocator,
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
	envManager.On("List", mock.Anything).
		Return([]*environment.Description{}, nil)

	// lazyEnv and serviceLocator must NOT be called when no environments exist
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		t.Fatal("lazyEnv should not be evaluated when no environments exist")
		return nil, nil
	})

	action := newTestDownAction(t, mockContext, envManager, lazyEnv, nil)

	_, err := action.Run(*mockContext.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no environments found")

	envManager.AssertExpectations(t)
}

func Test_DownAction_EnvironmentNotFound_ReturnsError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	envManager := &mockenv.MockEnvManager{}
	envManager.On("List", mock.Anything).
		Return([]*environment.Description{{Name: "some-env"}}, nil)

	// Simulate -e flag pointing to a missing environment
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return nil, fmt.Errorf("'missing-env': %w", environment.ErrNotFound)
	})

	action := newTestDownAction(t, mockContext, envManager, lazyEnv, nil)

	_, err := action.Run(*mockContext.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "environment not found")
	require.Contains(t, err.Error(), "azd env list")

	envManager.AssertExpectations(t)
}

func Test_DownAction_NoDefaultEnvironment_ReturnsError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	envManager := &mockenv.MockEnvManager{}
	envManager.On("List", mock.Anything).
		Return([]*environment.Description{{Name: "env1"}, {Name: "env2"}}, nil)

	// No -e flag and no default environment set
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return nil, environment.ErrNameNotSpecified
	})

	action := newTestDownAction(t, mockContext, envManager, lazyEnv, nil)

	_, err := action.Run(*mockContext.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no environment selected")
	require.Contains(t, err.Error(), "azd env select")

	envManager.AssertExpectations(t)
}
