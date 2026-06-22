// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemaPath is the editor-tooling JSON schema for the Foundry service body,
// resolved from this package directory.
const schemaPath = "../../schemas/microsoft.foundry.json"

// TestSchema_NetworkStructuralInvariants guards the network surface of the
// hand-maintained JSON schema against drift from the synthesizer's contract:
// peSubnet is mandatory, the old mode/byo/managed shape is gone, and every
// subnet requires an explicit vnet + name.
func TestSchema_NetworkStructuralInvariants(t *testing.T) {
	raw, err := os.ReadFile(schemaPath)
	require.NoError(t, err)

	var doc struct {
		Properties struct {
			Network struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"network"`
		} `json:"properties"`
		Definitions struct {
			Subnet struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"subnet"`
		} `json:"definitions"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc), "schema must be valid JSON")

	net := doc.Properties.Network
	assert.Contains(t, net.Required, "peSubnet",
		"network must require peSubnet (no public data-plane fallback)")
	assert.Contains(t, net.Properties, "agentSubnet", "network must expose agentSubnet")
	assert.Contains(t, net.Properties, "isolationMode", "network must expose isolationMode")
	assert.Contains(t, net.Properties, "peSubnet", "network must expose peSubnet")

	// The retired mode-enum shape must not reappear.
	assert.NotContains(t, net.Properties, "mode", "network.mode was removed")
	assert.NotContains(t, net.Properties, "byo", "network.byo was removed")
	assert.NotContains(t, net.Properties, "managed", "network.managed was removed")

	sub := doc.Definitions.Subnet
	assert.ElementsMatch(t, []string{"vnet", "name"}, sub.Required,
		"a subnet must require exactly vnet + name")
	assert.Contains(t, sub.Properties, "prefix", "subnet must expose prefix (create vs reference)")
}

// TestARMTemplate_MatchesBicepBuild fails if templates/main.arm.json is stale
// relative to main.bicep. AGENTS guidance forbids hand-editing the ARM JSON;
// this catches a forgotten `bicep build`. Skipped when the bicep CLI is not on
// PATH (e.g. minimal CI images) so it never produces a phantom failure.
func TestARMTemplate_MatchesBicepBuild(t *testing.T) {
	bicep := lookupBicep()
	if bicep == "" {
		t.Skip("bicep CLI not found on PATH; skipping ARM drift check")
	}

	templatesDir := "templates"
	committed, err := os.ReadFile(filepath.Join(templatesDir, "main.arm.json"))
	require.NoError(t, err)

	out := filepath.Join(t.TempDir(), "main.arm.json")
	cmd := exec.CommandContext(t.Context(), bicep, "build",
		filepath.Join(templatesDir, "main.bicep"), "--outfile", out)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoErrorf(t, cmd.Run(), "bicep build failed: %s", stderr.String())

	rebuilt, err := os.ReadFile(out)
	require.NoError(t, err)

	assert.True(t, bytes.Equal(committed, rebuilt),
		"templates/main.arm.json is stale; regenerate with `bicep build main.bicep "+
			"--outfile main.arm.json` from the templates directory")
}

// lookupBicep returns a usable bicep binary path, preferring PATH and falling
// back to the az-bundled location.
func lookupBicep() string {
	if p, err := exec.LookPath("bicep"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		azBicep := filepath.Join(home, ".azure", "bin", "bicep")
		if _, err := os.Stat(azBicep); err == nil {
			return azBicep
		}
	}
	return ""
}
