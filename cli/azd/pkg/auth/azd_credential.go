// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
)

type azdCredential struct {
	client      publicClient
	account     *public.Account
	cloud       *cloud.Cloud
	tenantID    string
	cacheTracer *msalCacheTracer
}

// newAzdCredential creates a credential that acquires tokens via MSAL's public client.
// tenantID, when non-empty, is forwarded to AcquireTokenSilent so MSAL issues tokens
// for that specific tenant instead of defaulting to the account's home tenant.
func newAzdCredential(
	client publicClient,
	account *public.Account,
	cloud *cloud.Cloud,
	tenantID string,
	cacheTracer *msalCacheTracer,
) *azdCredential {
	return &azdCredential{
		client:      client,
		account:     account,
		cloud:       cloud,
		tenantID:    tenantID,
		cacheTracer: cacheTracer,
	}
}

func (c *azdCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	silentOpts := []public.AcquireSilentOption{
		public.WithSilentAccount(*c.account),
		public.WithClaims(options.Claims),
	}

	// Forward the tenant ID so MSAL acquires a token from the correct tenant
	// authority. The credential's tenantID is set when the credential is created
	// for a specific tenant (e.g. resource tenant for B2B guests). The caller
	// can override via options.TenantID (e.g. during CAE challenges).
	// Without this, MSAL defaults to the account's home tenant, which causes
	// cross-tenant calls (e.g. Graph /me for B2B guests) to return identity
	// information for the home tenant instead of the resource tenant.
	tenantID := c.tenantID
	if options.TenantID != "" {
		tenantID = options.TenantID
	}
	if tenantID != "" {
		silentOpts = append(silentOpts, public.WithTenantID(tenantID))
	}

	phase := "after-first-acquire-token-silent"
	failurePhase := "after-first-acquire-token-silent-failure"
	if tenantID != "" {
		phase = "after-first-tenant-acquire-token-silent"
		failurePhase = "after-first-tenant-acquire-token-silent-failure"
	}

	res, err := c.client.AcquireTokenSilent(ctx, options.Scopes, silentOpts...)
	if err != nil {
		c.cacheTracer.LogSnapshotOnce(failurePhase)

		if authFailed, ok := errors.AsType[*AuthFailedError](err); ok {
			if loginErr, ok := newReLoginRequiredError(authFailed.Parsed, options.Scopes, c.cloud); ok {
				log.Println(authFailed.httpErrorDetails())

				if options.Claims != "" {
					if err := saveClaims(options.Claims); err != nil {
						return azcore.AccessToken{}, fmt.Errorf("saving claims: %w", err)
					}
				}

				return azcore.AccessToken{}, loginErr
			}

			return azcore.AccessToken{}, authFailed
		}

		return azcore.AccessToken{}, err
	}

	c.cacheTracer.LogSnapshotOnce(phase)

	return azcore.AccessToken{
		Token:     res.AccessToken,
		ExpiresOn: res.ExpiresOn.UTC(),
	}, nil
}
