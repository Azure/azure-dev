// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestTargetResourceMapping(t *testing.T) {
	// TargetResource should be automatically registered via init()
	targetResource := NewTargetResource(
		"sub123",
		"rg-test",
		"app-service",
		"Microsoft.Web/sites",
	)

	// Set metadata separately
	targetResource.SetMetadata(map[string]string{
		"location": "eastus",
		"sku":      "S1",
	})

	var protoTarget *azdext.TargetResource
	err := mapper.Convert(targetResource, &protoTarget)
	require.NoError(t, err)
	require.NotNil(t, protoTarget)
	require.Equal(t, "sub123", protoTarget.SubscriptionId)
	require.Equal(t, "rg-test", protoTarget.ResourceGroupName)
	require.Equal(t, "app-service", protoTarget.ResourceName)
	require.Equal(t, "Microsoft.Web/sites", protoTarget.ResourceType)
	require.NotNil(t, protoTarget.Metadata)
}

func TestTargetResourceMappingNil(t *testing.T) {
	var targetResource *TargetResource

	var protoTarget *azdext.TargetResource
	err := mapper.Convert(targetResource, &protoTarget)
	require.NoError(t, err)
	require.Nil(t, protoTarget)
}
