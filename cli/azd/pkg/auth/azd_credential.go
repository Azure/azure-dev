// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
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
		// https://login.microsoftonline.com/error?code=50158. External security challenge not satisfied.
		if strings.Contains(err.Error(), "AADSTS50158") {
			loginCmd := "azd login"
			for _, scope := range options.Scopes {
				// azure.ManagementScope is the default scope we would always use.
				if scope != azure.ManagementScope {
					loginCmd += fmt.Sprintf(" --scope %s", scope)
				}
			}

			return azcore.AccessToken{},
				fmt.Errorf("%w\nre-authentication required, run `%s`", err, loginCmd)
		}

		return azcore.AccessToken{}, err
	}

	return azcore.AccessToken{
		Token:     res.AccessToken,
		ExpiresOn: res.ExpiresOn.UTC(),
	}, nil
}
