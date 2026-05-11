// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDevRegistryFileIsValid(t *testing.T) {
	registryPath := filepath.Join("..", "..", "extensions", "registry.dev.json")
	data, err := os.ReadFile(registryPath)
	require.NoError(t, err)

	var registry Registry
	require.NoError(t, json.Unmarshal(data, &registry))
	require.Equal(t, CurrentRegistrySchemaVersion, registry.SchemaVersion)

	result := ValidateRegistry(&registry, false)
	require.True(t, result.Valid, "registry.dev.json failed validation: %+v", result)
}
