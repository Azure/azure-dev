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
