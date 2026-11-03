// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestConfigureExtensionHostMatchesManifest(t *testing.T) {
	require.NoError(t, azdext.VerifyProvidersMatchManifest(
		configureExtensionHost,
		filepath.Join("..", "..", "extension.yaml")))
}

func TestConfigureExtensionHostUsesSharedProvisioningProvider(t *testing.T) {
	host := azdext.NewExtensionHost(nil)
	p := configureResourcesProvider(host)
	t.Cleanup(func() { require.NoError(t, p.Close()) })

	registrations := host.ProvisioningProviders()
	require.Len(t, registrations, 1)
	require.Equal(t, "resources", registrations[0].Name)
	require.Same(t, p, registrations[0].Factory())
	require.Same(t, p, registrations[0].Factory())
}
