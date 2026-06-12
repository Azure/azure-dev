// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindFoundryService(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    string
		wantErr bool
	}{
		{
			name: "single foundry service",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
`,
			want: "my-project",
		},
		{
			name: "foundry service alongside other hosts",
			yaml: `
services:
  webapp:
    host: containerapp
    project: src/web
  my-foundry:
    host: azure.ai.agent
`,
			want: "my-foundry",
		},
		{
			name: "no foundry service",
			yaml: `
services:
  webapp:
    host: containerapp
`,
			wantErr: true,
		},
		{
			name: "multiple foundry services rejected",
			yaml: `
services:
  a:
    host: azure.ai.agent
  b:
    host: azure.ai.agent
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findFoundryService([]byte(tt.yaml))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSanitizeFoundryName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "foundryproject"},
		{"dev", "dev"},
		{"my-project", "my-project"},
		{"MyProject", "myproject"},
		{"a", "aprj"},                 // too short -> padded
		{"my project!", "my-project"}, // spaces/symbols -> '-', trailing trimmed
		{"---", "prj"},                // trim outer hyphens then pad
		{"123456789012345678901234567890extra", "123456789012345678901234567890ex"}, // 32-char cap
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeFoundryName(tt.in))
		})
	}
}

func TestFoundryProvider_ImplementsContract(t *testing.T) {
	// Compile-time check is already in the package; runtime sanity check
	// guards against future signature drift in azdext.
	p := NewFoundryProvisioningProvider(nil)
	assert.NotNil(t, p)
}

func TestArmOutputsToProto(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		wantLen int
		wantKey string
		wantVal string
	}{
		{
			name: "nil yields empty map",
			in:   nil,
		},
		{
			name: "non-map yields empty map",
			in:   "scalar",
		},
		{
			name: "single string output",
			in: map[string]any{
				"FOUNDRY_PROJECT_ENDPOINT": map[string]any{
					"type":  "String",
					"value": "https://foo.services.ai.azure.com",
				},
			},
			wantLen: 1,
			wantKey: "FOUNDRY_PROJECT_ENDPOINT",
			wantVal: "https://foo.services.ai.azure.com",
		},
		{
			name: "skips malformed entries",
			in: map[string]any{
				"GOOD": map[string]any{"type": "String", "value": "ok"},
				"BAD":  "not a map",
			},
			wantLen: 1,
			wantKey: "GOOD",
			wantVal: "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := armOutputsToProto(tt.in)
			assert.Len(t, got, tt.wantLen)
			if tt.wantKey != "" {
				assert.Equal(t, tt.wantVal, got[tt.wantKey].Value)
			}
		})
	}
}

func TestArmInputsToProto(t *testing.T) {
	in := map[string]any{
		"location":   map[string]any{"value": "eastus"},
		"includeAcr": map[string]any{"value": true},
		"malformed":  "not a map",
	}
	got := armInputsToProto(in)
	assert.Equal(t, "eastus", got["location"].Value)
	assert.Equal(t, "true", got["includeAcr"].Value)
	assert.NotContains(t, got, "malformed")
}

func TestDeploymentName_StableForEnv(t *testing.T) {
	p := &FoundryProvisioningProvider{envName: "dev"}
	assert.Equal(t, "azd-foundry-dev", p.deploymentName())

	p.envName = "production"
	assert.Equal(t, "azd-foundry-production", p.deploymentName())
}

func TestPreview_NotImplemented(t *testing.T) {
	// Preview is intentionally stubbed: azd-core's extension preview
	// adapter does not yet render the extension's payload, so any what-if
	// output we produced would be silently dropped. Surface a clean
	// "not implemented" error instead of pretending the preview ran.
	p := &FoundryProvisioningProvider{}

	_, err := p.Preview(t.Context(), func(string) {})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented yet")
	assert.Contains(t, err.Error(), "microsoft.foundry")

	// Structured error must carry the validation category + stable code
	// so telemetry and downstream classifiers can group on it.
	var local *azdext.LocalError
	require.True(t, errors.As(err, &local), "expected *azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodePreviewNotImplemented, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, local.Category)
	assert.NotEmpty(t, local.Suggestion)
}

func TestDeploymentOutputsResources_NilSafe(t *testing.T) {
	assert.Nil(t, deploymentOutputs(nil))
	assert.Nil(t, deploymentResources(nil))

	props := &armresources.DeploymentPropertiesExtended{
		Outputs:         map[string]any{"K": "V"},
		OutputResources: []*armresources.ResourceReference{{}},
	}
	assert.NotNil(t, deploymentOutputs(props))
	assert.Len(t, deploymentResources(props), 1)
}
