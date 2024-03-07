// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import "context"

// LoggedInGuard doesn't hold anything.
// It simply represents a type that can be used to expressed the logged in constraint.
type LoggedInGuard struct{}

// NewLoggedInGuard checks if the user is logged in. An error is returned if the user is not logged in.
func NewLoggedInGuard(manager *Manager, ctx context.Context) (LoggedInGuard, error) {
	cred, err := manager.CredentialForCurrentUser(ctx, nil)
	if err != nil {
		return LoggedInGuard{}, err
	}

	_, err = EnsureLoggedInCredential(ctx, cred, manager.cloud)
	if err != nil {
		return LoggedInGuard{}, err
	}

	return LoggedInGuard{}, nil
}
