package environment

type TargetResource struct {
	subscriptionId    string
	resourceGroupName string
	resourceName      string
	resourceType      string
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
