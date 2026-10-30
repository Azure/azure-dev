// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// NewCredential returns a Foundry credential bound to the project's user-access tenant.
// An empty tenant ID retains default CLI credential behavior for unscoped operations.
func NewCredential(tenantID string) (azcore.TokenCredential, error) {
	options := &azidentity.AzureDeveloperCLICredentialOptions{}
	if tenantID != "" {
		options.TenantID = tenantID
		options.AdditionallyAllowedTenants = []string{"*"}
	}
	cred, err := azidentity.NewAzureDeveloperCLICredential(options)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	return cred, nil
}
