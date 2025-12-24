// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
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
	var tenantId string

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
		tenantId = tenantResponse.TenantId
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

		currentTenantId, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: existingEnv.Name,
			Key:     "AZURE_TENANT_ID",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get current AZURE_TENANT_ID from azd environment: %w", err)
		}
		if currentTenantId.Value == "" {
			_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
				EnvName: existingEnv.Name,
				Key:     "AZURE_TENANT_ID",
				Value:   tenantId,
			})
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
		if a.isGitHubUrl(a.flags.manifestPointer) {
			// For container agents, download the entire parent directory
			fmt.Println("Downloading full directory for fine-tuning configuration from GitHub...")
			var ghCli *github.Cli
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
			ghCli, err = github.NewGitHubCli(ctx, console, commandRunner)
			if err != nil {
				return fmt.Errorf("creating GitHub CLI: %w", err)
			}

			urlInfo, err = parseGitHubUrl(a.flags.manifestPointer)
			if err != nil {
				return err
			}

			apiPath := fmt.Sprintf("/repos/%s/contents/%s", urlInfo.RepoSlug, urlInfo.FilePath)
			if urlInfo.Branch != "" {
				fmt.Printf("Downloaded manifest from branch: %s\n", urlInfo.Branch)
				apiPath += fmt.Sprintf("?ref=%s", urlInfo.Branch)
			}
			err := downloadParentDirectory(ctx, urlInfo, cwd, ghCli, console)
			if err != nil {
				return fmt.Errorf("downloading parent directory: %w", err)
			}
		} else {
			if err := copyDirectory(a.flags.manifestPointer, cwd); err != nil {
				return fmt.Errorf("failed to copy directory: %w", err)
			}
		}
	}
	fmt.Println()
	color.Green("Initialized fine-tuning Project.")

	return nil
}

// parseGitHubUrl extracts repository information from various GitHub URL formats
// TODO: This will fail if the branch contains a slash. Update to handle that case if needed.
func parseGitHubUrl(manifestPointer string) (*GitHubUrlInfo, error) {
	parsedURL, err := url.Parse(manifestPointer)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	hostname := parsedURL.Hostname()
	var repoSlug, branch, filePath string

	if strings.HasPrefix(hostname, "raw.") {
		// https://raw.githubusercontent.com/<owner>/<repo>/refs/heads/<branch>/[...path]/<file>.yaml
		pathParts := strings.Split(parsedURL.Path, "/")
		if len(pathParts) < 7 {
			return nil, fmt.Errorf("invalid URL format using 'raw.'. Expected the form of " +
				"'https://raw.<hostname>/<owner>/<repo>/refs/heads/<branch>/[...path]/<fileName>.json'")
		}
		if pathParts[3] != "refs" || pathParts[4] != "heads" {
			return nil, fmt.Errorf("invalid raw GitHub URL format. Expected 'refs/heads' in the URL path")
		}
		repoSlug = fmt.Sprintf("%s/%s", pathParts[1], pathParts[2])
		branch = pathParts[5]
		filePath = strings.Join(pathParts[6:], "/")
	} else if strings.HasPrefix(hostname, "api.") {
		// https://api.github.com/repos/<owner>/<repo>/contents/[...path]/<file>.yaml
		pathParts := strings.Split(parsedURL.Path, "/")
		if len(pathParts) < 6 {
			return nil, fmt.Errorf("invalid URL format using 'api.'. Expected the form of " +
				"'https://api.<hostname>/repos/<owner>/<repo>/contents/[...path]/<fileName>.json[?ref=<branch>]'")
		}
		repoSlug = fmt.Sprintf("%s/%s", pathParts[2], pathParts[3])
		filePath = strings.Join(pathParts[5:], "/")
		// For API URLs, branch is specified in the query parameter ref
		branch = parsedURL.Query().Get("ref")
		if branch == "" {
			branch = "main" // default branch if not specified
		}
	} else if strings.HasPrefix(manifestPointer, "https://") {
		// https://github.com/<owner>/<repo>/blob/<branch>/[...path]/<file>.yaml
		pathParts := strings.Split(parsedURL.Path, "/")
		if len(pathParts) < 6 {
			return nil, fmt.Errorf("invalid URL format. Expected the form of " +
				"'https://<hostname>/<owner>/<repo>/blob/<branch>/[...path]/<fileName>.json'")
		}
		if pathParts[3] != "blob" {
			return nil, fmt.Errorf("invalid GitHub URL format. Expected 'blob' in the URL path")
		}
		repoSlug = fmt.Sprintf("%s/%s", pathParts[1], pathParts[2])
		branch = pathParts[4]
		filePath = strings.Join(pathParts[5:], "/")
	} else {
		return nil, fmt.Errorf(
			"invalid URL format. Expected formats are:\n" +
				"  - 'https://raw.<hostname>/<owner>/<repo>/refs/heads/<branch>/[...path]/<fileName>.json'\n" +
				"  - 'https://<hostname>/<owner>/<repo>/blob/<branch>/[...path]/<fileName>.json'\n" +
				"  - 'https://api.<hostname>/repos/<owner>/<repo>/contents/[...path]/<fileName>.json[?ref=<branch>]'",
		)
	}

	// Normalize hostname for API calls
	if hostname == "raw.githubusercontent.com" {
		hostname = "github.com"
	}

	return &GitHubUrlInfo{
		RepoSlug: repoSlug,
		Branch:   branch,
		FilePath: filePath,
		Hostname: hostname,
	}, nil
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
