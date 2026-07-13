// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

// countingCredential is a TokenCredential that records how many times GetToken is invoked and,
// optionally, blocks until released so concurrent behavior can be exercised deterministically.
type countingCredential struct {
	calls     atomic.Int32
	token     azcore.AccessToken
	err       error
	gate      chan struct{}
	gateReady chan struct{}
}

func (c *countingCredential) GetToken(
	_ context.Context,
	_ policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	c.calls.Add(1)
	if c.gate != nil {
		c.gateReady <- struct{}{}
		<-c.gate
	}
	if c.err != nil {
		return azcore.AccessToken{}, c.err
	}
	return c.token, nil
}

func TestCachingCredentialReusesToken(t *testing.T) {
	inner := &countingCredential{
		token: azcore.AccessToken{Token: "abc", ExpiresOn: time.Now().Add(time.Hour)},
	}
	cred := newCachingCredential(inner)

	opts := policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}}

	for range 5 {
		tk, err := cred.GetToken(t.Context(), opts)
		require.NoError(t, err)
		require.Equal(t, "abc", tk.Token)
	}

	// All five requests for the same scope should have hit the underlying credential exactly once.
	require.Equal(t, int32(1), inner.calls.Load())
}

func TestCachingCredentialSeparatesByScope(t *testing.T) {
	inner := &countingCredential{
		token: azcore.AccessToken{Token: "abc", ExpiresOn: time.Now().Add(time.Hour)},
	}
	cred := newCachingCredential(inner)

	_, err := cred.GetToken(t.Context(), policy.TokenRequestOptions{Scopes: []string{"scope-a"}})
	require.NoError(t, err)
	_, err = cred.GetToken(t.Context(), policy.TokenRequestOptions{Scopes: []string{"scope-b"}})
	require.NoError(t, err)

	// Distinct scopes are cached independently, so each triggers its own acquisition.
	require.Equal(t, int32(2), inner.calls.Load())
}

func TestCachingCredentialRefreshesNearExpiry(t *testing.T) {
	inner := &countingCredential{
		// Token already within the refresh offset of expiry, so it must not be served from cache.
		token: azcore.AccessToken{Token: "abc", ExpiresOn: time.Now().Add(time.Minute)},
	}
	cred := newCachingCredential(inner)

	opts := policy.TokenRequestOptions{Scopes: []string{"scope"}}

	_, err := cred.GetToken(t.Context(), opts)
	require.NoError(t, err)
	_, err = cred.GetToken(t.Context(), opts)
	require.NoError(t, err)

	require.Equal(t, int32(2), inner.calls.Load())
}

func TestCachingCredentialDoesNotCacheErrors(t *testing.T) {
	inner := &countingCredential{err: errors.New("boom")}
	cred := newCachingCredential(inner)

	opts := policy.TokenRequestOptions{Scopes: []string{"scope"}}

	// A failed acquisition must not be written to the cache, otherwise a single transient `az`
	// failure would wedge auth for the rest of the command.
	_, err := cred.GetToken(t.Context(), opts)
	require.Error(t, err)

	// A subsequent successful acquisition for the same key should reach the inner credential and
	// return the fresh token rather than a cached error.
	inner.err = nil
	inner.token = azcore.AccessToken{Token: "ok", ExpiresOn: time.Now().Add(time.Hour)}

	tk, err := cred.GetToken(t.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, "ok", tk.Token)
	require.Equal(t, int32(2), inner.calls.Load())
}

func TestCachingCredentialSingleFlight(t *testing.T) {
	inner := &countingCredential{
		token:     azcore.AccessToken{Token: "abc", ExpiresOn: time.Now().Add(time.Hour)},
		gate:      make(chan struct{}),
		gateReady: make(chan struct{}),
	}
	cred := newCachingCredential(inner)

	opts := policy.TokenRequestOptions{Scopes: []string{"scope"}}

	const goroutines = 8
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			tk, err := cred.GetToken(t.Context(), opts)
			require.NoError(t, err)
			require.Equal(t, "abc", tk.Token)
		})
	}

	// Wait until the single in-flight acquisition is running, then release it. The remaining
	// goroutines should resolve from the shared singleflight result rather than calling the inner
	// credential again.
	<-inner.gateReady
	close(inner.gate)
	wg.Wait()

	require.Equal(t, int32(1), inner.calls.Load())
}
