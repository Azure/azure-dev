// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

type TargetResource struct {
	subscriptionId    string
	resourceGroupName string
	resourceName      string
	resourceType      string
	metadata          map[string]string
}

func NewTargetResource(
	subscriptionId string,
	resourceGroupName string,
	resourceName string,
	resourceType string,
) *TargetResource {
	return &TargetResource{
		subscriptionId:    subscriptionId,
		resourceGroupName: resourceGroupName,
		resourceName:      resourceName,
		resourceType:      resourceType,
		metadata:          nil,
	}
}

func (ds *TargetResource) SubscriptionId() string {
	return ds.subscriptionId
}

func (ds *TargetResource) ResourceGroupName() string {
	return ds.resourceGroupName
}

func (ds *TargetResource) ResourceName() string {
	return ds.resourceName
}

func (ds *TargetResource) ResourceType() string {
	return ds.resourceType
}

func (ds *TargetResource) Metadata() map[string]string {
	if ds.metadata == nil {
		return nil
	}

	copyMap := make(map[string]string, len(ds.metadata))
	for key, value := range ds.metadata {
		copyMap[key] = value
	}

	return copyMap
}

func (ds *TargetResource) SetMetadata(metadata map[string]string) {
	if metadata == nil {
		ds.metadata = nil
		return
	}

	copyMap := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copyMap[key] = value
	}

	ds.metadata = copyMap
}
