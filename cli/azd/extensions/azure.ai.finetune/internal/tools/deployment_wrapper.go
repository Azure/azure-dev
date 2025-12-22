// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package JobWrapper

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
)

// DeploymentConfig contains the configuration for deploying a fine-tuned model
type DeploymentConfig struct {
	JobID             string
	DeploymentName    string
	ModelFormat       string
	SKU               string
	Version           string
	Capacity          int32
	SubscriptionID    string
	ResourceGroup     string
	AccountName       string
	TenantID          string
	WaitForCompletion bool
}

// DeployModelResult represents the result of a model deployment operation
type DeployModelResult struct {
	DeploymentName string
	Status         string
	Message        string
}

// DeployModel deploys a fine-tuned model to an Azure Cognitive Services account
func DeployModel(ctx context.Context, azdClient *azdext.AzdClient, config DeploymentConfig) (*DeployModelResult, error) {
	// Validate required fields
	if config.JobID == "" {
		return nil, fmt.Errorf("job ID is required")
	}
	if config.DeploymentName == "" {
		return nil, fmt.Errorf("deployment name is required")
	}
	if config.SubscriptionID == "" {
		return nil, fmt.Errorf("subscription ID is required")
	}
	if config.ResourceGroup == "" {
		return nil, fmt.Errorf("resource group is required")
	}
	if config.AccountName == "" {
		return nil, fmt.Errorf("account name is required")
	}
	if config.TenantID == "" {
		return nil, fmt.Errorf("tenant ID is required")
	}

	// Get fine-tuned model details
	jobDetails, err := GetJobDetails(ctx, azdClient, config.JobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job details: %w", err)
	}

	if jobDetails.FineTunedModel == "" {
		return nil, fmt.Errorf("job does not have a fine-tuned model yet")
	}

	// Create Azure credential
	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   config.TenantID,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	// Create Cognitive Services client factory
	clientFactory, err := armcognitiveservices.NewClientFactory(
		config.SubscriptionID,
		credential,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create client factory: %w", err)
	}

	// Show spinner while creating deployment
	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: fmt.Sprintf("Deploying model to %s...", config.DeploymentName),
	})
	if err := spinner.Start(ctx); err != nil {
		fmt.Printf("Failed to start spinner: %v\n", err)
	}

	// Create or update the deployment
	poller, err := clientFactory.NewDeploymentsClient().BeginCreateOrUpdate(
		ctx,
		config.ResourceGroup,
		config.AccountName,
		config.DeploymentName,
		armcognitiveservices.Deployment{
			Properties: &armcognitiveservices.DeploymentProperties{
				Model: &armcognitiveservices.DeploymentModel{
					Name:    to.Ptr(jobDetails.FineTunedModel),
					Format:  to.Ptr(config.ModelFormat),
					Version: to.Ptr(config.Version),
				},
			},
			SKU: &armcognitiveservices.SKU{
				Name:     to.Ptr(config.SKU),
				Capacity: to.Ptr(config.Capacity),
			},
		},
		nil,
	)
	if err != nil {
		_ = spinner.Stop(ctx)
		return nil, fmt.Errorf("failed to start deployment: %w", err)
	}

	// Wait for deployment to complete if requested
	var status string
	var message string

	if config.WaitForCompletion {
		_, err := poller.PollUntilDone(ctx, nil)
		_ = spinner.Stop(ctx)
		if err != nil {
			return nil, fmt.Errorf("deployment failed: %w", err)
		}
		status = "succeeded"
		message = fmt.Sprintf("Model deployed successfully to %s", config.DeploymentName)
	} else {
		_ = spinner.Stop(ctx)
		status = "in_progress"
		message = fmt.Sprintf("Deployment %s initiated. Check deployment status in Azure Portal", config.DeploymentName)
	}

	// Return result
	return &DeployModelResult{
		DeploymentName: config.DeploymentName,
		Status:         status,
		Message:        message,
	}, nil
}
