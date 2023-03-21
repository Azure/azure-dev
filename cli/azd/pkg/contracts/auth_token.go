// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package contracts

import (
	"encoding/json"
	"fmt"
	"time"
)

// AuthTokenResult is the value returned by `azd get-access-token`. It matches the shape of `azcore.AccessToken`
type AuthTokenResult struct {
	// Token is the opaque access token, which may be provided to an Azure service.
	Token string `json:"token"`
	// ExpiresOn is the time at which the token is no longer valid. The time is a quoted string in
	// RFC 3339 format.
	ExpiresOn RFC3339Time `json:"expiresOn"`
}

// RFC3339Time is a time.Time that uses time.RFC3339 format when marshaling to JSON, not time.RFC3339Nano as
// the standard library time.Time does.
type RFC3339Time time.Time

func (r RFC3339Time) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"%s"`, time.Time(r).Format(time.RFC3339))), nil
}

func (r *RFC3339Time) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return err
	}

	*((*time.Time)(r)) = t
	return nil
}
