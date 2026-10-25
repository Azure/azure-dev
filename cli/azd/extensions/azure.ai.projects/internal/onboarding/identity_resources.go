// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
)

func (c *sdkResourceChecker) readSuppliedIdentity(
	ctx context.Context, resource Resource, tenant string,
) (Identity, Check) {
	check := resourceCheck("I02", resource.ID,
		"existing user-assigned identity with matching ID, principal, client and tenant")
	id, err := ParseID(resource.ID, "Microsoft.ManagedIdentity/userAssignedIdentities")
	if err != nil {
		check.Status, check.Observed, check.Cause = Fail, "invalid supplied identity resource ID", err
		return Identity{}, check
	}
	client, err := armmsi.NewUserAssignedIdentitiesClient(id.SubscriptionID, c.credential, c.options)
	if err != nil {
		check.Cause = err
		return Identity{}, check
	}
	response, err := client.Get(ctx, id.ResourceGroupName, id.Name, nil)
	if err != nil {
		return Identity{}, readFailure("I02", resource.ID, "Microsoft.ManagedIdentity/userAssignedIdentities/read", err)
	}
	properties := response.Properties
	if response.ID == nil || properties == nil || authString(properties.PrincipalID) == "" ||
		authString(properties.ClientID) == "" || authString(properties.TenantID) == "" || tenant == "" {
		check.Observed = "supplied identity ID, principal, client or owning tenant evidence is missing"
		return Identity{}, check
	}
	identity := Identity{
		ID: *response.ID, PrincipalID: *properties.PrincipalID,
		ClientID: *properties.ClientID, TenantID: *properties.TenantID,
	}
	if !strings.EqualFold(resource.ID, identity.ID) || !strings.EqualFold(tenant, identity.TenantID) {
		return Identity{}, resourceResult(check, Fail, "supplied identity ID or owning tenant does not match")
	}
	return identity, resourceResult(check, Pass,
		"existing identity metadata matches; no identity PUT or owner tag is planned")
}
