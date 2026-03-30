// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/stretchr/testify/require"
)

// stubSecretGetter is a test double for the Key Vault data-plane client.
// It records the name and version args it receives for verification.
type stubSecretGetter struct {
	resp azsecrets.GetSecretResponse
	err  error

	// Recorded call args (set on each GetSecret call).
	calledName    string
	calledVersion string
}

func (s *stubSecretGetter) GetSecret(
	_ context.Context, name string, version string, _ *azsecrets.GetSecretOptions,
) (azsecrets.GetSecretResponse, error) {
	s.calledName = name
	s.calledVersion = version
	return s.resp, s.err
}

// stubSecretFactory returns a factory that always returns the given stubSecretGetter.
func stubSecretFactory(g SecretGetter, factoryErr error) func(string, azcore.TokenCredential) (SecretGetter, error) {
	return func(_ string, _ azcore.TokenCredential) (SecretGetter, error) {
		if factoryErr != nil {
			return nil, factoryErr
		}
		return g, nil
	}
}

// --- NewKeyVaultResolver ---

func TestNewKeyVaultResolver_NilCredential(t *testing.T) {
	t.Parallel()

	_, err := NewKeyVaultResolver(nil, nil)
	if err == nil {
		t.Fatal("expected error for nil credential")
	}
}

func TestNewKeyVaultResolver_Defaults(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolver.opts.VaultSuffix != "vault.azure.net" {
		t.Errorf("VaultSuffix = %q, want %q", resolver.opts.VaultSuffix, "vault.azure.net")
	}
}

func TestNewKeyVaultResolver_CustomSuffix(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		VaultSuffix: "vault.azure.cn",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolver.opts.VaultSuffix != "vault.azure.cn" {
		t.Errorf("VaultSuffix = %q, want %q", resolver.opts.VaultSuffix, "vault.azure.cn")
	}
}

// --- IsSecretReference ---

func TestIsSecretReference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  bool
	}{
		{"akvs://sub/vault/secret", true},
		{"akvs://", true},
		{"AKVS://sub/vault/secret", false}, // case-sensitive
		{"https://vault.azure.net", false},
		{"", false},
		// @Microsoft.KeyVault format
		{"@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/s)", true},
		// case-insensitive prefix (matches Azure App Service behavior)
		{"@microsoft.keyvault(secreturi=https://v.vault.azure.net/secrets/s)", true},
		// VaultName/SecretName form is now supported
		{"@Microsoft.KeyVault(VaultName=v;SecretName=s)", true},
	}

	for _, tt := range tests {
		if got := IsSecretReference(tt.input); got != tt.want {
			t.Errorf("IsSecretReference(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- ParseSecretReference ---

func TestParseSecretReference_Valid(t *testing.T) {
	t.Parallel()

	ref, err := ParseSecretReference("akvs://sub-123/my-vault/my-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ref.SubscriptionID != "sub-123" {
		t.Errorf("SubscriptionID = %q, want %q", ref.SubscriptionID, "sub-123")
	}
	if ref.VaultName != "my-vault" {
		t.Errorf("VaultName = %q, want %q", ref.VaultName, "my-vault")
	}
	if ref.SecretName != "my-secret" {
		t.Errorf("SecretName = %q, want %q", ref.SecretName, "my-secret")
	}
}

func TestParseSecretReference_NotAkvsScheme(t *testing.T) {
	t.Parallel()

	_, err := ParseSecretReference("https://vault.azure.net/secrets/x")
	if err == nil {
		t.Fatal("expected error for non-akvs scheme")
	}
}

func TestParseSecretReference_TooFewParts(t *testing.T) {
	t.Parallel()

	_, err := ParseSecretReference("akvs://sub/vault")
	if err == nil {
		t.Fatal("expected error for two-part ref")
	}
}

func TestParseSecretReference_TooManyParts(t *testing.T) {
	t.Parallel()

	_, err := ParseSecretReference("akvs://sub/vault/secret/extra")
	if err == nil {
		t.Fatal("expected error for four-part ref")
	}
}

func TestParseSecretReference_EmptyComponent(t *testing.T) {
	t.Parallel()

	cases := []string{
		"akvs:///vault/secret",   // empty subscription
		"akvs://sub//secret",     // empty vault
		"akvs://sub/vault/",      // empty secret
		"akvs://  /vault/secret", // whitespace subscription
		"akvs://sub/  /secret",   // whitespace vault
		"akvs://sub/vault/   ",   // whitespace secret
	}

	for _, ref := range cases {
		_, err := ParseSecretReference(ref)
		if err == nil {
			t.Errorf("ParseSecretReference(%q) expected error, got nil", ref)
		}
	}
}

// --- Resolve ---

func TestResolve_Success(t *testing.T) {
	t.Parallel()

	secretValue := "super-secret-value"
	getter := &stubSecretGetter{
		resp: azsecrets.GetSecretResponse{
			Secret: azsecrets.Secret{
				Value: &secretValue,
			},
		},
	}

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := resolver.Resolve(t.Context(), "akvs://sub-id/my-vault/my-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if val != secretValue {
		t.Errorf("Resolve() = %q, want %q", val, secretValue)
	}

	// Verify the stub received the correct name and version args.
	if getter.calledName != "my-secret" {
		t.Errorf("stub received name = %q, want %q", getter.calledName, "my-secret")
	}
	if getter.calledVersion != "" {
		t.Errorf("stub received version = %q, want empty", getter.calledVersion)
	}
}

func TestResolve_NilContext(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(&stubSecretGetter{}, nil),
	})

	//nolint:staticcheck // intentionally testing nil context
	//lint:ignore SA1012 intentionally testing nil context handling
	_, err := resolver.Resolve(nil, "akvs://sub/vault/secret")
	if err == nil {
		t.Fatal("expected error for nil context")
	}
}

func TestResolve_InvalidReference(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(&stubSecretGetter{}, nil),
	})

	_, err := resolver.Resolve(t.Context(), "not-akvs://x")
	if err == nil {
		t.Fatal("expected error for invalid reference")
	}

	var resolveErr *KeyVaultResolveError
	if !errors.As(err, &resolveErr) {
		t.Fatalf("error type = %T, want *KeyVaultResolveError", err)
	}

	if resolveErr.Reason != ResolveReasonInvalidReference {
		t.Errorf("Reason = %v, want %v", resolveErr.Reason, ResolveReasonInvalidReference)
	}
}

func TestResolve_ClientCreationFailure(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(nil, errors.New("connection refused")),
	})

	_, err := resolver.Resolve(t.Context(), "akvs://sub/vault/secret")
	if err == nil {
		t.Fatal("expected error for client creation failure")
	}

	var resolveErr *KeyVaultResolveError
	if !errors.As(err, &resolveErr) {
		t.Fatalf("error type = %T, want *KeyVaultResolveError", err)
	}

	if resolveErr.Reason != ResolveReasonClientCreation {
		t.Errorf("Reason = %v, want %v", resolveErr.Reason, ResolveReasonClientCreation)
	}
}

func TestResolve_HTTPErrorClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		wantReason ResolveReason
	}{
		{"NotFound", http.StatusNotFound, ResolveReasonNotFound},
		{"Forbidden", http.StatusForbidden, ResolveReasonAccessDenied},
		{"Unauthorized", http.StatusUnauthorized, ResolveReasonAccessDenied},
		{"InternalServerError", http.StatusInternalServerError, ResolveReasonServiceError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			getter := &stubSecretGetter{
				err: &azcore.ResponseError{StatusCode: tt.statusCode},
			}

			cred := &stubCredential{}
			resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
				ClientFactory: stubSecretFactory(getter, nil),
			})

			_, err := resolver.Resolve(t.Context(), "akvs://sub/vault/secret")
			if err == nil {
				t.Fatalf("expected error for HTTP %d", tt.statusCode)
			}

			var resolveErr *KeyVaultResolveError
			if !errors.As(err, &resolveErr) {
				t.Fatalf("error type = %T, want *KeyVaultResolveError", err)
			}

			if resolveErr.Reason != tt.wantReason {
				t.Errorf("Reason = %v, want %v", resolveErr.Reason, tt.wantReason)
			}
		})
	}
}

func TestResolve_NilValue(t *testing.T) {
	t.Parallel()

	getter := &stubSecretGetter{
		resp: azsecrets.GetSecretResponse{
			Secret: azsecrets.Secret{
				Value: nil,
			},
		},
	}

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})

	_, err := resolver.Resolve(t.Context(), "akvs://sub/vault/secret")
	if err == nil {
		t.Fatal("expected error for nil secret value")
	}

	var resolveErr *KeyVaultResolveError
	if !errors.As(err, &resolveErr) {
		t.Fatalf("error type = %T, want *KeyVaultResolveError", err)
	}

	if resolveErr.Reason != ResolveReasonNotFound {
		t.Errorf("Reason = %v, want %v", resolveErr.Reason, ResolveReasonNotFound)
	}
}

func TestResolve_NonResponseError(t *testing.T) {
	t.Parallel()

	getter := &stubSecretGetter{
		err: errors.New("network timeout"),
	}

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})

	_, err := resolver.Resolve(t.Context(), "akvs://sub/vault/secret")
	if err == nil {
		t.Fatal("expected error for network failure")
	}

	var resolveErr *KeyVaultResolveError
	if !errors.As(err, &resolveErr) {
		t.Fatalf("error type = %T, want *KeyVaultResolveError", err)
	}

	// Non-ResponseError defaults to service_error (not access_denied),
	// since non-HTTP errors are typically connectivity/network issues.
	if resolveErr.Reason != ResolveReasonServiceError {
		t.Errorf("Reason = %v, want %v", resolveErr.Reason, ResolveReasonServiceError)
	}
}

// --- ResolveMap ---

func TestResolveMap_MixedValues(t *testing.T) {
	t.Parallel()

	secretValue := "resolved-secret"
	getter := &stubSecretGetter{
		resp: azsecrets.GetSecretResponse{
			Secret: azsecrets.Secret{
				Value: &secretValue,
			},
		},
	}

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})

	input := map[string]string{ //nolint:gosec // G101 false positive: test fixture, not real credentials
		"plain":  "hello-world",
		"secret": "akvs://sub/vault/secret",
	}

	result, err := resolver.ResolveMap(t.Context(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result["plain"] != "hello-world" {
		t.Errorf("result[plain] = %q, want %q", result["plain"], "hello-world")
	}

	if result["secret"] != secretValue {
		t.Errorf("result[secret] = %q, want %q", result["secret"], secretValue)
	}
}

func TestResolveMap_Empty(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(&stubSecretGetter{}, nil),
	})

	result, err := resolver.ResolveMap(t.Context(), map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0", len(result))
	}
}

func TestResolveMap_ErrorCollectsAllFailures(t *testing.T) {
	t.Parallel()

	getter := &stubSecretGetter{
		err: &azcore.ResponseError{StatusCode: http.StatusNotFound},
	}

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})

	input := map[string]string{ //nolint:gosec // G101 false positive: test fixture, not real credentials
		"secret1": "akvs://sub/vault/missing1",
		"secret2": "akvs://sub/vault/missing2",
		"secret3": "akvs://sub/vault/missing3",
		"plain":   "not-a-secret-ref",
	}

	// ResolveMap collects errors instead of stopping at the first one.
	result, err := resolver.ResolveMap(t.Context(), input)
	if err == nil {
		t.Fatal("expected error when resolution fails")
	}

	// Partial result should be non-nil and contain the plain value.
	if result == nil {
		t.Fatal("expected non-nil partial result")
	}

	if result["plain"] != "not-a-secret-ref" {
		t.Errorf("result[plain] = %q, want %q", result["plain"], "not-a-secret-ref")
	}

	// The error should mention all 3 failing keys.
	errMsg := err.Error()
	for _, key := range []string{"secret1", "secret2", "secret3"} {
		if !strings.Contains(errMsg, key) {
			t.Errorf("error should mention %q, got: %s", key, errMsg)
		}
	}
}

func TestResolveMap_NilContext(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(&stubSecretGetter{}, nil),
	})

	//nolint:staticcheck // intentionally testing nil context
	//lint:ignore SA1012 intentionally testing nil context handling
	_, err := resolver.ResolveMap(nil, map[string]string{"k": "v"})
	if err == nil {
		t.Fatal("expected error for nil context")
	}
}

// --- @Microsoft.KeyVault format ---

func TestParseSecretReference_AppRefValid(t *testing.T) {
	t.Parallel()

	ref, err := ParseSecretReference(
		"@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/mysecret)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ref.VaultName != "myvault" {
		t.Errorf("VaultName = %q, want %q", ref.VaultName, "myvault")
	}
	if ref.SecretName != "mysecret" {
		t.Errorf("SecretName = %q, want %q", ref.SecretName, "mysecret")
	}
	if ref.SecretVersion != "" {
		t.Errorf("SecretVersion = %q, want empty", ref.SecretVersion)
	}
	if ref.VaultURL != "https://myvault.vault.azure.net" {
		t.Errorf("VaultURL = %q, want %q", ref.VaultURL, "https://myvault.vault.azure.net")
	}
}

func TestParseSecretReference_AppRefValidWithVersion(t *testing.T) {
	t.Parallel()

	ref, err := ParseSecretReference(
		"@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/mysecret/version123)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ref.SecretName != "mysecret" {
		t.Errorf("SecretName = %q, want %q", ref.SecretName, "mysecret")
	}
	if ref.SecretVersion != "version123" {
		t.Errorf("SecretVersion = %q, want %q", ref.SecretVersion, "version123")
	}
}

func TestParseSecretReference_AppRefInvalidHost(t *testing.T) {
	t.Parallel()

	_, err := ParseSecretReference(
		"@Microsoft.KeyVault(SecretUri=https://evil.com/secrets/foo)")
	if err == nil {
		t.Fatal("expected error for non-Azure Key Vault host")
	}

	if !strings.Contains(err.Error(), "not a known Azure Key Vault endpoint") {
		t.Errorf("error = %q, want mention of 'not a known Azure Key Vault endpoint'", err.Error())
	}
}

func TestParseSecretReference_AppRefMalformedURI(t *testing.T) {
	t.Parallel()

	_, err := ParseSecretReference(
		"@Microsoft.KeyVault(SecretUri=not-a-url)")
	if err == nil {
		t.Fatal("expected error for malformed SecretUri")
	}
}

func TestParseSecretReference_AppRefSovereignClouds(t *testing.T) {
	t.Parallel()

	validHosts := []struct {
		name string
		uri  string
	}{
		{"AzureChina", "https://myvault.vault.azure.cn/secrets/s"},
		{"AzureGov", "https://myvault.vault.usgovcloudapi.net/secrets/s"},
		{"AzureGermany", "https://myvault.vault.microsoftazure.de/secrets/s"},
		{"ManagedHSM", "https://myvault.managedhsm.azure.net/secrets/s"},
	}

	for _, tc := range validHosts {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := ParseSecretReference(
				fmt.Sprintf("@Microsoft.KeyVault(SecretUri=%s)", tc.uri))
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.name, err)
			}
			if ref.SecretName != "s" {
				t.Errorf("SecretName = %q, want %q", ref.SecretName, "s")
			}
		})
	}
}

func TestResolve_AppRefSuccess(t *testing.T) {
	t.Parallel()

	secretValue := "app-ref-secret-value" //nolint:gosec // test data, not a real credential
	getter := &stubSecretGetter{
		resp: azsecrets.GetSecretResponse{
			Secret: azsecrets.Secret{
				Value: &secretValue,
			},
		},
	}

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := resolver.Resolve(t.Context(),
		"@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/mysecret)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if val != secretValue {
		t.Errorf("Resolve() = %q, want %q", val, secretValue)
	}
}

func TestResolve_AppRefWithVersion(t *testing.T) {
	t.Parallel()

	secretValue := "versioned-value"
	getter := &stubSecretGetter{
		resp: azsecrets.GetSecretResponse{
			Secret: azsecrets.Secret{
				Value: &secretValue,
			},
		},
	}

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := resolver.Resolve(t.Context(),
		"@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/mysecret/v1)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if val != secretValue {
		t.Errorf("Resolve() = %q, want %q", val, secretValue)
	}

	// Verify name and version were dispatched correctly.
	if getter.calledName != "mysecret" {
		t.Errorf("stub received name = %q, want %q", getter.calledName, "mysecret")
	}
	if getter.calledVersion != "v1" {
		t.Errorf("stub received version = %q, want %q", getter.calledVersion, "v1")
	}
}

func TestResolve_AppRefInvalidHostReturnsError(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(&stubSecretGetter{}, nil),
	})

	_, err := resolver.Resolve(t.Context(),
		"@Microsoft.KeyVault(SecretUri=https://evil.com/secrets/foo)")
	if err == nil {
		t.Fatal("expected error for invalid vault host")
	}

	var resolveErr *KeyVaultResolveError
	if !errors.As(err, &resolveErr) {
		t.Fatalf("error type = %T, want *KeyVaultResolveError", err)
	}

	if resolveErr.Reason != ResolveReasonInvalidReference {
		t.Errorf("Reason = %v, want %v", resolveErr.Reason, ResolveReasonInvalidReference)
	}
}

// --- Error types ---

func TestKeyVaultResolveError_Error(t *testing.T) {
	t.Parallel()

	err := &KeyVaultResolveError{
		Reference: "akvs://sub/vault/secret",
		Reason:    ResolveReasonNotFound,
		Err:       errors.New("secret not found"),
	}

	got := err.Error()
	if got == "" {
		t.Fatal("Error() returned empty string")
	}
}

func TestKeyVaultResolveError_Unwrap(t *testing.T) {
	t.Parallel()

	inner := errors.New("inner error")
	err := &KeyVaultResolveError{
		Reference: "akvs://sub/vault/secret",
		Reason:    ResolveReasonServiceError,
		Err:       inner,
	}

	if !errors.Is(err, inner) {
		t.Error("Unwrap should expose inner error via errors.Is")
	}
}

func TestResolveReason_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reason ResolveReason
		want   string
	}{
		{ResolveReasonInvalidReference, "invalid_reference"},
		{ResolveReasonClientCreation, "client_creation"},
		{ResolveReasonNotFound, "not_found"},
		{ResolveReasonAccessDenied, "access_denied"},
		{ResolveReasonServiceError, "service_error"},
		{ResolveReason(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.reason.String(); got != tt.want {
			t.Errorf("ResolveReason(%d).String() = %q, want %q", tt.reason, got, tt.want)
		}
	}
}

// --- VaultName/SecretName format ---

func TestIsSecretReference_VaultNameFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"basic", "@Microsoft.KeyVault(VaultName=myvault;SecretName=mysecret)", true},
		{"with_version", "@Microsoft.KeyVault(VaultName=myvault;SecretName=mysecret;SecretVersion=v1)", true},
		{"case_insensitive", "@microsoft.keyvault(vaultname=v;secretname=s)", true},
		{"mixed_case", "@Microsoft.KeyVault(vaultName=v;secretName=s)", true},
		{"missing_secret_name", "@Microsoft.KeyVault(VaultName=v)", false},
		{"empty_inner", "@Microsoft.KeyVault()", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, IsSecretReference(tt.input),
				"IsSecretReference(%q)", tt.input)
		})
	}
}

func TestIsSecretReference_QuoteStripping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"double_quoted_akvs", `"akvs://sub/vault/secret"`, true},
		{"single_quoted_akvs", `'akvs://sub/vault/secret'`, true},
		{"whitespace_akvs", "  akvs://sub/vault/secret  ", true},
		{"quoted_with_whitespace", `  "akvs://sub/vault/secret"  `, true},
		{"double_quoted_appref",
			`"@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/s)"`, true},
		{"single_quoted_vaultname",
			`'@Microsoft.KeyVault(VaultName=v;SecretName=s)'`, true},
		{"empty_after_strip", `""`, false},
		{"only_whitespace", "   ", false},
		{"mismatched_quotes", `"akvs://sub/vault/secret'`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, IsSecretReference(tt.input),
				"IsSecretReference(%q)", tt.input)
		})
	}
}

func TestParseSecretReference_VaultNameFormat(t *testing.T) {
	t.Parallel()

	ref, err := ParseSecretReference(
		"@Microsoft.KeyVault(VaultName=myvault;SecretName=mysecret)")
	require.NoError(t, err)

	require.Equal(t, "myvault", ref.VaultName)
	require.Equal(t, "mysecret", ref.SecretName)
	require.Empty(t, ref.SecretVersion, "SecretVersion should be empty when not specified")
	require.Empty(t, ref.SubscriptionID, "SubscriptionID should be empty for VaultName format")
	require.Empty(t, ref.VaultURL, "VaultURL should be empty for VaultName format (derived by resolver)")
}

func TestParseSecretReference_VaultNameWithVersion(t *testing.T) {
	t.Parallel()

	ref, err := ParseSecretReference(
		"@Microsoft.KeyVault(VaultName=myvault;SecretName=mysecret;SecretVersion=abc123)")
	require.NoError(t, err)

	require.Equal(t, "myvault", ref.VaultName)
	require.Equal(t, "mysecret", ref.SecretName)
	require.Equal(t, "abc123", ref.SecretVersion)
}

func TestParseSecretReference_VaultNameMissingSecretName(t *testing.T) {
	t.Parallel()

	_, err := ParseSecretReference("@Microsoft.KeyVault(VaultName=myvault)")
	require.Error(t, err)
	require.Contains(t, err.Error(), "SecretName must not be empty")
}

func TestParseSecretReference_VaultNameMissingVaultName(t *testing.T) {
	t.Parallel()

	_, err := ParseSecretReference("@Microsoft.KeyVault(VaultName=;SecretName=mysecret)")
	require.Error(t, err)
	require.Contains(t, err.Error(), "VaultName must not be empty")
}

func TestParseSecretReference_QuotedInput(t *testing.T) {
	t.Parallel()

	ref, err := ParseSecretReference(`"akvs://sub-123/my-vault/my-secret"`)
	require.NoError(t, err)
	require.Equal(t, "sub-123", ref.SubscriptionID)
	require.Equal(t, "my-vault", ref.VaultName)
	require.Equal(t, "my-secret", ref.SecretName)
}

// --- ResolveEnvironment ---

func TestResolveEnvironment_MixedValues(t *testing.T) {
	t.Parallel()

	secretValue := "resolved-env-secret" //nolint:gosec // test data, not a real credential
	getter := &stubSecretGetter{
		resp: azsecrets.GetSecretResponse{
			Secret: azsecrets.Secret{
				Value: &secretValue,
			},
		},
	}

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})
	require.NoError(t, err)

	env := map[string]string{ //nolint:gosec // G101 false positive: test fixture, not real credentials
		"DATABASE_URL": "postgres://localhost/mydb",
		"API_KEY":      "akvs://sub/vault/api-key",
		"APP_NAME":     "my-app",
	}

	result, err := resolver.ResolveEnvironment(t.Context(), env)
	require.NoError(t, err)

	// Plain values pass through unchanged.
	require.Equal(t, "postgres://localhost/mydb", result["DATABASE_URL"])
	require.Equal(t, "my-app", result["APP_NAME"])

	// Secret reference is resolved.
	require.Equal(t, secretValue, result["API_KEY"])

	// All keys are present.
	require.Len(t, result, 3)
}

func TestResolveEnvironment_NoRefs(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(&stubSecretGetter{}, nil),
	})
	require.NoError(t, err)

	env := map[string]string{
		"PLAIN1": "value1",
		"PLAIN2": "value2",
	}

	result, err := resolver.ResolveEnvironment(t.Context(), env)
	require.NoError(t, err)
	require.Equal(t, env, result)
}

func TestResolveEnvironment_Empty(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(&stubSecretGetter{}, nil),
	})
	require.NoError(t, err)

	result, err := resolver.ResolveEnvironment(t.Context(), map[string]string{})
	require.NoError(t, err)
	require.Empty(t, result)
}

func TestResolveEnvironment_PartialError(t *testing.T) {
	t.Parallel()

	getter := &stubSecretGetter{
		err: &azcore.ResponseError{StatusCode: http.StatusNotFound},
	}

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})
	require.NoError(t, err)

	env := map[string]string{ //nolint:gosec // G101 false positive: test fixture, not real credentials
		"PLAIN":  "hello",
		"SECRET": "akvs://sub/vault/missing",
	}

	result, err := resolver.ResolveEnvironment(t.Context(), env)
	require.Error(t, err)

	// Partial result should contain all keys.
	require.NotNil(t, result)
	require.Equal(t, "hello", result["PLAIN"])

	// Failed ref is preserved as original value.
	require.Equal(t, "akvs://sub/vault/missing", result["SECRET"])
}

func TestResolveEnvironment_NilContext(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(&stubSecretGetter{}, nil),
	})

	//nolint:staticcheck // intentionally testing nil context
	//lint:ignore SA1012 intentionally testing nil context handling
	_, err := resolver.ResolveEnvironment(nil, map[string]string{"k": "v"})
	require.Error(t, err)
}

// --- Resolve with VaultName format ---

func TestResolve_VaultNameFormat(t *testing.T) {
	t.Parallel()

	secretValue := "vaultname-secret-value" //nolint:gosec // test data, not a real credential
	getter := &stubSecretGetter{
		resp: azsecrets.GetSecretResponse{
			Secret: azsecrets.Secret{
				Value: &secretValue,
			},
		},
	}

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})
	require.NoError(t, err)

	val, err := resolver.Resolve(t.Context(),
		"@Microsoft.KeyVault(VaultName=myvault;SecretName=mysecret)")
	require.NoError(t, err)
	require.Equal(t, secretValue, val)

	require.Equal(t, "mysecret", getter.calledName)
	require.Empty(t, getter.calledVersion)
}

func TestResolve_VaultNameFormatWithVersion(t *testing.T) {
	t.Parallel()

	secretValue := "versioned-vaultname-value" //nolint:gosec // test data, not a real credential
	getter := &stubSecretGetter{
		resp: azsecrets.GetSecretResponse{
			Secret: azsecrets.Secret{
				Value: &secretValue,
			},
		},
	}

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})
	require.NoError(t, err)

	val, err := resolver.Resolve(t.Context(),
		"@Microsoft.KeyVault(VaultName=myvault;SecretName=mysecret;SecretVersion=v42)")
	require.NoError(t, err)
	require.Equal(t, secretValue, val)

	require.Equal(t, "mysecret", getter.calledName)
	require.Equal(t, "v42", getter.calledVersion)
}

// --- Additional edge-case tests ---

func TestParseSecretReference_VaultNameEmptySecretName(t *testing.T) {
	t.Parallel()

	_, err := ParseSecretReference("@Microsoft.KeyVault(VaultName=myvault;SecretName=)")
	require.Error(t, err)
	require.Contains(t, err.Error(), "SecretName must not be empty")
}

func TestParseSecretReference_VaultNameMalformedParameter(t *testing.T) {
	t.Parallel()

	_, err := ParseSecretReference("@Microsoft.KeyVault(VaultName=v;badparam;SecretName=s)")
	require.Error(t, err)
	require.Contains(t, err.Error(), "malformed parameter")
}

func TestParseSecretReference_VaultNameMixedCaseKeys(t *testing.T) {
	t.Parallel()

	ref, err := ParseSecretReference(
		"@Microsoft.KeyVault(vaultName=myvault;secretName=mysecret;secretVersion=v1)")
	require.NoError(t, err)

	require.Equal(t, "myvault", ref.VaultName)
	require.Equal(t, "mysecret", ref.SecretName)
	require.Equal(t, "v1", ref.SecretVersion)
}

func TestParseSecretReference_VaultNameInvalidVaultName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{"too_short", "@Microsoft.KeyVault(VaultName=ab;SecretName=s)"},
		{"starts_with_digit", "@Microsoft.KeyVault(VaultName=1vault;SecretName=s)"},
		{"special_chars", "@Microsoft.KeyVault(VaultName=my_vault!;SecretName=s)"},
		{"ends_with_hyphen", "@Microsoft.KeyVault(VaultName=my-vault-;SecretName=s)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseSecretReference(tt.input)
			require.Error(t, err)
			require.Contains(t, err.Error(), "must be 3-24 characters")
		})
	}
}

func TestParseSecretReference_QuotedWithInnerWhitespace(t *testing.T) {
	t.Parallel()

	// Quotes with internal whitespace — the whitespace inside quotes should be trimmed.
	ref, err := ParseSecretReference(`"  akvs://sub-123/my-vault/my-secret  "`)
	require.NoError(t, err)
	require.Equal(t, "sub-123", ref.SubscriptionID)
	require.Equal(t, "my-vault", ref.VaultName)
	require.Equal(t, "my-secret", ref.SecretName)
}

func TestParseSecretReference_NestedQuotesFails(t *testing.T) {
	t.Parallel()

	// Only one layer of quotes is stripped; the inner quotes remain and break parsing.
	_, err := ParseSecretReference(`'"akvs://sub/vault/secret"'`)
	require.Error(t, err)
}

func TestResolveEnvironment_NilMap(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(&stubSecretGetter{}, nil),
	})
	require.NoError(t, err)

	result, err := resolver.ResolveEnvironment(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Empty(t, result)
}

func TestResolveEnvironment_AllRefs(t *testing.T) {
	t.Parallel()

	secretValue := "all-refs-value" //nolint:gosec // test data, not a real credential
	getter := &stubSecretGetter{
		resp: azsecrets.GetSecretResponse{
			Secret: azsecrets.Secret{
				Value: &secretValue,
			},
		},
	}

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})
	require.NoError(t, err)

	env := map[string]string{ //nolint:gosec // G101 false positive: test fixture, not real credentials
		"SECRET1": "akvs://sub/vault/secret1",
		"SECRET2": "akvs://sub/vault/secret2",
	}

	result, err := resolver.ResolveEnvironment(t.Context(), env)
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, secretValue, result["SECRET1"])
	require.Equal(t, secretValue, result["SECRET2"])
}

func TestIsSecretReference_QuotesWithInnerWhitespace(t *testing.T) {
	t.Parallel()

	// Whitespace inside quotes should be trimmed after quote removal.
	require.True(t, IsSecretReference(`"  akvs://sub/vault/secret  "`))
	require.True(t, IsSecretReference(`'  akvs://sub/vault/secret  '`))
}

// --- CR-010: Verify VaultSuffix is used in URL construction ---

func TestResolve_VaultSuffixUsedInURL(t *testing.T) {
	t.Parallel()

	secretValue := "suffix-test" //nolint:gosec // test data, not a real credential
	getter := &stubSecretGetter{
		resp: azsecrets.GetSecretResponse{
			Secret: azsecrets.Secret{Value: &secretValue},
		},
	}

	var capturedURL string
	factory := func(vaultURL string, _ azcore.TokenCredential) (SecretGetter, error) {
		capturedURL = vaultURL
		return getter, nil
	}

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		VaultSuffix:   "vault.azure.cn",
		ClientFactory: factory,
	})
	require.NoError(t, err)

	_, err = resolver.Resolve(t.Context(), "akvs://sub/myvault/mysecret")
	require.NoError(t, err)

	require.Equal(t, "https://myvault.vault.azure.cn", capturedURL,
		"vault URL should use the custom VaultSuffix")
}

// --- CR-007: NewKeyVaultResolver does not mutate caller opts ---

func TestNewKeyVaultResolver_DoesNotMutateCallerOpts(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{}
	opts := &KeyVaultResolverOptions{} // VaultSuffix intentionally empty

	_, err := NewKeyVaultResolver(cred, opts)
	require.NoError(t, err)

	// Caller's opts should not be mutated.
	require.Empty(t, opts.VaultSuffix,
		"NewKeyVaultResolver must not mutate the caller's options struct")
}

// --- CR-006: Duplicate parameter keys are rejected ---

func TestParseSecretReference_VaultNameDuplicateKeyRejected(t *testing.T) {
	t.Parallel()

	_, err := ParseSecretReference(
		"@Microsoft.KeyVault(VaultName=vault1;VaultName=vault2;SecretName=s)")
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate parameter")
}

// --- CR-012: Nil client from factory returns error ---

func TestResolve_NilClientFromFactory(t *testing.T) {
	t.Parallel()

	factory := func(_ string, _ azcore.TokenCredential) (SecretGetter, error) {
		return nil, nil // buggy factory: no error but nil client
	}

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: factory,
	})
	require.NoError(t, err)

	_, err = resolver.Resolve(t.Context(), "akvs://sub/vault/secret")
	require.Error(t, err, "should fail when factory returns nil client without error")

	var resolveErr *KeyVaultResolveError
	require.ErrorAs(t, err, &resolveErr)
	require.Equal(t, ResolveReasonClientCreation, resolveErr.Reason)
}

// --- CR-011: Concurrent getOrCreateClient ---

func TestResolve_ConcurrentSameVault(t *testing.T) {
	t.Parallel()

	secretValue := "concurrent-value" //nolint:gosec // test data, not a real credential
	getter := &stubSecretGetter{
		resp: azsecrets.GetSecretResponse{
			Secret: azsecrets.Secret{Value: &secretValue},
		},
	}

	var callCount atomic.Int64
	factory := func(_ string, _ azcore.TokenCredential) (SecretGetter, error) {
		callCount.Add(1)
		return getter, nil
	}

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: factory,
	})
	require.NoError(t, err)

	const goroutines = 10
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ref := fmt.Sprintf("akvs://sub/vault/secret%d", idx)
			_, resolveErr := resolver.Resolve(t.Context(), ref)
			errs <- resolveErr
		}(i)
	}

	wg.Wait()
	close(errs)

	for resolveErr := range errs {
		require.NoError(t, resolveErr)
	}
}

// --- CR-008: Cache key normalization (case-insensitive) ---

func TestResolve_CacheKeyNormalization(t *testing.T) {
	t.Parallel()

	secretValue := "cache-norm-value" //nolint:gosec // test data, not a real credential
	getter := &stubSecretGetter{
		resp: azsecrets.GetSecretResponse{
			Secret: azsecrets.Secret{Value: &secretValue},
		},
	}

	var factoryCalls int
	factory := func(_ string, _ azcore.TokenCredential) (SecretGetter, error) {
		factoryCalls++
		return getter, nil
	}

	cred := &stubCredential{}
	resolver, err := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: factory,
	})
	require.NoError(t, err)

	// Two references to the same vault with different casing — should share one client.
	_, err = resolver.Resolve(t.Context(),
		"@Microsoft.KeyVault(SecretUri=https://MyVault.vault.azure.net/secrets/s1)")
	require.NoError(t, err)

	_, err = resolver.Resolve(t.Context(),
		"@Microsoft.KeyVault(SecretUri=https://MYVAULT.vault.azure.net/secrets/s2)")
	require.NoError(t, err)

	require.Equal(t, 1, factoryCalls,
		"same vault with different URL casing should reuse cached client")
}
