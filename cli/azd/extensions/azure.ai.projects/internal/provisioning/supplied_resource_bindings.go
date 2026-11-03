// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore deploymenttemplate

package provisioning

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/google/uuid"
)

func (p *FoundryProvisioningProvider) readBYOIdentityPrincipal(ctx context.Context, resourceID string) (string, error) {
	id, err := parseBYOResourceID(resourceID, "Microsoft.ManagedIdentity/userAssignedIdentities")
	if err != nil {
		return "", err
	}
	client, err := armmsi.NewUserAssignedIdentitiesClient(id.SubscriptionID, p.credential, byoARMOptions(p.armClientOptions))
	if err != nil {
		return "", fmt.Errorf("create supplied identity client: %w", err)
	}
	identity, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return "", fmt.Errorf("read supplied identity deployment binding: %w", err)
	}
	if identity.ID == nil || !strings.EqualFold(*identity.ID, resourceID) ||
		identity.Properties == nil || identity.Properties.PrincipalID == nil {
		return "", fmt.Errorf("supplied identity deployment binding requires a matching resource ID and principal ID")
	}
	principal := *identity.Properties.PrincipalID
	if _, err := uuid.Parse(principal); err != nil {
		return "", fmt.Errorf("supplied identity principal ID must be a GUID: %w", err)
	}
	return principal, nil
}
