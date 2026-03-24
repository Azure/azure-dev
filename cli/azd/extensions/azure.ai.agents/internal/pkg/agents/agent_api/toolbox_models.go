// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import "encoding/json"

// ToolboxAPIVersion is the API version for toolset operations.
const ToolboxAPIVersion = "v1"

// ToolboxFeatureHeader is the required preview feature flag header for toolset operations.
const ToolboxFeatureHeader = "Toolsets=V1Preview"

// ToolboxObject represents a toolset returned by the Foundry Toolsets API.
type ToolboxObject struct {
	Object      string            `json:"object"`
	ID          string            `json:"id"`
	CreatedAt   int64             `json:"created_at"`
	UpdatedAt   int64             `json:"updated_at"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Tools       []json.RawMessage `json:"tools"`
}

// ToolboxList represents the paginated list response from the Toolsets API.
type ToolboxList struct {
	Data []ToolboxObject `json:"data"`
}

// DeleteToolboxResponse represents the response from deleting a toolset.
type DeleteToolboxResponse struct {
	Object  string `json:"object"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// CreateToolboxRequest represents the request body for creating a toolset.
type CreateToolboxRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Tools       []json.RawMessage `json:"tools"`
}

// UpdateToolboxRequest represents the request body for updating a toolset.
type UpdateToolboxRequest struct {
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Tools       []json.RawMessage `json:"tools"`
}

// ToolSummary extracts a display-friendly summary from a raw tool JSON object.
// Returns the tool type and name (if available).
func ToolSummary(raw json.RawMessage) (toolType string, toolName string) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return "unknown", ""
	}

	if t, ok := m["type"]; ok {
		var s string
		if json.Unmarshal(t, &s) == nil {
			toolType = s
		}
	}
	if toolType == "" {
		toolType = "unknown"
	}

	// Try common name fields
	for _, key := range []string{"server_label", "name"} {
		if v, ok := m[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				toolName = s
				break
			}
		}
	}

	return toolType, toolName
}
