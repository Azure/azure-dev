// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceIDContractRejectsCredentialURLsWithoutDisclosure(t *testing.T) {
	for _, resourceType := range []string{
		"Microsoft.ContainerService/managedClusters",
		"Microsoft.Storage/storageAccounts",
		"Microsoft.ContainerRegistry/registries",
		"Microsoft.Network/virtualNetworks/subnets",
		"Microsoft.ManagedIdentity/userAssignedIdentities",
	} {
		t.Run(resourceType, func(t *testing.T) {
			for _, input := range []struct {
				name  string
				value string
			}{
				{
					name: "userinfo query and fragment",
					value: "https://operator:credential-password@arm.invalid/resource" +
						"?sig=signature-token#fragment-token",
				},
				{
					name: "userinfo only",
					// #nosec G101 -- Synthetic credentials for the non-disclosure regression.
					value: "https://operator:credential-password@arm.invalid/resource",
				},
				{
					name:  "query only",
					value: "https://arm.invalid/resource?sig=signature-token",
				},
			} {
				t.Run(input.name, func(t *testing.T) {
					id, err := ParseID(input.value, resourceType)
					require.Error(t, err)
					assert.Nil(t, id)
					for _, secret := range []string{
						"operator", "credential-password", "signature-token", "fragment-token",
					} {
						assert.NotContains(t, err.Error(), secret)
					}
				})
			}
		})
	}
}
