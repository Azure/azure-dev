// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import "context"

// LoggedInGuard doesn't hold anything.
// It simply represents a type that can be used to express the logged-in constraint.
type LoggedInGuard struct{}

// NewLoggedInGuard checks if the user is logged in. If the user is not logged in,
// it attempts to log the user in using device code flow.
func NewLoggedInGuard(manager *Manager, ctx context.Context) (LoggedInGuard, error) {
	// Attempt to retrieve the current user's credentials
	cred, err := manager.CredentialForCurrentUser(ctx, nil)
	if err == nil {
		// If no error, ensure the credential is indeed valid
		_, err = EnsureLoggedInCredential(ctx, cred)
		if err == nil {
			// If the credentials are valid, return without error
			return LoggedInGuard{}, nil
		}
	}

	// At this point, either there was an error fetching credentials, or they are not valid,
	// so we attempt to log in using device code flow.
	tenantID := ""
	scopes := []string{"https://management.azure.com//.default"} // Define the required scopes, if specific scopes are needed

	// Attempt to log in with device code. You might replace 'nil' with a custom 'withOpenUrl' function if needed.
	_, err = manager.LoginWithDeviceCode(ctx, tenantID, scopes, nil)
	if err != nil {
		// If login fails, return the error
		return LoggedInGuard{}, err
	}

	// After a successful login, return a new LoggedInGuard instance
	return LoggedInGuard{}, nil
}
