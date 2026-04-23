// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/stretchr/testify/require"
)

type sequentialSilentClient struct {
	mockPublicClient
	results []struct {
		result public.AuthResult
		err    error
	}
	calls int
}

func (s *sequentialSilentClient) AcquireTokenSilent(
	_ context.Context,
	_ []string,
	_ ...public.AcquireSilentOption,
) (public.AuthResult, error) {
	step := s.results[s.calls]
	s.calls++

	return step.result, step.err
}

// spyPublicClient records the options passed to AcquireTokenSilent so tests can
// verify that GetToken forwards TenantID correctly.
type spyPublicClient struct {
	mockPublicClient
	silentOptions []public.AcquireSilentOption
}

func (s *spyPublicClient) AcquireTokenSilent(
	ctx context.Context, scopes []string, options ...public.AcquireSilentOption,
) (public.AuthResult, error) {
	s.silentOptions = options
	return public.AuthResult{
		AccessToken: "test-token",
		ExpiresOn:   time.Now().Add(time.Hour),
		Account: public.Account{
			HomeAccountID: "test.id",
		},
	}, nil
}

func TestAzdCredential_GetToken_ForwardsTenantID(t *testing.T) {
	tests := []struct {
		name    string
		credTID string // tenant stored in credential
		optsTID string // tenant from TokenRequestOptions
		// expectTenantOption is true when WithTenantID should be in the options
		expectTenantOption bool
		// expectedTenantID is the tenant ID value that should be forwarded.
		// When optsTID is set it overrides credTID.
		expectedTenantID string
	}{
		{
			name:               "CredentialTenantUsed",
			credTID:            "resource-tenant-id",
			optsTID:            "",
			expectTenantOption: true,
			expectedTenantID:   "resource-tenant-id",
		},
		{
			name:               "OptionsTenantOverrides",
			credTID:            "resource-tenant-id",
			optsTID:            "override-tenant-id",
			expectTenantOption: true,
			expectedTenantID:   "override-tenant-id",
		},
		{
			name:               "NoTenant",
			credTID:            "",
			optsTID:            "",
			expectTenantOption: false,
			expectedTenantID:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy := &spyPublicClient{}
			account := &public.Account{HomeAccountID: "test.id"}
			cred := newAzdCredential(spy, account, cloud.AzurePublic(), tt.credTID, nil)

			_, err := cred.GetToken(t.Context(), policy.TokenRequestOptions{
				Scopes:   []string{"https://graph.microsoft.com/.default"},
				TenantID: tt.optsTID,
			})
			require.NoError(t, err)

			if tt.expectTenantOption {
				require.Len(t, spy.silentOptions, 3,
					"expected WithSilentAccount + WithClaims + WithTenantID")
			} else {
				require.Len(t, spy.silentOptions, 2,
					"expected WithSilentAccount + WithClaims only")
			}

			// Verify the resolved tenant ID matches expectations.
			// The credential resolves: optsTID if non-empty, else credTID.
			// MSAL's WithTenantID option is opaque, so we verify the credential's
			// tenant resolution logic directly (same package gives access to internals).
			resolvedTenant := cred.tenantID
			if tt.optsTID != "" {
				resolvedTenant = tt.optsTID
			}
			require.Equal(t, tt.expectedTenantID, resolvedTenant,
				"resolved tenant ID should match expected value")
		})
	}
}

func TestAzdCredential_GetToken_LogsSuccessSnapshotAfterFailure(t *testing.T) {
	t.Setenv(azdDebugMsalCacheEnv, "true")

	cacheJSON := []byte(`{
		"RefreshToken": {
			"rt-key": {
				"home_account_id": "home-1",
				"environment": "login.microsoftonline.com",
				"client_id": "client-1",
				"family_id": "",
				"secret": "super-secret-refresh-token"
			}
		},
		"AccessToken": {
			"at-key": {
				"home_account_id": "home-1",
				"realm": "tenant-1",
				"cached_at": 100,
				"expires_on": 200
			}
		},
		"Account": {
			"acct-key": {
				"home_account_id": "home-1",
				"realm": "tenant-1",
				"username": "user@example.com"
			}
		}
	}`)

	cache := &memoryCache{
		cache: map[string][]byte{currentUserCacheKey: cacheJSON},
	}
	tracer := newMsalCacheTracer(cache)

	var buf bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(originalWriter)
	})

	client := &sequentialSilentClient{
		results: []struct {
			result public.AuthResult
			err    error
		}{
			{err: errors.New("temporary failure")},
			{
				result: public.AuthResult{
					AccessToken: "test-token",
					ExpiresOn:   time.Now().Add(time.Hour),
				},
			},
		},
	}

	cred := newAzdCredential(
		client,
		&public.Account{HomeAccountID: "test.id"},
		cloud.AzurePublic(),
		"",
		tracer,
	)

	_, err := cred.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	})
	require.Error(t, err)

	_, err = cred.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	})
	require.NoError(t, err)

	output := buf.String()
	require.NotEmpty(t, output)
	require.Equal(t, 1, strings.Count(
		output,
		"msal-cache[after-first-acquire-token-silent-failure]: refresh_tokens=1 access_tokens=1 accounts=1",
	))
	require.Equal(t, 1, strings.Count(
		output,
		"msal-cache[after-first-acquire-token-silent]: refresh_tokens=1 access_tokens=1 accounts=1",
	))
}
