// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// tokenRefreshMargin renews a cached token before it can expire mid-request.
const tokenRefreshMargin = 5 * time.Minute

// cachedTokenCredential reuses tokens until they near expiry. Developer
// credentials such as the Azure CLI start a process for every token, which is
// seconds per call and makes a polling monitor queue behind itself.
type cachedTokenCredential struct {
	inner azcore.TokenCredential
	now   func() time.Time

	mu     sync.Mutex
	tokens map[string]azcore.AccessToken
}

func newCachedTokenCredential(inner azcore.TokenCredential) *cachedTokenCredential {
	return &cachedTokenCredential{inner: inner, now: time.Now, tokens: map[string]azcore.AccessToken{}}
}

func (c *cachedTokenCredential) GetToken(
	ctx context.Context, options policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	// Claims challenges must reach the identity provider.
	if options.Claims != "" {
		return c.inner.GetToken(ctx, options)
	}
	key := options.TenantID + "|" + strings.Join(options.Scopes, " ")
	// Holding the lock while fetching lets concurrent callers share one fetch.
	c.mu.Lock()
	defer c.mu.Unlock()
	if token, ok := c.tokens[key]; ok && c.now().Add(tokenRefreshMargin).Before(token.ExpiresOn) {
		return token, nil
	}
	token, err := c.inner.GetToken(ctx, options)
	if err != nil {
		return token, err
	}
	c.tokens[key] = token
	return token, nil
}
