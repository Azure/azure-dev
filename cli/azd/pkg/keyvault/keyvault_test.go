// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package keyvault

import (
"context"
"errors"
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

// mockKeyVaultService is a minimal mock for the KeyVaultService interface.
// It only implements SecretFromKeyVaultReference (the method under test);
// all other methods panic if called.
type mockKeyVaultService struct {
	// resolveFunc, when set, is called by SecretFromKeyVaultReference.
	resolveFunc func(ctx context.Context, ref string, defaultSubID string) (string, error)
}

func (m *mockKeyVaultService) GetKeyVault(context.Context, string, string, string) (*KeyVault, error) {
	panic("not implemented")
}
func (m *mockKeyVaultService) GetKeyVaultSecret(context.Context, string, string, string) (*Secret, error) {
	panic("not implemented")
}
func (m *mockKeyVaultService) PurgeKeyVault(context.Context, string, string, string) error {
	panic("not implemented")
}
func (m *mockKeyVaultService) ListSubscriptionVaults(context.Context, string) ([]Vault, error) {
	panic("not implemented")
}
func (m *mockKeyVaultService) CreateVault(context.Context, string, string, string, string, string) (Vault, error) {
	panic("not implemented")
}
func (m *mockKeyVaultService) ListKeyVaultSecrets(context.Context, string, string) ([]string, error) {
	panic("not implemented")
}
func (m *mockKeyVaultService) CreateKeyVaultSecret(context.Context, string, string, string, string) error {
	panic("not implemented")
}
func (m *mockKeyVaultService) SecretFromAkvs(context.Context, string) (string, error) {
	panic("not implemented")
}

func (m *mockKeyVaultService) SecretFromKeyVaultReference(
	ctx context.Context, ref string, defaultSubID string,
) (string, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(ctx, ref, defaultSubID)
	}
	return "", errors.New("mockKeyVaultService: resolveFunc not set")
}

// --- ResolveSecretEnvironment ---

func TestResolveSecretEnvironment_NilService(t *testing.T) {
	t.Parallel()

	input := []string{"FOO=bar", "SECRET=akvs://sub/vault/name"}
	result, err := ResolveSecretEnvironment(t.Context(), nil, input, "sub")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With nil kvService, input is returned unchanged.
	if len(result) != len(input) {
		t.Fatalf("len(result) = %d, want %d", len(result), len(input))
	}
	for i, v := range result {
		if v != input[i] {
			t.Errorf("result[%d] = %q, want %q", i, v, input[i])
		}
	}
}

func TestResolveSecretEnvironment_PlainValues(t *testing.T) {
	t.Parallel()

	mock := &mockKeyVaultService{
		resolveFunc: func(_ context.Context, _ string, _ string) (string, error) {
			t.Fatal("resolveFunc should not be called for plain values")
			return "", nil
		},
	}

	input := []string{"FOO=bar", "BAZ=qux", "EMPTY="}
	result, err := ResolveSecretEnvironment(t.Context(), mock, input, "sub")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, v := range result {
		if v != input[i] {
			t.Errorf("result[%d] = %q, want %q", i, v, input[i])
		}
	}
}

func TestResolveSecretEnvironment_MalformedEnvVar(t *testing.T) {
	t.Parallel()

	mock := &mockKeyVaultService{}
	// Entries without '=' should be passed through unchanged.
	input := []string{"NO_EQUALS_SIGN", "FOO=bar"}
	result, err := ResolveSecretEnvironment(t.Context(), mock, input, "sub")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result[0] != "NO_EQUALS_SIGN" {
		t.Errorf("result[0] = %q, want %q", result[0], "NO_EQUALS_SIGN")
	}
	if result[1] != "FOO=bar" {
		t.Errorf("result[1] = %q, want %q", result[1], "FOO=bar")
	}
}

func TestResolveSecretEnvironment_MixedAkvsAndAppRef(t *testing.T) {
	t.Parallel()

	mock := &mockKeyVaultService{
		resolveFunc: func(_ context.Context, ref string, _ string) (string, error) {
			switch {
			case strings.HasPrefix(ref, "akvs://"):
				return "akvs-resolved", nil
			case IsKeyVaultAppReference(ref):
				return "appref-resolved", nil
			default:
				return "", errors.New("unexpected ref: " + ref)
			}
		},
	}

	input := []string{
		"PLAIN=hello",
		"AKVS_SECRET=akvs://sub/vault/secret",
		"APPREF_SECRET=@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/s)",
	}

	result, err := ResolveSecretEnvironment(t.Context(), mock, input, "sub")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{
		"PLAIN=hello",
		"AKVS_SECRET=akvs-resolved",
		"APPREF_SECRET=appref-resolved",
	}
	for i, v := range result {
		if v != expected[i] {
			t.Errorf("result[%d] = %q, want %q", i, v, expected[i])
		}
	}
}

func TestResolveSecretEnvironment_ErrorCollection(t *testing.T) {
	t.Parallel()

	mock := &mockKeyVaultService{
		resolveFunc: func(_ context.Context, _ string, _ string) (string, error) {
			return "", errors.New("vault unavailable")
		},
	}

	input := []string{
		"SECRET1=akvs://sub/vault/s1",
		"SECRET2=akvs://sub/vault/s2",
		"PLAIN=hello",
	}

	result, err := ResolveSecretEnvironment(t.Context(), mock, input, "sub")
	if err == nil {
		t.Fatal("expected error for failed resolutions")
	}

	// Both failing keys should appear in the error message.
	errMsg := err.Error()
	if !strings.Contains(errMsg, `"SECRET1"`) {
		t.Errorf("error should mention SECRET1, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, `"SECRET2"`) {
		t.Errorf("error should mention SECRET2, got: %s", errMsg)
	}

	// Failed secrets get empty values; plain value passes through.
	if result[0] != "SECRET1=" {
		t.Errorf("result[0] = %q, want %q", result[0], "SECRET1=")
	}
	if result[1] != "SECRET2=" {
		t.Errorf("result[1] = %q, want %q", result[1], "SECRET2=")
	}
	if result[2] != "PLAIN=hello" {
		t.Errorf("result[2] = %q, want %q", result[2], "PLAIN=hello")
	}
}

func TestResolveSecretEnvironment_PreservesOrdering(t *testing.T) {
	t.Parallel()

	mock := &mockKeyVaultService{
		resolveFunc: func(_ context.Context, _ string, _ string) (string, error) {
			return "resolved", nil
		},
	}

	// System env first, then azd override — last-wins semantics.
	input := []string{
		"PATH=/usr/bin",
		"DB_CONN=akvs://sub/vault/db",
		"PATH=/override",
	}

	result, err := ResolveSecretEnvironment(t.Context(), mock, input, "sub")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Order must be preserved (not sorted alphabetically).
	if result[0] != "PATH=/usr/bin" {
		t.Errorf("result[0] = %q, want %q", result[0], "PATH=/usr/bin")
	}
	if result[1] != "DB_CONN=resolved" {
		t.Errorf("result[1] = %q, want %q", result[1], "DB_CONN=resolved")
	}
	if result[2] != "PATH=/override" {
		t.Errorf("result[2] = %q, want %q", result[2], "PATH=/override")
	}
}

func TestResolveSecretEnvironment_UnrecognizedFormatError(t *testing.T) {
	t.Parallel()

	// A ref that passes IsSecretReference but SecretFromKeyVaultReference
	// returns "unrecognized format" — simulates the fallthrough path.
	mock := &mockKeyVaultService{
		resolveFunc: func(_ context.Context, ref string, _ string) (string, error) {
			return "", errors.New("unrecognized Key Vault reference format: " + ref)
		},
	}

	input := []string{
		"SECRET=akvs://sub/vault/secret",
	}

	_, err := ResolveSecretEnvironment(t.Context(), mock, input, "sub")
	if err == nil {
		t.Fatal("expected error for unrecognized format")
	}
	if !strings.Contains(err.Error(), "unrecognized") {
		t.Errorf("error should mention 'unrecognized', got: %s", err.Error())
	}
}

// --- ParseKeyVaultAppReference additional cases ---

func TestParseKeyVaultAppReference_NonStandardPort(t *testing.T) {
	t.Parallel()

	_, err := ParseKeyVaultAppReference(
		"@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net:9999/secrets/foo)")
	if err == nil {
		t.Fatal("expected error for non-standard port")
	}
	if !strings.Contains(err.Error(), "non-standard port") {
		t.Errorf("error = %q, want mention of 'non-standard port'", err.Error())
	}
}

func TestParseKeyVaultAppReference_Port443Allowed(t *testing.T) {
	t.Parallel()

	ref, err := ParseKeyVaultAppReference(
		"@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net:443/secrets/foo)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.SecretName != "foo" {
		t.Errorf("SecretName = %q, want %q", ref.SecretName, "foo")
	}
}

func TestParseKeyVaultAppReference_EmptyVaultName(t *testing.T) {
	t.Parallel()

	// "vault.azure.net" is the bare suffix without a vault-name subdomain.
	// isValidVaultHost rejects it (needs ".vault.azure.net" suffix with a
	// leading dot), so the error reports an unknown endpoint rather than
	// reaching the vault-name extraction guard.
	_, err := ParseKeyVaultAppReference(
		"@Microsoft.KeyVault(SecretUri=https://vault.azure.net/secrets/foo)")
	if err == nil {
		t.Fatal("expected error for bare suffix hostname")
	}
	if !strings.Contains(err.Error(), "vault.azure.net") {
		t.Errorf("error = %q, want mention of problematic host", err.Error())
	}
}

func TestParseKeyVaultAppReference_CaseInsensitive(t *testing.T) {
	t.Parallel()

	ref, err := ParseKeyVaultAppReference(
		"@microsoft.keyvault(secreturi=https://myvault.vault.azure.net/secrets/mysecret)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.VaultName != "myvault" {
		t.Errorf("VaultName = %q, want %q", ref.VaultName, "myvault")
	}
	if ref.SecretName != "mysecret" {
		t.Errorf("SecretName = %q, want %q", ref.SecretName, "mysecret")
	}
}

// --- IsSecretReference ---

func TestIsSecretReference_Comprehensive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  bool
	}{
		{"akvs://sub/vault/secret", true},
		{"@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/s)", true},
		{"@microsoft.keyvault(secreturi=https://v.vault.azure.net/secrets/s)", true},
		{"@Microsoft.KeyVault(VaultName=v;SecretName=s)", false},
		{"plain-value", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := IsSecretReference(tt.input); got != tt.want {
			t.Errorf("IsSecretReference(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
