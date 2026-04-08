// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceIdName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple lowercase",
			input:    "storage",
			expected: "AZURE_RESOURCE_STORAGE_ID",
		},
		{
			name:     "mixed case",
			input:    "myStorage",
			expected: "AZURE_RESOURCE_MYSTORAGE_ID",
		},
		{
			name:     "with dashes",
			input:    "my-resource",
			expected: "AZURE_RESOURCE_MY_RESOURCE_ID",
		},
		{
			name:     "already uppercase",
			input:    "COSMOS",
			expected: "AZURE_RESOURCE_COSMOS_ID",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "AZURE_RESOURCE__ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResourceIdName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestResourceId(t *testing.T) {
	//nolint:lll
	validResourceID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Storage/storageAccounts/mystorage"

	t.Run("parses a valid resource ID directly", func(t *testing.T) {
		env := environment.New("test")
		resId, err := ResourceId(validResourceID, env)
		require.NoError(t, err)
		assert.Equal(
			t,
			"mystorage",
			resId.Name,
		)
		assert.Equal(
			t,
			"Microsoft.Storage/storageAccounts",
			resId.ResourceType.String(),
		)
	})

	t.Run("resolves from env when name is not a resource ID",
		func(t *testing.T) {
			env := environment.NewWithValues("test", map[string]string{
				"AZURE_RESOURCE_STORAGE_ID": validResourceID,
			})
			resId, err := ResourceId("storage", env)
			require.NoError(t, err)
			assert.Equal(t, "mystorage", resId.Name)
		})

	t.Run("error when env var not set", func(t *testing.T) {
		env := environment.New("test")
		_, err := ResourceId("notexist", env)
		require.Error(t, err)
		assert.Contains(
			t,
			err.Error(),
			"AZURE_RESOURCE_NOTEXIST_ID is not set",
		)
	})

	t.Run("error when env var is empty", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{
			"AZURE_RESOURCE_EMPTY_ID": "",
		})
		_, err := ResourceId("empty", env)
		require.Error(t, err)
		assert.Contains(
			t,
			err.Error(),
			"AZURE_RESOURCE_EMPTY_ID is empty",
		)
	})

	t.Run("error when env var has invalid resource ID",
		func(t *testing.T) {
			env := environment.NewWithValues("test", map[string]string{
				"AZURE_RESOURCE_BAD_ID": "not-a-resource-id",
			})
			_, err := ResourceId("bad", env)
			require.Error(t, err)
			assert.Contains(
				t,
				err.Error(),
				"parsing AZURE_RESOURCE_BAD_ID",
			)
		})
}

func TestKeyVaultName(t *testing.T) {
	t.Run("returns value when set", func(t *testing.T) {
		env := environment.NewWithValues("test", map[string]string{
			"AZURE_KEY_VAULT_NAME": "my-keyvault",
		})
		assert.Equal(t, "my-keyvault", KeyVaultName(env))
	})

	t.Run("returns empty string when not set", func(t *testing.T) {
		env := environment.New("test")
		assert.Equal(t, "", KeyVaultName(env))
	})
}
