// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package local_preflight

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
)

// AuthCheck verifies that the user (or service principal) is currently logged in and that
// the stored credential can be exchanged for a valid access token.
type AuthCheck struct {
	authManager *auth.Manager
}

// NewAuthCheck creates a new AuthCheck backed by the provided auth manager.
func NewAuthCheck(authManager *auth.Manager) *AuthCheck {
	return &AuthCheck{authManager: authManager}
}

// Name returns the display name of the check.
func (c *AuthCheck) Name() string {
	return "Authentication"
}

// Run validates authentication state.
func (c *AuthCheck) Run(ctx context.Context) Result {
	scopes := c.authManager.LoginScopes()

	cred, err := c.authManager.CredentialForCurrentUser(ctx, nil)
	if err != nil {
		if errors.Is(err, auth.ErrNoCurrentUser) {
			return Result{
				Status:     StatusFail,
				Message:    "Not logged in to Azure.",
				Suggestion: "Run 'azd auth login' to sign in.",
			}
		}
		return Result{
			Status:     StatusFail,
			Message:    fmt.Sprintf("Failed to load credentials: %v", err),
			Suggestion: "Run 'azd auth login' to sign in again.",
		}
	}

	// Verify the credential can actually fetch a token (catches expired credentials).
	_, err = cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: scopes})
	if err != nil {
		var reloginErr *auth.ReLoginRequiredError
		if errors.As(err, &reloginErr) {
			return Result{
				Status:     StatusFail,
				Message:    "Azure credentials have expired.",
				Suggestion: "Run 'azd auth login' to refresh your credentials.",
			}
		}
		return Result{
			Status:     StatusFail,
			Message:    fmt.Sprintf("Unable to acquire an access token: %v", err),
			Suggestion: "Run 'azd auth login' to sign in again.",
		}
	}

	return Result{
		Status:  StatusPass,
		Message: "Logged in to Azure.",
	}
}
