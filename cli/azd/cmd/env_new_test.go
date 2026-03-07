// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestEnvNewAction_NoPrompt_AutoSetsDefault(t *testing.T) {
	t.Run("auto-sets default when multiple envs exist", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.SetNoPromptMode(true)

		azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
		envMgr := &mockenv.MockEnvManager{}

		testEnv := environment.New("test-env")
		envMgr.On("Create", mock.Anything, mock.Anything).Return(testEnv, nil)
		envMgr.On("List", mock.Anything).Return([]*environment.Description{
			{Name: "existing-env"},
			{Name: "test-env"},
		}, nil)

		action := &envNewAction{
			azdCtx:     azdCtx,
			envManager: envMgr,
			flags:      &envNewFlags{global: &internal.GlobalCommandOptions{NoPrompt: true}},
			args:       []string{"test-env"},
			console:    mockContext.Console,
		}

		_, err := action.Run(*mockContext.Context)
		require.NoError(t, err)

		// Verify the default environment was set
		defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
		require.NoError(t, err)
		require.Equal(t, "test-env", defaultEnv)
	})
}
