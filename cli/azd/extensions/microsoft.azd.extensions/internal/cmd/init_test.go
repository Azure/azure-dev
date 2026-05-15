// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/assert"
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

func TestNamespaceCommandPath(t *testing.T) {
	tests := []struct {
		namespace string
		want      string
	}{
		{namespace: "demo", want: "demo"},
		{namespace: "ai.project", want: "ai project"},
		{namespace: "company.team.tool", want: "company team tool"},
		{namespace: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			assert.Equal(t, tt.want, namespaceCommandPath(tt.namespace))
		})
	}
}

func TestFormatUsage(t *testing.T) {
	assert.Equal(t, "azd demo <command> [options]", formatUsage("demo"))
	assert.Equal(t, "azd ai project <command> [options]", formatUsage("ai.project"))
}

func TestValidateExtensionMetadata(t *testing.T) {
	tests := []struct {
		name         string
		schema       *models.ExtensionSchema
		wantWarnings []string
		wantErrors   []string
	}{
		{
			name: "complete schema produces no warnings or errors",
			schema: &models.ExtensionSchema{
				Id:           "test.extension",
				Version:      "0.0.1",
				DisplayName:  "Test Extension",
				Description:  "A test extension",
				Namespace:    "test",
				Usage:        "azd test <command>",
				Capabilities: []extensions.CapabilityType{extensions.CustomCommandCapability},
			},
		},
		{
			name:   "empty schema reports all required-field errors",
			schema: &models.ExtensionSchema{},
			wantWarnings: []string{
				"Missing 'usage' field in extension.yaml - shown to users as a usage hint in 'azd <namespace> --help'.",
			},
			wantErrors: []string{
				"Missing required field: id",
				"Missing required field: version",
				"Missing required field: capabilities",
				"Missing required field: displayName",
				"Missing required field: description",
			},
		},
		{
			name: "service target provider without providers emits warning",
			schema: &models.ExtensionSchema{
				Id:           "test.extension",
				Version:      "0.0.1",
				DisplayName:  "Test Extension",
				Description:  "A test extension",
				Namespace:    "test",
				Usage:        "azd test <command>",
				Capabilities: []extensions.CapabilityType{extensions.ServiceTargetProviderCapability},
			},
			wantWarnings: []string{
				"Missing 'providers' field in extension.yaml - " +
					"required by the 'service-target-provider' capability. " +
					"List the providers your extension contributes (each entry needs a name, type, and description).",
			},
		},
		{
			name: "custom commands without namespace emits warning",
			schema: &models.ExtensionSchema{
				Id:           "test.extension",
				Version:      "0.0.1",
				DisplayName:  "Test Extension",
				Description:  "A test extension",
				Usage:        "azd test <command>",
				Capabilities: []extensions.CapabilityType{extensions.CustomCommandCapability},
			},
			wantWarnings: []string{
				"Missing 'namespace' field in extension.yaml - " +
					"required by the 'custom-commands' capability. " +
					"Set it to the prefix users will type after 'azd' (e.g. 'demo' to expose 'azd demo <command>').",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, errs := validateExtensionMetadata(tt.schema)
			assert.Equal(t, tt.wantWarnings, warnings)
			assert.Equal(t, tt.wantErrors, errs)
		})
	}
}
