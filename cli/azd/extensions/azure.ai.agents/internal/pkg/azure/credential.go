// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// CredentialScope defines the Azure resource scope for token validation.
type CredentialScope string

const (
	// ScopeAIFoundry is the scope for Azure AI Foundry APIs.
	ScopeAIFoundry CredentialScope = "https://ai.azure.com/.default"
	// ScopeARM is the scope for Azure Resource Manager APIs.
	ScopeARM CredentialScope = "https://management.azure.com/.default"
)

// CredentialOptions configures credential creation and validation.
type CredentialOptions struct {
	// TenantID is the Azure AD tenant to authenticate against.
	TenantID string
	// SubscriptionID is used for error messages to help users identify the context.
	SubscriptionID string
	// Scope is the Azure resource scope to validate the credential against.
	// If empty, defaults to ScopeARM.
	Scope CredentialScope
}

// NewCredential creates an AzureDeveloperCLICredential and validates it can obtain a token.
// This catches multi-tenant authentication issues early with a helpful error message.
func NewCredential(ctx context.Context, options CredentialOptions) (*azidentity.AzureDeveloperCLICredential, error) {
	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   options.TenantID,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	scope := options.Scope
	if scope == "" {
		scope = ScopeARM
	}

	// Validate the credential by attempting to get a token.
	// The token is cached by the SDK, so subsequent calls reuse it.
	_, err = cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{string(scope)},
	})
	if err != nil {
		return nil, &AuthError{
			SubscriptionID: options.SubscriptionID,
			TenantID:       options.TenantID,
			Cause:          err,
		}
	}

	return cred, nil
}

// AuthError represents an authentication failure with context for helpful error messages.
type AuthError struct {
	SubscriptionID string
	TenantID       string
	Cause          error
}

func (e *AuthError) Error() string {
	return fmt.Sprintf(
		"failed to authenticate for subscription '%s' in tenant '%s'.\n"+
			"Suggestion: if you recently gained access to this subscription, re-run `azd auth login`. Otherwise, visit this subscription in Azure Portal, then run `azd auth login`",
		e.SubscriptionID,
		e.TenantID)
}

func (e *AuthError) Unwrap() error {
	return e.Cause
}
