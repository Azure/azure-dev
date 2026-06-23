// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequiredPlugins locks the azure-skills plugin that every Copilot
// session bootstraps (see ensureRequiredPlugins in copilot_agent.go).
// The Name must match the installed plugin name the agent dedupes on
// (`azure`), and the Source must reference the azure-skills plugin
// directory consumed by `copilot plugin install <source>`.
func TestRequiredPlugins(t *testing.T) {
	require.Len(t, requiredPlugins, 1)

	azure := requiredPlugins[0]
	assert.Equal(t, "azure", azure.Name,
		"the agent dedupes installed plugins on Name; it must match the "+
			"plugin's manifest name (.plugin/plugin.json -> name: \"azure\")",
	)
	assert.Equal(t,
		"microsoft/azure-skills:.github/plugins/azure-skills",
		azure.Source,
		"Source must point at the azure-skills Copilot plugin directory",
	)
}
