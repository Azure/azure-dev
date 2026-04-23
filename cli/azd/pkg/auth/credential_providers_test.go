// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/stretchr/testify/require"
)

// tokenServer creates an httptest.Server that responds to RemoteCredential token
// requests. It returns a valid success response and tracks the number of calls
// received via the returned *atomic.Int32.
func tokenServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w,
			`{"status":"success","token":"tok","expiresOn":"2099-01-01T00:00:00Z"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// errorTokenServer creates an httptest.Server that always returns an error
// response so EnsureLoggedInCredential fails.
func errorTokenServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w,
			`{"status":"error","code":"auth_failed","message":"token denied"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// externalAuthManager returns a *Manager configured to use external auth backed
// by the given endpoint URL. The cloud field is set to AzurePublic.
func externalAuthManager(endpoint string, client *http.Client) *Manager {
	return &Manager{
		cloud: cloud.AzurePublic(),
		externalAuthCfg: ExternalAuthConfiguration{
			Endpoint:    endpoint,
			Key:         "test-key",
			Transporter: client,
		},
	}
}

func TestCredentialProvider_SuccessAndCaching(t *testing.T) {
	t.Parallel()

	srv, calls := tokenServer(t)
	m := externalAuthManager(srv.URL, srv.Client())
	provider := NewMultiTenantCredentialProvider(m)

	// First call: should hit the server (via EnsureLoggedInCredential)
	cred1, err := provider.GetTokenCredential(t.Context(), "tenant-a")
	require.NoError(t, err)
	require.NotNil(t, cred1)
	require.Equal(t, int32(1), calls.Load(), "expected exactly one HTTP call on first fetch")

	// Second call with same tenant: should return cached credential, no new HTTP call
	cred2, err := provider.GetTokenCredential(t.Context(), "tenant-a")
	require.NoError(t, err)
	require.Same(t, cred1, cred2, "expected same pointer from cache")
	require.Equal(t, int32(1), calls.Load(), "expected no additional HTTP call on cache hit")
}

func TestCredentialProvider_DifferentTenants(t *testing.T) {
	t.Parallel()

	srv, calls := tokenServer(t)
	m := externalAuthManager(srv.URL, srv.Client())
	provider := NewMultiTenantCredentialProvider(m)

	credA, err := provider.GetTokenCredential(t.Context(), "tenant-a")
	require.NoError(t, err)

	credB, err := provider.GetTokenCredential(t.Context(), "tenant-b")
	require.NoError(t, err)

	require.NotSame(t, credA, credB, "different tenants must return different credential instances")
	require.Equal(t, int32(2), calls.Load(), "expected one HTTP call per distinct tenant")
}

func TestCredentialProvider_EmptyTenantID(t *testing.T) {
	t.Parallel()

	srv, calls := tokenServer(t)
	m := externalAuthManager(srv.URL, srv.Client())
	provider := NewMultiTenantCredentialProvider(m)

	cred, err := provider.GetTokenCredential(t.Context(), "")
	require.NoError(t, err)
	require.NotNil(t, cred)
	require.Equal(t, int32(1), calls.Load())

	// Empty tenant should also be cached under the "" key
	cred2, err := provider.GetTokenCredential(t.Context(), "")
	require.NoError(t, err)
	require.Same(t, cred, cred2)
	require.Equal(t, int32(1), calls.Load(), "empty tenant credential should be cached")
}

func TestCredentialProvider_ErrorFromCredentialForCurrentUser(t *testing.T) {
	t.Parallel()

	// Manager with no auth config and no current user - CredentialForCurrentUser
	// will return ErrNoCurrentUser.
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		publicClient:      &mockPublicClient{},
	}

	provider := NewMultiTenantCredentialProvider(m)
	_, err := provider.GetTokenCredential(t.Context(), "any-tenant")

	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoCurrentUser)
}

func TestCredentialProvider_ErrorFromEnsureLoggedIn(t *testing.T) {
	t.Parallel()

	// The remote credential server returns an error response, so
	// EnsureLoggedInCredential (which calls GetToken) will fail.
	srv := errorTokenServer(t)
	m := externalAuthManager(srv.URL, srv.Client())
	provider := NewMultiTenantCredentialProvider(m)

	_, err := provider.GetTokenCredential(t.Context(), "tenant-x")

	require.Error(t, err)
	require.Contains(t, err.Error(), "token denied")
}

func TestCredentialProvider_EnsureLoggedInErrorDoesNotCache(t *testing.T) {
	t.Parallel()

	// Use a server that fails first, then succeeds. This verifies that a failed
	// EnsureLoggedInCredential call does NOT store the credential in the cache.
	var attempt atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if attempt.Add(1) == 1 {
			_, _ = io.WriteString(w,
				`{"status":"error","code":"auth_failed","message":"transient failure"}`)
			return
		}
		_, _ = io.WriteString(w,
			`{"status":"success","token":"recovered","expiresOn":"2099-01-01T00:00:00Z"}`)
	}))
	t.Cleanup(srv.Close)

	m := externalAuthManager(srv.URL, srv.Client())
	provider := NewMultiTenantCredentialProvider(m)

	// First call: EnsureLoggedInCredential fails
	_, err := provider.GetTokenCredential(t.Context(), "tenant-retry")
	require.Error(t, err)
	require.Contains(t, err.Error(), "transient failure")

	// Second call: should NOT return a cached (bad) credential; should retry the
	// full flow and succeed.
	cred, err := provider.GetTokenCredential(t.Context(), "tenant-retry")
	require.NoError(t, err)
	require.NotNil(t, cred)
}

func TestCredentialProvider_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	srv, calls := tokenServer(t)
	m := externalAuthManager(srv.URL, srv.Client())
	provider := NewMultiTenantCredentialProvider(m)

	const goroutines = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	errs := make([]error, goroutines)
	creds := make([]azcore.TokenCredential, goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			c, err := provider.GetTokenCredential(t.Context(), "shared-tenant")
			creds[idx] = c
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	// All goroutines must succeed
	for i, err := range errs {
		require.NoError(t, err, "goroutine %d returned error", i)
		require.NotNil(t, creds[i], "goroutine %d returned nil credential", i)
	}

	// After concurrent access, a subsequent call must return a cached credential
	cachedCred, err := provider.GetTokenCredential(t.Context(), "shared-tenant")
	require.NoError(t, err)
	require.NotNil(t, cachedCred)

	// Verify that the HTTP server wasn't called an excessive number of times.
	// The implementation doesn't use LoadOrStore so multiple goroutines may
	// redundantly create credentials, but total calls should be bounded.
	totalCalls := calls.Load()
	require.LessOrEqual(t, totalCalls, int32(goroutines),
		"expected at most %d HTTP calls, got %d", goroutines, totalCalls)
	require.GreaterOrEqual(t, totalCalls, int32(1),
		"expected at least 1 HTTP call")
}

func TestCredentialProvider_ConcurrentDifferentTenants(t *testing.T) {
	t.Parallel()

	srv, calls := tokenServer(t)
	m := externalAuthManager(srv.URL, srv.Client())
	provider := NewMultiTenantCredentialProvider(m)

	const tenantCount = 10
	var wg sync.WaitGroup
	wg.Add(tenantCount)

	errs := make([]error, tenantCount)
	creds := make([]azcore.TokenCredential, tenantCount)

	for i := range tenantCount {
		go func(idx int) {
			defer wg.Done()
			c, err := provider.GetTokenCredential(t.Context(), fmt.Sprintf("tenant-%d", idx))
			creds[idx] = c
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "tenant-%d returned error", i)
		require.NotNil(t, creds[i], "tenant-%d returned nil credential", i)
	}

	// Each distinct tenant should have made at least one HTTP call
	require.GreaterOrEqual(t, calls.Load(), int32(tenantCount),
		"expected at least %d HTTP calls for %d distinct tenants", tenantCount, tenantCount)
}

func TestCredentialProvider_NewMultiTenantCredentialProviderReturnsInterface(t *testing.T) {
	t.Parallel()

	m := &Manager{cloud: cloud.AzurePublic()}
	provider := NewMultiTenantCredentialProvider(m)

	// Verify the returned value satisfies the interface
	var _ MultiTenantCredentialProvider = provider
	require.NotNil(t, provider)
}

func TestCredentialProvider_CredentialForCurrentUserWrapsErrors(t *testing.T) {
	t.Parallel()

	// A Manager with a userConfigManager that returns an error on Load
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: &failingUserConfigManager{err: errors.New("config load boom")},
		publicClient:      &mockPublicClient{},
	}

	provider := NewMultiTenantCredentialProvider(m)
	_, err := provider.GetTokenCredential(t.Context(), "some-tenant")

	require.Error(t, err)
	require.Contains(t, err.Error(), "config load boom")
}
