// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// stubCredential is a test double for azcore.TokenCredential.
type stubCredential struct {
	token azcore.AccessToken
	err   error
}

func (s *stubCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return s.token, s.err
}

func TestNewTokenProvider_NilClient(t *testing.T) {
	t.Parallel()

	_, err := NewTokenProvider(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestNewTokenProvider_ExplicitTenantAndCredential(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{
		token: azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)},
	}

	// Use a minimal AzdClient (no gRPC connection needed since we supply tenant+cred).
	client := &AzdClient{}
	tp, err := NewTokenProvider(context.Background(), client, &TokenProviderOptions{
		TenantID:   "test-tenant-id",
		Credential: cred,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tp.TenantID() != "test-tenant-id" {
		t.Errorf("TenantID() = %q, want %q", tp.TenantID(), "test-tenant-id")
	}

	tok, err := tp.GetToken(context.Background(), policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		t.Fatalf("GetToken failed: %v", err)
	}

	if tok.Token != "test-token" {
		t.Errorf("Token = %q, want %q", tok.Token, "test-token")
	}
}

func TestTokenProvider_GetToken_NoScopes(t *testing.T) {
	t.Parallel()

	cred := &stubCredential{
		token: azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)},
	}

	tp := &TokenProvider{
		credential: cred,
		tenantID:   "tenant",
	}

	_, err := tp.GetToken(context.Background(), policy.TokenRequestOptions{})
	if err == nil {
		t.Fatal("expected error when no scopes provided")
	}
}

func TestTokenProvider_GetToken_CredentialError(t *testing.T) {
	t.Parallel()

	credErr := errors.New("credential unavailable")
	cred := &stubCredential{err: credErr}

	tp := &TokenProvider{
		credential: cred,
		tenantID:   "tenant",
	}

	_, err := tp.GetToken(context.Background(), policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err == nil {
		t.Fatal("expected error when credential fails")
	}

	if !errors.Is(err, credErr) {
		t.Errorf("error = %v, want wrapping %v", err, credErr)
	}
}

func TestTokenProvider_ImplementsTokenCredential(t *testing.T) {
	t.Parallel()

	// Compile-time check is in the production file; this is a runtime confirmation.
	var _ azcore.TokenCredential = &TokenProvider{}
}
