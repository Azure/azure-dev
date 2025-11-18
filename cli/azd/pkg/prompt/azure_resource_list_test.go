// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	// nolint:lll
	testVMResourceID = "/subscriptions/123/resourceGroups/testGroup/providers/Microsoft.Compute/virtualMachines/testVM"
	// nolint:lll
	testVM2ResourceID = "/subscriptions/123/resourceGroups/testGroup/providers/Microsoft.Compute/virtualMachines/testVM2"
	// nolint:lll
	nonExistentVMResourceID = "/subscriptions/123/resourceGroups/testGroup/providers/Microsoft.Compute/virtualMachines/nonExistentVM"
)

func TestAzureResourceList_Add(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)

	err := resourceList.Add(testVMResourceID)
	require.NoError(t, err)
	require.Equal(t, 1, len(resourceList.resources))
}

func TestAzureResourceList_FindById(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)
	err := resourceList.Add(testVMResourceID)
	require.NoError(t, err)

	resource, found := resourceList.FindById(testVMResourceID)
	require.True(t, found)
	require.NotNil(t, resource)
}

func TestAzureResourceList_FindById_NotFound(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)

	resource, found := resourceList.FindById(nonExistentVMResourceID)
	require.False(t, found)
	require.Nil(t, resource)
}

func TestAzureResourceList_FindByType(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)
	err := resourceList.Add(testVMResourceID)
	require.NoError(t, err)

	resource, found := resourceList.FindByType("Microsoft.Compute/virtualMachines")
	require.True(t, found)
	require.NotNil(t, resource)
}

func TestAzureResourceList_FindByType_NotFound(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)

	resource, found := resourceList.FindByType("Microsoft.Compute/nonExistentType")
	require.False(t, found)
	require.Nil(t, resource)
}

func TestAzureResourceList_FindByTypeAndKind(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)
	err := resourceList.Add(testVMResourceID)
	require.NoError(t, err)

	resourceService.On(
		"GetResource",
		mock.Anything,
		"123",
		testVMResourceID,
		"",
	).Return(azapi.ResourceExtended{Kind: "testKind"}, nil)

	resource, found := resourceList.FindByTypeAndKind(
		context.Background(),
		"Microsoft.Compute/virtualMachines",
		[]string{"testKind"},
	)
	require.True(t, found)
	require.NotNil(t, resource)
}

func TestAzureResourceList_FindByTypeAndKind_NotFound(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)
	err := resourceList.Add(testVMResourceID)
	require.NoError(t, err)

	resourceService.On(
		"GetResource",
		mock.Anything,
		"123",
		testVMResourceID,
		"",
	).Return(azapi.ResourceExtended{Kind: "testKind"}, nil)

	resource, found := resourceList.FindByTypeAndKind(
		context.Background(),
		"Microsoft.Compute/virtualMachines",
		[]string{"nonExistentKind"},
	)
	require.False(t, found)
	require.Nil(t, resource)
}

func TestAzureResourceList_Find(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)
	err := resourceList.Add(testVMResourceID)
	require.NoError(t, err)

	resource, found := resourceList.Find(func(resourceId *arm.ResourceID) bool {
		return resourceId.Name == "testVM"
	})
	require.True(t, found)
	require.NotNil(t, resource)
}

func TestAzureResourceList_Find_NotFound(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)

	resource, found := resourceList.Find(func(resourceId *arm.ResourceID) bool {
		return resourceId.Name == "nonExistentVM"
	})
	require.False(t, found)
	require.Nil(t, resource)
}

func TestAzureResourceList_FindAll(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)
	err := resourceList.Add(testVMResourceID)
	require.NoError(t, err)
	err = resourceList.Add(testVM2ResourceID)
	require.NoError(t, err)

	resources, found := resourceList.FindAll(func(resourceId *arm.ResourceID) bool {
		return resourceId.ResourceType.String() == "Microsoft.Compute/virtualMachines"
	})
	require.True(t, found)
	require.Equal(t, 2, len(resources))
}

func TestAzureResourceList_FindAll_NotFound(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)

	resources, found := resourceList.FindAll(func(resourceId *arm.ResourceID) bool {
		return resourceId.ResourceType.String() == "Microsoft.Compute/nonExistentType"
	})
	require.False(t, found)
	require.Equal(t, 0, len(resources))
}

func TestAzureResourceList_FindAllByType(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)
	err := resourceList.Add(testVMResourceID)
	require.NoError(t, err)
	err = resourceList.Add(testVM2ResourceID)
	require.NoError(t, err)

	resources, found := resourceList.FindAllByType("Microsoft.Compute/virtualMachines")
	require.True(t, found)
	require.Equal(t, 2, len(resources))
}

func TestAzureResourceList_FindAllByType_NotFound(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)

	resources, found := resourceList.FindAllByType("Microsoft.Compute/nonExistentType")
	require.False(t, found)
	require.Equal(t, 0, len(resources))
}

func TestAzureResourceList_FindByTypeAndName(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)
	err := resourceList.Add(testVMResourceID)
	require.NoError(t, err)

	resource, found := resourceList.FindByTypeAndName("Microsoft.Compute/virtualMachines", "testVM")
	require.True(t, found)
	require.NotNil(t, resource)
}

func TestAzureResourceList_FindByTypeAndName_NotFound(t *testing.T) {
	resourceService := &mockazapi.MockResourceService{}
	resourceList := NewAzureResourceList(resourceService, nil)

	resource, found := resourceList.FindByTypeAndName("Microsoft.Compute/virtualMachines", "nonExistentVM")
	require.False(t, found)
	require.Nil(t, resource)
}
