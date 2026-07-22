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

// TestCapabilitySchemaTypesInSyncWithGo keeps both JSON schema capability enums
// aligned with the capabilities accepted by azd.
func TestCapabilitySchemaTypesInSyncWithGo(t *testing.T) {
	expected := capabilityStrings()

	t.Run("extension.schema.json", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("..", "..", "extensions", "extension.schema.json"))
		require.NoError(t, err)

		var schema struct {
			Properties struct {
				Capabilities struct {
					Items struct {
						OneOf []struct {
							Const string `json:"const"`
						} `json:"oneOf"`
					} `json:"items"`
				} `json:"capabilities"`
			} `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(data, &schema))

		actual := make([]string, 0, len(schema.Properties.Capabilities.Items.OneOf))
		for _, capability := range schema.Properties.Capabilities.Items.OneOf {
			actual = append(actual, capability.Const)
		}
		require.ElementsMatch(t, expected, actual)
	})

	t.Run("registry.schema.json", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("..", "..", "extensions", "registry.schema.json"))
		require.NoError(t, err)

		var schema struct {
			Definitions struct {
				Version struct {
					Properties struct {
						Capabilities struct {
							Items struct {
								Enum []string `json:"enum"`
							} `json:"items"`
						} `json:"capabilities"`
					} `json:"properties"`
				} `json:"Version"`
			} `json:"definitions"`
		}
		require.NoError(t, json.Unmarshal(data, &schema))

		require.ElementsMatch(t, expected, schema.Definitions.Version.Properties.Capabilities.Items.Enum)
	})
}
