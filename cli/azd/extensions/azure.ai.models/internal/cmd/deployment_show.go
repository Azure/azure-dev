// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"azure.ai.models/internal/client"
	"azure.ai.models/internal/utils"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

type deploymentShowFlags struct {
	Name   string
	Output string
}

func newDeploymentShowCommand(parentFlags *deploymentFlags) *cobra.Command {
	flags := &deploymentShowFlags{}

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show details of a model deployment",
		Long:  "Show detailed information about a specific model deployment in the Azure AI Foundry project.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			return runDeploymentShow(ctx, parentFlags, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Deployment name (required)")
	cmd.Flags().StringVarP(&flags.Output, "output", "o", "table", "Output format (table, json)")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func runDeploymentShow(ctx context.Context, parentFlags *deploymentFlags, flags *deploymentShowFlags) error {
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

	// Resolve context
	accountName, err := resolveAccountName(parentFlags.projectEndpoint)
	if err != nil {
		return fmt.Errorf("failed to resolve account name: %w", err)
	}

	resourceGroup := parentFlags.resourceGroup
	if resourceGroup == "" {
		envMap := loadEnvMap(ctx, azdClient)
		resourceGroup = envMap["AZURE_RESOURCE_GROUP_NAME"]
	}
	if resourceGroup == "" {
		return fmt.Errorf(
			"resource group is required to show deployment details.\n\n" +
				"Provide it with --resource-group (-g) or run 'azd ai models init' to configure your project")
	}

	subscriptionID := parentFlags.subscriptionId
	if subscriptionID == "" {
		return fmt.Errorf(
			"subscription ID is required to show deployment details.\n\n" +
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
		Text: "Fetching deployment details...",
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

	detail, err := deployClient.GetDeployment(ctx, resourceGroup, accountName, flags.Name)
	_ = spinner.Stop(ctx)
	fmt.Print("\n\n")

	if err != nil {
		return fmt.Errorf("failed to get deployment: %w", err)
	}

	switch flags.Output {
	case "json":
		if err := utils.PrintObject(detail, utils.FormatJSON); err != nil {
			return err
		}
	case "table", "":
		fmt.Printf("Deployment: %s\n", detail.Name)
		fmt.Println(strings.Repeat("─", 50))

		fmt.Println("\nGeneral:")
		fmt.Printf("  Name:   %s\n", detail.Name)
		fmt.Printf("  State:  %s\n", detail.ProvisioningState)
		if detail.ID != "" {
			fmt.Printf("  ID:     %s\n", detail.ID)
		}

		fmt.Println("\nModel:")
		fmt.Printf("  Name:    %s\n", detail.ModelName)
		fmt.Printf("  Format:  %s\n", detail.ModelFormat)
		fmt.Printf("  Version: %s\n", detail.ModelVersion)
		if detail.ModelSource != "" {
			fmt.Printf("  Source:  %s\n", detail.ModelSource)
		}

		fmt.Println("\nSKU:")
		fmt.Printf("  Name:     %s\n", detail.SkuName)
		fmt.Printf("  Capacity: %d\n", detail.SkuCapacity)

		if detail.RaiPolicyName != "" {
			fmt.Println("\nPolicies:")
			fmt.Printf("  RAI Policy: %s\n", detail.RaiPolicyName)
		}

		if detail.CreatedAt != "" || detail.LastModifiedAt != "" {
			fmt.Println("\nTimestamps:")
			if detail.CreatedAt != "" {
				fmt.Printf("  Created:       %s\n", detail.CreatedAt)
			}
			if detail.LastModifiedAt != "" {
				fmt.Printf("  Last Modified: %s\n", detail.LastModifiedAt)
			}
		}

		fmt.Println(strings.Repeat("─", 50))
	default:
		return fmt.Errorf("unsupported output format: %s (supported: table, json)", flags.Output)
	}

	return nil
}
