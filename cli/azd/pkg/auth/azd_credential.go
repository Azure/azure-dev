// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
)

var _ azcore.TokenCredential = &azdCredential{}

type azdCredential struct {
	client  *public.Client
	account *public.Account
}

func (c *azdCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	res, err := c.client.AcquireTokenSilent(ctx, options.Scopes, public.WithSilentAccount(*c.account))

	if err != nil {
		return azcore.AccessToken{}, err
	}

	return azcore.AccessToken{
		Token:     res.AccessToken,
		ExpiresOn: res.ExpiresOn.UTC(),
	}, nil
}
