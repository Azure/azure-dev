// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

type countingCredential struct {
	calls   atomic.Int32
	expires time.Time
}

func (c *countingCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	n := c.calls.Add(1)
	return azcore.AccessToken{Token: string(rune('a' + n)), ExpiresOn: c.expires}, nil
}

func TestCachedTokenCredentialReusesTokenPerScope(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	inner := &countingCredential{expires: now.Add(time.Hour)}
	cred := newCachedTokenCredential(inner)
	cred.now = func() time.Time { return now }

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, err := cred.GetToken(t.Context(), policy.TokenRequestOptions{Scopes: []string{"a"}})
			require.NoError(t, err)
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, inner.calls.Load())

	_, err := cred.GetToken(t.Context(), policy.TokenRequestOptions{Scopes: []string{"b"}})
	require.NoError(t, err)
	require.EqualValues(t, 2, inner.calls.Load())

	_, err = cred.GetToken(t.Context(), policy.TokenRequestOptions{Scopes: []string{"a"}, Claims: "c"})
	require.NoError(t, err)
	require.EqualValues(t, 3, inner.calls.Load())
}

func TestCachedTokenCredentialRenewsNearExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	inner := &countingCredential{expires: now.Add(time.Hour)}
	cred := newCachedTokenCredential(inner)
	cred.now = func() time.Time { return now }
	options := policy.TokenRequestOptions{Scopes: []string{"a"}}

	_, err := cred.GetToken(t.Context(), options)
	require.NoError(t, err)
	now = now.Add(56 * time.Minute)
	inner.expires = now.Add(time.Hour)
	_, err = cred.GetToken(t.Context(), options)
	require.NoError(t, err)
	require.EqualValues(t, 2, inner.calls.Load())
}
