// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
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
		name                string
		schema              *models.ExtensionSchema
		wantWarningCount    int
		wantWarningContains []string
		wantErrorCount      int
		wantErrorContains   []string
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
			name:             "empty schema reports all required-field errors",
			schema:           &models.ExtensionSchema{},
			wantWarningCount: 1,
			wantWarningContains: []string{
				"Missing 'usage' field in extension.yaml",
			},
			wantErrorCount: 5,
			wantErrorContains: []string{
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
			wantWarningCount: 1,
			wantWarningContains: []string{
				"Missing 'providers' field in extension.yaml",
				"service-target-provider",
			},
		},
		{
			name: "custom commands without namespace is a fatal error",
			schema: &models.ExtensionSchema{
				Id:           "test.extension",
				Version:      "0.0.1",
				DisplayName:  "Test Extension",
				Description:  "A test extension",
				Usage:        "azd test <command>",
				Capabilities: []extensions.CapabilityType{extensions.CustomCommandCapability},
			},
			wantErrorCount: 1,
			wantErrorContains: []string{
				"Missing 'namespace' field in extension.yaml",
				"custom-commands",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, errs := validateExtensionMetadata(tt.schema)
			assert.Len(t, warnings, tt.wantWarningCount)
			for _, want := range tt.wantWarningContains {
				assert.True(
					t,
					slicesContainSubstring(warnings, want),
					"expected warning containing %q in %v", want, warnings,
				)
			}

			assert.Len(t, errs, tt.wantErrorCount)
			for _, want := range tt.wantErrorContains {
				assert.True(
					t,
					slicesContainSubstring(errs, want),
					"expected error containing %q in %v", want, errs,
				)
			}
		})
	}
}

func TestValidateExtensionNamespace(t *testing.T) {
	tests := []struct {
		namespace string
		wantErr   bool
	}{
		{namespace: "demo"},
		{namespace: "ai.project"},
		{namespace: "company1.team2.tool3"},
		{namespace: "", wantErr: true},
		{namespace: "a..b", wantErr: true},
		{namespace: ".demo", wantErr: true},
		{namespace: "demo.", wantErr: true},
		{namespace: "Demo", wantErr: true},
		{namespace: "demo-tool", wantErr: true},
		{namespace: "demo.tool_name", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			err := validateExtensionNamespace(tt.namespace)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestParseTags(t *testing.T) {
	tags, err := parseTags("alpha, beta,,gamma")
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, tags)

	// Boundary: exactly maxExtensionTags must succeed.
	boundary := make([]string, maxExtensionTags)
	for i := range maxExtensionTags {
		boundary[i] = fmt.Sprintf("tag%d", i)
	}
	tags, err = parseTags(strings.Join(boundary, ","))
	require.NoError(t, err)
	assert.Len(t, tags, maxExtensionTags)

	_, err = parseTags(strings.Join(append(boundary, "overflow"), ","))
	require.ErrorContains(t, err, "too many tags")

	_, err = parseTags(strings.Repeat("a", maxExtensionTagLength+1))
	require.ErrorContains(t, err, "too long")

	_, err = parseTags("valid,ba\nd")
	require.ErrorContains(t, err, "control characters")
}

func TestWriteCollectedWarnings(t *testing.T) {
	var buf bytes.Buffer
	writeCollectedWarnings(&buf, []string{"first warning", "second warning"})

	output := buf.String()
	assert.Contains(t, output, "Validation warnings:")
	assert.NotContains(t, output, "(!) Warning")
	assert.Contains(t, output, "  - first warning")
	assert.Contains(t, output, "  - second warning")
}

func TestWriteCommandOutputAddsMissingTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	writeCommandOutput(&buf, []byte("command output"))

	assert.Equal(t, "command output\n", buf.String())
}

func TestValidationWarningSummary(t *testing.T) {
	assert.Equal(t, "1 validation warning", validationWarningSummary([]string{"first"}))
	assert.Equal(t, "2 validation warnings", validationWarningSummary([]string{"first", "second"}))
}

func slicesContainSubstring(values []string, substring string) bool {
	for _, value := range values {
		if strings.Contains(value, substring) {
			return true
		}
	}

	return false
}
