// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

type resourceExtensionManifest struct {
	Dependencies []struct {
		ID      string `yaml:"id"`
		Version string `yaml:"version"`
	} `yaml:"dependencies"`
	Providers []struct {
		Name string `yaml:"name"`
		Type string `yaml:"type"`
	} `yaml:"providers"`
}

func TestFoundryResourceOwnershipMetadata(t *testing.T) {
	t.Parallel()

	agents := readResourceExtensionManifest(t, "../../extension.yaml")
	connections := readResourceExtensionManifest(t, "../../../azure.ai.connections/extension.yaml")
	toolboxes := readResourceExtensionManifest(t, "../../../azure.ai.toolboxes/extension.yaml")

	assert.True(t, hasResourceDependency(agents, "azure.ai.connections", "~1.0.0-beta.4"))
	assert.True(t, hasResourceDependency(agents, "azure.ai.toolboxes", "~1.0.0-beta.5"))
	assert.True(t, hasResourceProvider(agents, "azure.ai.agent", "service-target"))
	assert.False(t, hasResourceProvider(agents, "azure.ai.connection", "service-target"))
	assert.False(t, hasResourceProvider(agents, "azure.ai.toolbox", "service-target"))
	assert.True(t, hasResourceProvider(connections, "azure.ai.connection", "service-target"))
	assert.True(t, hasResourceProvider(toolboxes, "azure.ai.toolbox", "service-target"))
	assert.True(t, hasResourceDependency(toolboxes, "azure.ai.connections", "~1.0.0-beta.5"))
}

func readResourceExtensionManifest(t *testing.T, path string) resourceExtensionManifest {
	t.Helper()
	//nolint:gosec // repository-controlled manifest path
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var manifest resourceExtensionManifest
	require.NoError(t, yaml.Unmarshal(data, &manifest))
	return manifest
}

func hasResourceDependency(manifest resourceExtensionManifest, id, version string) bool {
	for _, dependency := range manifest.Dependencies {
		if dependency.ID == id && dependency.Version == version {
			return true
		}
	}
	return false
}

func hasResourceProvider(manifest resourceExtensionManifest, name, providerType string) bool {
	for _, provider := range manifest.Providers {
		if provider.Name == name && provider.Type == providerType {
			return true
		}
	}
	return false
}
