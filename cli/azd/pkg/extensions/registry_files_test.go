// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDevRegistryFileIsEmptyAndValid(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	registryPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "extensions", "registry.dev.json")
	data, err := os.ReadFile(registryPath)
	require.NoError(t, err)

	var registry Registry
	require.NoError(t, json.Unmarshal(data, &registry))
	require.Equal(t, CurrentRegistrySchemaVersion, registry.SchemaVersion)
	require.Empty(t, registry.Extensions)

	result := ValidateRegistry(&registry, false)
	require.True(t, result.Valid)
	require.Empty(t, result.Issues)
	require.Empty(t, result.Extensions)
}
