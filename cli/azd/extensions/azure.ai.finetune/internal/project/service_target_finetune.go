// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
)

// Reference implementation

// Ensure FineTuneServiceTargetProvider implements ServiceTargetProvider interface
var _ azdext.ServiceTargetProvider = &FineTuneServiceTargetProvider{}

// AgentServiceTargetProvider is a minimal implementation of ServiceTargetProvider for demonstration
type FineTuneServiceTargetProvider struct {
	azdClient           *azdext.AzdClient
	serviceConfig       *azdext.ServiceConfig
	agentDefinitionPath string
	credential          *azidentity.AzureDeveloperCLICredential
	tenantId            string
	env                 *azdext.Environment
	foundryProject      *arm.ResourceID
}

// NewFineTuneServiceTargetProvider creates a new FineTuneServiceTargetProvider instance
func NewFineTuneServiceTargetProvider(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &FineTuneServiceTargetProvider{
		azdClient: azdClient,
	}
}

// Initialize initializes the service target by looking for the agent definition file
func (p *FineTuneServiceTargetProvider) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	fmt.Println("Initializing the deployment")
	return nil
}

// Endpoints returns endpoints exposed by the agent service
func (p *FineTuneServiceTargetProvider) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	endpoint := "https://foundrysdk-eastus2-foundry-resou.services.ai.azure.com/api/projects/foundrysdk-eastus2-project"
	return []string{endpoint}, nil

}

func (p *FineTuneServiceTargetProvider) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	targetResource := &azdext.TargetResource{
		SubscriptionId:    p.foundryProject.SubscriptionID,
		ResourceGroupName: p.foundryProject.ResourceGroupName,
		ResourceName:      "projectName",
		ResourceType:      "Microsoft.CognitiveServices/accounts/projects",
		Metadata: map[string]string{
			"accountName": "accountName",
			"projectName": "projectName",
		},
	}

	return targetResource, nil
}

// Package performs packaging for the agent service
func (p *FineTuneServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	return nil, fmt.Errorf("failed building container:")

}

// Publish performs the publish operation for the agent service
func (p *FineTuneServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {

	progress("Publishing container")
	publishResponse, err := p.azdClient.
		Container().
		Publish(ctx, &azdext.ContainerPublishRequest{
			ServiceName:    serviceConfig.Name,
			ServiceContext: serviceContext,
		})

	if err != nil {
		return nil, fmt.Errorf("failed publishing container: %w", err)
	}

	return &azdext.ServicePublishResult{
		Artifacts: publishResponse.Result.Artifacts,
	}, nil
}

// Deploy performs the deployment operation for the agent service
func (p *FineTuneServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	color.Green("Deploying the AI Project...")
	time.Sleep(1 * time.Second)
	color.Green("Deployed the AI Project successfully. Project URI : https://foundrysdk-eastus2-foundry-resou.services.ai.azure.com/api/projects/foundrysdk-eastus2-project")
	color.Green("Deploying validation file...")
	time.Sleep(1 * time.Second)
	color.Green("Deployed validation file successfully. File ID: file-7219fd8e93954c039203203f953bab3b.jsonl")

	color.Green("Deploying Training file...")
	time.Sleep(1 * time.Second)
	color.Green("Deployed training file successfully. File ID: file-7219fd8e93954c039203203f953bab4b.jsonl")

	color.Green("Starting Fine-tuning...")
	time.Sleep(2 * time.Second)
	color.Green("Fine-tuning started successfully. Fine-tune ID: ftjob-4485dc4da8694d3b8c13c516baa18bc0")

	return nil, nil

}
