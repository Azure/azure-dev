// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore demostore

package project

import (
	"context"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

// Ensure DemoProvisioningProvider implements ProvisioningProvider interface
var _ azdext.ProvisioningProvider = &DemoProvisioningProvider{}

// DemoProvisioningProvider is a demonstration implementation of a provisioning provider.
// This shows how to implement infrastructure provisioning support as an azd extension.
type DemoProvisioningProvider struct {
	azdClient   *azdext.AzdClient
	projectPath string
	options     *azdext.ProvisioningOptions
}

// NewDemoProvisioningProvider creates a new DemoProvisioningProvider instance
func NewDemoProvisioningProvider(
	azdClient *azdext.AzdClient,
) azdext.ProvisioningProvider {
	return &DemoProvisioningProvider{
		azdClient: azdClient,
	}
}

// Initialize initializes the provisioning provider with the project path and options
func (p *DemoProvisioningProvider) Initialize(
	ctx context.Context,
	projectPath string,
	options *azdext.ProvisioningOptions,
) error {
	p.projectPath = projectPath
	p.options = options

	fmt.Printf(
		"Demo provisioning provider initialized (project: %s, provider: %s)\n",
		projectPath,
		options.GetProvider(),
	)

	// Demonstrate calling back to azd to get environment info
	envResponse, err := p.azdClient.Environment().
		GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Printf("Warning: could not get current environment: %v\n", err)
	} else {
		fmt.Printf(
			"Current environment: %s\n",
			envResponse.GetEnvironment().GetName(),
		)
	}

	return nil
}

// State returns the current provisioning state with sample outputs and resources
func (p *DemoProvisioningProvider) State(
	ctx context.Context,
	options *azdext.ProvisioningStateOptions,
) (*azdext.ProvisioningStateResult, error) {
	fmt.Println("Demo provisioning provider: retrieving state")
	time.Sleep(500 * time.Millisecond)

	return &azdext.ProvisioningStateResult{
		State: &azdext.ProvisioningState{
			Outputs: map[string]*azdext.ProvisioningOutputParameter{
				"DEMO_ENDPOINT": {
					Type:  "string",
					Value: "https://demo-app.example.com",
				},
				"DEMO_RESOURCE_GROUP": {
					Type:  "string",
					Value: "rg-demo-dev",
				},
				"DEMO_REGION": {
					Type:  "string",
					Value: "eastus2",
				},
			},
			Resources: []*azdext.ProvisioningResource{
				{Id: "/subscriptions/00000000-0000-0000-0000-000000000000" +
					"/resourceGroups/rg-demo-dev"},
				{Id: "/subscriptions/00000000-0000-0000-0000-000000000000" +
					"/resourceGroups/rg-demo-dev" +
					"/providers/Microsoft.Web/sites/demo-app"},
				{Id: "/subscriptions/00000000-0000-0000-0000-000000000000" +
					"/resourceGroups/rg-demo-dev" +
					"/providers/Microsoft.Storage/storageAccounts/demostore"},
			},
		},
	}, nil
}

// Deploy simulates a multi-step infrastructure deployment with progress updates
func (p *DemoProvisioningProvider) Deploy(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDeployResult, error) {
	fmt.Println("Demo provisioning provider: starting deployment")

	progress("Preparing resources...")
	time.Sleep(1 * time.Second)

	progress("Deploying infrastructure...")
	time.Sleep(2 * time.Second)

	progress("Configuring endpoints...")
	time.Sleep(1 * time.Second)

	progress("Deployment complete")
	time.Sleep(500 * time.Millisecond)

	fmt.Println("Demo provisioning provider: deployment finished")

	return &azdext.ProvisioningDeployResult{
		Deployment: &azdext.ProvisioningDeployment{
			Parameters: map[string]*azdext.ProvisioningInputParameter{
				"location": {
					Type:         "string",
					DefaultValue: "eastus2",
					Value:        "eastus2",
				},
				"appServicePlanSku": {
					Type:         "string",
					DefaultValue: "B1",
					Value:        "B1",
				},
			},
			Outputs: map[string]*azdext.ProvisioningOutputParameter{
				"DEMO_ENDPOINT": {
					Type:  "string",
					Value: "https://demo-app.example.com",
				},
				"DEMO_RESOURCE_GROUP": {
					Type:  "string",
					Value: "rg-demo-dev",
				},
			},
		},
	}, nil
}

// Preview returns a simulated preview of the deployment plan
func (p *DemoProvisioningProvider) Preview(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningPreviewResult, error) {
	fmt.Println("Demo provisioning provider: generating preview")

	progress("Analyzing current state...")
	time.Sleep(1 * time.Second)

	progress("Computing deployment plan...")
	time.Sleep(1 * time.Second)

	fmt.Println("Demo provisioning provider: preview generated")

	return &azdext.ProvisioningPreviewResult{
		Preview: &azdext.ProvisioningDeploymentPreview{
			// Note: Resource-level parameters/outputs in preview are
			// reserved for future use.
			// Only Summary is currently consumed by the core adapter.
			Summary: "Demo deployment: 3 resources to create, " +
				"0 to update, 0 to delete",
		},
	}, nil
}

// Destroy simulates infrastructure destruction with progress updates
func (p *DemoProvisioningProvider) Destroy(
	ctx context.Context,
	options *azdext.ProvisioningDestroyOptions,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDestroyResult, error) {
	fmt.Printf(
		"Demo provisioning provider: starting destroy (force: %t, purge: %t)\n",
		options.GetForce(),
		options.GetPurge(),
	)

	progress("Identifying resources to destroy...")
	time.Sleep(1 * time.Second)

	progress("Removing application resources...")
	time.Sleep(2 * time.Second)

	progress("Cleaning up resource group...")
	time.Sleep(1 * time.Second)

	progress("Destroy complete")
	time.Sleep(500 * time.Millisecond)

	fmt.Println("Demo provisioning provider: destroy finished")

	return &azdext.ProvisioningDestroyResult{
		InvalidatedEnvKeys: []string{
			"DEMO_ENDPOINT",
			"DEMO_RESOURCE_GROUP",
			"DEMO_REGION",
		},
	}, nil
}

// EnsureEnv ensures the environment is properly configured for provisioning
func (p *DemoProvisioningProvider) EnsureEnv(ctx context.Context) error {
	fmt.Println("Demo provisioning provider: ensuring environment")
	return nil
}

// Parameters returns sample provisioning parameters
func (p *DemoProvisioningProvider) Parameters(
	ctx context.Context,
) ([]*azdext.ProvisioningParameter, error) {
	fmt.Println("Demo provisioning provider: retrieving parameters")

	return []*azdext.ProvisioningParameter{
		{
			Name:  "location",
			Value: "eastus2",
		},
		{
			Name:   "adminPassword",
			Secret: true,
		},
		{
			Name:          "appName",
			Value:         "demo-app",
			EnvVarMapping: []string{"AZURE_APP_NAME"},
		},
	}, nil
}

// PlannedOutputs returns the list of outputs this provider plans to produce
func (p *DemoProvisioningProvider) PlannedOutputs(
	ctx context.Context,
) ([]*azdext.ProvisioningPlannedOutput, error) {
	return []*azdext.ProvisioningPlannedOutput{
		{Name: "DEMO_ENDPOINT"},
		{Name: "DEMO_RESOURCE_GROUP"},
		{Name: "DEMO_REGION"},
	}, nil
}
