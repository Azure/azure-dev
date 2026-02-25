// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/mcp"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestSessionConfigBuilder_Build(t *testing.T) {
	t.Run("EmptyConfig", func(t *testing.T) {
		ucm := &mockUserConfigManager{
			config: config.NewConfig(nil),
		}
		builder := NewSessionConfigBuilder(ucm)

		cfg, err := builder.Build(context.Background(), nil)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.True(t, cfg.Streaming)
		require.Empty(t, cfg.Model)
	})

	t.Run("ModelFromConfig", func(t *testing.T) {
		c := config.NewConfig(nil)
		_ = c.Set("ai.agent.model", "gpt-4.1")

		ucm := &mockUserConfigManager{config: c}
		builder := NewSessionConfigBuilder(ucm)

		cfg, err := builder.Build(context.Background(), nil)
		require.NoError(t, err)
		require.Equal(t, "gpt-4.1", cfg.Model)
	})

	t.Run("SystemMessage", func(t *testing.T) {
		c := config.NewConfig(nil)
		_ = c.Set("ai.agent.systemMessage", "Use TypeScript")

		ucm := &mockUserConfigManager{config: c}
		builder := NewSessionConfigBuilder(ucm)

		cfg, err := builder.Build(context.Background(), nil)
		require.NoError(t, err)
		require.NotNil(t, cfg.SystemMessage)
		require.Equal(t, "append", cfg.SystemMessage.Mode)
		require.Equal(t, "Use TypeScript", cfg.SystemMessage.Content)
	})

	t.Run("ToolControl", func(t *testing.T) {
		c := config.NewConfig(nil)
		_ = c.Set("ai.agent.tools.available", []any{"read_file", "write_file"})
		_ = c.Set("ai.agent.tools.excluded", []any{"execute_command"})

		ucm := &mockUserConfigManager{config: c}
		builder := NewSessionConfigBuilder(ucm)

		cfg, err := builder.Build(context.Background(), nil)
		require.NoError(t, err)
		require.Equal(t, []string{"read_file", "write_file"}, cfg.AvailableTools)
		require.Equal(t, []string{"execute_command"}, cfg.ExcludedTools)
	})

	t.Run("MergesMCPServers", func(t *testing.T) {
		c := config.NewConfig(nil)
		ucm := &mockUserConfigManager{config: c}
		builder := NewSessionConfigBuilder(ucm)

		builtIn := map[string]*mcp.ServerConfig{
			"azd": {
				Type:    "stdio",
				Command: "azd",
				Args:    []string{"mcp", "start"},
			},
		}

		cfg, err := builder.Build(context.Background(), builtIn)
		require.NoError(t, err)
		require.Len(t, cfg.MCPServers, 1)
		require.Contains(t, cfg.MCPServers, "azd")
	})

	t.Run("UserMCPServersOverrideBuiltIn", func(t *testing.T) {
		c := config.NewConfig(nil)
		_ = c.Set("ai.agent.mcp.servers", map[string]any{
			"azd": map[string]any{
				"type":    "stdio",
				"command": "/custom/azd",
				"args":    []any{"custom-mcp"},
			},
			"custom": map[string]any{
				"type": "http",
				"url":  "https://mcp.example.com",
			},
		})

		ucm := &mockUserConfigManager{config: c}
		builder := NewSessionConfigBuilder(ucm)

		builtIn := map[string]*mcp.ServerConfig{
			"azd": {
				Type:    "stdio",
				Command: "azd",
				Args:    []string{"mcp", "start"},
			},
		}

		cfg, err := builder.Build(context.Background(), builtIn)
		require.NoError(t, err)
		require.Len(t, cfg.MCPServers, 2)

		// User config overrides built-in "azd"
		azdServer := cfg.MCPServers["azd"]
		require.Equal(t, "/custom/azd", azdServer["command"])

		// User adds new "custom" server
		customServer := cfg.MCPServers["custom"]
		require.Equal(t, "http", customServer["type"])
	})
}

func TestConvertServerConfig(t *testing.T) {
	t.Run("StdioServer", func(t *testing.T) {
		srv := &mcp.ServerConfig{
			Type:    "stdio",
			Command: "npx",
			Args:    []string{"-y", "@azure/mcp@latest"},
			Env:     []string{"KEY=VALUE", "OTHER=test"},
		}

		result := convertServerConfig(srv)
		require.Equal(t, "stdio", result["type"])
		require.Equal(t, "npx", result["command"])
		require.Equal(t, []string{"-y", "@azure/mcp@latest"}, result["args"])

		envMap, ok := result["env"].(map[string]string)
		require.True(t, ok)
		require.Equal(t, "VALUE", envMap["KEY"])
		require.Equal(t, "test", envMap["OTHER"])
	})

	t.Run("HttpServer", func(t *testing.T) {
		srv := &mcp.ServerConfig{
			Type: "http",
			Url:  "https://example.com/mcp",
		}

		result := convertServerConfig(srv)
		require.Equal(t, "http", result["type"])
		require.Equal(t, "https://example.com/mcp", result["url"])
	})
}

type mockUserConfigManager struct {
	config config.Config
}

func (m *mockUserConfigManager) Load() (config.Config, error) {
	return m.config, nil
}

func (m *mockUserConfigManager) Save(_ config.Config) error {
	return nil
}
