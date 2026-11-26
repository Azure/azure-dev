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

// AuthStatusReason explains an unauthenticated result without exposing error details.
type AuthStatusReason string

const (
	AuthStatusReasonNotLoggedIn        AuthStatusReason = "not_logged_in"
	AuthStatusReasonLoginRequired      AuthStatusReason = "login_required"
	AuthStatusReasonVerificationFailed AuthStatusReason = "verification_failed"
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
	Status AuthStatus `json:"status"`

	// Reason is set only when the status is unauthenticated.
	Reason AuthStatusReason `json:"reason,omitempty"`

	// The type of account signed in.
	Type AccountType `json:"type,omitempty"`

	// The email of the signed-in user. Only set when Type is AccountTypeUser.
	Email string `json:"email,omitempty"`

	// The client ID of the service principal. Only set when Type is AccountTypeServicePrincipal.
	ClientID string `json:"clientId,omitempty"`

	// When authenticated, the time at which the current access token expires.
	ExpiresOn *RFC3339Time `json:"expiresOn,omitempty"`
}
