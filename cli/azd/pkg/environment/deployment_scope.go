package environment

type DeploymentScope struct {
	subscriptionId    string
	resourceGroupName string
	resourceName      string
	resourceType      string
}

func NewDeploymentScope(subscriptionId string, resourceGroupName string, resourceName string, resourceType string) *DeploymentScope {
	return &DeploymentScope{subscriptionId: subscriptionId, resourceGroupName: resourceGroupName, resourceName: resourceName, resourceType: resourceType}
}

func (ds *DeploymentScope) SubscriptionId() string {
	return ds.subscriptionId
}

func (ds *DeploymentScope) ResourceGroupName() string {
	return ds.resourceGroupName
}

func (ds *DeploymentScope) ResourceName() string {
	return ds.resourceName
}

func (ds *DeploymentScope) ResourceType() string {
	return ds.resourceType
}
