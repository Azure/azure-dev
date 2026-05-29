// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"golang.org/x/sync/singleflight"
)

// tokenRefreshOffset is how long before a cached token's expiry it is treated as stale and
// re-acquired. It mirrors the default refresh window used by the azcore bearer-token policy.
const tokenRefreshOffset = 5 * time.Minute

// cachingCredential wraps a TokenCredential with an in-memory token cache.
//
// It exists primarily for the Azure CLI delegated-auth path. AzureCLICredential performs no
// caching of its own and spawns an `az account get-access-token` subprocess on every GetToken
// call. Flows that fan out many concurrent Azure SDK clients (for example, listing the AI model
// catalog across every region) otherwise trigger one `az` subprocess per request, serialized
// behind the credential's internal mutex, which is slow. Caching collapses repeated requests for
// the same scope/tenant into a single subprocess invocation and reuses the token until it is near
// expiry. The cache is in-memory only and lives for the lifetime of the credential instance.
type cachingCredential struct {
	inner azcore.TokenCredential

	mu    sync.RWMutex
	cache map[string]azcore.AccessToken

	// group deduplicates concurrent acquisitions for the same cache key so that only a single
	// inner GetToken call (and thus a single `az` subprocess) runs at a time per key.
	group singleflight.Group
}

// newCachingCredential wraps inner with an in-memory token cache.
func newCachingCredential(inner azcore.TokenCredential) *cachingCredential {
	return &cachingCredential{
		inner: inner,
		cache: map[string]azcore.AccessToken{},
	}
}

// GetToken returns a cached token for the requested options when one is available and not near
// expiry; otherwise it acquires a new token from the wrapped credential and caches it.
func (c *cachingCredential) GetToken(
	ctx context.Context,
	opts policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	key := tokenCacheKey(opts)

	if tk, ok := c.cachedToken(key); ok {
		return tk, nil
	}

	// singleflight shares the result of the first in-flight acquisition for this key with all
	// concurrent callers, so N goroutines requesting the same scope share a single `az` call.
	result, err, _ := c.group.Do(key, func() (any, error) {
		// Another goroutine may have populated the cache while this call was queued.
		if tk, ok := c.cachedToken(key); ok {
			return tk, nil
		}

		tk, err := c.inner.GetToken(ctx, opts)
		if err != nil {
			return azcore.AccessToken{}, err
		}

		c.mu.Lock()
		c.cache[key] = tk
		c.mu.Unlock()

		return tk, nil
	})
	if err != nil {
		return azcore.AccessToken{}, err
	}

	return result.(azcore.AccessToken), nil
}

// cachedToken returns the cached token for key when present and not within the refresh offset of
// its expiry.
func (c *cachingCredential) cachedToken(key string) (azcore.AccessToken, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	tk, ok := c.cache[key]
	if !ok {
		return azcore.AccessToken{}, false
	}

	if time.Now().Add(tokenRefreshOffset).After(tk.ExpiresOn) {
		return azcore.AccessToken{}, false
	}

	return tk, true
}

// tokenCacheKey derives a stable cache key from the token request options. Tokens differ by their
// requested scopes, tenant, CAE flag, and any claims challenge, so all are included in the key.
func tokenCacheKey(opts policy.TokenRequestOptions) string {
	return strings.Join(opts.Scopes, " ") + "\n" +
		opts.TenantID + "\n" +
		opts.Claims + "\n" +
		strconv.FormatBool(opts.EnableCAE)
}

var _ azcore.TokenCredential = (*cachingCredential)(nil)
