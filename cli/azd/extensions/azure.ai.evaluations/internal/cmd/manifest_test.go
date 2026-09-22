// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// extensionManifest is the subset of extension.yaml this test asserts on.
type extensionManifest struct {
	ID           string   `yaml:"id"`
	Version      string   `yaml:"version"`
	Capabilities []string `yaml:"capabilities"`
	Providers    []struct {
		Name string `yaml:"name"`
		Type string `yaml:"type"`
	} `yaml:"providers"`
}

func loadManifest(t *testing.T) extensionManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "extension.yaml"))
	require.NoError(t, err, "reading extension.yaml")

	var manifest extensionManifest
	require.NoError(t, yaml.Unmarshal(raw, &manifest))
	return manifest
}

// A declared capability azd cannot reach is worse than an undeclared one: azd
// invokes `metadata` to discover the command tree, and it was declared without
// the command being registered, so discovery failed with "unknown command".
func TestDeclaredCapabilitiesAreImplemented(t *testing.T) {
	manifest := loadManifest(t)
	root := NewRootCommand()

	hasCommand := func(name string) bool {
		for _, sub := range root.Commands() {
			if sub.Name() == name {
				return true
			}
		}
		return false
	}

	for _, capability := range manifest.Capabilities {
		switch capability {
		case "metadata":
			require.True(t, hasCommand("metadata"),
				"the metadata capability requires a metadata command")
		case "service-target-provider":
			require.True(t, hasCommand("listen"),
				"a service-target provider is registered through the listen command")
			require.NotEmpty(t, manifest.Providers,
				"the manifest must name the provider it registers")
		case "custom-commands":
			require.NotEmpty(t, root.Commands())
		case "lifecycle-events":
			// The SDK only starts the event manager when handlers are
			// registered, so declaring this without any is an unused
			// permission. Nothing here registers handlers today.
			t.Fatalf("lifecycle-events is declared but no event handlers are registered")
		}
	}
}

// The provider name in the manifest is what azd matches a service's `host`
// against, so a mismatch silently means the provider is never invoked.
func TestManifestProviderMatchesHostConstant(t *testing.T) {
	manifest := loadManifest(t)
	require.NotEmpty(t, manifest.Providers)

	names := make([]string, 0, len(manifest.Providers))
	for _, p := range manifest.Providers {
		names = append(names, p.Name)
	}
	require.Contains(t, names, "azure.ai.eval",
		"the manifest must declare the host the provider registers for")
}

// extension.yaml carries a note asking that version.txt be kept in sync. The
// build stamps the binary from version.txt while the registry reads
// extension.yaml, so a drift ships a binary that misreports its own version.
func TestManifestVersionMatchesVersionFile(t *testing.T) {
	manifest := loadManifest(t)

	raw, err := os.ReadFile(filepath.Join("..", "..", "version.txt"))
	require.NoError(t, err, "reading version.txt")

	require.Equal(t,
		strings.TrimSpace(string(raw)),
		strings.TrimSpace(manifest.Version),
		"version.txt and extension.yaml must agree")
}
