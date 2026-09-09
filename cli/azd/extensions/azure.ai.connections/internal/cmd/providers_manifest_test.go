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
	require.NoError(t, azdext.VerifyProvidersMatchManifest(func(host *azdext.ExtensionHost) {
		configureExtensionHostForEnvironment(host, "staging")
	}, manifestPath))
}

func TestConfigureExtensionHostPreservesSelectedEnvironment(t *testing.T) {
	t.Parallel()
	for _, environment := range []string{"", "staging"} {
		t.Run(environment, func(t *testing.T) {
			t.Parallel()
			host := azdext.NewExtensionHost(&azdext.AzdClient{})
			configureExtensionHostForEnvironment(host, environment)
			targets := host.ServiceTargets()
			require.Len(t, targets, 1)
			require.Equal(t, aiConnectionHost, targets[0].Host)
			target, ok := targets[0].Factory().(*connectionServiceTarget)
			require.True(t, ok)
			require.Equal(t, environment, target.environment)
		})
	}
}
