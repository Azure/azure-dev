// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
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

	// Structured error must carry the Compatibility category (feature
	// not implemented in this version) + stable code so telemetry and
	// downstream classifiers can group on it. NOT Validation: the user
	// provided no invalid input.
	var local *azdext.LocalError
	require.True(t, errors.As(err, &local), "expected *azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodePreviewNotImplemented, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryCompatibility, local.Category)
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

func TestEncodeParamValue(t *testing.T) {
	// encodeParamValue is the seam shared by armOutputsToProto and
	// armInputsToProto. Each row is one ARM-shaped value the converter
	// might see and the expected wire string. Non-string values must be
	// JSON-encoded so arrays/objects survive the round trip intact -
	// Go's default %v collapses ["a","b"] to "[a b]" which is unparseable
	// downstream.
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "nil becomes empty string", in: nil, want: ""},
		{name: "string passes through", in: "hello", want: "hello"},
		{name: "string with quotes passes through verbatim", in: `a"b`, want: `a"b`},
		{name: "bool encoded as JSON literal", in: true, want: "true"},
		{name: "integer encoded as JSON number", in: 42, want: "42"},
		{name: "float encoded as JSON number", in: 3.14, want: "3.14"},
		{name: "string slice encoded as JSON array", in: []any{"a", "b", "c"}, want: `["a","b","c"]`},
		{
			name: "object encoded as JSON object",
			in:   map[string]any{"k": "v", "n": 1.0},
			want: `{"k":"v","n":1}`,
		},
		{name: "empty array", in: []any{}, want: "[]"},
		{name: "empty object", in: map[string]any{}, want: "{}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeParamValue(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestArmOutputsToProto_JSONEncodesNonStrings(t *testing.T) {
	// ARM returns outputs as {type, value}. Verify that a future
	// `output myArr array = [...]` survives the converter intact: the
	// value field must be valid JSON so downstream consumers can parse
	// it back into a slice. Pre-fix this collapsed to "[a b c]".
	in := map[string]any{
		"projectEndpoint": map[string]any{"type": "String", "value": "https://x.azure.com"},
		"modelDeployments": map[string]any{
			"type":  "Array",
			"value": []any{"gpt-4", "gpt-4o"},
		},
		"projectMetadata": map[string]any{
			"type":  "Object",
			"value": map[string]any{"name": "p", "rg": "rg-1"},
		},
		"emptyValue": map[string]any{"type": "String", "value": nil},
	}
	got := armOutputsToProto(in)

	require.Contains(t, got, "projectEndpoint")
	assert.Equal(t, "https://x.azure.com", got["projectEndpoint"].Value)

	require.Contains(t, got, "modelDeployments")
	assert.Equal(t, `["gpt-4","gpt-4o"]`, got["modelDeployments"].Value,
		"array outputs must be JSON-encoded, not %%v-formatted")

	require.Contains(t, got, "projectMetadata")
	// JSON object keys come out in some deterministic order; assert via
	// re-parse rather than string equality so the test isn't brittle.
	var roundTrip map[string]any
	require.NoError(t, json.Unmarshal([]byte(got["projectMetadata"].Value), &roundTrip))
	assert.Equal(t, "p", roundTrip["name"])
	assert.Equal(t, "rg-1", roundTrip["rg"])

	require.Contains(t, got, "emptyValue")
	assert.Equal(t, "", got["emptyValue"].Value)
}

func TestArmInputsToProto_JSONEncodesNonStrings(t *testing.T) {
	// Same contract as armOutputsToProto but on the inputs side.
	in := map[string]any{
		"location":    map[string]any{"value": "eastus"},
		"includeAcr":  map[string]any{"value": true},
		"deployments": map[string]any{"value": []any{map[string]any{"name": "gpt-4"}}},
	}
	got := armInputsToProto(in)

	assert.Equal(t, "eastus", got["location"].Value)
	assert.Equal(t, "true", got["includeAcr"].Value)
	assert.Equal(t, `[{"name":"gpt-4"}]`, got["deployments"].Value)
}

func TestParameters_NilSafeOnMissingSynthResult(t *testing.T) {
	// Parameters is part of the gRPC contract; calling it before
	// Initialize succeeded must NOT panic on nil synthResult. Instead
	// return a structured Internal error so the host has something
	// actionable to surface.
	p := &FoundryProvisioningProvider{} // synthResult left nil
	_, err := p.Parameters(t.Context())
	require.Error(t, err)
	var local *azdext.LocalError
	require.True(t, errors.As(err, &local), "expected *azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeInvalidServiceConfig, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryInternal, local.Category)
	assert.Contains(t, local.Message, "before successful Initialize")
}

func TestArmParameters_NilSafeOnMissingSynthResult(t *testing.T) {
	// Internal helper, but defense in depth: Deploy is supposed to be
	// the only caller and is only reachable after Initialize. Still,
	// nil synthResult must not panic; it just means no synthesizer-
	// derived parameters are merged in.
	p := &FoundryProvisioningProvider{
		location:    "eastus",
		envName:     "dev",
		foundryName: "fp",
		principalID: "pid",
		// synthResult intentionally nil
	}
	out := p.armParameters() // must not panic
	require.Contains(t, out, "location")
	require.Contains(t, out, "foundryProjectName")
	require.NotContains(t, out, "includeAcr",
		"synthesizer-derived parameters should be absent when synthResult is nil")
}

func TestDestroy_RefusesWithoutForce(t *testing.T) {
	// Destroy must NEVER silently delete (or, worse, silently leak)
	// resources. Without --force the user gets a structured error
	// telling them exactly what would have been deleted and how to
	// confirm it. This is the bug we fixed: prior behavior was to
	// delete only the deployment record and return success, leaving
	// the Foundry account + ACR + role assignments live with no warning.
	p := &FoundryProvisioningProvider{
		rgName: "rg-foundry-test",
	}
	_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: false}, func(string) {})
	require.Error(t, err)
	var local *azdext.LocalError
	require.True(t, errors.As(err, &local), "expected *azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeDestroyRequiresForce, local.Code)
	assert.Equal(t, azdext.LocalErrorCategoryValidation, local.Category)
	// Message must name the RG so the user knows exactly what would be deleted.
	assert.Contains(t, local.Message, "rg-foundry-test")
	// Suggestion must point at the actual fix.
	assert.Contains(t, local.Suggestion, "--force")
}

func TestFindFoundryService_DependencyCategory(t *testing.T) {
	// Missing service in azure.yaml is a missing-dependency error, not
	// a validation error (the yaml parses fine). Telemetry classifiers
	// differentiate these; the wrong category buckets misconfigurations
	// alongside actual malformed yaml.
	_, err := findFoundryService([]byte("name: x\nservices:\n  web:\n    host: containerapp\n"))
	require.Error(t, err)
	var local *azdext.LocalError
	require.True(t, errors.As(err, &local))
	assert.Equal(t, azdext.LocalErrorCategoryDependency, local.Category,
		"missing foundry service is a Dependency, not a Validation")
}
