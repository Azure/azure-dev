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

type extensionManifest struct {
	Capabilities []string `yaml:"capabilities"`
	Dependencies []struct {
		ID      string `yaml:"id"`
		Version string `yaml:"version"`
	} `yaml:"dependencies"`
	Providers []struct {
		Name string `yaml:"name"`
		Type string `yaml:"type"`
	} `yaml:"providers"`
}

func TestProvisioningOwnershipMetadata(t *testing.T) {
	t.Parallel()

	projects := readExtensionManifest(t, "../../extension.yaml")
	agents := readExtensionManifest(
		t,
		"../../../azure.ai.agents/extension.yaml",
	)

	assert.Contains(t, projects.Capabilities, "lifecycle-events")
	assert.Contains(t, projects.Capabilities, "provisioning-provider")
	assert.Contains(t, projects.Capabilities, "validation-provider")
	assert.True(t, manifestHasProvider(
		projects,
		"microsoft.foundry",
		"provisioning-provider",
	))

	assert.NotContains(t, agents.Capabilities, "provisioning-provider")
	assert.NotContains(t, agents.Capabilities, "validation-provider")
	assert.False(t, manifestHasProvider(
		agents,
		"microsoft.foundry",
		"provisioning-provider",
	))
	assert.True(t, manifestHasDependency(
		agents,
		"azure.ai.projects",
		"~1.0.0-beta.3",
	))
}

func readExtensionManifest(
	t *testing.T,
	path string,
) extensionManifest {
	t.Helper()

	//nolint:gosec // repository-controlled manifest path
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var manifest extensionManifest
	require.NoError(t, yaml.Unmarshal(data, &manifest))
	return manifest
}

func manifestHasProvider(
	manifest extensionManifest,
	name string,
	providerType string,
) bool {
	for _, provider := range manifest.Providers {
		if provider.Name == name && provider.Type == providerType {
			return true
		}
	}
	return false
}

func manifestHasDependency(
	manifest extensionManifest,
	id string,
	version string,
) bool {
	for _, dependency := range manifest.Dependencies {
		if dependency.ID == id && dependency.Version == version {
			return true
		}
	}
	return false
}
