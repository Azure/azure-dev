// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// TestConfigureExtensionHostMatchesManifest verifies that the providers this
// extension registers match those declared in its extension.yaml.
func TestConfigureExtensionHostMatchesManifest(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "extension.yaml")
	require.NoError(t, azdext.VerifyProvidersMatchManifest(configureExtensionHost, manifestPath))
}
