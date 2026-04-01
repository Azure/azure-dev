// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// deploymentFlags holds common flags for all deployment subcommands.
type deploymentFlags struct {
	subscriptionId  string
	projectEndpoint string
	resourceGroup   string
}

// newDeploymentCommand creates the "deployment" command group for model deployment operations.
func newDeploymentCommand() *cobra.Command {
	flags := &deploymentFlags{}

	deploymentCmd := &cobra.Command{
		Use:   "deployment",
		Short: "Manage model deployments in Azure AI Foundry",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			return resolveDeploymentContext(ctx, flags)
		},
	}

	deploymentCmd.PersistentFlags().StringVarP(&flags.subscriptionId, "subscription", "s", "",
		"Azure subscription ID")
	deploymentCmd.PersistentFlags().StringVarP(&flags.projectEndpoint, "project-endpoint", "e", "",
		"Azure AI Foundry project endpoint URL")
	deploymentCmd.PersistentFlags().StringVarP(&flags.resourceGroup, "resource-group", "g", "",
		"Azure resource group name")

	deploymentCmd.AddCommand(newDeploymentCreateCommand(flags))
	deploymentCmd.AddCommand(newDeploymentListCommand(flags))
	deploymentCmd.AddCommand(newDeploymentShowCommand(flags))
	deploymentCmd.AddCommand(newDeploymentDeleteCommand(flags))

	return deploymentCmd
}

// resolveDeploymentContext resolves subscription, project endpoint, resource group, and account
// from flags or the azd environment. Priority:
//  1. Explicit flags (highest)
//  2. azd environment variables (from init)
//  3. Interactive prompt (lowest)
func resolveDeploymentContext(ctx context.Context, flags *deploymentFlags) error {
	// If all required context is already provided via flags, skip env lookup
	if flags.projectEndpoint != "" && flags.resourceGroup != "" && flags.subscriptionId != "" {
		return nil
	}

	// Try to read from azd environment
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		if flags.projectEndpoint == "" || flags.resourceGroup == "" {
			return fmt.Errorf(
				"--project-endpoint (-e) and --resource-group (-g) are required when azd is not available.\n\n" +
					"Or run 'azd ai models init' to set up your project first")
		}
		return nil
	}
	defer azdClient.Close()

	envMap := loadEnvMap(ctx, azdClient)

	if flags.projectEndpoint == "" {
		flags.projectEndpoint = envMap["AZURE_PROJECT_ENDPOINT"]
		if flags.projectEndpoint == "" {
			account := envMap["AZURE_ACCOUNT_NAME"]
			project := envMap["AZURE_PROJECT_NAME"]
			if account != "" && project != "" {
				flags.projectEndpoint = buildProjectEndpoint(account, project)
			}
		}
	}

	if flags.subscriptionId == "" {
		flags.subscriptionId = envMap["AZURE_SUBSCRIPTION_ID"]
	}

	if flags.resourceGroup == "" {
		flags.resourceGroup = envMap["AZURE_RESOURCE_GROUP_NAME"]
	}

	// If still missing critical context, fall back to prompt
	if flags.projectEndpoint == "" {
		customFlags := &customFlags{
			subscriptionId:  flags.subscriptionId,
			projectEndpoint: flags.projectEndpoint,
		}
		if err := promptForProject(ctx, customFlags, azdClient); err != nil {
			return err
		}
		flags.projectEndpoint = customFlags.projectEndpoint
		flags.subscriptionId = customFlags.subscriptionId
	}

	return nil
}

// loadEnvMap loads all environment variables from the current azd environment.
func loadEnvMap(ctx context.Context, azdClient *azdext.AzdClient) map[string]string {
	envMap := make(map[string]string)

	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || envResp.Environment == nil {
		return envMap
	}

	valuesResp, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: envResp.Environment.Name,
	})
	if err != nil {
		return envMap
	}

	for _, kv := range valuesResp.KeyValues {
		envMap[kv.Key] = kv.Value
	}

	return envMap
}

// resolveAccountName extracts the account name from the project endpoint URL.
func resolveAccountName(projectEndpoint string) (string, error) {
	accountName, _, err := parseProjectEndpoint(projectEndpoint)
	if err != nil {
		return "", fmt.Errorf("failed to extract account name from endpoint: %w", err)
	}
	return accountName, nil
}

// resolveTenantID resolves the tenant ID for the given subscription.
func resolveTenantID(ctx context.Context, subscriptionId string) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	// Try environment first
	envMap := loadEnvMap(ctx, azdClient)
	if tenantID := envMap["AZURE_TENANT_ID"]; tenantID != "" {
		return tenantID, nil
	}

	// Fall back to LookupTenant
	if subscriptionId != "" {
		tenantResp, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
			SubscriptionId: subscriptionId,
		})
		if err != nil {
			return "", fmt.Errorf("failed to get tenant ID: %w", err)
		}
		return tenantResp.TenantId, nil
	}

	return "", nil
}

// buildProjectResourceID constructs the ARM resource ID for the Foundry project.
func buildProjectResourceID(subscriptionID, resourceGroup, accountName, projectName string) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s/projects/%s",
		subscriptionID, resourceGroup, accountName, projectName,
	)
}

// createCredential creates an Azure Developer CLI credential with the given tenant ID.
func createCredential(tenantID string) (*azidentity.AzureDeveloperCLICredential, error) {
	opts := &azidentity.AzureDeveloperCLICredentialOptions{
		AdditionallyAllowedTenants: []string{"*"},
	}
	if tenantID != "" {
		opts.TenantID = tenantID
	}
	return azidentity.NewAzureDeveloperCLICredential(opts)
}
