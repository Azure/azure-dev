// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/stretchr/testify/require"
)

func TestPreviewProvision(t *testing.T) {
	pp := &PreviewProvision{
		Operations: []*Resource{
			{
				Type:      "some Azure resource",
				Name:      "resource name",
				Operation: OperationTypeCreate,
			},
			{
				Type:      "Key Vault",
				Name:      "resource name 2",
				Operation: OperationTypeIgnore,
			},
			{
				Type:      "Other",
				Name:      "resource name 3",
				Operation: OperationTypeModify,
			},
			{
				Type:      "Other",
				Name:      "resource name 3",
				Operation: OperationTypeDelete,
			},
		},
	}

	output := pp.ToString("   ")
	snapshot.SnapshotT(t, output)
}

func TestPreviewProvisionNoChanges(t *testing.T) {
	pp := &PreviewProvision{
		Operations: []*Resource{},
	}

	output := pp.ToString("   ")
	require.Equal(t, "", output)
}

func TestPreviewProvisionWithPropertyChanges(t *testing.T) {
	pp := &PreviewProvision{
		Operations: []*Resource{
			{
				Type:      "Microsoft.Storage/storageAccounts",
				Name:      "mystorageaccount",
				Operation: OperationTypeModify,
				PropertyDeltas: []PropertyDelta{
					{
						Path:       "properties.sku.name",
						ChangeType: "Modify",
						Before:     "Standard_LRS",
						After:      "Premium_LRS",
					},
					{
						Path:       "properties.minimumTlsVersion",
						ChangeType: "Create",
						After:      "TLS1_2",
					},
				},
			},
			{
				Type:      "Microsoft.KeyVault/vaults",
				Name:      "mykeyvault",
				Operation: OperationTypeCreate,
				PropertyDeltas: []PropertyDelta{
					{
						Path:       "properties.sku.name",
						ChangeType: "Create",
						After:      "standard",
					},
				},
			},
		},
	}

	output := pp.ToString("   ")
	snapshot.SnapshotT(t, output)
}

func TestPreviewProvisionSkipHidesProperties(t *testing.T) {
	pp := &PreviewProvision{
		Operations: []*Resource{
			{
				Type:      "Microsoft.Storage/storageAccounts",
				Name:      "mystorageaccount",
				Operation: OperationTypeModify,
				PropertyDeltas: []PropertyDelta{
					{
						Path:       "properties.sku.name",
						ChangeType: "Modify",
						Before:     "Standard_LRS",
						After:      "Premium_LRS",
					},
				},
			},
			{
				Type:      "Microsoft.KeyVault/vaults",
				Name:      "skippedvault",
				Operation: OperationTypeIgnore,
				PropertyDeltas: []PropertyDelta{
					{
						Path:       "properties.sku.name",
						ChangeType: "NoEffect",
						Before:     "standard",
						After:      "standard",
					},
				},
			},
			{
				Type:      "Microsoft.Network/virtualNetworks",
				Name:      "unchangedvnet",
				Operation: OperationTypeNoChange,
				PropertyDeltas: []PropertyDelta{
					{
						Path:       "properties.addressSpace",
						ChangeType: "NoEffect",
						Before:     "10.0.0.0/16",
						After:      "10.0.0.0/16",
					},
				},
			},
		},
	}

	output := pp.ToString("   ")
	snapshot.SnapshotT(t, output)
}
