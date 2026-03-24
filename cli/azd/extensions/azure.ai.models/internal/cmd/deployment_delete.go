// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"azure.ai.models/internal/client"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type deploymentDeleteFlags struct {
	Name  string
	Force bool
}

func newDeploymentDeleteCommand(parentFlags *deploymentFlags) *cobra.Command {
	flags := &deploymentDeleteFlags{}

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a model deployment",
		Long:  "Delete a model deployment from the Azure AI Foundry project's Cognitive Services account.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			return runDeploymentDelete(ctx, parentFlags, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Deployment name to delete (required)")
	cmd.Flags().BoolVarP(&flags.Force, "force", "f", false, "Skip confirmation prompt")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func runDeploymentDelete(ctx context.Context, parentFlags *deploymentFlags, flags *deploymentDeleteFlags) error {
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

	// Confirmation prompt unless --force
	if !flags.Force && !rootFlags.NoPrompt {
		fmt.Printf("Delete deployment '%s'? This action cannot be undone.\n", flags.Name)
		fmt.Print("Type the deployment name to confirm: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input != flags.Name {
			fmt.Println("Deletion cancelled.")
			return nil
		}
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
			"resource group is required to delete a deployment.\n\n" +
				"Provide it with --resource-group (-g) or run 'azd ai models init' to configure your project")
	}

	subscriptionID := parentFlags.subscriptionId
	if subscriptionID == "" {
		return fmt.Errorf(
			"subscription ID is required to delete a deployment.\n\n" +
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
		Text: fmt.Sprintf("Deleting deployment '%s'...", flags.Name),
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

	err = deployClient.DeleteDeployment(ctx, resourceGroup, accountName, flags.Name)
	_ = spinner.Stop(ctx)
	fmt.Println()

	if err != nil {
		color.Red("✗ Failed to delete deployment: %v", err)
		return fmt.Errorf("failed to delete deployment: %w", err)
	}

	color.Green("✓ Deployment '%s' deleted", flags.Name)
	return nil
}
