// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package contracts

import "time"

// AuthTokenResult is the value returned by `azd get-access-token`. It matches the shape of `azcore.AccessToken`
type AuthTokenResult struct {
	// Token is the opaque access token, which may be provided to an Azure service.
	Token string `json:"token"`
	// ExpiresOn is the time at which the token is no longer valid. The time is a quoted string in
	// RFC 3339 format, with sub-second precision added if present.
	ExpiresOn time.Time `json:"expiresOn"`
}
