// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package contracts

import "time"

// PrincipalType represents the type of principal
type PrincipalType string

const (
	UserPrincipalType             PrincipalType = "User"
	ServicePrincipalPrincipalType PrincipalType = "ServicePrincipal"
)

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
	// When status is `LoginStatusSuccess`, the account name the user is logged in as.
	Account *string `json:"account,omitempty"`
	// When status is `LoginStatusSuccess`, the type of login (User or ServicePrincipal).
	LoginType *PrincipalType `json:"loginType,omitempty"`
}
