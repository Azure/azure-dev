// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package connections

// Connection represents a Foundry project connection from the data-plane API.
type Connection struct {
	Name        string               `json:"name"`
	ID          string               `json:"id"`
	Type        string               `json:"type"`
	Target      string               `json:"target"`
	IsDefault   bool                 `json:"isDefault"`
	Credentials *ConnectionCredentials `json:"credentials,omitempty"`
	Metadata    map[string]string    `json:"metadata,omitempty"`
}

// ConnectionCredentials holds credential values returned by the data-plane
// getConnectionWithCredentials endpoint. The shape varies by auth type:
//   - ApiKey:     Key is populated
//   - CustomKeys: CustomKeys map is populated
//   - AAD/None:   Only Type is populated, no secret values
type ConnectionCredentials struct {
	Type       string            `json:"type"`
	Key        string            `json:"key,omitempty"`
	CustomKeys map[string]string `json:"keys,omitempty"`
}

// PagedConnection represents a paged collection of connections.
type PagedConnection struct {
	Value    []Connection `json:"value"`
	NextLink *string      `json:"nextLink,omitempty"`
}
