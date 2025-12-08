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
	client  publicClient
	account *public.Account
	cloud   *cloud.Cloud
}

func newAzdCredential(client publicClient, account *public.Account, cloud *cloud.Cloud) *azdCredential {
	return &azdCredential{
		client:  client,
		account: account,
		cloud:   cloud,
	}
}

func (c *azdCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	res, err := c.client.AcquireTokenSilent(ctx,
		options.Scopes,
		public.WithSilentAccount(*c.account),
		public.WithClaims(options.Claims))

	if err != nil {
		var authFailed *AuthFailedError
		if errors.As(err, &authFailed) {
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

	return azcore.AccessToken{
		Token:     res.AccessToken,
		ExpiresOn: res.ExpiresOn.UTC(),
	}, nil
}
