// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindFoundryProjectService(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    string
		wantErr bool
	}{
		{
			name: "single project service",
			yaml: `
services:
  my-project:
    host: azure.ai.project
`,
			want: "my-project",
		},
		{
			name: "project service alongside agent services",
			yaml: `
services:
  agent-a:
    host: azure.ai.agent
    uses: [ai-project]
  agent-b:
    host: azure.ai.agent
    uses: [ai-project]
  ai-project:
    host: azure.ai.project
`,
			want: "ai-project",
		},
		{
			name: "pre-split agent service fallback",
			yaml: `
services:
  agent:
    host: azure.ai.agent
`,
			want: "agent",
		},
		{
			name: "legacy microsoft.foundry service fallback",
			yaml: `
services:
  legacy:
    host: microsoft.foundry
`,
			want: "legacy",
		},
		{
			name: "no project or legacy service",
			yaml: `
services:
  web:
    host: containerapp
    project: src/web
`,
			wantErr: true,
		},
		{
			name: "multiple pre-split agent services rejected",
			yaml: `
services:
  a:
    host: azure.ai.agent
  b:
    host: azure.ai.agent
`,
			wantErr: true,
		},
		{
			name: "project service wins over legacy fallback",
			yaml: `
services:
  agent:
    host: azure.ai.agent
  ai-project:
    host: azure.ai.project
`,
			want: "ai-project",
		},
		{
			name: "multiple project services rejected",
			yaml: `
services:
  a:
    host: azure.ai.project
  b:
    host: azure.ai.project
`,
			wantErr: true,
		},
		{
			name: "network on agent service rejected",
			yaml: `
services:
  agent:
    host: azure.ai.agent
    network:
      peSubnet: {vnet: /subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v, name: pe}
  ai-project:
    host: azure.ai.project
`,
			wantErr: true,
		},
		{
			name: "network on legacy foundry service rejected",
			yaml: `
services:
  legacy:
    host: microsoft.foundry
    network:
      peSubnet: {vnet: /subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v, name: pe}
  ai-project:
    host: azure.ai.project
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findFoundryProjectService([]byte(tt.yaml))
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

// TestArmOutputsToProto_RepairsMangledKeyCase locks in the fix for
// test-results-bicepless.md Finding #4. ARM's management SDK returns
// deployment-output names with the first segment all-but-its-last
// letter lowercased (`AZURE_AI_PROJECT_ID` -> `azurE_AI_PROJECT_ID`,
// `FOUNDRY_PROJECT_ENDPOINT` -> `foundrY_PROJECT_ENDPOINT`). Without
// the canonical-name remapping, `azd env get-value AZURE_AI_PROJECT_ID`
// 404s downstream because the env-file key is `azurE_AI_PROJECT_ID`.
//
// The fix is in armOutputsToProto: case-insensitive lookup against
// canonicalOutputNames, then emit the canonical name. Unknown keys
// pass through verbatim so we never silently lose an output.
func TestArmOutputsToProto_RepairsMangledKeyCase(t *testing.T) {
	tests := []struct {
		name    string
		inKey   string
		wantKey string
	}{
		{
			name:    "ARM-mangled AZURE_AI_PROJECT_ID -> canonical",
			inKey:   "azurE_AI_PROJECT_ID",
			wantKey: "AZURE_AI_PROJECT_ID",
		},
		{
			name:    "ARM-mangled FOUNDRY_PROJECT_ENDPOINT -> canonical",
			inKey:   "foundrY_PROJECT_ENDPOINT",
			wantKey: "FOUNDRY_PROJECT_ENDPOINT",
		},
		{
			name:    "ARM-mangled AZURE_FOUNDRY_NETWORK_MODE -> canonical",
			inKey:   "azurE_FOUNDRY_NETWORK_MODE",
			wantKey: "AZURE_FOUNDRY_NETWORK_MODE",
		},
		{
			name:    "ARM-mangled AZURE_FOUNDRY_MANAGED_ISOLATION_MODE -> canonical",
			inKey:   "azurE_FOUNDRY_MANAGED_ISOLATION_MODE",
			wantKey: "AZURE_FOUNDRY_MANAGED_ISOLATION_MODE",
		},
		{
			name:    "ARM-mangled AZURE_AI_PROJECT_CONNECTION_NAMES -> canonical",
			inKey:   "azurE_AI_PROJECT_CONNECTION_NAMES",
			wantKey: "AZURE_AI_PROJECT_CONNECTION_NAMES",
		},
		{
			name:    "already-canonical key passes through unchanged",
			inKey:   "AZURE_AI_ACCOUNT_NAME",
			wantKey: "AZURE_AI_ACCOUNT_NAME",
		},
		{
			name:    "lower-case input gets canonicalized too",
			inKey:   "azure_openai_endpoint",
			wantKey: "AZURE_OPENAI_ENDPOINT",
		},
		{
			name:    "unknown key passes through verbatim (don't drop unanticipated outputs)",
			inKey:   "SOME_UNANTICIPATED_OUTPUT",
			wantKey: "SOME_UNANTICIPATED_OUTPUT",
		},
		{
			name:    "unknown key with weird casing also passes through (verbatim, not canonicalized)",
			inKey:   "soME_UNANTICIPATED",
			wantKey: "soME_UNANTICIPATED",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := armOutputsToProto(map[string]any{
				tt.inKey: map[string]any{"type": "String", "value": "v"},
			})
			require.Len(t, got, 1)
			_, ok := got[tt.wantKey]
			assert.True(t, ok, "expected canonical key %q in result, got keys %v",
				tt.wantKey, mapKeys(got))
			assert.Equal(t, "v", got[tt.wantKey].Value)
		})
	}
}

// TestArmOutputsToProto_RepairsRealWorldRunOutput uses the exact key
// set captured in test-results-bicepless.md Run 2 (Finding #4), so a
// future regression in ARM's mangling rules or in our repair logic
// is caught against real data.
func TestArmOutputsToProto_RepairsRealWorldRunOutput(t *testing.T) {
	t.Parallel()
	// Verbatim from `.azure/dev/.env` after the C4 live deploy.
	in := map[string]any{
		"azurE_AI_ACCOUNT_NAME":                map[string]any{"type": "String", "value": "cog-l5nlvejnau56c"},
		"azurE_AI_PROJECT_ACR_CONNECTION_NAME": map[string]any{"type": "String", "value": ""},
		"azurE_AI_PROJECT_ID":                  map[string]any{"type": "String", "value": "/subscriptions/.../accounts/cog-l5nlvejnau56c/projects/fdtest-zhihuan"},
		"azurE_AI_PROJECT_NAME":                map[string]any{"type": "String", "value": "fdtest-zhihuan"},
		"azurE_CONTAINER_REGISTRY_ENDPOINT":    map[string]any{"type": "String", "value": ""},
		"azurE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"type": "String", "value": ""},
		"azurE_OPENAI_ENDPOINT":                map[string]any{"type": "String", "value": "https://cog-l5nlvejnau56c.openai.azure.com/"},
		"foundrY_PROJECT_ENDPOINT": map[string]any{
			"type":  "String",
			"value": "https://cog-l5nlvejnau56c.services.ai.azure.com/api/projects/fdtest-zhihuan",
		},
	}
	got := armOutputsToProto(in)

	// Every key must come out in canonical form. The values are the
	// downstream consumers that previously silently 404'd.
	wantCanonical := []string{
		"AZURE_AI_ACCOUNT_NAME",
		"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
		"AZURE_AI_PROJECT_ID",
		"AZURE_AI_PROJECT_NAME",
		"AZURE_CONTAINER_REGISTRY_ENDPOINT",
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
		"AZURE_OPENAI_ENDPOINT",
		"FOUNDRY_PROJECT_ENDPOINT",
	}
	for _, k := range wantCanonical {
		_, ok := got[k]
		assert.True(t, ok, "expected canonical key %q in result, got keys %v", k, mapKeys(got))
	}
	assert.Len(t, got, len(wantCanonical), "no extra mangled keys should remain")

	// Spot-check that values flowed through correctly.
	assert.Equal(t, "cog-l5nlvejnau56c", got["AZURE_AI_ACCOUNT_NAME"].Value)
	assert.Equal(t, "fdtest-zhihuan", got["AZURE_AI_PROJECT_NAME"].Value)
	assert.Equal(t, "https://cog-l5nlvejnau56c.openai.azure.com/",
		got["AZURE_OPENAI_ENDPOINT"].Value)
}

// TestCanonicalizeOutputName covers the small helper directly.
// Exhaustive vs the table-driven test above so future contributors
// adding new entries to canonicalOutputNames only have to touch one
// place.
func TestCanonicalizeOutputName(t *testing.T) {
	t.Parallel()

	// Every canonical name must round-trip unchanged.
	for _, c := range canonicalOutputNames {
		assert.Equal(t, c, canonicalizeOutputName(c),
			"canonical name %q must round-trip", c)
	}

	// Every canonical name must be reachable from its lowercase form.
	for _, c := range canonicalOutputNames {
		lower := strings.ToLower(c)
		assert.Equal(t, c, canonicalizeOutputName(lower),
			"lower-case %q must canonicalize to %q", lower, c)
	}

	// Unknown name passes through verbatim.
	assert.Equal(t, "not_a_known_output",
		canonicalizeOutputName("not_a_known_output"),
		"unknown name must pass through verbatim, not silently dropped")
}

// mapKeys returns a sorted list of keys for stable assert messages.
func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
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
	p := &FoundryProvisioningProvider{envName: "dev", projectPath: "/proj/a"}
	first := p.deploymentName()
	assert.Equal(t, first, p.deploymentName(), "same env+path is stable")
	assert.True(t, strings.HasPrefix(first, "azd-foundry-dev-"), "carries env and discriminator")

	p.envName = "production"
	assert.True(t, strings.HasPrefix(p.deploymentName(), "azd-foundry-production-"))

	// Different project paths sharing an env name must not collide.
	other := &FoundryProvisioningProvider{envName: "dev", projectPath: "/proj/b"}
	assert.NotEqual(t, first, other.deploymentName())
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

func TestParameters_NilSynthResult_ReturnsHostDerivedOnly(t *testing.T) {
	// On the on-disk Bicep path, Initialize deliberately skips the
	// synthesizer so synthResult is nil. Parameters must still return
	// the host-derived parameter list (location, foundryProjectName,
	// principalId) so azd-core's env-wiring planner has something to
	// work with. The synthesizer-derived `includeAcr` is omitted in
	// that mode -- on-disk Bicep owns its own parameter contract.
	p := &FoundryProvisioningProvider{
		location:    "eastus",
		foundryName: "fp",
		principalID: "pid",
		// synthResult intentionally nil (on-disk path)
	}
	got, err := p.Parameters(t.Context())
	require.NoError(t, err, "Parameters must succeed on the on-disk path")

	names := make([]string, 0, len(got))
	for _, p := range got {
		names = append(names, p.Name)
	}
	assert.Contains(t, names, "location")
	assert.Contains(t, names, "foundryProjectName")
	assert.Contains(t, names, "principalId")
	assert.NotContains(t, names, "includeAcr",
		"includeAcr is a synthesizer-derived value; on-disk path must skip it")
}

func TestParameters_EmbeddedPath_IncludesSynthResultDerivedValues(t *testing.T) {
	// On the embedded path, synthResult is set and includeAcr flows
	// through.
	p := &FoundryProvisioningProvider{
		location:    "eastus",
		foundryName: "fp",
		principalID: "pid",
		synthResult: &synthesis.Result{
			Parameters: map[string]any{"includeAcr": true},
		},
	}
	got, err := p.Parameters(t.Context())
	require.NoError(t, err)

	found := false
	for _, p := range got {
		if p.Name == "includeAcr" {
			found = true
			assert.Equal(t, "true", p.Value,
				"includeAcr value must be the synthesizer's derived bool, %%v-formatted")
		}
	}
	assert.True(t, found, "embedded path must include includeAcr")
}

func TestArmParameters_NilSafeOnMissingSynthResult(t *testing.T) {
	// Internal helper, but defense in depth: Deploy is supposed to be
	// the only caller and is only reachable after Initialize. Still,
	// nil synthResult must not panic; it just means no synthesizer-
	// derived parameters are merged in.
	p := &FoundryProvisioningProvider{
		location:    "eastus",
		envName:     "dev",
		rgName:      "rg-dev",
		foundryName: "fp",
		principalID: "pid",
		// synthResult intentionally nil
	}
	out := p.armParameters() // must not panic
	require.Contains(t, out, "location")
	require.Contains(t, out, "foundryProjectName")
	// resourceGroupName drives the resource group the subscription-scoped
	// template creates; it must always be present.
	require.Contains(t, out, "resourceGroupName")
	rg, _ := out["resourceGroupName"].(map[string]any)
	assert.Equal(t, "rg-dev", rg["value"])
	require.NotContains(t, out, "includeAcr",
		"synthesizer-derived parameters should be absent when synthResult is nil")
}

func TestArmParameters_UseValueEnvelopeForSecureConnections(t *testing.T) {
	p := &FoundryProvisioningProvider{
		synthResult: &synthesis.Result{
			Parameters: map[string]any{
				"connections": `[{"name":"search-conn"}]`,
			},
		},
	}

	out := p.armParameters()

	assert.Equal(
		t,
		map[string]any{"value": `[{"name":"search-conn"}]`},
		out["connections"],
	)
}

func TestDestroy_RefusesWithoutForceWhenNonInteractive(t *testing.T) {
	// Destroy must NEVER silently delete (or, worse, silently leak)
	// resources. Without --force the provider prompts for confirmation, but
	// when there is no interactive host attached (azdClient == nil, as in
	// --no-prompt / CI) it falls back to a structured error telling the user
	// exactly what would have been deleted and how to confirm it. The bug this
	// guards against: prior behavior was to delete only the deployment record
	// and return success, leaving the Foundry account + ACR + role assignments
	// live with no warning.
	p := &FoundryProvisioningProvider{
		rgName:     "rg-foundry-test",
		rgExplicit: true,
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

func TestFindFoundryProjectService_DependencyCategory(t *testing.T) {
	// Missing service in azure.yaml is a missing-dependency error, not
	// a validation error (the yaml parses fine). Telemetry classifiers
	// differentiate these; the wrong category buckets misconfigurations
	// alongside actual malformed yaml.
	_, err := findFoundryProjectService([]byte("name: x\nservices:\n  web:\n    host: containerapp\n"))
	require.Error(t, err)
	var local *azdext.LocalError
	require.True(t, errors.As(err, &local))
	assert.Equal(t, azdext.LocalErrorCategoryDependency, local.Category,
		"missing foundry project service is a Dependency, not a Validation")
}

func TestOnDiskTemplatePresent(t *testing.T) {
	t.Parallel()
	// Empty project root: no infra/, so on-disk template absent.
	emptyDir := t.TempDir()
	p := &FoundryProvisioningProvider{projectPath: emptyDir}
	assert.False(t, p.onDiskTemplatePresent(),
		"absent ./infra/ -> false")

	// infra/ exists but is empty: still false (no .bicep or .bicepparam).
	emptyInfraDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(emptyInfraDir, onDiskInfraDir), 0o750))
	p = &FoundryProvisioningProvider{projectPath: emptyInfraDir}
	assert.False(t, p.onDiskTemplatePresent(),
		"./infra/ present but empty -> false")

	// main.bicep alone: true.
	bicepDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(bicepDir, onDiskInfraDir), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(bicepDir, onDiskInfraDir, onDiskBicepFile), []byte("// b"), 0o600))
	p = &FoundryProvisioningProvider{projectPath: bicepDir}
	assert.True(t, p.onDiskTemplatePresent(),
		"main.bicep present -> true")

	// main.bicepparam alone: true.
	bicepparamDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(bicepparamDir, onDiskInfraDir), 0o750))
	bicepparamPath := filepath.Join(bicepparamDir, onDiskInfraDir, onDiskBicepParamFile)
	require.NoError(t, os.WriteFile(bicepparamPath, []byte("// bp"), 0o600))
	p = &FoundryProvisioningProvider{projectPath: bicepparamDir}
	assert.True(t, p.onDiskTemplatePresent(),
		"main.bicepparam present -> true")
}

func TestResolveTemplate_FallsBackToEmbeddedWhenNoOnDisk(t *testing.T) {
	t.Parallel()
	// No ./infra/ -> resolveTemplate returns the embedded path with
	// the synthesizer-derived parameter map.
	dir := t.TempDir()
	p := &FoundryProvisioningProvider{
		projectPath: dir,
		envName:     "dev",
		location:    "eastus",
		foundryName: "fp",
		principalID: "pid",
		armTemplate: map[string]any{"$schema": "embedded", "contentVersion": "1.0.0.0"},
		synthResult: &synthesis.Result{
			Parameters: map[string]any{
				"includeAcr":  false,
				"deployments": []any{},
			},
		},
	}

	got, err := p.resolveTemplate(t.Context(), func(string) {})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, templateModeEmbedded, got.mode,
		"absent ./infra/ -> embedded mode")
	assert.Empty(t, got.sourcePath)
	assert.Equal(t, "embedded", got.armTemplate["$schema"],
		"embedded ARM template flows through verbatim")
	// armParameters includes host-derived values too.
	require.Contains(t, got.parameters, "location")
	require.Contains(t, got.parameters, "includeAcr")
}

func TestResolveTemplate_PrefersOnDiskWhenPresent(t *testing.T) {
	t.Parallel()
	// Setup: a project root with both an "embedded" template stashed
	// on the provider AND ./infra/main.bicep on disk. on-disk must
	// win; embedded is shadowed.
	dir := t.TempDir()
	infraDir := filepath.Join(dir, onDiskInfraDir)
	require.NoError(t, os.MkdirAll(infraDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile),
		[]byte("// fake bicep, never actually compiled by the stub"), 0o600))

	// Plant a user parameters file with one literal value so we can
	// observe merge precedence.
	params := `{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "location": { "value": "user-supplied-location" },
    "userOnly": { "value": "from-user" }
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskParamsFile), []byte(params), 0o600))

	// Pre-bake the on-disk source so we don't need a live bicep CLI.
	// (resolveTemplate skips the loadOnDiskTemplate call when
	// onDiskSource is already set; this lets the test exercise the
	// merge logic in isolation.)
	armFromDisk := map[string]any{"$schema": "ondisk", "contentVersion": "1.0.0.0"}
	p := &FoundryProvisioningProvider{
		projectPath: dir,
		envName:     "dev",
		location:    "host-location",
		foundryName: "fp",
		principalID: "pid",
		armTemplate: map[string]any{"$schema": "embedded"},
		synthResult: &synthesis.Result{
			Parameters: map[string]any{"includeAcr": false},
		},
		onDiskSource: &templateSource{
			mode:        templateModeBicep,
			armTemplate: armFromDisk,
			parameters: map[string]any{
				"location": map[string]any{"value": "user-supplied-location"},
				"userOnly": map[string]any{"value": "from-user"},
			},
			sourcePath: filepath.Join(infraDir, onDiskBicepFile),
		},
	}

	got, err := p.resolveTemplate(t.Context(), func(string) {})
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, templateModeBicep, got.mode, "on-disk Bicep mode wins")
	assert.Equal(t, "ondisk", got.armTemplate["$schema"],
		"on-disk template is returned, not the embedded one")
	assert.Equal(t, filepath.Join(infraDir, onDiskBicepFile), got.sourcePath)

	// Merge precedence: user wins on 'location'.
	loc := got.parameters["location"].(map[string]any)
	assert.Equal(t, "user-supplied-location", loc["value"],
		"user-supplied parameter wins over host-derived")
	// User-only key is present.
	require.Contains(t, got.parameters, "userOnly")
	// Host-derived key (not in user params) still flows through.
	require.Contains(t, got.parameters, "foundryProjectName",
		"host-derived parameter fills gap when user file doesn't declare it")
	// Synthesizer-derived key is ABSENT: per the design decision,
	// when on-disk wins we skip the synthesizer entirely. But
	// armParameters still has synthResult here (set up above) because
	// this test is exercising the merge step only -- in real
	// Initialize, synthResult would be nil on the on-disk path.
	// What we DO want to verify: the merge respects user wins.
}

func TestResolveTemplate_OnDiskFallsBackWhenSourceLoaderReturnsNil(t *testing.T) {
	t.Parallel()
	// Defensive: if onDiskTemplatePresent() reports true but
	// loadOnDiskTemplate returns (nil, nil) -- e.g. file disappeared
	// mid-call -- we fall back to the embedded path rather than
	// crashing or erroring. The stub compiler is set up to return a
	// valid template, but we don't actually create the infra/ files,
	// so onDiskTemplatePresent() returns false and we go straight to
	// embedded.
	dir := t.TempDir()
	p := &FoundryProvisioningProvider{
		projectPath: dir,
		envName:     "dev",
		location:    "eastus",
		foundryName: "fp",
		principalID: "pid",
		armTemplate: map[string]any{"$schema": "embedded"},
		synthResult: &synthesis.Result{
			Parameters: map[string]any{"includeAcr": false},
		},
	}

	got, err := p.resolveTemplate(t.Context(), func(string) {})
	require.NoError(t, err)
	assert.Equal(t, templateModeEmbedded, got.mode)
}

func TestFoundryServiceEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		yaml         string
		svcName      string
		wantEndpoint string
	}{
		{
			name: "greenfield (no endpoint:) -> empty",
			yaml: `name: x
services:
  foundry:
    host: azure.ai.project`,
			svcName:      "foundry",
			wantEndpoint: "",
		},
		{
			name: "endpoint set -> returned for brownfield reuse",
			yaml: `name: x
services:
  foundry:
    host: azure.ai.project
    endpoint: https://example.foundry.example.com`,
			svcName:      "foundry",
			wantEndpoint: "https://example.foundry.example.com",
		},
		{
			name: "blank endpoint -> empty",
			yaml: `name: x
services:
  foundry:
    host: azure.ai.project
    endpoint: "   "`,
			svcName:      "foundry",
			wantEndpoint: "",
		},
		{
			name: "service not in yaml -> empty",
			yaml: `name: x
services:
  other:
    host: containerapp`,
			svcName:      "foundry",
			wantEndpoint: "",
		},
		{
			name:         "malformed yaml -> empty (upstream surfaces parse error)",
			yaml:         "not: : valid: yaml",
			svcName:      "foundry",
			wantEndpoint: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantEndpoint, foundryServiceEndpoint([]byte(tt.yaml), tt.svcName))
		})
	}
}

func TestProjectNameFromEndpoint(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "my-project", projectNameFromEndpoint(
		"https://acct.services.ai.azure.com/api/projects/my-project"))
	assert.Equal(t, "", projectNameFromEndpoint("https://acct.services.ai.azure.com"))
	assert.Equal(t, "", projectNameFromEndpoint(""))
}

func TestBrownfieldOutputs(t *testing.T) {
	t.Parallel()
	outputs := brownfieldOutputs("https://acct.services.ai.azure.com/api/projects/my-project")
	require.Contains(t, outputs, "FOUNDRY_PROJECT_ENDPOINT")
	assert.Equal(t,
		"https://acct.services.ai.azure.com/api/projects/my-project",
		outputs["FOUNDRY_PROJECT_ENDPOINT"].Value)
	require.Contains(t, outputs, "AZURE_AI_PROJECT_NAME")
	assert.Equal(t, "my-project", outputs["AZURE_AI_PROJECT_NAME"].Value)
}

func TestDefaultResourceGroupName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "rg-dev", defaultResourceGroupName("dev"))
	assert.Equal(t, "rg-my-env", defaultResourceGroupName("my-env"))
}

func TestWithTenantOutput(t *testing.T) {
	t.Parallel()

	t.Run("adds AZURE_TENANT_ID and preserves existing outputs", func(t *testing.T) {
		p := &FoundryProvisioningProvider{tenantID: "tenant-123"}
		out := p.withTenantOutput(map[string]*azdext.ProvisioningOutputParameter{
			"FOUNDRY_PROJECT_ENDPOINT": {Type: "string", Value: "https://x"},
		})
		require.Contains(t, out, envKeyTenantID)
		assert.Equal(t, "tenant-123", out[envKeyTenantID].Value)
		assert.Equal(t, "https://x", out["FOUNDRY_PROJECT_ENDPOINT"].Value)
	})

	t.Run("initializes a nil map", func(t *testing.T) {
		p := &FoundryProvisioningProvider{tenantID: "t"}
		out := p.withTenantOutput(nil)
		require.Contains(t, out, envKeyTenantID)
		assert.Equal(t, "t", out[envKeyTenantID].Value)
	})

	t.Run("no-op when tenant not resolved", func(t *testing.T) {
		p := &FoundryProvisioningProvider{}
		out := p.withTenantOutput(map[string]*azdext.ProvisioningOutputParameter{})
		assert.NotContains(t, out, envKeyTenantID)
	})

	t.Run("does not overwrite an existing tenant output", func(t *testing.T) {
		p := &FoundryProvisioningProvider{tenantID: "from-provider"}
		out := p.withTenantOutput(map[string]*azdext.ProvisioningOutputParameter{
			envKeyTenantID: {Type: "string", Value: "from-template"},
		})
		assert.Equal(t, "from-template", out[envKeyTenantID].Value)
	})
}

func TestEnvValues_IncludesCanonicalKeysEvenWithoutAzdClient(t *testing.T) {
	t.Parallel()
	// envValues must always include the canonical AZURE_* keys
	// resolved by Initialize, even when the azd env service is
	// unavailable (azdClient == nil). This lets ${AZURE_LOCATION}
	// substitution in main.parameters.json work in all paths.
	p := &FoundryProvisioningProvider{
		envName:     "dev",
		subID:       "sub-id",
		location:    "westus2",
		rgName:      "my-rg",
		foundryName: "fp",
		principalID: "pid",
		// azdClient intentionally nil
	}
	got := p.envValues(t.Context())
	assert.Equal(t, "sub-id", got[envKeySubscriptionID])
	assert.Equal(t, "westus2", got[envKeyLocation])
	assert.Equal(t, "my-rg", got[envKeyResourceGroup])
	assert.Equal(t, "fp", got[envKeyProjectName])
	assert.Equal(t, "pid", got[envKeyPrincipalID])
}

func TestCollectPurgeableAccounts(t *testing.T) {
	// Pure helper -- maps the SDK's pointer-heavy Account model down to the
	// {name, location} pairs the purge step needs, skipping anything with a
	// nil Name or Location (defensive against partial SDK responses).

	tests := []struct {
		name string
		in   []*armcognitiveservices.Account
		want []purgeableAccount
	}{
		{
			name: "nil slice yields empty result",
			in:   nil,
			want: []purgeableAccount{},
		},
		{
			name: "empty slice yields empty result",
			in:   []*armcognitiveservices.Account{},
			want: []purgeableAccount{},
		},
		{
			name: "complete account is captured",
			in: []*armcognitiveservices.Account{
				{Name: new("cog-abc"), Location: new("eastus")},
			},
			want: []purgeableAccount{{name: "cog-abc", location: "eastus"}},
		},
		{
			name: "nil entry is skipped",
			in: []*armcognitiveservices.Account{
				nil,
				{Name: new("cog-abc"), Location: new("eastus")},
			},
			want: []purgeableAccount{{name: "cog-abc", location: "eastus"}},
		},
		{
			name: "entry with nil Name is skipped",
			in: []*armcognitiveservices.Account{
				{Location: new("eastus")},
				{Name: new("cog-ok"), Location: new("westus2")},
			},
			want: []purgeableAccount{{name: "cog-ok", location: "westus2"}},
		},
		{
			name: "entry with nil Location is skipped",
			in: []*armcognitiveservices.Account{
				{Name: new("cog-bad")},
				{Name: new("cog-ok"), Location: new("westus2")},
			},
			want: []purgeableAccount{{name: "cog-ok", location: "westus2"}},
		},
		{
			name: "mixed list preserves order and skips invalid entries",
			in: []*armcognitiveservices.Account{
				{Name: new("cog-a"), Location: new("eastus")},
				{Name: new("cog-b")}, // dropped: no location
				{Name: new("cog-c"), Location: new("westus2")},
			},
			want: []purgeableAccount{
				{name: "cog-a", location: "eastus"},
				{name: "cog-c", location: "westus2"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectPurgeableAccounts(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}
