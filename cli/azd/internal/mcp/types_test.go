// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMcpConfig_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		config McpConfig
	}{
		{
			name:   "empty config",
			config: McpConfig{},
		},
		{
			name: "config with stdio server",
			config: McpConfig{
				Servers: map[string]*ServerConfig{
					"my-server": {
						Type:    "stdio",
						Command: "/usr/bin/my-mcp-server",
						Args:    []string{"--port", "8080"},
						Env:     []string{"KEY=VALUE"},
					},
				},
			},
		},
		{
			name: "config with http server",
			config: McpConfig{
				Servers: map[string]*ServerConfig{
					"http-server": {
						Type: "http",
						Url:  "http://localhost:3000",
					},
				},
			},
		},
		{
			name: "config with multiple servers",
			config: McpConfig{
				Servers: map[string]*ServerConfig{
					"server-a": {
						Type:    "stdio",
						Command: "cmd-a",
					},
					"server-b": {
						Type: "http",
						Url:  "http://example.com",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.config)
			require.NoError(t, err)

			var decoded McpConfig
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.config, decoded)
		})
	}
}

func TestMcpConfig_JSONDeserialization(t *testing.T) {
	raw := `{
		"servers": {
			"test": {
				"type": "stdio",
				"url": "",
				"command": "my-cmd",
				"args": ["--flag"],
				"env": ["FOO=BAR"]
			}
		}
	}`

	var config McpConfig
	err := json.Unmarshal([]byte(raw), &config)
	require.NoError(t, err)

	require.Contains(t, config.Servers, "test")
	srv := config.Servers["test"]
	assert.Equal(t, "stdio", srv.Type)
	assert.Equal(t, "my-cmd", srv.Command)
	assert.Equal(t, []string{"--flag"}, srv.Args)
	assert.Equal(t, []string{"FOO=BAR"}, srv.Env)
}

func TestServerConfig_OmitsEmptyOptionalFields(t *testing.T) {
	srv := ServerConfig{
		Type:    "http",
		Url:     "http://localhost",
		Command: "",
	}

	data, err := json.Marshal(srv)
	require.NoError(t, err)

	var decoded map[string]any
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	// Args and Env should be omitted when empty
	_, hasArgs := decoded["args"]
	_, hasEnv := decoded["env"]
	assert.False(t, hasArgs, "empty args should be omitted")
	assert.False(t, hasEnv, "empty env should be omitted")
}

func TestCapabilities_ZeroValue(t *testing.T) {
	cap := Capabilities{}
	assert.Nil(t, cap.Sampling)
	assert.Nil(t, cap.Elicitation)
}

func TestCapabilities_WithHandlers(t *testing.T) {
	sampling := NewProxySamplingHandler()
	elicitation := NewProxyElicitationHandler()

	cap := Capabilities{
		Sampling:    sampling,
		Elicitation: elicitation,
	}

	assert.NotNil(t, cap.Sampling)
	assert.NotNil(t, cap.Elicitation)
}
