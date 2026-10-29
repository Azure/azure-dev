// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtensionMetadata_WebsiteField(t *testing.T) {
	t.Run("website field is parsed from JSON", func(t *testing.T) {
		jsonData := `{
			"id": "test.extension",
			"displayName": "Test Extension",
			"description": "A test extension",
			"website": "https://example.com/docs",
			"versions": []
		}`

		var metadata ExtensionMetadata
		err := json.Unmarshal([]byte(jsonData), &metadata)
		require.NoError(t, err)
		assert.Equal(t, "https://example.com/docs", metadata.Website)
	})

	t.Run("website field is omitted when empty", func(t *testing.T) {
		metadata := ExtensionMetadata{
			Id:          "test.extension",
			DisplayName: "Test Extension",
			Description: "A test extension",
		}

		data, err := json.Marshal(metadata)
		require.NoError(t, err)
		assert.NotContains(t, string(data), `"website"`)
	})

	t.Run("website field is included when set", func(t *testing.T) {
		metadata := ExtensionMetadata{
			Id:          "test.extension",
			DisplayName: "Test Extension",
			Description: "A test extension",
			Website:     "https://example.com/docs",
		}

		data, err := json.Marshal(metadata)
		require.NoError(t, err)
		assert.Contains(t, string(data), `"website":"https://example.com/docs"`)
	})

	t.Run("registry with per-extension websites", func(t *testing.T) {
		jsonData := `{
			"extensions": [
				{
					"id": "ext.one",
					"displayName": "Extension One",
					"description": "First extension",
					"website": "https://ext-one.example.com",
					"versions": []
				},
				{
					"id": "ext.two",
					"displayName": "Extension Two",
					"description": "Second extension",
					"website": "https://ext-two.example.com",
					"versions": []
				},
				{
					"id": "ext.three",
					"displayName": "Extension Three",
					"description": "Third extension without website",
					"versions": []
				}
			]
		}`

		var registry Registry
		err := json.Unmarshal([]byte(jsonData), &registry)
		require.NoError(t, err)
		require.Len(t, registry.Extensions, 3)
		assert.Equal(t, "https://ext-one.example.com", registry.Extensions[0].Website)
		assert.Equal(t, "https://ext-two.example.com", registry.Extensions[1].Website)
		assert.Empty(t, registry.Extensions[2].Website)
	})
}
