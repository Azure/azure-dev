package provisioning

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type Scope interface {
	// Deploy a given template with a set of parameters.
	Deploy(ctx context.Context, templatePath string, parametersPath string) error
	// GetDeployment fetches the result of the most recent deployment.
	GetDeployment(ctx context.Context) (azcli.AzCliDeployment, error)
}

type ResourceGroupScope struct {
	azCli          azcli.AzCli
	name           string
	subscriptionId string
	resourceGroup  string
}

func (s *ResourceGroupScope) Deploy(ctx context.Context, modulePath string, parametersPath string) error {
	_, err := s.azCli.DeployToResourceGroup(ctx, s.subscriptionId, s.resourceGroup, s.name, modulePath, parametersPath)
	return err
}

func (s *ResourceGroupScope) GetDeployment(ctx context.Context) (azcli.AzCliDeployment, error) {
	return s.azCli.GetResourceGroupDeployment(ctx, s.subscriptionId, s.resourceGroup, s.name)
}

func NewResourceGroupProvisioningScope(azCli azcli.AzCli, subscriptionId string, resourceGroup string, deploymentName string) Scope {
	return &ResourceGroupScope{
		azCli:          azCli,
		name:           deploymentName,
		subscriptionId: subscriptionId,
		resourceGroup:  resourceGroup,
	}
}

type SubscriptionScope struct {
	azCli          azcli.AzCli
	name           string
	subscriptionId string
	location       string
}

func (s *SubscriptionScope) Name() string {
	return s.name
}

func (s *SubscriptionScope) SubscriptionId() string {
	return s.subscriptionId
}

func (s *SubscriptionScope) Location() string {
	return s.location
}

func (s *SubscriptionScope) Deploy(ctx context.Context, bicepPath string, parametersPath string) error {
	_, err := s.azCli.DeployToSubscription(ctx, s.subscriptionId, s.name, bicepPath, parametersPath, s.location)
	return err
}

func (s *SubscriptionScope) GetDeployment(ctx context.Context) (azcli.AzCliDeployment, error) {
	return s.azCli.GetSubscriptionDeployment(ctx, s.subscriptionId, s.name)
}

func NewSubscriptionProvisioningScope(azCli azcli.AzCli, location string, subscriptionId string, deploymentName string) Scope {
	return &SubscriptionScope{
		azCli:          azCli,
		name:           deploymentName,
		subscriptionId: subscriptionId,
		location:       location,
	}
}
