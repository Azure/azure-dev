// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type initFlags struct {
	rootFlagsDefinition
	projectResourceId string
	manifestPointer   string
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
		Use:   "init [-m <manifest pointer>] [-p <foundry project arm id>]",
		Short: fmt.Sprintf("Initialize a new AI Fine-tuning project. %s", color.YellowString("(Preview)")),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			azureContext, projectConfig, environment, err := ensureAzureContext(ctx, flags, azdClient)
			if err != nil {
				return fmt.Errorf("failed to ground into a project context: %w", err)
			}

			// getComposedResourcesResponse, err := azdClient.Compose().ListResources(ctx, &azdext.EmptyRequest{})
			// if err != nil {
			// 	return fmt.Errorf("failed to get composed resources: %w", err)
			// }

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

	cmd.Flags().StringVarP(&flags.projectResourceId, "project-id", "p", "",
		"Existing Microsoft Foundry Project Id to initialize your azd environment with")

	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "",
		"Path or URI to an fine-tuning configuration to add to your azd project")

	cmd.Flags().StringVarP(&flags.env, "environment", "e", "", "The name of the azd environment to use.")

	return cmd
}

type FoundryProject struct {
	SubscriptionId    string `json:"subscriptionId"`
	ResourceGroupName string `json:"resourceGroupName"`
	AiAccountName     string `json:"aiAccountName"`
	AiProjectName     string `json:"aiProjectName"`
}

func extractProjectDetails(projectResourceId string) (*FoundryProject, error) {
	/// Define the regex pattern for the project resource ID
	pattern := `^/subscriptions/([^/]+)/resourceGroups/([^/]+)/providers/Microsoft\.CognitiveServices/accounts/([^/]+)/projects/([^/]+)$`

	regex, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to compile regex pattern: %w", err)
	}

	matches := regex.FindStringSubmatch(projectResourceId)
	if matches == nil || len(matches) != 5 {
		return nil, fmt.Errorf("the given Microsoft Foundry project ID does not match expected format: /subscriptions/[SUBSCRIPTION_ID]/resourceGroups/[RESOURCE_GROUP]/providers/Microsoft.CognitiveServices/accounts/[ACCOUNT_NAME]/projects/[PROJECT_NAME]")
	}

	// Extract the components
	return &FoundryProject{
		SubscriptionId:    matches[1],
		ResourceGroupName: matches[2],
		AiAccountName:     matches[3],
		AiProjectName:     matches[4],
	}, nil
}

func getExistingEnvironment(ctx context.Context, flags *initFlags, azdClient *azdext.AzdClient) *azdext.Environment {
	var env *azdext.Environment
	if flags.env == "" {
		if envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
			env = envResponse.Environment
		}
	} else {
		if envResponse, err := azdClient.Environment().Get(ctx, &azdext.GetEnvironmentRequest{
			Name: flags.env,
		}); err == nil {
			env = envResponse.Environment
		}
	}

	return env
}

func ensureEnvironment(ctx context.Context, flags *initFlags, azdClient *azdext.AzdClient) (*azdext.Environment, error) {
	var foundryProject *FoundryProject
	var foundryProjectLocation string

	if flags.projectResourceId != "" {
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

		credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   tenantResponse.TenantId,
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

		foundryProjectLocation = *projectResp.Location
	}

	// Get specified or current environment if it exists
	existingEnv := getExistingEnvironment(ctx, flags, azdClient)
	if existingEnv == nil {
		// Dispatch `azd env new` to create a new environment with interactive flow
		fmt.Println("Lets create a new default azd environment for your project.")

		envArgs := []string{"env", "new"}
		if flags.env != "" {
			envArgs = append(envArgs, flags.env)
		}

		if flags.projectResourceId != "" {
			envArgs = append(envArgs, "--subscription", foundryProject.SubscriptionId)
			envArgs = append(envArgs, "--location", foundryProjectLocation)
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
		existingEnv = getExistingEnvironment(ctx, flags, azdClient)
		if existingEnv == nil {
			return nil, fmt.Errorf("azd environment not found, please create an environment (azd env new) and try again")
		}
	}
	if flags.projectResourceId != "" {
		currentResouceGroupName, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: existingEnv.Name,
			Key:     "AZURE_RESOURCE_GROUP_NAME",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get current AZURE_RESOURCE_GROUP_NAME from azd environment: %w", err)
		}

		if currentResouceGroupName.Value != foundryProject.ResourceGroupName {
			// Set the subscription ID in the environment
			_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
				EnvName: existingEnv.Name,
				Key:     "AZURE_RESOURCE_GROUP_NAME",
				Value:   foundryProject.ResourceGroupName,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to set AZURE_ACCOUNT_NAME in azd environment: %w", err)
			}
		}

		currentAccount, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: existingEnv.Name,
			Key:     "AZURE_ACCOUNT_NAME",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get current AZURE_ACCOUNT_NAME from azd environment: %w", err)
		}

		if currentAccount.Value != foundryProject.AiAccountName {
			// Set the subscription ID in the environment
			_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
				EnvName: existingEnv.Name,
				Key:     "AZURE_ACCOUNT_NAME",
				Value:   foundryProject.AiAccountName,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to set AZURE_ACCOUNT_NAME in azd environment: %w", err)
			}
		}

		currentSubscription, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: existingEnv.Name,
			Key:     "AZURE_SUBSCRIPTION_ID",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get current AZURE_SUBSCRIPTION_ID from azd environment: %w", err)
		}

		if currentSubscription.Value == "" {
			// Set the subscription ID in the environment
			_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
				EnvName: existingEnv.Name,
				Key:     "AZURE_SUBSCRIPTION_ID",
				Value:   foundryProject.SubscriptionId,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to set AZURE_SUBSCRIPTION_ID in azd environment: %w", err)
			}
		} else if currentSubscription.Value != foundryProject.SubscriptionId {
			return nil, fmt.Errorf("the value for subscription ID (%s) stored in your azd environment does not match the provided Microsoft Foundry project subscription ID (%s), please update or recreate your environment (azd env new)", currentSubscription.Value, foundryProject.SubscriptionId)
		}

		// Get current location from environment
		currentLocation, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: existingEnv.Name,
			Key:     "AZURE_LOCATION",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get AZURE_LOCATION from azd environment: %w", err)
		}

		if currentLocation.Value == "" {
			// Set the location in the environment
			_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
				EnvName: existingEnv.Name,
				Key:     "AZURE_LOCATION",
				Value:   foundryProjectLocation,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to set AZURE_LOCATION in environment: %w", err)
			}
		} else if currentLocation.Value != foundryProjectLocation {
			return nil, fmt.Errorf("the value for location (%s) stored in your azd environment does not match the provided Microsoft Foundry project location (%s), please update or recreate your environment (azd env new)", currentLocation.Value, foundryProjectLocation)
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

	if azureContext.Scope.Location == "" {
		fmt.Println()
		fmt.Println(
			"Next, we need to select a default Azure location that will be used as the target for your infrastructure.",
		)

		locationResponse, err := azdClient.Prompt().PromptLocation(ctx, &azdext.PromptLocationRequest{
			AzureContext: azureContext,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to prompt for location: %w", err)
		}

		azureContext.Scope.Location = locationResponse.Location.Name

		// Set the location in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Name,
			Key:     "AZURE_LOCATION",
			Value:   azureContext.Scope.Location,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to set AZURE_LOCATION in environment: %w", err)
		}
	}

	return azureContext, project, env, nil
}

func (a *InitAction) Run(ctx context.Context) error {
	color.Green("Initializing Fine tuning project...")
	time.Sleep(1 * time.Second)
	color.Green("Downloading template files...")
	time.Sleep(2 * time.Second)

	color.Green("Creating fine-tuning Job definition...")
	defaultModel := "gpt-4o-mini"
	defaultMethod := "supervised"
	modelDeploymentInput, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        "Enter base model name for fine tuning  (defaults to model name)",
			IgnoreHintKeys: true,
			DefaultValue:   defaultModel,
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
	fmt.Printf("Base model : %s, Fine-tuning method: %s\n", modelDeploymentInput.Value, ftMethodInput.Value)
	if a.flags.manifestPointer != "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current working directory: %w", err)
		}
		if err := copyDirectory(a.flags.manifestPointer, cwd); err != nil {
			return fmt.Errorf("failed to copy directory: %w", err)
		}
	}
	fmt.Println()
	color.Green("Initialized fine-tuning Project.")

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
