// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package client

import (
	"context"
	"fmt"

	"azure.ai.models/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
)

const deploymentUserAgent = "azd-ai-models-extension"

// DeploymentClient wraps the ARM Cognitive Services SDK for deployment operations.
type DeploymentClient struct {
	deploymentsClient *armcognitiveservices.DeploymentsClient
}

// NewDeploymentClient creates a new deployment client using the ARM SDK.
func NewDeploymentClient(
	subscriptionID string,
	credential azcore.TokenCredential,
) (*DeploymentClient, error) {
	clientFactory, err := armcognitiveservices.NewClientFactory(
		subscriptionID,
		credential,
		&arm.ClientOptions{
			ClientOptions: policy.ClientOptions{
				Telemetry: policy.TelemetryOptions{
					ApplicationID: deploymentUserAgent,
				},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create ARM client factory: %w", err)
	}

	return &DeploymentClient{
		deploymentsClient: clientFactory.NewDeploymentsClient(),
	}, nil
}

// CreateDeployment creates or updates a model deployment using the ARM SDK.
func (c *DeploymentClient) CreateDeployment(
	ctx context.Context,
	config *models.DeploymentConfig,
) (*models.DeploymentResult, error) {
	skuCapacity := config.SkuCapacity

	deployment := armcognitiveservices.Deployment{
		Properties: &armcognitiveservices.DeploymentProperties{
			Model: &armcognitiveservices.DeploymentModel{
				Name:    &config.ModelName,
				Format:  &config.ModelFormat,
				Version: &config.ModelVersion,
			},
		},
		SKU: &armcognitiveservices.SKU{
			Name:     &config.SkuName,
			Capacity: &skuCapacity,
		},
	}

	// Set model source for custom models (points to the project ARM resource ID)
	if config.ModelSource != "" {
		deployment.Properties.Model.Source = &config.ModelSource
	}

	// Set RAI policy if specified
	if config.RaiPolicyName != "" {
		deployment.Properties.RaiPolicyName = &config.RaiPolicyName
	}

	poller, err := c.deploymentsClient.BeginCreateOrUpdate(
		ctx,
		config.ResourceGroup,
		config.AccountName,
		config.DeploymentName,
		deployment,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start deployment: %w", err)
	}

	if !config.WaitForCompletion {
		return &models.DeploymentResult{
			Name:              config.DeploymentName,
			ModelName:         config.ModelName,
			ProvisioningState: "Accepted",
		}, nil
	}

	pollResult, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("deployment failed: %w", err)
	}

	result := &models.DeploymentResult{
		Name:      config.DeploymentName,
		ModelName: config.ModelName,
	}

	if pollResult.ID != nil {
		result.ID = *pollResult.ID
	}
	if pollResult.Name != nil {
		result.Name = *pollResult.Name
	}
	if pollResult.Properties != nil && pollResult.Properties.ProvisioningState != nil {
		result.ProvisioningState = string(*pollResult.Properties.ProvisioningState)
	}
	if pollResult.Properties != nil && pollResult.Properties.Model != nil &&
		pollResult.Properties.Model.Name != nil {
		result.ModelName = *pollResult.Properties.Model.Name
	}

	return result, nil
}

// ListDeployments lists all deployments for a Cognitive Services account.
func (c *DeploymentClient) ListDeployments(
	ctx context.Context,
	resourceGroup string,
	accountName string,
) ([]models.DeploymentInfo, error) {
	pager := c.deploymentsClient.NewListPager(resourceGroup, accountName, nil)

	var deployments []models.DeploymentInfo
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list deployments: %w", err)
		}

		for _, d := range page.Value {
			info := models.DeploymentInfo{}
			if d.Name != nil {
				info.Name = *d.Name
			}
			if d.Properties != nil {
				if d.Properties.ProvisioningState != nil {
					info.ProvisioningState = string(*d.Properties.ProvisioningState)
				}
				if d.Properties.Model != nil {
					if d.Properties.Model.Name != nil {
						info.ModelName = *d.Properties.Model.Name
					}
					if d.Properties.Model.Format != nil {
						info.ModelFormat = *d.Properties.Model.Format
					}
					if d.Properties.Model.Version != nil {
						info.ModelVersion = *d.Properties.Model.Version
					}
				}
			}
			if d.SKU != nil {
				if d.SKU.Name != nil {
					info.SkuName = *d.SKU.Name
				}
				if d.SKU.Capacity != nil {
					info.SkuCapacity = *d.SKU.Capacity
				}
			}
			deployments = append(deployments, info)
		}
	}

	return deployments, nil
}

// DeleteDeployment deletes a deployment from a Cognitive Services account.
func (c *DeploymentClient) DeleteDeployment(
	ctx context.Context,
	resourceGroup string,
	accountName string,
	deploymentName string,
) error {
	poller, err := c.deploymentsClient.BeginDelete(ctx, resourceGroup, accountName, deploymentName, nil)
	if err != nil {
		return fmt.Errorf("failed to start deployment deletion: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("deployment deletion failed: %w", err)
	}

	return nil
}

// GetDeployment retrieves details of a specific deployment.
func (c *DeploymentClient) GetDeployment(
	ctx context.Context,
	resourceGroup string,
	accountName string,
	deploymentName string,
) (*models.DeploymentDetail, error) {
	resp, err := c.deploymentsClient.Get(ctx, resourceGroup, accountName, deploymentName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}

	detail := &models.DeploymentDetail{
		Name: deploymentName,
	}

	if resp.ID != nil {
		detail.ID = *resp.ID
	}
	if resp.Name != nil {
		detail.Name = *resp.Name
	}
	if resp.Properties != nil {
		if resp.Properties.ProvisioningState != nil {
			detail.ProvisioningState = string(*resp.Properties.ProvisioningState)
		}
		if resp.Properties.RaiPolicyName != nil {
			detail.RaiPolicyName = *resp.Properties.RaiPolicyName
		}
		if resp.Properties.Model != nil {
			if resp.Properties.Model.Name != nil {
				detail.ModelName = *resp.Properties.Model.Name
			}
			if resp.Properties.Model.Format != nil {
				detail.ModelFormat = *resp.Properties.Model.Format
			}
			if resp.Properties.Model.Version != nil {
				detail.ModelVersion = *resp.Properties.Model.Version
			}
			if resp.Properties.Model.Source != nil {
				detail.ModelSource = *resp.Properties.Model.Source
			}
		}
	}
	if resp.SKU != nil {
		if resp.SKU.Name != nil {
			detail.SkuName = *resp.SKU.Name
		}
		if resp.SKU.Capacity != nil {
			detail.SkuCapacity = *resp.SKU.Capacity
		}
	}
	if resp.SystemData != nil {
		if resp.SystemData.CreatedAt != nil {
			detail.CreatedAt = resp.SystemData.CreatedAt.String()
		}
		if resp.SystemData.LastModifiedAt != nil {
			detail.LastModifiedAt = resp.SystemData.LastModifiedAt.String()
		}
	}

	return detail, nil
}
