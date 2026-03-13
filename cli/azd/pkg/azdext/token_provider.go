// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// TokenProvider implements [azcore.TokenCredential] so that extensions can
// obtain Azure tokens without manual credential construction.
//
// It uses the AZD deployment context (tenant/subscription) retrieved via
// gRPC and delegates to [azidentity.AzureDeveloperCLICredential] for the
// actual token acquisition flow.
//
// Usage:
//
//	tp, err := azdext.NewTokenProvider(client, nil)
//	// use tp as azcore.TokenCredential with any Azure SDK client
type TokenProvider struct {
	credential azcore.TokenCredential
	tenantID   string
}

// Compile-time interface check.
var _ azcore.TokenCredential = (*TokenProvider)(nil)

// TokenProviderOptions configures a [TokenProvider].
type TokenProviderOptions struct {
	// TenantID overrides the tenant obtained from the AZD deployment context.
	// When empty, the provider queries the AZD gRPC server for the current tenant.
	TenantID string

	// Credential overrides the default credential chain.
	// When nil, [azidentity.AzureDeveloperCLICredential] is used.
	Credential azcore.TokenCredential
}

// NewTokenProvider creates a [TokenProvider] for the given AZD client.
//
// If opts is nil, the provider discovers the current tenant from the AZD
// deployment context and constructs an [azidentity.AzureDeveloperCLICredential].
func NewTokenProvider(ctx context.Context, client *AzdClient, opts *TokenProviderOptions) (*TokenProvider, error) {
	if client == nil {
		return nil, errors.New("azdext.NewTokenProvider: client must not be nil")
	}

	if opts == nil {
		opts = &TokenProviderOptions{}
	}

	tenantID := opts.TenantID

	// Resolve tenant from deployment context when not explicitly supplied.
	if tenantID == "" {
		resp, err := client.Deployment().GetDeploymentContext(ctx, &EmptyRequest{})
		if err != nil {
			return nil, fmt.Errorf("azdext.NewTokenProvider: failed to retrieve deployment context: %w", err)
		}

		if resp.GetAzureContext() != nil && resp.GetAzureContext().GetScope() != nil {
			tenantID = resp.GetAzureContext().GetScope().GetTenantId()
		}

		if tenantID == "" {
			return nil, errors.New(
				"azdext.NewTokenProvider: deployment context returned no tenant ID; " +
					"set TenantID explicitly",
			)
		}
	}

	cred := opts.Credential
	if cred == nil {
		azdCred, err := azidentity.NewAzureDeveloperCLICredential(
			&azidentity.AzureDeveloperCLICredentialOptions{
				TenantID: tenantID,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("azdext.NewTokenProvider: failed to create credential: %w", err)
		}

		cred = azdCred
	}

	return &TokenProvider{
		credential: cred,
		tenantID:   tenantID,
	}, nil
}

// GetToken satisfies [azcore.TokenCredential].
func (tp *TokenProvider) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if len(options.Scopes) == 0 {
		return azcore.AccessToken{}, errors.New("azdext.TokenProvider.GetToken: at least one scope is required")
	}

	return tp.credential.GetToken(ctx, options)
}

// TenantID returns the Azure tenant ID that was resolved or configured for this provider.
func (tp *TokenProvider) TenantID() string {
	return tp.tenantID
}
