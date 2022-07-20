package provisioning

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type ProvisioningScope interface {
	// Deploy a given template with a set of parameters.
	Deploy(ctx context.Context, templatePath string, parametersPath string) error
	// GetDeployment fetches the result of the most recent deployment.
	GetDeployment(ctx context.Context) (tools.AzCliDeployment, error)
}

type ResourceGroupProvisioningScope struct {
	azCli          tools.AzCli
	name           string
	subscriptionId string
	resourceGroup  string
}

func (s *ResourceGroupProvisioningScope) Deploy(ctx context.Context, bicepPath string, parametersPath string) error {
	_, err := s.azCli.DeployToResourceGroup(ctx, s.subscriptionId, s.resourceGroup, s.name, bicepPath, parametersPath)
	return err
}

func (s *ResourceGroupProvisioningScope) GetDeployment(ctx context.Context) (tools.AzCliDeployment, error) {
	return s.azCli.GetResourceGroupDeployment(ctx, s.subscriptionId, s.resourceGroup, s.name)
}

func NewResourceGroupProvisioningScope(azCli tools.AzCli, subscriptionId string, resourceGroup string, deploymentName string) ProvisioningScope {
	return &ResourceGroupProvisioningScope{
		azCli:          azCli,
		name:           deploymentName,
		subscriptionId: subscriptionId,
		resourceGroup:  resourceGroup,
	}
}

type SubscriptionProvisioningScope struct {
	azCli          tools.AzCli
	name           string
	subscriptionId string
	location       string
}

func (s *SubscriptionProvisioningScope) Name() string {
	return s.name
}

func (s *SubscriptionProvisioningScope) SubscriptionId() string {
	return s.subscriptionId
}

func (s *SubscriptionProvisioningScope) Location() string {
	return s.location
}

func (s *SubscriptionProvisioningScope) Deploy(ctx context.Context, bicepPath string, parametersPath string) error {
	_, err := s.azCli.DeployToSubscription(ctx, s.subscriptionId, s.name, bicepPath, parametersPath, s.location)
	return err
}

func (s *SubscriptionProvisioningScope) GetDeployment(ctx context.Context) (tools.AzCliDeployment, error) {
	return s.azCli.GetSubscriptionDeployment(ctx, s.subscriptionId, s.name)
}

func NewSubscriptionProvisioningScope(azCli tools.AzCli, location string, subscriptionId string, deploymentName string) ProvisioningScope {
	return &SubscriptionProvisioningScope{
		azCli:          azCli,
		name:           deploymentName,
		subscriptionId: subscriptionId,
		location:       location,
	}
}
