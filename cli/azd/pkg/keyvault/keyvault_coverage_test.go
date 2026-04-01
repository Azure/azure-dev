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

// ---------------------------------------------------------------------------
// ParseKeyVaultAppReference — extended edge cases
// ---------------------------------------------------------------------------

func TestParseKeyVaultAppReference_ValidationEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "https scheme without wrapper is rejected",
			input:     "https://myvault.vault.azure.net/secrets/mysecret",
			wantErr:   true,
			errSubstr: "invalid @Microsoft.KeyVault reference",
		},
		{
			name:      "kv scheme (not real) is rejected",
			input:     "kv://myvault.vault.azure.net/secrets/mysecret",
			wantErr:   true,
			errSubstr: "invalid @Microsoft.KeyVault reference",
		},
		{
			name:      "empty SecretUri value",
			input:     "@Microsoft.KeyVault(SecretUri=)",
			wantErr:   true,
			errSubstr: "invalid @Microsoft.KeyVault reference",
		},
		{
			name:      "SecretUri with http (not https)",
			input:     "@Microsoft.KeyVault(SecretUri=http://myvault.vault.azure.net/secrets/s)",
			wantErr:   true,
			errSubstr: "https scheme",
		},
		{
			name:      "wrong host pattern (not vault.azure.net)",
			input:     "@Microsoft.KeyVault(SecretUri=https://myvault.example.com/secrets/s)",
			wantErr:   true,
			errSubstr: "not a known Azure Key Vault endpoint",
		},
		{
			name:      "empty secret name in path",
			input:     "@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/)",
			wantErr:   true,
			errSubstr: "secret name must not be empty",
		},
		{
			name:      "path without /secrets/ segment",
			input:     "@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/keys/mykey)",
			wantErr:   true,
			errSubstr: "/secrets/<name>",
		},
		{
			name:    "valid with version segment",
			input:   "@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/mysecret/abc123)",
			wantErr: false,
		},
		{
			name:    "extra path segments after version are ignored",
			input:   "@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/mysecret/v1/extra)",
			wantErr: false,
		},
		{
			name:      "missing host",
			input:     "@Microsoft.KeyVault(SecretUri=https:///secrets/s)",
			wantErr:   true,
			errSubstr: "must include a host",
		},
		{
			name:      "leading .vault.azure.net host (no vault name)",
			input:     "@Microsoft.KeyVault(SecretUri=https://.vault.azure.net/secrets/s)",
			wantErr:   true,
			errSubstr: "could not extract vault name",
		},
		{
			name:    "sovereign cloud endpoint (Azure China)",
			input:   "@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.cn/secrets/mysecret)",
			wantErr: false,
		},
		{
			name: "sovereign cloud endpoint (US Gov)",
			input: "@Microsoft.KeyVault(" +
				"SecretUri=https://myvault.vault.usgovcloudapi.net/secrets/mysecret)",
			wantErr: false,
		},
		{
			name: "managed HSM endpoint",
			input: "@Microsoft.KeyVault(" +
				"SecretUri=https://myhsm.managedhsm.azure.net/secrets/mysecret)",
			wantErr: false,
		},
		{
			name: "non-standard port is rejected",
			input: "@Microsoft.KeyVault(" +
				"SecretUri=https://myvault.vault.azure.net:8443/secrets/mysecret)",
			wantErr:   true,
			errSubstr: "non-standard port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ref, err := ParseKeyVaultAppReference(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errSubstr != "" {
					assert.Contains(t, err.Error(), tt.errSubstr)
				}
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, ref.SecretName)
				assert.NotEmpty(t, ref.VaultName)
				assert.NotEmpty(t, ref.VaultURL)
			}
		})
	}
}

func TestParseKeyVaultAppReference_VersionExtracted(t *testing.T) {
	t.Parallel()

	ref, err := ParseKeyVaultAppReference(
		"@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/s/ver123)")
	require.NoError(t, err)
	assert.Equal(t, "v", ref.VaultName)
	assert.Equal(t, "s", ref.SecretName)
	assert.Equal(t, "ver123", ref.SecretVersion)
	assert.Equal(t, "https://v.vault.azure.net", ref.VaultURL)
}

func TestParseKeyVaultAppReference_NoVersionIsEmpty(t *testing.T) {
	t.Parallel()

	ref, err := ParseKeyVaultAppReference(
		"@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/s)")
	require.NoError(t, err)
	assert.Empty(t, ref.SecretVersion)
}

func TestParseKeyVaultAppReference_WhitespaceAroundSecretUri(t *testing.T) {
	t.Parallel()

	ref, err := ParseKeyVaultAppReference(
		"@Microsoft.KeyVault( SecretUri= https://v.vault.azure.net/secrets/s )")
	require.NoError(t, err)
	assert.Equal(t, "s", ref.SecretName)
}

// ---------------------------------------------------------------------------
// IsKeyVaultAppReference — false-positive / false-negative cases
// ---------------------------------------------------------------------------

func TestIsKeyVaultAppReference_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		// Valid refs
		{
			"standard format",
			"@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/s)",
			true,
		},
		{
			"lowercase is accepted",
			"@microsoft.keyvault(secreturi=https://v.vault.azure.net/secrets/s)",
			true,
		},
		{
			"mixed case is accepted",
			"@MICROSOFT.KEYVAULT(SECRETURI=https://v.vault.azure.net/secrets/s)",
			true,
		},
		// Not refs
		{"empty string", "", false},
		{"null-like string", "null", false},
		{"plain value", "my-secret-value", false},
		{
			"looks like KV ref but uses VaultName/SecretName form",
			"@Microsoft.KeyVault(VaultName=myvault;SecretName=mysecret)",
			false,
		},
		{
			"missing closing paren",
			"@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/s",
			false,
		},
		{
			"missing SecretUri=",
			"@Microsoft.KeyVault(https://v.vault.azure.net/secrets/s)",
			false,
		},
		{
			"only prefix",
			"@Microsoft.KeyVault()",
			false,
		},
		{
			"prefix without SecretUri content",
			"@Microsoft.KeyVault(SecretUri=)",
			false, // inner is just "SecretUri=" which has no value after =
		},
		{
			"akvs scheme is not an app reference",
			"akvs://sub/vault/secret",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsKeyVaultAppReference(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// isValidVaultHost — exercise each sovereign cloud suffix
// ---------------------------------------------------------------------------

func TestIsValidVaultHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host string
		want bool
	}{
		{"myvault.vault.azure.net", true},
		{"myvault.vault.azure.cn", true},
		{"myvault.vault.usgovcloudapi.net", true},
		{"myvault.vault.microsoftazure.de", true},
		{"myhsm.managedhsm.azure.net", true},
		{"MYVAULT.VAULT.AZURE.NET", true}, // case-insensitive
		{"myvault.example.com", false},
		{"vault.azure.net.evil.com", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isValidVaultHost(tt.host))
		})
	}
}

// ---------------------------------------------------------------------------
// ParseAzureKeyVaultSecret — additional bad-input cases
// ---------------------------------------------------------------------------

func TestParseAzureKeyVaultSecret_BadInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		errSubstr string
	}{
		{
			"malformed vault URI (full URL instead of akvs)",
			"https://myvault.vault.azure.net/secrets/mysecret",
			"invalid Azure Key Vault Secret",
		},
		{
			"missing secret name in URI (two parts only)",
			"akvs://sub-id/vault-name",
			"Expected format",
		},
		{
			"whitespace-only string",
			"   ",
			"invalid Azure Key Vault Secret",
		},
		{
			"just the schema prefix",
			"akvs://",
			"Expected format",
		},
		{
			"schema with only one segment",
			"akvs://subscription",
			"Expected format",
		},
		{
			"four segments (unexpected extra)",
			"akvs://sub/vault/secret/extra",
			"Expected format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseAzureKeyVaultSecret(tt.input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errSubstr)
		})
	}
}

// ---------------------------------------------------------------------------
// ResolveSecretEnvironment — additional scenarios
// ---------------------------------------------------------------------------

func TestResolveSecretEnvironment_AllKVRefs(t *testing.T) {
	t.Parallel()

	callCount := 0
	mock := &mockKeyVaultService{
		resolveFunc: func(_ context.Context, ref string, _ string) (string, error) {
			callCount++
			return "resolved-" + ref[len("akvs://"):], nil
		},
	}

	input := []string{
		"A=akvs://sub/vault/s1",
		"B=akvs://sub/vault/s2",
		"C=akvs://sub/vault/s3",
	}

	result, err := ResolveSecretEnvironment(t.Context(), mock, input, "sub")
	require.NoError(t, err)
	assert.Equal(t, 3, callCount)
	for _, r := range result {
		assert.Contains(t, r, "resolved-")
	}
}

func TestResolveSecretEnvironment_EmptySlice(t *testing.T) {
	t.Parallel()

	mock := &mockKeyVaultService{
		resolveFunc: func(_ context.Context, _ string, _ string) (string, error) {
			t.Fatal("should not be called for empty input")
			return "", nil
		},
	}

	result, err := ResolveSecretEnvironment(t.Context(), mock, []string{}, "sub")
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestResolveSecretEnvironment_MixedLiteralAndKVRef(t *testing.T) {
	t.Parallel()

	mock := &mockKeyVaultService{
		resolveFunc: func(_ context.Context, _ string, _ string) (string, error) {
			return "secret-value", nil
		},
	}

	input := []string{
		"PLAIN1=hello",
		"SECRET=akvs://sub/vault/name",
		"PLAIN2=world",
		"APPREF=@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/s)",
		"ANOTHER=plain",
	}

	result, err := ResolveSecretEnvironment(t.Context(), mock, input, "sub")
	require.NoError(t, err)
	assert.Equal(t, "PLAIN1=hello", result[0])
	assert.Equal(t, "SECRET=secret-value", result[1])
	assert.Equal(t, "PLAIN2=world", result[2])
	assert.Equal(t, "APPREF=secret-value", result[3])
	assert.Equal(t, "ANOTHER=plain", result[4])
}

func TestResolveSecretEnvironment_PartialError(t *testing.T) {
	t.Parallel()

	// One ref succeeds, another fails — the successful one should still resolve.
	mock := &mockKeyVaultService{
		resolveFunc: func(_ context.Context, ref string, _ string) (string, error) {
			if strings.Contains(ref, "good") {
				return "good-value", nil
			}
			return "", errors.New("vault unavailable")
		},
	}

	input := []string{
		"GOOD=akvs://sub/vault/good",
		"BAD=akvs://sub/vault/bad",
	}

	result, err := ResolveSecretEnvironment(t.Context(), mock, input, "sub")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"BAD"`)
	// The good one resolves; the bad one is empty
	assert.Equal(t, "GOOD=good-value", result[0])
	assert.Equal(t, "BAD=", result[1])
}

func TestResolveSecretEnvironment_ValueWithEqualsSign(t *testing.T) {
	t.Parallel()

	mock := &mockKeyVaultService{}

	// Values containing '=' after the first should be preserved
	input := []string{
		"CONN=Server=mydb;Password=abc=123",
	}

	result, err := ResolveSecretEnvironment(t.Context(), mock, input, "sub")
	require.NoError(t, err)
	assert.Equal(t, "CONN=Server=mydb;Password=abc=123", result[0])
}

func TestResolveSecretEnvironment_AppRefOnly(t *testing.T) {
	t.Parallel()

	mock := &mockKeyVaultService{
		resolveFunc: func(_ context.Context, ref string, _ string) (string, error) {
			return "app-ref-resolved", nil
		},
	}

	input := []string{
		"SECRET=@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/s)",
	}

	result, err := ResolveSecretEnvironment(t.Context(), mock, input, "sub")
	require.NoError(t, err)
	assert.Equal(t, "SECRET=app-ref-resolved", result[0])
}

// ---------------------------------------------------------------------------
// IsSecretReference — comprehensive coverage
// ---------------------------------------------------------------------------

func TestIsSecretReference_Extended(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  bool
	}{
		// akvs variants
		{"akvs://sub/vault/secret", true},
		{"akvs://x/y/z", true},
		// app reference variants
		{
			"@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/s)",
			true,
		},
		{
			"@microsoft.keyvault(secreturi=https://v.vault.azure.net/secrets/s)",
			true,
		},
		// Not refs
		{"", false},
		{"plain-text", false},
		{"https://vault.azure.net/secrets/s", false},
		{"AKVS://SUB/VAULT/SECRET", false}, // case-sensitive prefix
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsSecretReference(tt.input))
		})
	}
}

// ---------------------------------------------------------------------------
// IsAzureKeyVaultSecret — additional edge cases
// ---------------------------------------------------------------------------

func TestIsAzureKeyVaultSecret_Extended(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  bool
	}{
		{"akvs://x/y/z", true},
		{"akvs://", true},
		{"akvs://a", true},
		{"AKVS://x/y/z", false}, // case-sensitive
		{"Akvs://x/y/z", false},
		{"akvs:/x", false}, // single slash
		{"akvs", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsAzureKeyVaultSecret(tt.input))
		})
	}
}

// ---------------------------------------------------------------------------
// IsValidSecretName — additional edge cases
// ---------------------------------------------------------------------------

func TestIsValidSecretName_Extended(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"starts with hyphen", "-secret", true},
		{"ends with hyphen", "secret-", true},
		{"all hyphens", "---", true},
		{"single digit", "1", true},
		{"digits only", "12345", true},
		{"exactly 127 chars", strings.Repeat("a", 127), true},
		{"exactly 128 chars", strings.Repeat("a", 128), false},
		{"tab character", "my\tsecret", false},
		{"newline character", "my\nsecret", false},
		{"unicode char", "café", false},
		{"backslash", "my\\secret", false},
		{"colon", "my:secret", false},
		{"equals sign", "my=secret", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsValidSecretName(tt.input))
		})
	}
}

// ---------------------------------------------------------------------------
// NewAzureKeyVaultSecret — verify format
// ---------------------------------------------------------------------------

func TestNewAzureKeyVaultSecret_Format(t *testing.T) {
	t.Parallel()

	result := NewAzureKeyVaultSecret("sub", "vault", "secret")
	assert.Equal(t, "akvs://sub/vault/secret", result)
	assert.True(t, IsAzureKeyVaultSecret(result))

	// Roundtrip
	parsed, err := ParseAzureKeyVaultSecret(result)
	require.NoError(t, err)
	assert.Equal(t, "sub", parsed.SubscriptionId)
	assert.Equal(t, "vault", parsed.VaultName)
	assert.Equal(t, "secret", parsed.SecretName)
}

// ---------------------------------------------------------------------------
// ErrAzCliSecretNotFound — sentinel error behavior
// ---------------------------------------------------------------------------

func TestErrAzCliSecretNotFound_IsSentinel(t *testing.T) {
	t.Parallel()

	// Verify the sentinel can be matched with errors.Is
	wrappedErr := errors.New("secret not found")
	assert.Equal(t, wrappedErr.Error(), ErrAzCliSecretNotFound.Error())

	// errors.Is with the exact sentinel
	assert.True(t, errors.Is(ErrAzCliSecretNotFound, ErrAzCliSecretNotFound))
}
