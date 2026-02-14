// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// customFlags holds the common flags for all custom model subcommands
type customFlags struct {
	subscriptionId  string
	projectEndpoint string
}

// newCustomCommand creates the "custom" command group for custom model operations.
func newCustomCommand() *cobra.Command {
	flags := &customFlags{}

	customCmd := &cobra.Command{
		Use:   "custom",
		Short: "Manage custom models in Azure AI Foundry",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			return resolveProjectEndpoint(ctx, flags)
		},
	}

	customCmd.PersistentFlags().StringVarP(&flags.subscriptionId, "subscription", "s", "",
		"Azure subscription ID")
	customCmd.PersistentFlags().StringVarP(&flags.projectEndpoint, "project-endpoint", "e", "",
		"Azure AI Foundry project endpoint URL (e.g., https://account.services.ai.azure.com/api/projects/project-name)")

	customCmd.AddCommand(newCustomCreateCommand(flags))
	customCmd.AddCommand(newCustomListCommand(flags))
	customCmd.AddCommand(newCustomShowCommand(flags))
	customCmd.AddCommand(newCustomDeleteCommand(flags))

	return customCmd
}

// resolveProjectEndpoint resolves the project endpoint using this priority:
//  1. Explicit --project-endpoint flag (highest priority)
//  2. AZURE_PROJECT_ENDPOINT from azd environment
//  3. Lightweight interactive prompt (subscription → RG → Foundry project)
func resolveProjectEndpoint(ctx context.Context, flags *customFlags) error {
	// If user explicitly provided the endpoint flag, use it directly
	if flags.projectEndpoint != "" {
		return nil
	}

	// Try to read from azd environment
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		// Can't connect to azd — fall through to prompt
		return promptForProject(ctx, flags, nil)
	}
	defer azdClient.Close()

	endpoint, subId := loadFromEnvironment(ctx, azdClient)
	if endpoint != "" {
		flags.projectEndpoint = endpoint
		if flags.subscriptionId == "" && subId != "" {
			flags.subscriptionId = subId
		}
		return nil
	}

	// Environment not configured — run lightweight prompt
	return promptForProject(ctx, flags, azdClient)
}

// loadFromEnvironment tries to read AZURE_PROJECT_ENDPOINT from the current azd environment.
func loadFromEnvironment(ctx context.Context, azdClient *azdext.AzdClient) (endpoint, subscriptionId string) {
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || envResp.Environment == nil {
		return "", ""
	}

	valuesResp, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: envResp.Environment.Name,
	})
	if err != nil {
		return "", ""
	}

	envMap := make(map[string]string)
	for _, kv := range valuesResp.KeyValues {
		envMap[kv.Key] = kv.Value
	}

	// Try stored endpoint first, then construct from account + project
	ep := envMap["AZURE_PROJECT_ENDPOINT"]
	if ep == "" {
		account := envMap["AZURE_ACCOUNT_NAME"]
		project := envMap["AZURE_PROJECT_NAME"]
		if account != "" && project != "" {
			ep = buildProjectEndpoint(account, project)
		}
	}

	return ep, envMap["AZURE_SUBSCRIPTION_ID"]
}

// promptForProject runs a lightweight interactive flow to select a Foundry project.
// It prompts for subscription → resource group → Foundry project without creating
// an azd project or environment.
func promptForProject(ctx context.Context, flags *customFlags, azdClient *azdext.AzdClient) error {
	if azdClient == nil {
		var err error
		azdClient, err = azdext.NewAzdClient()
		if err != nil {
			return fmt.Errorf("--project-endpoint (-e) is required when azd is not available.\n\n" +
				"Example: azd ai models custom list -e https://<account>.services.ai.azure.com/api/projects/<project>\n\n" +
				"Or run 'azd ai models init' to set up your project first")
		}
		defer azdClient.Close()
	}

	if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
			return nil
		}
	}

	fmt.Println("No project endpoint configured. Let's select your Azure AI Foundry project.")
	fmt.Println()

	// Prompt for subscription
	subscriptionId := flags.subscriptionId
	var tenantId string

	if subscriptionId == "" {
		resp, err := azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
		if err != nil {
			return fmt.Errorf("failed to prompt for subscription: %w", err)
		}
		subscriptionId = resp.Subscription.Id
		tenantId = resp.Subscription.TenantId
	} else {
		tenantResp, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
			SubscriptionId: subscriptionId,
		})
		if err != nil {
			return fmt.Errorf("failed to get tenant ID: %w", err)
		}
		tenantId = tenantResp.TenantId
	}

	azureContext := &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			TenantId:       tenantId,
			SubscriptionId: subscriptionId,
		},
		Resources: []string{},
	}

	// Prompt for resource group
	rgResp, err := azdClient.Prompt().PromptResourceGroup(ctx, &azdext.PromptResourceGroupRequest{
		AzureContext: azureContext,
		Options: &azdext.PromptResourceGroupOptions{
			SelectOptions: &azdext.PromptResourceSelectOptions{
				AllowNewResource: to.Ptr(false),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to prompt for resource group: %w", err)
	}
	azureContext.Scope.ResourceGroup = rgResp.ResourceGroup.Name

	// Prompt for Foundry project
	projResp, err := azdClient.Prompt().PromptResourceGroupResource(ctx, &azdext.PromptResourceGroupResourceRequest{
		AzureContext: azureContext,
		Options: &azdext.PromptResourceOptions{
			ResourceType:            "Microsoft.CognitiveServices/accounts/projects",
			ResourceTypeDisplayName: "AI Foundry project",
			SelectOptions: &azdext.PromptResourceSelectOptions{
				AllowNewResource: to.Ptr(false),
				Message:          "Select a Foundry project",
				LoadingMessage:   "Fetching Foundry projects...",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to select Foundry project: %w", err)
	}

	// Extract account + project from the resource ID
	fp, err := extractProjectDetails(projResp.Resource.Id)
	if err != nil {
		return fmt.Errorf("failed to parse Foundry project ID: %w", err)
	}

	// Verify the project exists and get endpoint info
	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return fmt.Errorf("failed to create Azure credential: %w", err)
	}

	if err := verifyProject(ctx, subscriptionId, azureContext.Scope.ResourceGroup, fp.AiAccountName, fp.AiProjectName, credential); err != nil {
		return err
	}

	flags.projectEndpoint = buildProjectEndpoint(fp.AiAccountName, fp.AiProjectName)
	flags.subscriptionId = subscriptionId

	fmt.Printf("\nUsing project endpoint: %s\n\n", flags.projectEndpoint)
	fmt.Println("Tip: Run 'azd ai models init' to save this configuration for future use.")
	fmt.Println()

	return nil
}

// verifyProject checks that the Foundry project exists.
func verifyProject(ctx context.Context, subscriptionId, resourceGroup, accountName, projectName string, credential azcore.TokenCredential) error {
	projectsClient, err := armcognitiveservices.NewProjectsClient(subscriptionId, credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create Cognitive Services client: %w", err)
	}

	_, err = projectsClient.Get(ctx, resourceGroup, accountName, projectName, nil)
	if err != nil {
		return fmt.Errorf("failed to verify Foundry project '%s/%s': %w", accountName, projectName, err)
	}

	return nil
}
