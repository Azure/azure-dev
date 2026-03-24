// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"azure.ai.models/internal/client"
	"azure.ai.models/pkg/models"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type deploymentCreateFlags struct {
	Name         string
	ModelName    string
	ModelVersion string
	ModelFormat  string
	ModelSource  string
	SkuName      string
	SkuCapacity  int32
	RaiPolicy    string
	NoWait       bool
}

func newDeploymentCreateCommand(parentFlags *deploymentFlags) *cobra.Command {
	flags := &deploymentCreateFlags{}

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a model deployment in Azure AI Foundry",
		Long: `Create a model deployment for inference in Azure AI Foundry.

This command deploys a registered model (custom or base) to an inference endpoint
using the Azure Cognitive Services ARM API.

For custom models, --model-source is auto-resolved from the project context.
For base models, specify --model-format (e.g., OpenAI).`,
		Example: `  # Deploy a custom Fireworks model
  azd ai models deployment create --name my-deploy --model-name qwen3-14b \
    --model-version 1 --model-format FireworksCustom --sku-name GlobalProvisionedManaged --sku-capacity 80

  # Deploy with minimal flags (uses defaults: Standard SKU, capacity 1)
  azd ai models deployment create --name my-deploy --model-name my-model \
    --model-version 1 --model-format OpenAI

  # Deploy with explicit resource group (skip environment lookup)
  azd ai models deployment create --name my-deploy --model-name my-model \
    --model-version 1 --model-format OpenAI -g my-resource-group`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			return runDeploymentCreate(ctx, parentFlags, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Deployment name (required)")
	cmd.Flags().StringVar(&flags.ModelName, "model-name", "", "Model name to deploy (required)")
	cmd.Flags().StringVar(&flags.ModelVersion, "model-version", "", "Model version (required)")
	cmd.Flags().StringVar(&flags.ModelFormat, "model-format", "",
		"Model format (required, e.g., OpenAI, FireworksCustom)")
	cmd.Flags().StringVar(&flags.ModelSource, "model-source", "",
		"Model source ARM resource ID (auto-resolved for custom models if not provided)")
	cmd.Flags().StringVar(&flags.SkuName, "sku-name", "Standard",
		"SKU name (Standard, GlobalStandard, ProvisionedManaged, GlobalProvisionedManaged)")
	cmd.Flags().Int32Var(&flags.SkuCapacity, "sku-capacity", 1, "SKU capacity units")
	cmd.Flags().StringVar(&flags.RaiPolicy, "rai-policy", "",
		"RAI content filter policy name (e.g., Microsoft.DefaultV2)")
	cmd.Flags().BoolVar(&flags.NoWait, "no-wait", false,
		"Start deployment and return immediately without waiting for completion")

	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("model-name")
	_ = cmd.MarkFlagRequired("model-version")
	_ = cmd.MarkFlagRequired("model-format")

	return cmd
}

func runDeploymentCreate(ctx context.Context, parentFlags *deploymentFlags, flags *deploymentCreateFlags) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
			return nil
		}
		return fmt.Errorf("failed waiting for debugger: %w", err)
	}

	// Resolve account name from project endpoint
	accountName, err := resolveAccountName(parentFlags.projectEndpoint)
	if err != nil {
		return fmt.Errorf("failed to resolve account name: %w", err)
	}

	// Resolve resource group — flag > env > error
	resourceGroup := parentFlags.resourceGroup
	if resourceGroup == "" {
		envMap := loadEnvMap(ctx, azdClient)
		resourceGroup = envMap["AZURE_RESOURCE_GROUP_NAME"]
	}
	if resourceGroup == "" {
		return fmt.Errorf(
			"resource group is required for deployment.\n\n" +
				"Provide it with --resource-group (-g) or run 'azd ai models init' to configure your project")
	}

	// Resolve subscription ID
	subscriptionID := parentFlags.subscriptionId
	if subscriptionID == "" {
		return fmt.Errorf(
			"subscription ID is required for deployment.\n\n" +
				"Provide it with --subscription (-s) or run 'azd ai models init' to configure your project")
	}

	// Resolve tenant ID for credential
	tenantID, err := resolveTenantID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("failed to resolve tenant ID: %w", err)
	}

	// Create credential
	credential, err := createCredential(tenantID)
	if err != nil {
		return fmt.Errorf("failed to create Azure credential: %w", err)
	}

	// Auto-resolve model source for custom models if not explicitly provided
	modelSource := flags.ModelSource
	if modelSource == "" && isCustomModelFormat(flags.ModelFormat) {
		_, projectName, parseErr := parseProjectEndpoint(parentFlags.projectEndpoint)
		if parseErr == nil && projectName != "" {
			modelSource = buildProjectResourceID(subscriptionID, resourceGroup, accountName, projectName)
		}
	}

	// Build deployment config
	config := &models.DeploymentConfig{
		DeploymentName: flags.Name,
		ModelName:      flags.ModelName,
		ModelVersion:   flags.ModelVersion,
		ModelFormat:    flags.ModelFormat,
		ModelSource:    modelSource,
		SkuName:        flags.SkuName,
		SkuCapacity:    flags.SkuCapacity,
		RaiPolicyName:  flags.RaiPolicy,
		SubscriptionID: subscriptionID,
		ResourceGroup:  resourceGroup,
		AccountName:    accountName,
		TenantID:       tenantID,
		WaitForCompletion: !flags.NoWait,
	}

	// Display deployment info
	fmt.Printf("Creating deployment: %s\n", flags.Name)
	fmt.Printf("  Model:    %s (version %s, format %s)\n", flags.ModelName, flags.ModelVersion, flags.ModelFormat)
	fmt.Printf("  SKU:      %s (capacity %d)\n", flags.SkuName, flags.SkuCapacity)
	fmt.Printf("  Account:  %s\n", accountName)
	fmt.Printf("  Resource: %s\n", resourceGroup)
	if modelSource != "" {
		fmt.Printf("  Source:   %s\n", modelSource)
	}
	fmt.Println()

	// Create ARM deployment client
	deployClient, err := client.NewDeploymentClient(subscriptionID, credential)
	if err != nil {
		return fmt.Errorf("failed to create deployment client: %w", err)
	}

	if flags.NoWait {
		result, err := deployClient.CreateDeployment(ctx, config)
		if err != nil {
			return handleDeploymentError(err)
		}

		color.Green("✓ Deployment request accepted")
		fmt.Printf("  Name:   %s\n", result.Name)
		fmt.Printf("  Status: %s\n", result.ProvisioningState)
		fmt.Println()
		color.Yellow("Use 'azd ai models deployment show --name %s' to check deployment status.", flags.Name)
		return nil
	}

	// Wait for deployment with spinner
	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "Deploying model (this may take several minutes)...",
	})
	if err := spinner.Start(ctx); err != nil {
		fmt.Printf("failed to start spinner: %v\n", err)
	}

	result, err := deployClient.CreateDeployment(ctx, config)
	_ = spinner.Stop(ctx)
	fmt.Println()

	if err != nil {
		return handleDeploymentError(err)
	}

	// Success output
	color.Green("✓ Deployment created successfully!")
	fmt.Println()
	fmt.Println(strings.Repeat("─", 50))
	if result.ID != "" {
		fmt.Printf("  ID:     %s\n", result.ID)
	}
	fmt.Printf("  Name:   %s\n", result.Name)
	fmt.Printf("  Model:  %s\n", result.ModelName)
	fmt.Printf("  Status: %s\n", result.ProvisioningState)
	fmt.Println(strings.Repeat("─", 50))

	return nil
}

// handleDeploymentError provides user-friendly error messages for common deployment failures.
func handleDeploymentError(err error) error {
	errMsg := err.Error()

	if strings.Contains(errMsg, "403") || strings.Contains(strings.ToLower(errMsg), "forbidden") {
		fmt.Println()
		color.Red("✗ Permission denied: you do not have the required role to create deployments.")
		fmt.Println()
		color.Yellow("Ensure you have the appropriate role assigned for this Azure AI Foundry project.")
		fmt.Println()
		fmt.Println("  Prerequisites:")
		fmt.Println("  https://learn.microsoft.com/en-us/azure/foundry/how-to/fireworks/import-custom-models?tabs=rest-api#prerequisites")
		fmt.Println()
		fmt.Println("  Role-based access control (RBAC) details:")
		fmt.Println("  https://learn.microsoft.com/en-us/azure/foundry/concepts/rbac-foundry")
		return fmt.Errorf("insufficient permissions (403)")
	}

	if strings.Contains(errMsg, "409") || strings.Contains(strings.ToLower(errMsg), "conflict") {
		fmt.Println()
		color.Red("✗ A deployment with this name already exists.")
		fmt.Println()
		color.Yellow("Use a different --name or delete the existing deployment first.")
		return fmt.Errorf("deployment already exists (409)")
	}

	if strings.Contains(strings.ToLower(errMsg), "quota") ||
		strings.Contains(strings.ToLower(errMsg), "capacity") {
		fmt.Println()
		color.Red("✗ Insufficient quota or capacity for the requested deployment.")
		fmt.Println()
		color.Yellow("Try reducing --sku-capacity or using a different SKU/region.")
		return fmt.Errorf("insufficient quota: %w", err)
	}

	fmt.Println()
	color.Red("✗ Deployment failed: %v", err)
	return fmt.Errorf("deployment failed: %w", err)
}

// isCustomModelFormat returns true if the model format indicates a custom model.
func isCustomModelFormat(format string) bool {
	lower := strings.ToLower(format)
	return strings.Contains(lower, "custom") || strings.Contains(lower, "safetensors")
}
