// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// TestConfigureExtensionHostMatchesManifest verifies that the providers this
// extension registers match those declared in its extension.yaml.
func TestConfigureExtensionHostMatchesManifest(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "extension.yaml")
	require.NoError(t, azdext.VerifyProvidersMatchManifest(configureExtensionHost, manifestPath))
}

func TestDemoManifestHostCompatibilityForStableMessages(t *testing.T) {
	data, err := os.ReadFile("../../extension.yaml")
	require.NoError(t, err)

	var manifest struct {
		RequiredAzdVersion string `yaml:"requiredAzdVersion"`
	}
	require.NoError(t, yaml.Unmarshal(data, &manifest))
	version := &extensions.ExtensionVersion{RequiredAzdVersion: manifest.RequiredAzdVersion}

	for _, tt := range []struct {
		name       string
		azdVersion string
		compatible bool
	}{
		{name: "released host", azdVersion: "1.39.1", compatible: false},
		{name: "target host", azdVersion: "1.40.0", compatible: true},
		{name: "later host", azdVersion: "1.41.0", compatible: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			compatible := extensions.VersionIsCompatible(version, semver.MustParse(tt.azdVersion))
			require.Equal(t, tt.compatible, compatible)
		})
	}
}
