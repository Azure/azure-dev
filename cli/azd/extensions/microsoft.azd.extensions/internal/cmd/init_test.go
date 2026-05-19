// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
)

func TestCapabilityPromptChoicesMatchValidCapabilities(t *testing.T) {
	choices := capabilityPromptChoices()
	require.Len(t, choices, len(extensions.ValidCapabilities))

	for i, capability := range extensions.ValidCapabilities {
		require.Equal(t, string(capability), choices[i].Value)
		require.NotEmpty(t, choices[i].Label)
	}
}

func TestCapabilityLabel(t *testing.T) {
	require.Equal(t, "Custom Commands", capabilityLabel(extensions.CustomCommandCapability))
	require.Equal(t, "MCP Server", capabilityLabel(extensions.McpServerCapability))
	require.Equal(t, "Provisioning Provider", capabilityLabel(extensions.ProvisioningProviderCapability))
}
