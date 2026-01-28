// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"azure.ai.finetune/internal/services"
)

type initFlags struct {
	rootFlagsDefinition
	template          string
	projectResourceId string
	subscriptionId    string
	projectEndpoint   string
	jobId             string
	src               string
	env               string
}

// AiProjectResourceConfig represents the configuration for an AI project resource
type AiProjectResourceConfig struct {
	Models []map[string]interface{} `json:"models,omitempty"`
}

type InitAction struct {
	azdClient *azdext.AzdClient
	//azureClient       *azure.AzureClient
	azureContext *azdext.AzureContext
	//composedResources []*azdext.ComposedResource
	console       input.Console
	credential    azcore.TokenCredential
	projectConfig *azdext.ProjectConfig
	environment   *azdext.Environment
	flags         *initFlags
}

// GitHubUrlInfo holds parsed information from a GitHub URL
type GitHubUrlInfo struct {
	RepoSlug string
	Branch   string
	FilePath string
	Hostname string
}

const AiFineTuningHost = "azure.ai.finetune"

func newInitCommand(rootFlags rootFlagsDefinition) *cobra.Command {
	flags := &initFlags{
		rootFlagsDefinition: rootFlags,
	}

	cmd := &cobra.Command{
		Use:   "init [-t <fine tuning job template>] [-p <foundry project arm id>]",
		Short: fmt.Sprintf("Initialize a new AI Fine-tuning project. %s", color.YellowString("(Preview)")),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Wait for debugger if AZD_EXT_DEBUG is set
			if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
					return nil
				}
				return fmt.Errorf("failed waiting for debugger: %w", err)
			}

			azureContext, projectConfig, environment, err := ensureAzureContext(ctx, flags, azdClient)
			if err != nil {
				return fmt.Errorf("failed to ground into a project context: %w", err)
			}

			credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
				TenantID:                   azureContext.Scope.TenantId,
				AdditionallyAllowedTenants: []string{"*"},
			})
			if err != nil {
				return fmt.Errorf("failed to create azure credential: %w", err)
			}

			console := input.NewConsole(
				false, // noPrompt
				true,  // isTerminal
				input.Writers{Output: os.Stdout},
				input.ConsoleHandles{
					Stderr: os.Stderr,
					Stdin:  os.Stdin,
					Stdout: os.Stdout,
				},
				nil, // formatter
				nil, // externalPromptCfg
			)

			action := &InitAction{
				azdClient:     azdClient,
				azureContext:  azureContext,
				console:       console,
				credential:    credential,
				projectConfig: projectConfig,
				environment:   environment,
				flags:         flags,
			}

			if err := action.Run(ctx); err != nil {
				return fmt.Errorf("failed to run start action: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&flags.template, "template", "t", "",
		"URL or path to a fine-tune job template")

	cmd.Flags().StringVarP(&flags.projectResourceId, "project-resource-id", "p", "",
		"ARM resource ID of the Microsoft Foundry Project (e.g., /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{account}/projects/{project})")

	cmd.Flags().StringVarP(&flags.subscriptionId, "subscription", "s", "",
		"Azure subscription ID")

	cmd.Flags().StringVarP(&flags.projectEndpoint, "project-endpoint", "e", "",
		"Azure AI Foundry project endpoint URL (e.g., https://account.services.ai.azure.com/api/projects/project-name)")

	cmd.Flags().StringVarP(&flags.src, "working-directory", "w", "",
		"Local path for project output")

	cmd.Flags().StringVarP(&flags.jobId, "from-job", "j", "",
		"Clone configuration from an existing job ID")

	cmd.Flags().StringVarP(&flags.env, "environment", "n", "", "The name of the azd environment to use.")

	return cmd
}

type FoundryProject struct {
	TenantId          string `json:"tenantId"`
	SubscriptionId    string `json:"subscriptionId"`
	Location          string `json:"location"`
	ResourceGroupName string `json:"resourceGroupName"`
	AiAccountName     string `json:"aiAccountName"`
	AiProjectName     string `json:"aiProjectName"`
}

func extractProjectDetails(projectResourceId string) (*FoundryProject, error) {
	resourceId, err := arm.ParseResourceID(projectResourceId)
	if err != nil {
		return nil, fmt.Errorf("failed to parse project resource ID: %w", err)
	}

	// Validate that this is a Cognitive Services project resource
	if resourceId.ResourceType.Namespace != "Microsoft.CognitiveServices" || len(resourceId.ResourceType.Types) != 2 ||
		resourceId.ResourceType.Types[0] != "accounts" || resourceId.ResourceType.Types[1] != "projects" {
		return nil, fmt.Errorf("the given resource ID is not a Microsoft Foundry project. Expected format: /subscriptions/[SUBSCRIPTION_ID]/resourceGroups/[RESOURCE_GROUP]/providers/Microsoft.CognitiveServices/accounts/[ACCOUNT_NAME]/projects/[PROJECT_NAME]")
	}

	// Extract the components
	return &FoundryProject{
		SubscriptionId:    resourceId.SubscriptionID,
		ResourceGroupName: resourceId.ResourceGroupName,
		AiAccountName:     resourceId.Parent.Name,
		AiProjectName:     resourceId.Name,
	}, nil
}

// parseProjectEndpoint extracts account name and project name from an endpoint URL
// Example: https://account-name.services.ai.azure.com/api/projects/project-name
func parseProjectEndpoint(endpoint string) (accountName string, projectName string, err error) {
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse endpoint URL: %w", err)
	}

	// Extract account name from hostname (e.g., "account-name.services.ai.azure.com")
	hostname := parsedURL.Hostname()
	hostParts := strings.Split(hostname, ".")
	if len(hostParts) < 1 || hostParts[0] == "" {
		return "", "", fmt.Errorf("invalid endpoint URL: cannot extract account name from hostname")
	}
	accountName = hostParts[0]

	// Extract project name from path (e.g., "/api/projects/project-name")
	pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
	// Expected path: api/projects/{project-name}
	if len(pathParts) >= 3 && pathParts[0] == "api" && pathParts[1] == "projects" {
		projectName = pathParts[2]
	} else {
		return "", "", fmt.Errorf("invalid endpoint URL: cannot extract project name from path. Expected format: /api/projects/{project-name}")
	}

	return accountName, projectName, nil
}

// findProjectByEndpoint searches for a Foundry project matching the endpoint URL
func findProjectByEndpoint(
	ctx context.Context,
	subscriptionId string,
	accountName string,
	projectName string,
	credential azcore.TokenCredential,
) (*FoundryProject, error) {
	// Create Cognitive Services Accounts client to search for the account
	accountsClient, err := armcognitiveservices.NewAccountsClient(subscriptionId, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cognitive Services Accounts client: %w", err)
	}

	// List all accounts in the subscription and find the matching one
	pager := accountsClient.NewListPager(nil)
	var foundAccount *armcognitiveservices.Account
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list Cognitive Services accounts: %w", err)
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
		return nil, fmt.Errorf("could not find Cognitive Services account '%s' in subscription '%s'", accountName, subscriptionId)
	}

	// Parse the account's resource ID to get resource group
	accountResourceId, err := arm.ParseResourceID(*foundAccount.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse account resource ID: %w", err)
	}

	// Create Projects client to verify the project exists
	projectsClient, err := armcognitiveservices.NewProjectsClient(subscriptionId, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cognitive Services Projects client: %w", err)
	}

	// Get the project to verify it exists and get its details
	projectResp, err := projectsClient.Get(ctx, accountResourceId.ResourceGroupName, accountName, projectName, nil)
	if err != nil {
		return nil, fmt.Errorf("could not find project '%s' under account '%s': %w", projectName, accountName, err)
	}

	return &FoundryProject{
		SubscriptionId:    subscriptionId,
		ResourceGroupName: accountResourceId.ResourceGroupName,
		AiAccountName:     accountName,
		AiProjectName:     projectName,
		Location:          *projectResp.Location,
	}, nil
}

func getExistingEnvironment(ctx context.Context, name *string, azdClient *azdext.AzdClient) (*azdext.Environment, error) {
	var env *azdext.Environment
	if name == nil || *name == "" {
		envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return nil, fmt.Errorf("failed to get current environment: %w", err)
		}
		env = envResponse.Environment
	} else {
		envResponse, err := azdClient.Environment().Get(ctx, &azdext.GetEnvironmentRequest{
			Name: *name,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get environment '%s': %w", *name, err)
		}
		env = envResponse.Environment
	}

	return env, nil
}

func ensureEnvironment(ctx context.Context, flags *initFlags, azdClient *azdext.AzdClient) (*azdext.Environment, error) {
	var foundryProject *FoundryProject

	// Handle project endpoint URL - extract account/project names and find the ARM resource
	if flags.projectEndpoint != "" {
		accountName, projectName, err := parseProjectEndpoint(flags.projectEndpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to parse project endpoint: %w", err)
		}

		fmt.Printf("Parsed endpoint - Account: %s, Project: %s\n", accountName, projectName)

		// Get subscription ID - either from flag or prompt
		subscriptionId := flags.subscriptionId
		var tenantId string

		if subscriptionId == "" {
			fmt.Println("Subscription ID is required to find the project. Let's select one.")
			subscriptionResponse, err := azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
			if err != nil {
				return nil, fmt.Errorf("failed to prompt for subscription: %w", err)
			}
			subscriptionId = subscriptionResponse.Subscription.Id
			tenantId = subscriptionResponse.Subscription.TenantId
		} else {
			// Get tenant ID from subscription
			tenantResponse, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
				SubscriptionId: subscriptionId,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to get tenant ID: %w", err)
			}
			tenantId = tenantResponse.TenantId
		}

		// Create credential
		credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   tenantId,
			AdditionallyAllowedTenants: []string{"*"},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure credential: %w", err)
		}

		// Find the project by searching the subscription
		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text: fmt.Sprintf("Searching for project in subscription %s...", subscriptionId),
		})
		if err := spinner.Start(ctx); err != nil {
			fmt.Printf("failed to start spinner: %v\n", err)
		}

		foundryProject, err = findProjectByEndpoint(ctx, subscriptionId, accountName, projectName, credential)
		_ = spinner.Stop(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to find project from endpoint: %w", err)
		}
		foundryProject.TenantId = tenantId

		fmt.Printf("Found project - Resource Group: %s, Account: %s, Project: %s\n",
			foundryProject.ResourceGroupName, foundryProject.AiAccountName, foundryProject.AiProjectName)

	} else if flags.projectResourceId != "" {
		// Parse the Microsoft Foundry project resource ID if provided & Fetch Tenant Id and Location using parsed information
		var err error
		foundryProject, err = extractProjectDetails(flags.projectResourceId)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Microsoft Foundry project ID: %w", err)
		}

		// Get the tenant ID
		tenantResponse, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
			SubscriptionId: foundryProject.SubscriptionId,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get tenant ID: %w", err)
		}
		foundryProject.TenantId = tenantResponse.TenantId
		credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   foundryProject.TenantId,
			AdditionallyAllowedTenants: []string{"*"},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure credential: %w", err)
		}

		// Create Cognitive Services Projects client
		projectsClient, err := armcognitiveservices.NewProjectsClient(foundryProject.SubscriptionId, credential, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create Cognitive Services Projects client: %w", err)
		}

		// Get the Microsoft Foundry project
		projectResp, err := projectsClient.Get(ctx, foundryProject.ResourceGroupName, foundryProject.AiAccountName, foundryProject.AiProjectName, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get Microsoft Foundry project: %w", err)
		}

		foundryProject.Location = *projectResp.Location
	}

	// Get specified or current environment if it exists
	existingEnv, err := getExistingEnvironment(ctx, &flags.env, azdClient)
	if err != nil {
		fmt.Printf("Warning: failed to get existing environment: %v\n", err)
	}
	if existingEnv == nil {
		// Dispatch `azd env new` to create a new environment with interactive flow
		fmt.Println("Lets create a new default azd environment for your project.")

		envArgs := []string{"env", "new"}
		if flags.env != "" {
			envArgs = append(envArgs, flags.env)
		}

		if foundryProject != nil {
			envArgs = append(envArgs, "--subscription", foundryProject.SubscriptionId)
			envArgs = append(envArgs, "--location", foundryProject.Location)
		}

		// Dispatch a workflow to create a new environment
		// Handles both interactive and no-prompt flows
		workflow := &azdext.Workflow{
			Name: "env new",
			Steps: []*azdext.WorkflowStep{
				{Command: &azdext.WorkflowCommand{Args: envArgs}},
			},
		}

		_, err := azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
			Workflow: workflow,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create new azd environment: %w", err)
		}

		// Re-fetch the environment after creation
		existingEnv, err = getExistingEnvironment(ctx, &flags.env, azdClient)
		if err != nil {
			return nil, fmt.Errorf("failed to get environment after creation: %w", err)
		}
	}

	// Set TenantId, SubscriptionId, ResourceGroupName, AiAccountName, and Location in the environment
	if foundryProject != nil {

		_, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: existingEnv.Name,
			Key:     "AZURE_TENANT_ID",
			Value:   foundryProject.TenantId,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to set AZURE_TENANT_ID in azd environment: %w", err)
		}

		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: existingEnv.Name,
			Key:     "AZURE_SUBSCRIPTION_ID",
			Value:   foundryProject.SubscriptionId,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to set AZURE_SUBSCRIPTION_ID in azd environment: %w", err)
		}

		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: existingEnv.Name,
			Key:     "AZURE_RESOURCE_GROUP_NAME",
			Value:   foundryProject.ResourceGroupName,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to set AZURE_RESOURCE_GROUP_NAME in azd environment: %w", err)
		}

		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: existingEnv.Name,
			Key:     "AZURE_ACCOUNT_NAME",
			Value:   foundryProject.AiAccountName,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to set AZURE_ACCOUNT_NAME in azd environment: %w", err)
		}

		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: existingEnv.Name,
			Key:     "AZURE_PROJECT_NAME",
			Value:   foundryProject.AiProjectName,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to set AZURE_PROJECT_NAME in azd environment: %w", err)
		}

		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: existingEnv.Name,
			Key:     "AZURE_LOCATION",
			Value:   foundryProject.Location,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to set AZURE_LOCATION in environment: %w", err)
		}

	}

	return existingEnv, nil
}
func ensureProject(ctx context.Context, flags *initFlags, azdClient *azdext.AzdClient) (*azdext.ProjectConfig, error) {
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Println("Lets get your project initialized.")

		initArgs := []string{"init"}
		if flags.env != "" {
			initArgs = append(initArgs, "-e", flags.env)
		}

		// We don't have a project yet
		// Dispatch a workflow to init the project
		workflow := &azdext.Workflow{
			Name: "init",
			Steps: []*azdext.WorkflowStep{
				{Command: &azdext.WorkflowCommand{Args: initArgs}},
			},
		}

		_, err := azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
			Workflow: workflow,
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

func ensureAzureContext(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
) (*azdext.AzureContext, *azdext.ProjectConfig, *azdext.Environment, error) {
	project, err := ensureProject(ctx, flags, azdClient)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to ensure project: %w", err)
	}

	env, err := ensureEnvironment(ctx, flags, azdClient)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to ensure environment: %w", err)
	}

	envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: env.Name,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get environment values: %w", err)
	}

	envValueMap := make(map[string]string)
	for _, value := range envValues.KeyValues {
		envValueMap[value.Key] = value.Value
	}

	azureContext := &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			TenantId:       envValueMap["AZURE_TENANT_ID"],
			SubscriptionId: envValueMap["AZURE_SUBSCRIPTION_ID"],
			Location:       envValueMap["AZURE_LOCATION"],
			ResourceGroup:  envValueMap["AZURE_RESOURCE_GROUP_NAME"],
		},
		Resources: []string{},
	}

	if azureContext.Scope.SubscriptionId == "" {
		fmt.Print()
		fmt.Println("It looks like we first need to connect to your Azure subscription.")

		subscriptionResponse, err := azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to prompt for subscription: %w", err)
		}

		azureContext.Scope.SubscriptionId = subscriptionResponse.Subscription.Id
		azureContext.Scope.TenantId = subscriptionResponse.Subscription.TenantId

		// Set the subscription ID in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Name,
			Key:     "AZURE_TENANT_ID",
			Value:   azureContext.Scope.TenantId,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to set AZURE_TENANT_ID in environment: %w", err)
		}

		// Set the tenant ID in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Name,
			Key:     "AZURE_SUBSCRIPTION_ID",
			Value:   azureContext.Scope.SubscriptionId,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to set AZURE_SUBSCRIPTION_ID in environment: %w", err)
		}
	}
	if azureContext.Scope.ResourceGroup == "" {
		fmt.Print()

		resourceGroupResponse, err := azdClient.Prompt().
			PromptResourceGroup(ctx, &azdext.PromptResourceGroupRequest{
				AzureContext: azureContext,
				Options: &azdext.PromptResourceGroupOptions{
					SelectOptions: &azdext.PromptResourceSelectOptions{
						AllowNewResource: to.Ptr(false),
					},
				},
			})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to prompt for resource group: %w", err)
		}

		azureContext.Scope.ResourceGroup = resourceGroupResponse.ResourceGroup.Name

		// Set the subscription ID in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Name,
			Key:     "AZURE_RESOURCE_GROUP_NAME",
			Value:   azureContext.Scope.ResourceGroup,
		})

	}

	if envValueMap["AZURE_ACCOUNT_NAME"] == "" {

		foundryProjectResponse, err := azdClient.Prompt().PromptResourceGroupResource(ctx, &azdext.PromptResourceGroupResourceRequest{
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
			return nil, nil, nil, fmt.Errorf("failed to get Microsoft Foundry project: %w", err)
		}

		fpDetails, err := extractProjectDetails(foundryProjectResponse.Resource.Id)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to parse Microsoft Foundry project ID: %w", err)
		}

		credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   azureContext.Scope.TenantId,
			AdditionallyAllowedTenants: []string{"*"},
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to create Azure credential: %w", err)
		}

		// Create Cognitive Services Projects client
		projectsClient, err := armcognitiveservices.NewProjectsClient(azureContext.Scope.SubscriptionId, credential, nil)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to create Cognitive Services Projects client: %w", err)
		}

		// Get the Microsoft Foundry project
		projectResp, err := projectsClient.Get(ctx, azureContext.Scope.ResourceGroup, fpDetails.AiAccountName, fpDetails.AiProjectName, nil)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to get Microsoft Foundry project: %w", err)
		}

		// Set the subscription ID in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Name,
			Key:     "AZURE_ACCOUNT_NAME",
			Value:   fpDetails.AiAccountName,
		})

		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Name,
			Key:     "AZURE_PROJECT_NAME",
			Value:   fpDetails.AiProjectName,
		})

		location := *projectResp.Location

		// Set the location in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Name,
			Key:     "AZURE_LOCATION",
			Value:   location,
		})
	}

	return azureContext, project, env, nil
}

func (a *InitAction) Run(ctx context.Context) error {
	// Validate that either template or from-job is provided, but not both
	if a.flags.template != "" && a.flags.jobId != "" {
		return fmt.Errorf("cannot specify both --template and --from-job flags")
	}

	color.Green("Creating fine-tuning Job definition...")

	var cwd string
	var err error

	// Use src flag if provided, otherwise use current working directory
	if a.flags.src != "" {
		cwd = a.flags.src
	} else {
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current working directory: %w", err)
		}
	}

	if a.flags.template == "" && a.flags.jobId == "" {
		defaultBaseModel := "gpt-4o-mini"
		defaultMethod := "supervised"
		baseModelForFineTuningInput, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Enter base model name for fine tuning  (defaults to model name)",
				IgnoreHintKeys: true,
				DefaultValue:   defaultBaseModel,
			},
		})
		ftMethodInput, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Enter fine-tuning method (defaults to supervised)",
				IgnoreHintKeys: true,
				DefaultValue:   defaultMethod,
			},
		})
		if err != nil {
			return err
		}
		fmt.Printf("Base model : %s, Fine-tuning method: %s\n", baseModelForFineTuningInput.Value, ftMethodInput.Value)

		// Create YAML file with the fine-tuning job template
		yamlContent := fmt.Sprintf(`name: ft-cli-job
description: Template to demonstrate fine-tuning via CLI
model: %s
method:
  type: %s
`, baseModelForFineTuningInput.Value, ftMethodInput.Value)

		// Determine the output directory (use src flag or current directory)
		outputDir := a.flags.src
		if outputDir == "" {
			var err error
			outputDir, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current working directory: %w", err)
			}
		}

		yamlFilePath := filepath.Join(outputDir, "config", "job.yaml")
		if err := os.MkdirAll(filepath.Dir(yamlFilePath), 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}

		if err := os.WriteFile(yamlFilePath, []byte(yamlContent), 0644); err != nil {
			return fmt.Errorf("failed to write job.yaml file: %w", err)
		}

		fmt.Printf("Created fine-tuning job template at: %s\n", yamlFilePath)

		// Set the template flag to the newly created YAML file
		a.flags.template = yamlFilePath
	} else if a.flags.template != "" {

		if a.isGitHubUrl(a.flags.template) {
			// For container agents, download the entire parent directory
			fmt.Println("Downloading full directory for fine-tuning configuration from GitHub...")
			var console input.Console
			var urlInfo *GitHubUrlInfo
			// Create a simple console and command runner for GitHub CLI
			commandRunner := exec.NewCommandRunner(&exec.RunnerOptions{
				Stdout: os.Stdout,
				Stderr: os.Stderr,
			})

			console = input.NewConsole(
				false, // noPrompt
				true,  // isTerminal
				input.Writers{Output: os.Stdout},
				input.ConsoleHandles{
					Stderr: os.Stderr,
					Stdin:  os.Stdin,
					Stdout: os.Stdout,
				},
				nil, // formatter
				nil, // externalPromptCfg
			)

			ghCli := github.NewGitHubCli(console, commandRunner)
			if err := ghCli.EnsureInstalled(ctx); err != nil {
				return fmt.Errorf("ensuring gh is installed: %w", err)
			}

			// Create a new AZD client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// Call the ParseGitHubUrl RPC method
			parseResponse, err := azdClient.Project().ParseGitHubUrl(ctx, &azdext.ParseGitHubUrlRequest{
				Url: a.flags.template,
			})
			if err != nil {
				return fmt.Errorf("parsing GitHub URL via azd extension: %w", err)
			}

			// Map the response to GitHubUrlInfo
			urlInfo = &GitHubUrlInfo{
				RepoSlug: parseResponse.RepoSlug,
				Branch:   parseResponse.Branch,
				FilePath: parseResponse.FilePath,
				Hostname: parseResponse.Hostname,
			}

			if urlInfo.Branch != "" {
				fmt.Printf("Downloaded manifest from branch: %s\n", urlInfo.Branch)
			}
			err = downloadParentDirectory(ctx, urlInfo, cwd, ghCli, console)
			if err != nil {
				return fmt.Errorf("downloading parent directory: %w", err)
			}
		} else {
			if err := copyDirectory(a.flags.template, cwd); err != nil {
				return fmt.Errorf("failed to copy directory: %w", err)
			}
		}
	} else if a.flags.jobId != "" {
		fmt.Printf("Cloning fine-tuning job configuration from job ID: %s\n", a.flags.jobId)
		fineTuneSvc, err := services.NewFineTuningService(ctx, a.azdClient, nil)
		if err != nil {
			return fmt.Errorf("failed to create fine-tuning service: %w", err)
		}

		// Fetch job details
		fmt.Printf("Fetching fine-tuning job %s...\n", a.flags.jobId)
		job, err := fineTuneSvc.GetFineTuningJobDetails(ctx, a.flags.jobId)
		if err != nil {
			return fmt.Errorf("failed to fetch fine-tuning job details: %w", err)
		}

		// Create YAML file with job configuration
		yamlContent := fmt.Sprintf(`name: %s
description: Cloned configuration from job %s
model: %s
seed: %d
method:
  type: %s
`, a.flags.jobId, a.flags.jobId, job.Model, job.Seed, job.Method)

		// Add hyperparameters nested under method type if present
		if job.Hyperparameters != nil {
			yamlContent += fmt.Sprintf(`  %s:
    hyperparameters:
      epochs: %d
      batch_size: %d
      learning_rate_multiplier: %f
`, job.Method, job.Hyperparameters.NEpochs, job.Hyperparameters.BatchSize, job.Hyperparameters.LearningRateMultiplier)

			// Add beta parameter only for DPO method
			if strings.ToLower(job.Method) == "dpo" {
				yamlContent += fmt.Sprintf("      beta: %v\n", job.Hyperparameters.Beta)
			}

			// Add reinforcement-specific hyperparameters
			if strings.ToLower(job.Method) == "reinforcement" {
				yamlContent += fmt.Sprintf("      compute_multiplier: %f\n", job.Hyperparameters.ComputeMultiplier)
				yamlContent += fmt.Sprintf("      eval_interval: %d\n", job.Hyperparameters.EvalInterval)
				yamlContent += fmt.Sprintf("      eval_samples: %d\n", job.Hyperparameters.EvalSamples)
				yamlContent += fmt.Sprintf("      reasoning_effort: %s\n", job.Hyperparameters.ReasoningEffort)
			}
		}

		// Add training and validation files
		yamlContent += fmt.Sprintf("training_file: %s\n", job.TrainingFile)
		if job.ValidationFile != "" {
			yamlContent += fmt.Sprintf("validation_file: %s\n", job.ValidationFile)
		}

		// Add extra_body with trainingType if present
		if trainingType, ok := job.ExtraFields["trainingType"]; ok {
			yamlContent += fmt.Sprintf("extra_body:\n  trainingType: %v\n", trainingType)
		}

		// Determine the output directory (use src flag or current directory)
		outputDir := a.flags.src
		if outputDir == "" {
			var err error
			outputDir, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current working directory: %w", err)
			}
		}

		yamlFilePath := filepath.Join(outputDir, "config", "job.yaml")
		if err := os.MkdirAll(filepath.Dir(yamlFilePath), 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}

		if err := os.WriteFile(yamlFilePath, []byte(yamlContent), 0644); err != nil {
			return fmt.Errorf("failed to write job.yaml file: %w", err)
		}

		fmt.Printf("Created fine-tuning job configuration at: %s\n", yamlFilePath)

		// Set the template flag to the newly created YAML file
		a.flags.template = yamlFilePath
	}
	fmt.Println()
	color.Green("Initialized fine-tuning Project.")

	return nil
}

func (a *InitAction) isGitHubUrl(manifestPointer string) bool {
	// Check if it's a GitHub URL based on the patterns from downloadGithubManifest
	parsedURL, err := url.Parse(manifestPointer)
	if err != nil {
		return false
	}
	hostname := parsedURL.Hostname()

	// Check for GitHub URL patterns as defined in downloadGithubManifest
	return strings.HasPrefix(hostname, "raw.githubusercontent") ||
		strings.HasPrefix(hostname, "api.github") ||
		strings.Contains(hostname, "github")
}

func downloadParentDirectory(
	ctx context.Context, urlInfo *GitHubUrlInfo, targetDir string, ghCli *github.Cli, console input.Console) error {

	// Get parent directory by removing the filename from the file path
	pathParts := strings.Split(urlInfo.FilePath, "/")
	if len(pathParts) <= 1 {
		fmt.Println("The file agent.yaml is at repository root, no parent directory to download")
		return nil
	}

	parentDirPath := strings.Join(pathParts[:len(pathParts)-1], "/")
	fmt.Printf("Downloading parent directory '%s' from repository '%s', branch '%s'\n", parentDirPath, urlInfo.RepoSlug, urlInfo.Branch)

	// Download directory contents
	if err := downloadDirectoryContents(ctx, urlInfo.Hostname, urlInfo.RepoSlug, parentDirPath, urlInfo.Branch, targetDir, ghCli, console); err != nil {
		return fmt.Errorf("failed to download directory contents: %w", err)
	}

	fmt.Printf("Successfully downloaded parent directory to: %s\n", targetDir)
	return nil
}

func downloadDirectoryContents(
	ctx context.Context, hostname string, repoSlug string, dirPath string, branch string, localPath string, ghCli *github.Cli, console input.Console) error {

	// Get directory contents using GitHub API
	apiPath := fmt.Sprintf("/repos/%s/contents/%s", repoSlug, dirPath)
	if branch != "" {
		apiPath += fmt.Sprintf("?ref=%s", branch)
	}

	dirContentsJson, err := ghCli.ApiCall(ctx, hostname, apiPath, github.ApiCallOptions{})
	if err != nil {
		return fmt.Errorf("failed to get directory contents: %w", err)
	}

	// Parse the directory contents JSON
	var dirContents []map[string]interface{}
	if err := json.Unmarshal([]byte(dirContentsJson), &dirContents); err != nil {
		return fmt.Errorf("failed to parse directory contents JSON: %w", err)
	}

	// Download each file and subdirectory
	for _, item := range dirContents {
		name, ok := item["name"].(string)
		if !ok {
			continue
		}

		itemType, ok := item["type"].(string)
		if !ok {
			continue
		}

		itemPath := fmt.Sprintf("%s/%s", dirPath, name)
		itemLocalPath := filepath.Join(localPath, name)

		if itemType == "file" {
			// Download file
			fmt.Printf("Downloading file: %s\n", itemPath)
			fileApiPath := fmt.Sprintf("/repos/%s/contents/%s", repoSlug, itemPath)
			if branch != "" {
				fileApiPath += fmt.Sprintf("?ref=%s", branch)
			}

			fileContent, err := ghCli.ApiCall(ctx, hostname, fileApiPath, github.ApiCallOptions{
				Headers: []string{"Accept: application/vnd.github.v3.raw"},
			})
			if err != nil {
				return fmt.Errorf("failed to download file %s: %w", itemPath, err)
			}

			if err := os.WriteFile(itemLocalPath, []byte(fileContent), 0644); err != nil {
				return fmt.Errorf("failed to write file %s: %w", itemLocalPath, err)
			}
		} else if itemType == "dir" {
			// Recursively download subdirectory
			fmt.Printf("Downloading directory: %s\n", itemPath)
			if err := os.MkdirAll(itemLocalPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", itemLocalPath, err)
			}

			// Recursively download directory contents
			if err := downloadDirectoryContents(ctx, hostname, repoSlug, itemPath, branch, itemLocalPath, ghCli, console); err != nil {
				return fmt.Errorf("failed to download subdirectory %s: %w", itemPath, err)
			}
		}
	}

	return nil
}

// copyDirectory recursively copies all files and directories from src to dst
func copyDirectory(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Calculate the destination path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if d.IsDir() {
			// Create directory and continue processing its contents
			return os.MkdirAll(dstPath, 0755)
		} else {
			// Copy file
			return copyFile(path, dstPath)
		}
	})
}

// copyFile copies a single file from src to dst
func copyFile(src, dst string) error {
	// Create the destination directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	// Open source file
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Create destination file
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	// Copy file contents
	_, err = srcFile.WriteTo(dstFile)
	return err
}
