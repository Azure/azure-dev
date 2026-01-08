// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package contracts

import "time"

// LoginStatus are the values of the "status" property of a LoginResult
type LoginStatus string

const (
	// The user is logged in and we were able to obtain an access token for them.
	// The "ExpiresOn" property of the result will contain information on when the
	// access token expires.
	LoginStatusSuccess LoginStatus = "success"
	// The user is not logged in.
	LoginStatusUnauthenticated LoginStatus = "unauthenticated"
)

// LoginResult is the contract for the output of `azd auth login`.
type LoginResult struct {
	// The result of checking for a valid access token.
	Status LoginStatus `json:"status"`
	// When status is `LoginStatusSuccess`, the time at which the access token
	// expires.
	ExpiresOn *time.Time `json:"expiresOn,omitempty"`
}

// AuthStatus represents the authentication state for `azd auth status`.
type AuthStatus string

const (
	AuthStatusAuthenticated   AuthStatus = "authenticated"
	AuthStatusUnauthenticated AuthStatus = "unauthenticated"
)

// AccountType represents the type of account signed in.
type AccountType string

const (
	// AccountTypeUser indicates a user account (email-based login).
	AccountTypeUser AccountType = "user"
	// AccountTypeServicePrincipal indicates a service principal (client ID-based login).
	AccountTypeServicePrincipal AccountType = "servicePrincipal"
)

// StatusResult is the contract for the output of `azd auth status`.
type StatusResult struct {
	// The authentication state.
	// When value is AuthStatusUnauthenticated, the user is not logged in and no other
	// properties will be set.
	Status AuthStatus `json:"status"`

	// The type of account signed in.
	Type AccountType `json:"type,omitempty"`

	// The email of the signed-in user. Only set when Type is AccountTypeUser.
	Email string `json:"email,omitempty"`

	// The client ID of the service principal. Only set when Type is AccountTypeServicePrincipal.
	ClientID string `json:"clientId,omitempty"`
}
