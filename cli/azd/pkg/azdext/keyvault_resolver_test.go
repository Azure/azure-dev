// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// stubSecretGetter is a test double for the Key Vault data-plane client.
type stubSecretGetter struct {
	resp azsecrets.GetSecretResponse
	err  error
}

func (s *stubSecretGetter) GetSecret(
	_ context.Context, _ string, _ string, _ *azsecrets.GetSecretOptions,
) (azsecrets.GetSecretResponse, error) {
	return s.resp, s.err
}

// stubSecretFactory returns a factory that always returns the given stubSecretGetter.
func stubSecretFactory(g secretGetter, factoryErr error) func(string, azcore.TokenCredential) (secretGetter, error) {
	return func(_ string, _ azcore.TokenCredential) (secretGetter, error) {
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

	val, err := resolver.Resolve(context.Background(), "akvs://sub-id/my-vault/my-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if val != secretValue {
		t.Errorf("Resolve() = %q, want %q", val, secretValue)
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

	_, err := resolver.Resolve(context.Background(), "not-akvs://x")
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

	_, err := resolver.Resolve(context.Background(), "akvs://sub/vault/secret")
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

func TestResolve_SecretNotFound(t *testing.T) {
	t.Parallel()

	getter := &stubSecretGetter{
		err: &azcore.ResponseError{StatusCode: http.StatusNotFound},
	}

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})

	_, err := resolver.Resolve(context.Background(), "akvs://sub/vault/missing-secret")
	if err == nil {
		t.Fatal("expected error for missing secret")
	}

	var resolveErr *KeyVaultResolveError
	if !errors.As(err, &resolveErr) {
		t.Fatalf("error type = %T, want *KeyVaultResolveError", err)
	}

	if resolveErr.Reason != ResolveReasonNotFound {
		t.Errorf("Reason = %v, want %v", resolveErr.Reason, ResolveReasonNotFound)
	}
}

func TestResolve_AccessDenied(t *testing.T) {
	t.Parallel()

	getter := &stubSecretGetter{
		err: &azcore.ResponseError{StatusCode: http.StatusForbidden},
	}

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})

	_, err := resolver.Resolve(context.Background(), "akvs://sub/vault/secret")
	if err == nil {
		t.Fatal("expected error for forbidden access")
	}

	var resolveErr *KeyVaultResolveError
	if !errors.As(err, &resolveErr) {
		t.Fatalf("error type = %T, want *KeyVaultResolveError", err)
	}

	if resolveErr.Reason != ResolveReasonAccessDenied {
		t.Errorf("Reason = %v, want %v", resolveErr.Reason, ResolveReasonAccessDenied)
	}
}

func TestResolve_Unauthorized(t *testing.T) {
	t.Parallel()

	getter := &stubSecretGetter{
		err: &azcore.ResponseError{StatusCode: http.StatusUnauthorized},
	}

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})

	_, err := resolver.Resolve(context.Background(), "akvs://sub/vault/secret")
	if err == nil {
		t.Fatal("expected error for unauthorized access")
	}

	var resolveErr *KeyVaultResolveError
	if !errors.As(err, &resolveErr) {
		t.Fatalf("error type = %T, want *KeyVaultResolveError", err)
	}

	if resolveErr.Reason != ResolveReasonAccessDenied {
		t.Errorf("Reason = %v, want %v", resolveErr.Reason, ResolveReasonAccessDenied)
	}
}

func TestResolve_ServiceError(t *testing.T) {
	t.Parallel()

	getter := &stubSecretGetter{
		err: &azcore.ResponseError{StatusCode: http.StatusInternalServerError},
	}

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})

	_, err := resolver.Resolve(context.Background(), "akvs://sub/vault/secret")
	if err == nil {
		t.Fatal("expected error for server error")
	}

	var resolveErr *KeyVaultResolveError
	if !errors.As(err, &resolveErr) {
		t.Fatalf("error type = %T, want *KeyVaultResolveError", err)
	}

	if resolveErr.Reason != ResolveReasonServiceError {
		t.Errorf("Reason = %v, want %v", resolveErr.Reason, ResolveReasonServiceError)
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

	_, err := resolver.Resolve(context.Background(), "akvs://sub/vault/secret")
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

	_, err := resolver.Resolve(context.Background(), "akvs://sub/vault/secret")
	if err == nil {
		t.Fatal("expected error for network failure")
	}

	var resolveErr *KeyVaultResolveError
	if !errors.As(err, &resolveErr) {
		t.Fatalf("error type = %T, want *KeyVaultResolveError", err)
	}

	// Non-ResponseError defaults to access_denied
	if resolveErr.Reason != ResolveReasonAccessDenied {
		t.Errorf("Reason = %v, want %v", resolveErr.Reason, ResolveReasonAccessDenied)
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

	result, err := resolver.ResolveMap(context.Background(), input)
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

	result, err := resolver.ResolveMap(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("len(result) = %d, want 0", len(result))
	}
}

func TestResolveMap_ErrorStopsProcessing(t *testing.T) {
	t.Parallel()

	getter := &stubSecretGetter{
		err: &azcore.ResponseError{StatusCode: http.StatusNotFound},
	}

	cred := &stubCredential{}
	resolver, _ := NewKeyVaultResolver(cred, &KeyVaultResolverOptions{
		ClientFactory: stubSecretFactory(getter, nil),
	})

	input := map[string]string{ //nolint:gosec // G101 false positive: test fixture, not real credentials
		"secret": "akvs://sub/vault/missing",
	}

	_, err := resolver.ResolveMap(context.Background(), input)
	if err == nil {
		t.Fatal("expected error when resolution fails")
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
