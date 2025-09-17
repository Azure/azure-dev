// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultPublishFlags(t *testing.T) {
	t.Run("sets registryPathDefault when registryPath is empty", func(t *testing.T) {
		flags := &publishFlags{}
		
		err := defaultPublishFlags(flags)
		require.NoError(t, err)
		
		// Should set the registry path and mark it as default
		require.NotEmpty(t, flags.registryPath)
		require.True(t, flags.registryPathDefault)
		require.Contains(t, flags.registryPath, "registry.json")
	})
	
	t.Run("does not set registryPathDefault when registryPath is provided", func(t *testing.T) {
		flags := &publishFlags{
			registryPath: "/custom/path/registry.json",
		}
		
		err := defaultPublishFlags(flags)
		require.NoError(t, err)
		
		// Should not change the custom path or set the default flag
		require.Equal(t, "/custom/path/registry.json", flags.registryPath)
		require.False(t, flags.registryPathDefault)
	})
}