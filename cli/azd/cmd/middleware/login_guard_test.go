// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"os"
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
	t.Run("NotLoggedInCI", func(t *testing.T) {
		// Test with different CI environment variables
		testCases := []struct {
			envVar   string
			envValue string
			testName string
		}{
			{"GITHUB_ACTIONS", "true", "GitHub Actions"},
			{"TF_BUILD", "True", "Azure Pipelines"},
			{"CI", "true", "Generic CI"},
		}

		for _, tc := range testCases {
			t.Run(tc.testName, func(t *testing.T) {
				// Set up CI environment variable to simulate CI/CD
				originalValue := os.Getenv(tc.envVar)
				os.Setenv(tc.envVar, tc.envValue)
				defer func() {
					if originalValue == "" {
						os.Unsetenv(tc.envVar)
					} else {
						os.Setenv(tc.envVar, originalValue)
					}
				}()

				mockContext := mocks.NewMockContext(context.Background())
				// In CI, we should NOT get a console confirmation prompt
				// The test will fail if console.Confirm is called
				mockContext.Console.
					WhenConfirm(func(options input.ConsoleOptions) bool {
						t.Fatal("Console.Confirm should not be called in CI/CD environment")
						return false
					})

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
				require.Equal(t, auth.ErrNoCurrentUser, err)
			})
		}
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
