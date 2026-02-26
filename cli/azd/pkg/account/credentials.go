// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package account

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
)

var (
	// Matches AADSTS70043 (refresh token expired due to sign-in frequency) and AADSTS700082 (refresh token expired)
	aadRefreshTokenExpiredRegex = regexp.MustCompile(`AADSTS(70043|700082)`)
)

// SubscriptionCredentialProvider provides an [azcore.TokenCredential] configured
// to use the tenant id that corresponds to the tenant the given subscription
// is located in.
type SubscriptionCredentialProvider interface {
	CredentialForSubscription(ctx context.Context, subscriptionId string) (azcore.TokenCredential, error)
}

type subscriptionCredentialProvider struct {
	credProvider auth.MultiTenantCredentialProvider
	subResolver  SubscriptionTenantResolver
}

func NewSubscriptionCredentialProvider(
	subResolver SubscriptionTenantResolver,
	credProvider auth.MultiTenantCredentialProvider,
) SubscriptionCredentialProvider {
	return &subscriptionCredentialProvider{
		credProvider: credProvider,
		subResolver:  subResolver,
	}
}

func (p *subscriptionCredentialProvider) CredentialForSubscription(
	ctx context.Context,
	subscriptionId string,
) (azcore.TokenCredential, error) {
	tenantId, err := p.subResolver.LookupTenant(ctx, subscriptionId)
	if err != nil {
		// If we can't resolve the tenant for this subscription, it might be because:
		// 1. User manually set AZURE_SUBSCRIPTION_ID in .env
		// 2. User called `azd env set AZURE_SUBSCRIPTION_ID` instead of selecting from azd's cache
		// In these cases, suggest they also set AZURE_TENANT_ID
		return nil, fmt.Errorf(
			"%w\n\n"+
				"If you manually set the subscription ID (e.g., via AZURE_SUBSCRIPTION_ID in .env or `azd env set`), "+
				"you must also set AZURE_TENANT_ID to the tenant ID that contains this subscription. "+
				"Alternatively, run `azd auth login --tenant-id <tenant-id>` "+
				"to allow azd to discover subscriptions in that tenant.",
			err,
		)
	}

	cred, err := p.credProvider.GetTokenCredential(ctx, tenantId)
	if err != nil {
		// If this is an AADSTS refresh token error, enhance it with tenant-specific login guidance
		if aadRefreshTokenExpiredRegex.MatchString(err.Error()) {
			// Check if the error already has a suggestion (ErrorWithSuggestion from auth layer)
			var errWithSuggestion *internal.ErrorWithSuggestion
			if errors.As(err, &errWithSuggestion) {
				// Enhance the existing suggestion with tenant-specific guidance
				enhancedSuggestion := fmt.Sprintf(
					"%s To re-authenticate specifically to this tenant, run `azd auth login --tenant-id %s`.",
					errWithSuggestion.Suggestion,
					tenantId,
				)
				return nil, &internal.ErrorWithSuggestion{
					Err:        errWithSuggestion.Err,
					Suggestion: enhancedSuggestion,
				}
			}

			// If it's not wrapped yet, create a new ErrorWithSuggestion
			return nil, &internal.ErrorWithSuggestion{
				Err: err,
				Suggestion: fmt.Sprintf(
					"Access to tenant '%s' has expired or requires re-authentication. "+
						"Run `azd auth login --tenant-id %s` to re-authenticate to this tenant.",
					tenantId,
					tenantId,
				),
			}
		}
		return nil, err
	}

	return cred, nil
}
