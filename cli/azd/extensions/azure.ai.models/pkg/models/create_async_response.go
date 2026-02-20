// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package models

// CreateAsyncResponse is the response from POST models/{name}/versions/{version}/createAsync.
// The server returns 202 Accepted with a Location header for polling.
type CreateAsyncResponse struct {
	Location string `json:"location"`
}
