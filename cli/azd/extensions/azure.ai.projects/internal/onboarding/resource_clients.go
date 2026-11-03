// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v7"
)

type resourceClients struct {
	identities passReads[*armmsi.UserAssignedIdentitiesClient]
	subnets    passReads[*armnetwork.SubnetsClient]
	networks   passReads[*armnetwork.VirtualNetworksClient]
}

func (c *sdkResourceChecker) clients() *resourceClients {
	if c.readClients != nil {
		return c.readClients
	}
	return &resourceClients{}
}
