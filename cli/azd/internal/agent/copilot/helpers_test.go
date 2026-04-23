// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestIndexOf(t *testing.T) {
	tests := []struct {
		name string
		s    string
		c    byte
		want int
	}{
		{"Found", "KEY=VALUE", '=', 3},
		{"NotFound", "KEYVALUE", '=', -1},
		{"Empty", "", '=', -1},
		{"FirstChar", "=value", '=', 0},
		{"LastChar", "key=", '=', 3},
		{"MultipleOccurrences", "a=b=c", '=', 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, indexOf(tt.s, tt.c))
		})
	}
}

func TestGetStringSliceFromConfig(t *testing.T) {
	t.Run("NotPresent", func(t *testing.T) {
		c := config.NewConfig(nil)
		result := getStringSliceFromConfig(c, "missing.key")
		require.Nil(t, result)
	})

	t.Run("ValidStrings", func(t *testing.T) {
		c := config.NewConfig(nil)
		_ = c.Set("tools", []any{"a", "b", "c"})
		result := getStringSliceFromConfig(c, "tools")
		require.Equal(t, []string{"a", "b", "c"}, result)
	})

	t.Run("MixedTypesFiltered", func(t *testing.T) {
		c := config.NewConfig(nil)
		_ = c.Set("tools", []any{"a", 42, "", "b", nil})
		result := getStringSliceFromConfig(c, "tools")
		// Empty strings and non-strings are filtered
		require.Equal(t, []string{"a", "b"}, result)
	})

	t.Run("EmptySlice", func(t *testing.T) {
		c := config.NewConfig(nil)
		_ = c.Set("tools", []any{})
		result := getStringSliceFromConfig(c, "tools")
		require.Empty(t, result)
	})
}

func TestGetUserMCPServers(t *testing.T) {
	t.Run("NoServers", func(t *testing.T) {
		c := config.NewConfig(nil)
		result := getUserMCPServers(c)
		require.Nil(t, result)
	})

	t.Run("WithServers", func(t *testing.T) {
		c := config.NewConfig(nil)
		_ = c.Set(ConfigKeyMCPServers, map[string]any{
			"myServer": map[string]any{
				"type":  "http",
				"url":   "https://example.com",
				"tools": []any{"*"},
			},
		})
		result := getUserMCPServers(c)
		require.Len(t, result, 1)
		require.Equal(t, "http", result["myServer"]["type"])
		require.Equal(
			t, "https://example.com", result["myServer"]["url"],
		)
	})

	t.Run("EmptyMap", func(t *testing.T) {
		c := config.NewConfig(nil)
		_ = c.Set(ConfigKeyMCPServers, map[string]any{})
		result := getUserMCPServers(c)
		require.Nil(t, result)
	})
}

func TestCopilotClientManager_StopNilClient(t *testing.T) {
	mgr := NewCopilotClientManager(nil, nil)
	// Stop with nil client should not error
	err := mgr.Stop()
	require.NoError(t, err)
}

func TestCopilotClientManager_ClientAccessor(t *testing.T) {
	mgr := NewCopilotClientManager(nil, nil)
	// Before Start, Client() returns nil
	require.Nil(t, mgr.Client())
}

func TestCopilotClientManager_OptionsDefaults(t *testing.T) {
	mgr := NewCopilotClientManager(nil, nil)
	require.NotNil(t, mgr.options)
	require.Empty(t, mgr.options.LogLevel)
	require.Empty(t, mgr.options.CLIPath)
}

func TestCopilotClientManager_WithCLIPath(t *testing.T) {
	mgr := NewCopilotClientManager(
		&CopilotClientOptions{CLIPath: "/custom/path"},
		nil,
	)
	require.Equal(t, "/custom/path", mgr.options.CLIPath)
}
