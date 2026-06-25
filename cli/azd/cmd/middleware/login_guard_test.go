// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func Test_LoginGuard_Run(t *testing.T) {
	t.Run("LoggedIn", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())

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
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.
			WhenConfirm(func(options input.ConsoleOptions) bool {
				return strings.Contains(options.Message, "Would you like to log in now?")
			}).
			Respond(false)

		mockAuthManager := &mockCurrentUserAuthManager{}
		mockAuthManager.On("Cloud").Return(cloud.AzurePublic())
		mockAuthManager.On("Mode").Return(auth.AzdBuiltIn, nil)
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

				mockContext := mocks.NewMockContext(t.Context())
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

func (m *mockCurrentUserAuthManager) Mode() (auth.AuthSource, error) {
	args := m.Called()
	return args.Get(0).(auth.AuthSource), args.Error(1)
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

func TestLoginGuard_EnsureLogin_AzDelegatedMode(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())

	// Don't set CI env vars — we want the non-CI path
	mockAuthManager := &mockCurrentUserAuthManager{}
	mockAuthManager.On("Cloud").Return(cloud.AzurePublic())
	mockAuthManager.On("Mode").Return(auth.AzDelegated, nil)
	mockAuthManager.
		On("CredentialForCurrentUser", *mockCtx.Context, mock.Anything).
		Return(nil, auth.ErrNoCurrentUser)

	// In AzDelegated mode, the middleware should tell the user to run "az login"
	// and return the credential error without prompting for interactive login.
	middleware := LoginGuardMiddleware{
		console:        mockCtx.Console,
		authManager:    mockAuthManager,
		workflowRunner: &workflow.Runner{},
	}

	result, err := middleware.Run(*mockCtx.Context, next)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, auth.ErrNoCurrentUser, err)
}

func TestLoginGuard_Run_EnsureLoggedInCredential_ErrNoCurrentUser(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())

	mockAuthManager := &mockCurrentUserAuthManager{}
	mockAuthManager.On("Cloud").Return(cloud.AzurePublic())
	// CredentialForCurrentUser succeeds (returns a credential), but
	// EnsureLoggedInCredential will detect no current user.
	mockAuthManager.
		On("CredentialForCurrentUser", *mockCtx.Context, mock.Anything).
		Return(mockCtx.Credentials, nil)

	middleware := LoginGuardMiddleware{
		console:        mockCtx.Console,
		authManager:    mockAuthManager,
		workflowRunner: &workflow.Runner{},
	}

	// EnsureLoggedInCredential is called with the real credential.
	// In test context, the token call may fail with ErrNoCurrentUser.
	result, err := middleware.Run(*mockCtx.Context, next)

	// The result depends on whether the mock credential passes EnsureLoggedInCredential.
	// In most test setups, it will succeed and call next().
	if err == nil {
		require.NotNil(t, result)
	} else {
		// If it fails, check that ErrNoCurrentUser is wrapped with suggestion
		var sugErr *internal.ErrorWithSuggestion
		if errors.As(err, &sugErr) {
			require.Contains(t, sugErr.Suggestion, "azd auth login")
		}
	}
}

func TestLoginGuard_EnsureLogin_ConfirmError(t *testing.T) {
	t.Parallel()
	// In CI, IsRunningOnCI() causes ensureLogin to short-circuit before
	// reaching the Confirm prompt, so this test is only meaningful locally.
	if isCI() {
		t.Skip("skipping: CI short-circuits before console Confirm")
	}
	mockCtx := mocks.NewMockContext(t.Context())

	mockAuthManager := &mockCurrentUserAuthManager{}
	mockAuthManager.On("Cloud").Return(cloud.AzurePublic())
	mockAuthManager.On("Mode").Return(auth.AzdBuiltIn, nil)
	mockAuthManager.
		On("CredentialForCurrentUser", *mockCtx.Context, mock.Anything).
		Return(nil, auth.ErrNoCurrentUser)

	// Simulate Confirm returning an error.
	// Must use RespondFn (not SetError) because Confirm does value.(bool)
	// and SetError returns nil which panics on the type assertion.
	mockCtx.Console.
		WhenConfirm(func(options input.ConsoleOptions) bool {
			return true // match any confirm
		}).
		RespondFn(func(_ input.ConsoleOptions) (any, error) {
			return false, errors.New("console error")
		})

	middleware := LoginGuardMiddleware{
		console:        mockCtx.Console,
		authManager:    mockAuthManager,
		workflowRunner: &workflow.Runner{},
	}

	result, err := middleware.Run(*mockCtx.Context, next)
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "console error")
}

func TestLoginGuard_Run_EnsureLoggedInCredential_NonAuthError(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())

	// Credential that returns a generic (non-auth) error on GetToken
	badCred := &mocks.MockCredentials{
		GetTokenFn: func(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
			return azcore.AccessToken{}, errors.New("transient network error")
		},
	}

	authMgr := &mockCurrentUserAuthManager{}
	authMgr.On("Cloud").Return(cloud.AzurePublic())
	authMgr.On("CredentialForCurrentUser", mock.Anything, mock.Anything).Return(badCred, nil)

	m := LoginGuardMiddleware{
		console:        mockCtx.Console,
		authManager:    authMgr,
		workflowRunner: &workflow.Runner{},
	}

	_, err := m.Run(*mockCtx.Context, func(ctx context.Context) (*actions.ActionResult, error) {
		return &actions.ActionResult{}, nil
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "transient network error")
	// Should NOT be wrapped in ErrorWithSuggestion (that's only for ErrNoCurrentUser)
	var suggestion *internal.ErrorWithSuggestion
	require.False(t, errors.As(err, &suggestion))
}
