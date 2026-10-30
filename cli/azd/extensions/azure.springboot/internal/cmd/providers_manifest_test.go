// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestConfigureExtensionHostMatchesManifest(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "extension.yaml")
	require.NoError(t, azdext.VerifyProvidersMatchManifest(configureExtensionHost, manifestPath))

	host := azdext.NewExtensionHost(nil)
	configureExtensionHost(host)
	require.Len(t, host.ServiceTargets(), 1)
	require.Equal(t, "springbootapp", host.ServiceTargets()[0].Host)
	require.NotEqual(t, "azure.springboot", host.ServiceTargets()[0].Host)
}
