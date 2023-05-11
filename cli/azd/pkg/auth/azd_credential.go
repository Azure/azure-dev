// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
)

type azdCredential struct {
	client  publicClient
	account *public.Account
}

func newAzdCredential(client publicClient, account *public.Account) *azdCredential {
	return &azdCredential{
		client:  client,
		account: account,
	}
}

func (c *azdCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	res, err := c.client.AcquireTokenSilent(ctx, options.Scopes, public.WithSilentAccount(*c.account))
	if err != nil {
		loginCmd := cLoginCmd
		if !matchesLoginScopes(options.Scopes) { // if matching default login scopes, no scopes need to be specified
			for _, scope := range options.Scopes {
				loginCmd += fmt.Sprintf(" --scope %s", scope)
			}
		}

		return azcore.AccessToken{}, newAuthFailedError(err, loginCmd)
	}

	return azcore.AccessToken{
		Token:     res.AccessToken,
		ExpiresOn: res.ExpiresOn.UTC(),
	}, nil
}

// matchesLoginScopes checks if the elements contained in the slice match the default login scopes
func matchesLoginScopes(scopes []string) bool {
	for _, scope := range scopes {
		_, matchLogin := loginScopesMap[scope]
		if !matchLogin {
			return false
		}
	}

	return true
}
