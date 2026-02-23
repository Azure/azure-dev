// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_DownAction_NoEnvironments_ReturnsError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	envManager := &mockenv.MockEnvManager{}
	envManager.On("List", mock.Anything).
		Return([]*environment.Description{}, nil)

	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.New("test-env"), nil
	})

	lazyProvisionManager := lazy.NewLazy(func() (*provisioning.Manager, error) {
		return nil, nil
	})

	alphaManager := alpha.NewFeaturesManagerWithConfig(nil)

	action := newDownAction(
		[]string{},
		&downFlags{},
		lazyProvisionManager,
		lazyEnv,
		envManager,
		&project.ProjectConfig{},
		mockContext.Console,
		alphaManager,
		project.NewImportManager(nil),
	)

	_, err := action.Run(*mockContext.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no environments found")

	envManager.AssertExpectations(t)
}
