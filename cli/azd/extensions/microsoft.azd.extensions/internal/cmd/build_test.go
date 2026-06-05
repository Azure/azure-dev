// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalRegistryWarning(t *testing.T) {
	const extensionId = "my.custom.extension"

	t.Run("missing registry", func(t *testing.T) {
		dir := t.TempDir()

		warning := localRegistryWarning(dir, extensionId)

		require.Contains(t, warning, "was not found")
		require.Contains(t, warning, "azd x publish")
	})

	t.Run("extension not registered", func(t *testing.T) {
		dir := t.TempDir()
		registryPath := filepath.Join(dir, "registry.json")
		require.NoError(t, os.WriteFile(
			registryPath,
			[]byte(`{"extensions":[{"id":"some.other.extension"}]}`),
			0600,
		))

		warning := localRegistryWarning(dir, extensionId)

		require.Contains(t, warning, "is not registered")
		require.Contains(t, warning, extensionId)
	})

	t.Run("extension registered", func(t *testing.T) {
		dir := t.TempDir()
		registryPath := filepath.Join(dir, "registry.json")
		require.NoError(t, os.WriteFile(
			registryPath,
			[]byte(`{"extensions":[{"id":"`+extensionId+`"}]}`),
			0600,
		))

		warning := localRegistryWarning(dir, extensionId)

		require.Empty(t, warning)
	})

	t.Run("invalid registry", func(t *testing.T) {
		dir := t.TempDir()
		registryPath := filepath.Join(dir, "registry.json")
		require.NoError(t, os.WriteFile(registryPath, []byte("not-json"), 0600))

		warning := localRegistryWarning(dir, extensionId)

		require.True(t, strings.HasPrefix(warning, "Failed to read"))
	})
}
