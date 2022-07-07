package environment

type DeploymentScope struct {
	subscriptionId    string
	resourceGroupName string
	resourceName      string
}

func NewDeploymentScope(subscriptionId string, resourceGroupName string, resourceName string) *DeploymentScope {
	return &DeploymentScope{subscriptionId: subscriptionId, resourceGroupName: resourceGroupName, resourceName: resourceName}
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
