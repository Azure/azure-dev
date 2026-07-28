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
