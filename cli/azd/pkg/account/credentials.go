// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package account

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
)

var (
	// Matches AADSTS70043 (refresh token expired due to sign-in frequency) and AADSTS700082 (refresh token expired)
	aadRefreshTokenExpiredRegex = regexp.MustCompile(`AADSTS(70043|700082)`)
)

// SubscriptionCredentialProvider provides an [azcore.TokenCredential] configured
// to use the access tenant required by the current account for the given subscription.
type SubscriptionCredentialProvider interface {
	CredentialForSubscription(ctx context.Context, subscriptionId string) (azcore.TokenCredential, error)
}

type subscriptionCredentialProvider struct {
	credProvider auth.MultiTenantCredentialProvider
	subResolver  SubscriptionResolver
}

func NewSubscriptionCredentialProvider(
	subResolver SubscriptionResolver,
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
	subscription, err := p.subResolver.GetSubscription(ctx, subscriptionId)
	if err != nil {
		// If we can't resolve the subscription for this ID, it might be because:
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
	tenantId := subscription.UserAccessTenantId

	cred, err := p.credProvider.GetTokenCredential(ctx, tenantId)
	if err != nil {
		// If this is an AADSTS refresh token error, enhance it with tenant-specific login guidance
		if aadRefreshTokenExpiredRegex.MatchString(err.Error()) {
			message := fmt.Sprintf(
				"Access to tenant '%s' requires re-authentication before azd can use this subscription.",
				tenantId,
			)

			// Check if the error already has a suggestion (ErrorWithSuggestion from auth layer)
			if errWithSuggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err); ok {
				if errWithSuggestion.Message != "" {
					message = errWithSuggestion.Message
				}

				suggestion := strings.TrimSpace(errWithSuggestion.Suggestion)
				// If the auth layer's suggestion doesn't already include --tenant-id,
				// append tenant-specific login guidance.
				if !strings.Contains(suggestion, "--tenant-id") {
					tenantHint := fmt.Sprintf(
						"Run `azd auth login --tenant-id %s` to re-authenticate to this tenant.",
						tenantId,
					)
					if suggestion != "" {
						suggestion = fmt.Sprintf("%s %s", suggestion, tenantHint)
					} else {
						suggestion = tenantHint
					}
				}

				return nil, &internal.ErrorWithSuggestion{
					Err:        errWithSuggestion.Err,
					Message:    message,
					Suggestion: suggestion,
					Links:      errWithSuggestion.Links,
				}
			}

			// If it's not wrapped yet, create a new ErrorWithSuggestion
			tenantSpecificSuggestion := fmt.Sprintf(
				"Run `azd auth login --tenant-id %s` to re-authenticate to this tenant.",
				tenantId,
			)
			return nil, &internal.ErrorWithSuggestion{
				Err:        err,
				Message:    message,
				Suggestion: tenantSpecificSuggestion,
			}
		}
		return nil, err
	}

	return cred, nil
}
