// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_LoginGuard_Run(t *testing.T) {
	t.Run("LoggedIn", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())

		mockAuthManager := &mockCurrentUserAuthManager{}
		mockAuthManager.On("Cloud").Return(cloud.AzurePublic())
		mockAuthManager.
			On("CredentialForCurrentUser", *mockContext.Context, mock.Anything).
			Return(mockContext.Credentials, nil)

		middleware := LoginGuardMiddleware{
			console:        mockContext.Console,
			authManager:    mockAuthManager,
			workflowRunner: &workflow.Runner{},
		}

		result, err := middleware.Run(*mockContext.Context, next)
		require.NoError(t, err)
		require.NotNil(t, result)
	})
	t.Run("NotLoggedIn", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.
			WhenConfirm(func(options input.ConsoleOptions) bool {
				return strings.Contains(options.Message, "Would you like to log in now?")
			}).
			Respond(false)

		mockAuthManager := &mockCurrentUserAuthManager{}
		mockAuthManager.On("Cloud").Return(cloud.AzurePublic())
		mockAuthManager.
			On("CredentialForCurrentUser", *mockContext.Context, mock.Anything).
			Return(nil, auth.ErrNoCurrentUser)

		middleware := LoginGuardMiddleware{
			console:        mockContext.Console,
			authManager:    mockAuthManager,
			workflowRunner: &workflow.Runner{},
		}

		result, err := middleware.Run(*mockContext.Context, next)
		require.Error(t, err)
		require.Nil(t, result)
	})
}

func next(ctx context.Context) (*actions.ActionResult, error) {
	return &actions.ActionResult{}, nil
}

type mockCurrentUserAuthManager struct {
	mock.Mock
}

func (m *mockCurrentUserAuthManager) Cloud() *cloud.Cloud {
	args := m.Called()
	return args.Get(0).(*cloud.Cloud)
}

func (m *mockCurrentUserAuthManager) CredentialForCurrentUser(
	ctx context.Context,
	options *auth.CredentialForCurrentUserOptions,
) (azcore.TokenCredential, error) {
	args := m.Called(ctx, options)

	tokenVal := args.Get(0)
	if tokenVal == nil {
		return nil, args.Error(1)
	}

	return tokenVal.(azcore.TokenCredential), args.Error(1)
}
