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

// LoginResult is the contract for the output of `azd login`.
type LoginResult struct {
	// The result of checking for a valid access token.
	Status LoginStatus `json:"status"`
	// When status is `LoginStatusSuccess`, the time at which the access token
	// expires.
	ExpiresOn *time.Time `json:"expiresOn,omitempty"`
}
