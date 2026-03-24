// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"

	"azure.ai.models/internal/client"
	"azure.ai.models/internal/utils"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

type deploymentListFlags struct {
	Output string
}

func newDeploymentListCommand(parentFlags *deploymentFlags) *cobra.Command {
	flags := &deploymentListFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all model deployments",
		Long:  "List all model deployments for the Azure AI Foundry project's Cognitive Services account.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			return runDeploymentList(ctx, parentFlags, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.Output, "output", "o", "table", "Output format (table, json)")

	return cmd
}

func runDeploymentList(ctx context.Context, parentFlags *deploymentFlags, flags *deploymentListFlags) error {
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

	// Resolve resource group
	resourceGroup := parentFlags.resourceGroup
	if resourceGroup == "" {
		envMap := loadEnvMap(ctx, azdClient)
		resourceGroup = envMap["AZURE_RESOURCE_GROUP_NAME"]
	}
	if resourceGroup == "" {
		return fmt.Errorf(
			"resource group is required to list deployments.\n\n" +
				"Provide it with --resource-group (-g) or run 'azd ai models init' to configure your project")
	}

	// Resolve subscription and tenant
	subscriptionID := parentFlags.subscriptionId
	if subscriptionID == "" {
		return fmt.Errorf(
			"subscription ID is required to list deployments.\n\n" +
				"Provide it with --subscription (-s) or run 'azd ai models init' to configure your project")
	}

	tenantID, err := resolveTenantID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("failed to resolve tenant ID: %w", err)
	}

	credential, err := createCredential(tenantID)
	if err != nil {
		return fmt.Errorf("failed to create Azure credential: %w", err)
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "Fetching deployments...",
	})
	if err := spinner.Start(ctx); err != nil {
		fmt.Printf("failed to start spinner: %v\n", err)
	}

	deployClient, err := client.NewDeploymentClient(subscriptionID, credential)
	if err != nil {
		_ = spinner.Stop(ctx)
		fmt.Println()
		return fmt.Errorf("failed to create deployment client: %w", err)
	}

	deployments, err := deployClient.ListDeployments(ctx, resourceGroup, accountName)
	_ = spinner.Stop(ctx)
	fmt.Print("\n\n")

	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	switch flags.Output {
	case "json":
		if err := utils.PrintObject(deployments, utils.FormatJSON); err != nil {
			return err
		}
	case "table", "":
		if err := utils.PrintObject(deployments, utils.FormatTable); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported output format: %s (supported: table, json)", flags.Output)
	}

	fmt.Printf("\n%d deployment(s) found\n", len(deployments))
	return nil
}
