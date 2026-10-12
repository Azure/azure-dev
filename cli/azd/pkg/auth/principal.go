// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"fmt"
)

// PrincipalType is the identity type used for Azure role assignments.
type PrincipalType string

const (
	// UserPrincipalType identifies an interactive user.
	UserPrincipalType PrincipalType = "User"
	// ServicePrincipalType identifies an application or managed identity.
	ServicePrincipalType PrincipalType = "ServicePrincipal"
)

// CurrentPrincipalType returns the principal type from the recorded login details.
func (m *Manager) CurrentPrincipalType(ctx context.Context) (PrincipalType, error) {
	loginDetails, err := m.LogInDetails(ctx)
	if err != nil {
		return "", fmt.Errorf("fetching login details: %w", err)
	}

	if loginDetails.LoginType == ClientIdLoginType {
		return ServicePrincipalType, nil
	}

	return UserPrincipalType, nil
}
