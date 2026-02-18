// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type initFlags struct {
	subscriptionId    string
	projectEndpoint   string
	projectResourceId string
	env               string
}

// FoundryProject stores the resolved Azure resource details for the Foundry project.
type FoundryProject struct {
	TenantId          string
	SubscriptionId    string
	Location          string
	ResourceGroupName string
	AiAccountName     string
	AiProjectName     string
}

func newInitCommand() *cobra.Command {
	flags := &initFlags{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: fmt.Sprintf("Initialize a new AI models project. %s", color.YellowString("(Preview)")),
		Long: `Initialize a new AI models project by setting up an azd environment
and configuring the Azure AI Foundry project connection.

The init command will:
  1. Ensure an azd project is initialized
  2. Create or select an azd environment
  3. Configure Azure subscription, resource group, and Foundry project
  4. Store all settings as environment variables for use by other commands`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

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

			azureContext, env, err := runInit(ctx, flags, azdClient)
			if err != nil {
				return err
			}

			// Print summary
			fmt.Println()
			fmt.Println(color.GreenString("SUCCESS: ") + "AI models project initialized!")
			fmt.Println()
			fmt.Printf("  Environment:    %s\n", env.Name)
			fmt.Printf("  Subscription:   %s\n", azureContext.Scope.SubscriptionId)
			fmt.Printf("  Resource Group: %s\n", azureContext.Scope.ResourceGroup)
			fmt.Println()
			fmt.Println("You can now use commands like:")
			fmt.Println("  azd ai models custom list")
			fmt.Println("  azd ai models custom create --name <model-name> --model <path>")

			return nil
		},
	}

	cmd.Flags().StringVarP(&flags.subscriptionId, "subscription", "s", "",
		"Azure subscription ID")

	cmd.Flags().StringVarP(&flags.projectEndpoint, "project-endpoint", "e", "",
		"Azure AI Foundry project endpoint URL (e.g., https://account.services.ai.azure.com/api/projects/project-name)")

	cmd.Flags().StringVarP(&flags.projectResourceId, "project-resource-id", "p", "",
		"ARM resource ID of the Foundry project")

	cmd.Flags().StringVarP(&flags.env, "environment", "n", "", "The name of the azd environment to use")

	return cmd
}

// runInit orchestrates the full init flow: project → environment → Azure context.
func runInit(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
) (*azdext.AzureContext, *azdext.Environment, error) {
	// Step 1: Ensure azd project is initialized
	_, err := ensureProject(ctx, flags, azdClient)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to ensure project: %w", err)
	}

	// Step 2: Ensure environment exists
	env, err := ensureEnvironment(ctx, flags, azdClient)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to ensure environment: %w", err)
	}

	// Step 3: Ensure Azure context (subscription, RG, foundry project)
	azureContext, err := ensureAzureContext(ctx, flags, azdClient, env)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to configure Azure context: %w", err)
	}

	return azureContext, env, nil
}

// ensureProject checks if an azd project exists, and creates one if not.
func ensureProject(ctx context.Context, flags *initFlags, azdClient *azdext.AzdClient) (*azdext.ProjectConfig, error) {
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Println("Let's get your project initialized.")

		initArgs := []string{"init", "--minimal"}
		if rootFlags.NoPrompt {
			initArgs = append(initArgs, "--no-prompt")
		}

		_, err := azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
			Workflow: &azdext.Workflow{
				Name: "init",
				Steps: []*azdext.WorkflowStep{
					{Command: &azdext.WorkflowCommand{Args: initArgs}},
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to initialize project: %w", err)
		}

		projectResponse, err = azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return nil, fmt.Errorf("failed to get project: %w", err)
		}
		fmt.Println()
	}

	if projectResponse.Project == nil {
		return nil, fmt.Errorf("project not found")
	}

	return projectResponse.Project, nil
}

// ensureEnvironment creates or retrieves the azd environment.
// If a project-endpoint or project-resource-id is provided, it resolves the Foundry project
// and pre-populates environment variables.
func ensureEnvironment(ctx context.Context, flags *initFlags, azdClient *azdext.AzdClient) (*azdext.Environment, error) {
	var foundryProject *FoundryProject

	// If project-endpoint is provided, resolve the Foundry project from it
	if flags.projectEndpoint != "" {
		accountName, projectName, err := parseProjectEndpoint(flags.projectEndpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to parse project endpoint: %w", err)
		}
		fmt.Printf("Parsed endpoint — Account: %s, Project: %s\n", accountName, projectName)

		subscriptionId := flags.subscriptionId
		var tenantId string

		if subscriptionId == "" {
			fmt.Println("Subscription ID is required to find the project. Let's select one.")
			resp, err := azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
			if err != nil {
				return nil, fmt.Errorf("failed to prompt for subscription: %w", err)
			}
			subscriptionId = resp.Subscription.Id
			tenantId = resp.Subscription.TenantId
		} else {
			tenantResp, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
				SubscriptionId: subscriptionId,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to get tenant ID: %w", err)
			}
			tenantId = tenantResp.TenantId
		}

		credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   tenantId,
			AdditionallyAllowedTenants: []string{"*"},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure credential: %w", err)
		}

		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text: fmt.Sprintf("Searching for project in subscription %s...", subscriptionId),
		})
		_ = spinner.Start(ctx)

		foundryProject, err = findProjectByEndpoint(ctx, subscriptionId, accountName, projectName, credential)
		_ = spinner.Stop(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to find project from endpoint: %w", err)
		}
		foundryProject.TenantId = tenantId

		fmt.Printf("Found project — Resource Group: %s, Account: %s, Project: %s\n",
			foundryProject.ResourceGroupName, foundryProject.AiAccountName, foundryProject.AiProjectName)

	} else if flags.projectResourceId != "" {
		// Parse the ARM resource ID directly
		fp, err := extractProjectDetails(flags.projectResourceId)
		if err != nil {
			return nil, fmt.Errorf("failed to parse project resource ID: %w", err)
		}

		tenantResp, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
			SubscriptionId: fp.SubscriptionId,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get tenant ID: %w", err)
		}
		fp.TenantId = tenantResp.TenantId

		credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   fp.TenantId,
			AdditionallyAllowedTenants: []string{"*"},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure credential: %w", err)
		}

		projectsClient, err := armcognitiveservices.NewProjectsClient(fp.SubscriptionId, credential, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create Cognitive Services client: %w", err)
		}

		projectResp, err := projectsClient.Get(ctx, fp.ResourceGroupName, fp.AiAccountName, fp.AiProjectName, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get Foundry project: %w", err)
		}
		fp.Location = *projectResp.Location

		foundryProject = fp
	}

	// Get or create the environment
	existingEnv := getExistingEnvironment(ctx, flags, azdClient)
	if existingEnv == nil {
		fmt.Println("Let's create a new azd environment for your project.")

		envArgs := []string{"env", "new"}
		if flags.env != "" {
			envArgs = append(envArgs, flags.env)
		}
		if foundryProject != nil {
			envArgs = append(envArgs, "--subscription", foundryProject.SubscriptionId)
			envArgs = append(envArgs, "--location", foundryProject.Location)
		}
		if rootFlags.NoPrompt {
			envArgs = append(envArgs, "--no-prompt")
		}

		_, err := azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
			Workflow: &azdext.Workflow{
				Name: "env new",
				Steps: []*azdext.WorkflowStep{
					{Command: &azdext.WorkflowCommand{Args: envArgs}},
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create new azd environment: %w", err)
		}

		existingEnv = getExistingEnvironment(ctx, flags, azdClient)
		if existingEnv == nil {
			return nil, fmt.Errorf("azd environment not found after creation, please create one with 'azd env new' and try again")
		}
	}

	// Set environment variables from resolved project
	if foundryProject != nil {
		envVars := map[string]string{
			"AZURE_TENANT_ID":           foundryProject.TenantId,
			"AZURE_SUBSCRIPTION_ID":     foundryProject.SubscriptionId,
			"AZURE_RESOURCE_GROUP_NAME": foundryProject.ResourceGroupName,
			"AZURE_ACCOUNT_NAME":        foundryProject.AiAccountName,
			"AZURE_PROJECT_NAME":        foundryProject.AiProjectName,
			"AZURE_LOCATION":            foundryProject.Location,
			"AZURE_PROJECT_ENDPOINT":    buildProjectEndpoint(foundryProject.AiAccountName, foundryProject.AiProjectName),
		}

		for key, value := range envVars {
			if _, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
				EnvName: existingEnv.Name,
				Key:     key,
				Value:   value,
			}); err != nil {
				return nil, fmt.Errorf("failed to set %s in azd environment: %w", key, err)
			}
		}
	}

	return existingEnv, nil
}

// ensureAzureContext prompts for any missing Azure context (subscription, RG, Foundry project)
// and stores the values in the environment.
func ensureAzureContext(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	env *azdext.Environment,
) (*azdext.AzureContext, error) {
	// Load existing env values
	envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: env.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get environment values: %w", err)
	}

	envMap := make(map[string]string)
	for _, kv := range envValues.KeyValues {
		envMap[kv.Key] = kv.Value
	}

	azureContext := &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			TenantId:       envMap["AZURE_TENANT_ID"],
			SubscriptionId: envMap["AZURE_SUBSCRIPTION_ID"],
			Location:       envMap["AZURE_LOCATION"],
			ResourceGroup:  envMap["AZURE_RESOURCE_GROUP_NAME"],
		},
		Resources: []string{},
	}

	// Prompt for subscription if missing
	if azureContext.Scope.SubscriptionId == "" {
		fmt.Println()
		fmt.Println("Let's connect to your Azure subscription.")

		resp, err := azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
		if err != nil {
			return nil, fmt.Errorf("failed to prompt for subscription: %w", err)
		}

		azureContext.Scope.SubscriptionId = resp.Subscription.Id
		azureContext.Scope.TenantId = resp.Subscription.TenantId

		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_TENANT_ID", azureContext.Scope.TenantId); err != nil {
			return nil, err
		}
		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_SUBSCRIPTION_ID", azureContext.Scope.SubscriptionId); err != nil {
			return nil, err
		}
	}

	// Prompt for resource group if missing
	if azureContext.Scope.ResourceGroup == "" {
		resp, err := azdClient.Prompt().PromptResourceGroup(ctx, &azdext.PromptResourceGroupRequest{
			AzureContext: azureContext,
			Options: &azdext.PromptResourceGroupOptions{
				SelectOptions: &azdext.PromptResourceSelectOptions{
					AllowNewResource: to.Ptr(false),
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to prompt for resource group: %w", err)
		}

		azureContext.Scope.ResourceGroup = resp.ResourceGroup.Name
		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_RESOURCE_GROUP_NAME", azureContext.Scope.ResourceGroup); err != nil {
			return nil, err
		}
	}

	// Prompt for Foundry project if missing
	if envMap["AZURE_ACCOUNT_NAME"] == "" {
		resp, err := azdClient.Prompt().PromptResourceGroupResource(ctx, &azdext.PromptResourceGroupResourceRequest{
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
			return nil, fmt.Errorf("failed to select Foundry project: %w", err)
		}

		fp, err := extractProjectDetails(resp.Resource.Id)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Foundry project ID: %w", err)
		}

		credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   azureContext.Scope.TenantId,
			AdditionallyAllowedTenants: []string{"*"},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure credential: %w", err)
		}

		projectsClient, err := armcognitiveservices.NewProjectsClient(azureContext.Scope.SubscriptionId, credential, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create Cognitive Services client: %w", err)
		}

		projectResp, err := projectsClient.Get(ctx, azureContext.Scope.ResourceGroup, fp.AiAccountName, fp.AiProjectName, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get Foundry project: %w", err)
		}

		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_ACCOUNT_NAME", fp.AiAccountName); err != nil {
			return nil, err
		}
		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_PROJECT_NAME", fp.AiProjectName); err != nil {
			return nil, err
		}
		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_LOCATION", *projectResp.Location); err != nil {
			return nil, err
		}
		if err := setEnvValue(ctx, azdClient, env.Name, "AZURE_PROJECT_ENDPOINT", buildProjectEndpoint(fp.AiAccountName, fp.AiProjectName)); err != nil {
			return nil, err
		}
	}

	return azureContext, nil
}

// setEnvValue is a helper to set a single environment value.
func setEnvValue(ctx context.Context, azdClient *azdext.AzdClient, envName, key, value string) error {
	_, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("failed to set environment value %s: %w", key, err)
	}
	return nil
}

// buildProjectEndpoint constructs the Foundry project endpoint URL from account and project names.
func buildProjectEndpoint(accountName, projectName string) string {
	return fmt.Sprintf("https://%s.services.ai.azure.com/api/projects/%s", accountName, projectName)
}

// getExistingEnvironment retrieves the current or named environment.
func getExistingEnvironment(ctx context.Context, flags *initFlags, azdClient *azdext.AzdClient) *azdext.Environment {
	if flags.env == "" {
		if resp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
			return resp.Environment
		}
	} else {
		if resp, err := azdClient.Environment().Get(ctx, &azdext.GetEnvironmentRequest{
			Name: flags.env,
		}); err == nil {
			return resp.Environment
		}
	}
	return nil
}

// parseProjectEndpoint extracts account name and project name from an endpoint URL.
// Example: https://account-name.services.ai.azure.com/api/projects/project-name
func parseProjectEndpoint(endpoint string) (accountName string, projectName string, err error) {
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse endpoint URL: %w", err)
	}

	hostname := parsedURL.Hostname()
	hostParts := strings.Split(hostname, ".")
	if len(hostParts) < 1 || hostParts[0] == "" {
		return "", "", fmt.Errorf("invalid endpoint URL: cannot extract account name from hostname")
	}
	accountName = hostParts[0]

	pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
	if len(pathParts) == 3 && pathParts[0] == "api" && pathParts[1] == "projects" && pathParts[2] != "" {
		projectName = pathParts[2]
	} else {
		return "", "", fmt.Errorf("invalid endpoint URL: expected path format /api/projects/{project-name}")
	}

	return accountName, projectName, nil
}

// extractProjectDetails parses a Foundry project ARM resource ID into its components.
func extractProjectDetails(projectResourceId string) (*FoundryProject, error) {
	resourceId, err := arm.ParseResourceID(projectResourceId)
	if err != nil {
		return nil, fmt.Errorf("failed to parse project resource ID: %w", err)
	}

	if resourceId.ResourceType.Namespace != "Microsoft.CognitiveServices" ||
		len(resourceId.ResourceType.Types) != 2 ||
		resourceId.ResourceType.Types[0] != "accounts" ||
		resourceId.ResourceType.Types[1] != "projects" {
		return nil, fmt.Errorf("not a Foundry project resource ID. Expected: /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{account}/projects/{project}")
	}

	return &FoundryProject{
		SubscriptionId:    resourceId.SubscriptionID,
		ResourceGroupName: resourceId.ResourceGroupName,
		AiAccountName:     resourceId.Parent.Name,
		AiProjectName:     resourceId.Name,
	}, nil
}

// findProjectByEndpoint searches for a Foundry project matching the given account and project names.
func findProjectByEndpoint(
	ctx context.Context,
	subscriptionId string,
	accountName string,
	projectName string,
	credential azcore.TokenCredential,
) (*FoundryProject, error) {
	accountsClient, err := armcognitiveservices.NewAccountsClient(subscriptionId, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cognitive Services client: %w", err)
	}

	// Find the matching account
	pager := accountsClient.NewListPager(nil)
	var foundAccount *armcognitiveservices.Account
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list accounts: %w", err)
		}
		for _, account := range page.Value {
			if account.Name != nil && strings.EqualFold(*account.Name, accountName) {
				foundAccount = account
				break
			}
		}
		if foundAccount != nil {
			break
		}
	}

	if foundAccount == nil {
		return nil, fmt.Errorf("account '%s' not found in subscription '%s'", accountName, subscriptionId)
	}

	accountResourceId, err := arm.ParseResourceID(*foundAccount.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse account resource ID: %w", err)
	}

	// Verify the project exists
	projectsClient, err := armcognitiveservices.NewProjectsClient(subscriptionId, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Projects client: %w", err)
	}

	projectResp, err := projectsClient.Get(ctx, accountResourceId.ResourceGroupName, accountName, projectName, nil)
	if err != nil {
		return nil, fmt.Errorf("project '%s' not found under account '%s': %w", projectName, accountName, err)
	}

	return &FoundryProject{
		SubscriptionId:    subscriptionId,
		ResourceGroupName: accountResourceId.ResourceGroupName,
		AiAccountName:     accountName,
		AiProjectName:     projectName,
		Location:          *projectResp.Location,
	}, nil
}
