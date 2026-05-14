// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package connections

// Connection represents a Foundry project connection from the data-plane API.
type Connection struct {
	Name        string                 `json:"name"`
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	Target      string                 `json:"target"`
	IsDefault   bool                   `json:"isDefault"`
	Credentials *ConnectionCredentials `json:"credentials,omitempty"`
	Metadata    map[string]string      `json:"metadata,omitempty"`
}

// ConnectionCredentials holds credential values returned by the data-plane
// getConnectionWithCredentials endpoint.
//
// The API returns credentials as a flat JSON object where "type" identifies
// the auth type and all other fields are credential key-value pairs:
//
//	ApiKey:     {"type": "ApiKey", "key": "abc123"}
//	CustomKeys: {"type": "CustomKeys", "my-secret": "val", "x-api-key": "val"}
//	AAD/None:  {"type": "AAD"} or {"type": "None"} — no secret fields
type ConnectionCredentials struct {
	Type       string            `json:"-"`
	Key        string            `json:"-"`
	CustomKeys map[string]string `json:"-"`
	// RawFields holds all fields from the JSON response for flexible access.
	RawFields map[string]string `json:"-"`
}

// ParseCredentials parses a raw credentials JSON object into a typed struct.
// The "type" field is extracted and remaining fields become either Key (for ApiKey)
// or CustomKeys entries.
func ParseCredentials(raw map[string]any) *ConnectionCredentials {
	if raw == nil {
		return nil
	}

	creds := &ConnectionCredentials{
		CustomKeys: make(map[string]string),
		RawFields:  make(map[string]string),
	}

	for k, v := range raw {
		strVal, ok := v.(string)
		if !ok {
			continue
		}

		switch k {
		case "type":
			creds.Type = strVal
		case "key":
			creds.Key = strVal
			creds.RawFields[k] = strVal
		default:
			creds.CustomKeys[k] = strVal
			creds.RawFields[k] = strVal
		}
	}

	return creds
}

// PagedConnection represents a paged collection of connections.
type PagedConnection struct {
	Value    []Connection `json:"value"`
	NextLink *string      `json:"nextLink,omitempty"`
}
