// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package keyvault

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAzureKeyVaultSecret(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid akvs reference", "akvs://sub-id/vault/secret", true},
		{"empty string", "", false},
		{"wrong prefix", "https://vault.azure.net/secrets/foo", false},
		{"partial prefix", "akvs:/", false},
		{"case sensitive", "AKVS://sub/vault/secret", false},
		{"just prefix", "akvs://", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAzureKeyVaultSecret(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsValidSecretName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"simple lowercase", "mysecret", true},
		{"simple uppercase", "MySecret", true},
		{"with numbers", "secret123", true},
		{"with hyphens", "my-secret-name", true},
		{"single char", "a", true},
		{
			"max length 127 chars",
			strings.Repeat("a", 127),
			true,
		},
		{"empty string", "", false},
		{
			"too long 128 chars",
			strings.Repeat("a", 128),
			false,
		},
		{"with underscore", "my_secret", false},
		{"with dot", "my.secret", false},
		{"with space", "my secret", false},
		{"with slash", "my/secret", false},
		{"with special chars", "my@secret!", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidSecretName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewAzureKeyVaultSecret(t *testing.T) {
	tests := []struct {
		name       string
		subId      string
		vaultId    string
		secretName string
		expected   string
	}{
		{
			"standard reference",
			"sub-123", "my-vault", "my-secret",
			"akvs://sub-123/my-vault/my-secret",
		},
		{
			"empty values",
			"", "", "",
			"akvs:////",
		},
		{
			"guid-style subscription",
			"00000000-0000-0000-0000-000000000000",
			"production-vault",
			"db-connection-string",
			"akvs://00000000-0000-0000-0000-000000000000" +
				"/production-vault/db-connection-string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewAzureKeyVaultSecret(
				tt.subId, tt.vaultId, tt.secretName,
			)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseAzureKeyVaultSecret(t *testing.T) {
	t.Run("valid reference", func(t *testing.T) {
		result, err := ParseAzureKeyVaultSecret(
			"akvs://sub-123/my-vault/my-secret",
		)
		require.NoError(t, err)
		assert.Equal(t, "sub-123", result.SubscriptionId)
		assert.Equal(t, "my-vault", result.VaultName)
		assert.Equal(t, "my-secret", result.SecretName)
	})

	t.Run("invalid prefix", func(t *testing.T) {
		_, err := ParseAzureKeyVaultSecret("https://foo/bar/baz")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid Azure Key Vault Secret")
	})

	t.Run("too few parts", func(t *testing.T) {
		_, err := ParseAzureKeyVaultSecret("akvs://sub-123/vault-only")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Expected format")
	})

	t.Run("too many parts", func(t *testing.T) {
		_, err := ParseAzureKeyVaultSecret(
			"akvs://sub/vault/secret/extra",
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Expected format")
	})

	t.Run("empty string", func(t *testing.T) {
		_, err := ParseAzureKeyVaultSecret("")
		require.Error(t, err)
	})

	t.Run("roundtrip with NewAzureKeyVaultSecret", func(t *testing.T) {
		original := NewAzureKeyVaultSecret(
			"sub-abc", "vault-xyz", "secret-123",
		)
		parsed, err := ParseAzureKeyVaultSecret(original)
		require.NoError(t, err)
		assert.Equal(t, "sub-abc", parsed.SubscriptionId)
		assert.Equal(t, "vault-xyz", parsed.VaultName)
		assert.Equal(t, "secret-123", parsed.SecretName)
	})
}

func TestConstants(t *testing.T) {
	t.Run("ErrAzCliSecretNotFound is not nil", func(t *testing.T) {
		assert.NotNil(t, ErrAzCliSecretNotFound)
		assert.Equal(t, "secret not found", ErrAzCliSecretNotFound.Error())
	})

	t.Run("role IDs have correct prefix", func(t *testing.T) {
		prefix := "/providers/Microsoft.Authorization/roleDefinitions/"
		assert.Contains(t, RoleIdKeyVaultAdministrator, prefix)
		assert.Contains(t, RoleIdKeyVaultSecretsUser, prefix)
	})
}
