// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"azure.ai.customtraining/internal/utils"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type initFlags struct {
	rootFlagsDefinition
	subscriptionId  string
	projectEndpoint string
	env             string
}

type FoundryProject struct {
	TenantId          string
	SubscriptionId    string
	Location          string
	ResourceGroupName string
	AiAccountName     string
	AiProjectName     string
}

func newInitCommand(rootFlags rootFlagsDefinition) *cobra.Command {
	flags := &initFlags{
		rootFlagsDefinition: rootFlags,
	}

	cmd := &cobra.Command{
		Use:   "init",
		Short: fmt.Sprintf("Initialize project configuration for custom training. %s", color.YellowString("(Preview)")),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			azureContext, err := ensureProject(ctx, flags, azdClient)
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

			if err := ensureAzdProject(ctx, flags, azdClient); err != nil {
				return err
			}

			env, err := ensureEnvironment(ctx, flags, azdClient)
			if err != nil {
				return err
			}

			// If endpoint provided, resolve the project and store env vars
			if flags.projectEndpoint != "" {
				accountName, projectName, err := parseProjectEndpoint(flags.projectEndpoint)
				if err != nil {
					return fmt.Errorf("failed to parse project endpoint: %w", err)
				}

				spinner := ux.NewSpinner(&ux.SpinnerOptions{
					Text: fmt.Sprintf("Searching for project '%s' under account '%s'...", projectName, accountName),
				})
				if startErr := spinner.Start(ctx); startErr != nil {
					fmt.Printf("failed to start spinner: %v\n", startErr)
				}

				project, err := findProjectByEndpoint(ctx, flags.subscriptionId, accountName, projectName, credential)
				_ = spinner.Stop(ctx)
				if err != nil {
					return fmt.Errorf("failed to find project: %w", err)
				}

				if err := setEnvValues(ctx, azdClient, env.Name, map[string]string{
					utils.EnvAzureTenantID:       azureContext.Scope.TenantId,
					utils.EnvAzureSubscriptionID: project.SubscriptionId,
					utils.EnvAzureResourceGroup:  project.ResourceGroupName,
					utils.EnvAzureLocation:       project.Location,
					utils.EnvAzureAccountName:    project.AiAccountName,
					utils.EnvAzureProjectName:    project.AiProjectName,
				}); err != nil {
					return err
				}

				fmt.Printf("\n✓ Environment configured for project '%s'\n", projectName)
				return nil
			}

			// Interactive mode: prompt for subscription and endpoint
			console := input.NewConsole(
				false, true,
				input.Writers{Output: os.Stdout},
				input.ConsoleHandles{Stderr: os.Stderr, Stdin: os.Stdin, Stdout: os.Stdout},
				nil, nil,
			)

			// Prompt for subscription
			if flags.subscriptionId == "" {
				subResponse, err := azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
				if err != nil {
					return fmt.Errorf("failed to prompt for subscription: %w", err)
				}
				flags.subscriptionId = subResponse.Subscription.Id
			}

			// Prompt for project endpoint
			endpoint, err := console.Prompt(ctx, input.ConsoleOptions{
				Message: "Enter the Azure AI Foundry project endpoint URL:",
			})
			if err != nil {
				return fmt.Errorf("failed to prompt for endpoint: %w", err)
			}
			flags.projectEndpoint = endpoint

			accountName, projectName, err := parseProjectEndpoint(flags.projectEndpoint)
			if err != nil {
				return fmt.Errorf("invalid endpoint URL: %w", err)
			}

			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: fmt.Sprintf("Searching for project '%s'...", projectName),
			})
			if startErr := spinner.Start(ctx); startErr != nil {
				fmt.Printf("failed to start spinner: %v\n", startErr)
			}

			project, err := findProjectByEndpoint(ctx, flags.subscriptionId, accountName, projectName, credential)
			_ = spinner.Stop(ctx)
			if err != nil {
				return fmt.Errorf("failed to find project: %w", err)
			}

			if err := setEnvValues(ctx, azdClient, env.Name, map[string]string{
				utils.EnvAzureTenantID:       azureContext.Scope.TenantId,
				utils.EnvAzureSubscriptionID: project.SubscriptionId,
				utils.EnvAzureResourceGroup:  project.ResourceGroupName,
				utils.EnvAzureLocation:       project.Location,
				utils.EnvAzureAccountName:    project.AiAccountName,
				utils.EnvAzureProjectName:    project.AiProjectName,
			}); err != nil {
				return err
			}

			fmt.Printf("\n✓ Environment configured for project '%s'\n", projectName)
			return nil
		},
	}

	cmd.Flags().StringVarP(&flags.subscriptionId, "subscription", "s", "", "Azure subscription ID")
	cmd.Flags().StringVarP(&flags.projectEndpoint, "project-endpoint", "e", "",
		"Azure AI Foundry project endpoint URL (e.g., https://account.services.ai.azure.com/api/projects/project-name)")
	cmd.Flags().StringVarP(&flags.env, "environment", "n", "", "The name of the azd environment to use.")

	return cmd
}

func findProjectByEndpoint(
	ctx context.Context,
	subscriptionId string,
	accountName string,
	projectName string,
	credential azcore.TokenCredential,
) (*FoundryProject, error) {
	accountsClient, err := armcognitiveservices.NewAccountsClient(subscriptionId, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cognitive Services Accounts client: %w", err)
	}

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

	accountResourceId, err := arm.ParseResourceID(*foundAccount.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse account resource ID: %w", err)
	}

	projectsClient, err := armcognitiveservices.NewProjectsClient(subscriptionId, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cognitive Services Projects client: %w", err)
	}

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

func ensureAzdProject(ctx context.Context, flags *initFlags, azdClient *azdext.AzdClient) error {
	// Check if azd project already exists
	if _, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{}); err == nil {
		return nil
	}

	fmt.Println("Lets get your project initialized.")

	initArgs := []string{"init", "--minimal"}
	if flags.NoPrompt {
		initArgs = append(initArgs, "--no-prompt")
	}

	workflow := &azdext.Workflow{
		Name: "init",
		Steps: []*azdext.WorkflowStep{
			{Command: &azdext.WorkflowCommand{Args: initArgs}},
		},
	}

	if _, err := azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
		Workflow: workflow,
	}); err != nil {
		return fmt.Errorf("failed to initialize project: %w", err)
	}

	return nil
}

func ensureProject(ctx context.Context, flags *initFlags, azdClient *azdext.AzdClient) (*azdext.AzureContext, error) {
	// If subscription ID is provided, build context from it
	if flags.subscriptionId != "" {
		tenantResponse, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
			SubscriptionId: flags.subscriptionId,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to lookup tenant: %w", err)
		}

		return &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				TenantId:       tenantResponse.TenantId,
				SubscriptionId: flags.subscriptionId,
			},
		}, nil
	}

	// Interactive: prompt for subscription
	subResponse, err := azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for subscription: %w", err)
	}

	flags.subscriptionId = subResponse.Subscription.Id

	return &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			TenantId:       subResponse.Subscription.TenantId,
			SubscriptionId: subResponse.Subscription.Id,
		},
	}, nil
}

func ensureEnvironment(ctx context.Context, flags *initFlags, azdClient *azdext.AzdClient) (*azdext.Environment, error) {
	// Check for existing environment
	existingEnv := getExistingEnvironment(ctx, flags, azdClient)
	if existingEnv != nil {
		return existingEnv, nil
	}

	// Dispatch `azd env new` to create a new environment with interactive flow
	fmt.Println("Let's create a new default azd environment for your project.")

	envArgs := []string{"env", "new"}
	if flags.env != "" {
		envArgs = append(envArgs, flags.env)
	}

	if flags.NoPrompt {
		envArgs = append(envArgs, "--no-prompt")
	}

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
	existingEnv = getExistingEnvironment(ctx, flags, azdClient)
	if existingEnv == nil {
		return nil, fmt.Errorf("azd environment not found, please create an environment (azd env new) and try again")
	}

	return existingEnv, nil
}

func getExistingEnvironment(ctx context.Context, flags *initFlags, azdClient *azdext.AzdClient) *azdext.Environment {
	if flags.env == "" {
		if envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
			return envResponse.Environment
		}
	} else {
		if envResponse, err := azdClient.Environment().Get(ctx, &azdext.GetEnvironmentRequest{
			Name: flags.env,
		}); err == nil {
			return envResponse.Environment
		}
	}
	return nil
}

func setEnvValues(ctx context.Context, azdClient *azdext.AzdClient, envName string, values map[string]string) error {
	for key, value := range values {
		if _, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName,
			Key:     key,
			Value:   value,
		}); err != nil {
			return fmt.Errorf("failed to set environment variable %s: %w", key, err)
		}
	}
	return nil
}

// buildProjectEndpoint constructs a Foundry project endpoint from account name and project name.
func buildProjectEndpoint(accountName, projectName string) string {
	return fmt.Sprintf("https://%s.services.ai.azure.com/api/projects/%s",
		url.PathEscape(accountName), url.PathEscape(projectName))
}
