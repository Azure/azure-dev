// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToolSummary(t *testing.T) {
	tests := []struct {
		name     string
		raw      json.RawMessage
		wantType string
		wantName string
	}{
		{
			name:     "mcp_server with server_label",
			raw:      json.RawMessage(`{"type":"mcp_server","server_label":"my-mcp"}`),
			wantType: "mcp_server",
			wantName: "my-mcp",
		},
		{
			name:     "openapi with name",
			raw:      json.RawMessage(`{"type":"openapi","name":"weather-api"}`),
			wantType: "openapi",
			wantName: "weather-api",
		},
		{
			name:     "server_label takes precedence over name",
			raw:      json.RawMessage(`{"type":"mcp_server","server_label":"label","name":"fallback"}`),
			wantType: "mcp_server",
			wantName: "label",
		},
		{
			name:     "type only no name",
			raw:      json.RawMessage(`{"type":"bing_grounding"}`),
			wantType: "bing_grounding",
			wantName: "",
		},
		{
			name:     "empty object",
			raw:      json.RawMessage(`{}`),
			wantType: "unknown",
			wantName: "",
		},
		{
			name:     "invalid json",
			raw:      json.RawMessage(`not-json`),
			wantType: "unknown",
			wantName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotName := ToolSummary(tt.raw)
			assert.Equal(t, tt.wantType, gotType)
			assert.Equal(t, tt.wantName, gotName)
		})
	}
}
