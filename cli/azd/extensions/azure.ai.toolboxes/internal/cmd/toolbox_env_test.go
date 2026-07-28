// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolboxProjectEndpoint(t *testing.T) {
	t.Parallel()
	require.Equal(t,
		"https://account.services.ai.azure.com/api/projects/project",
		toolboxProjectEndpoint(
			"https://account.services.ai.azure.com/api/projects/project/toolboxes/tools/versions/1/mcp?api-version=v1",
		),
	)
	require.Empty(t, toolboxProjectEndpoint("https://example.com/mcp"))
}

func TestClearToolboxMarkers(t *testing.T) {
	values := map[string]string{
		"TOOLBOX_TOOLS_MCP_ENDPOINT":     "https://example/mcp",
		"TOOLBOX_TOOLS_PROJECT_ENDPOINT": "https://example/project",
	}
	err := clearToolboxMarkers("tools", func(key, value string) error {
		values[key] = value
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, values["TOOLBOX_TOOLS_MCP_ENDPOINT"])
	require.Empty(t, values["TOOLBOX_TOOLS_PROJECT_ENDPOINT"])
}
