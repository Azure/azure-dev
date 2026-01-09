// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/registry_api"
	"azureaiagent/internal/pkg/azure"
	"azureaiagent/internal/pkg/azure/ai"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

type initFlags struct {
	rootFlagsDefinition
	projectResourceId string
	manifestPointer   string
	src               string
	host              string
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
	console             input.Console
	credential          azcore.TokenCredential
	modelCatalog        map[string]*ai.AiModel
	modelCatalogService *ai.ModelCatalogService
	projectConfig       *azdext.ProjectConfig
	environment         *azdext.Environment
	flags               *initFlags
	deploymentDetails   []project.Deployment
}

// GitHubUrlInfo holds parsed information from a GitHub URL
type GitHubUrlInfo struct {
	RepoSlug string
	Branch   string
	FilePath string
	Hostname string
}

const AiAgentHost = "azure.ai.agent"
const ContainerAppHost = "containerapp"

func newInitCommand(rootFlags rootFlagsDefinition) *cobra.Command {
	flags := &initFlags{
		rootFlagsDefinition: rootFlags,
	}

	cmd := &cobra.Command{
		Use:   "init [-m <manifest pointer>] [--src <source directory>]",
		Short: fmt.Sprintf("Initialize a new AI agent project. %s", color.YellowString("(Preview)")),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			setupDebugLogging(cmd.Flags())

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
				azdClient: azdClient,
				// azureClient:         azure.NewAzureClient(credential),
				azureContext: azureContext,
				// composedResources:   getComposedResourcesResponse.Resources,
				console:             console,
				credential:          credential,
				modelCatalogService: ai.NewModelCatalogService(credential),
				projectConfig:       projectConfig,
				environment:         environment,
				flags:               flags,
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
		"Path or URI to an agent manifest to add to your azd project")

	cmd.Flags().StringVarP(&flags.src, "src", "s", "",
		"[Optional] Directory to download the agent definition to (defaults to 'src/<agent-id>')")

	cmd.Flags().StringVarP(&flags.host, "host", "", "",
		"[Optional] For container based agents, can override the default host to target a container app instead. Accepted values: 'containerapp'")

	cmd.Flags().StringVarP(&flags.env, "environment", "e", "", "The name of the azd environment to use.")

	return cmd
}

func (a *InitAction) Run(ctx context.Context) error {
	color.Green("Initializing AI agent project...")
	fmt.Println()

	// If --project-id is given
	if a.flags.projectResourceId != "" {
		// projectResourceId is a string of the format
		// /subscriptions/[AZURE_SUBSCRIPTION]/resourceGroups/[AZURE_RESOURCE_GROUP]/providers/Microsoft.CognitiveServices/accounts/[AI_ACCOUNT_NAME]/projects/[AI_PROJECT_NAME]
		// extract each of those fields from the string, issue an error if it doesn't match the format
		fmt.Println("Setting up your azd environment to use the provided Microsoft Foundry project resource ID...")
		if err := a.parseAndSetProjectResourceId(ctx); err != nil {
			return fmt.Errorf("failed to parse project resource ID: %w", err)
		}

		color.Green("\nYour azd environment has been initialized to use your existing Microsoft Foundry project.")
	}

	// If --manifest is given
	if a.flags.manifestPointer != "" {
		// Validate that the manifest pointer is either a valid URL or existing file path
		isValidURL := false
		isValidFile := false

		if a.flags.host != "" && a.flags.host != "containerapp" {
			return fmt.Errorf("unsupported host value: '%s'. Accepted values are: 'containerapp'", a.flags.host)
		}

		if _, err := url.ParseRequestURI(a.flags.manifestPointer); err == nil {
			isValidURL = true
		} else if _, fileErr := os.Stat(a.flags.manifestPointer); fileErr == nil {
			isValidFile = true
		}

		if !isValidURL && !isValidFile {
			return fmt.Errorf("agent manifest pointer '%s' is neither a valid URI nor an existing file path", a.flags.manifestPointer)
		}

		// Download/read agent.yaml file from the provided URI or file path and save it to project's "agents" directory
		agentManifest, targetDir, err := a.downloadAgentYaml(ctx, a.flags.manifestPointer, a.flags.src)
		if err != nil {
			return fmt.Errorf("downloading agent.yaml: %w", err)
		}

		// Add the agent to the azd project (azure.yaml) services
		if err := a.addToProject(ctx, targetDir, agentManifest, a.flags.host); err != nil {
			return fmt.Errorf("failed to add agent to azure.yaml: %w", err)
		}

		color.Green("\nAI agent definition added to your azd project successfully!")
	}

	// // Validate command flags
	// if err := a.validateFlags(flags); err != nil {
	// 	return err
	// }

	// // Prompt for any missing input values
	// if err := a.promptForMissingValues(ctx, a.azdClient, flags); err != nil {
	// 	return fmt.Errorf("collecting required information: %w", err)
	// }

	return nil
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
		projectsClient, err := armcognitiveservices.NewProjectsClient(foundryProject.SubscriptionId, credential, azure.NewArmClientOptions())
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
	} else if flags.projectResourceId != "" {
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

func (a *InitAction) validateFlags(flags *initFlags) error {
	if flags.manifestPointer != "" {
		// Check if it's a valid URL
		if _, err := url.ParseRequestURI(flags.manifestPointer); err != nil {
			// If not a valid URL, check if it's an existing local file path
			if _, fileErr := os.Stat(flags.manifestPointer); fileErr != nil {
				return fmt.Errorf("manifest pointer '%s' is neither a valid URI nor an existing file path", flags.manifestPointer)
			}
		}
	}

	return nil
}

func (a *InitAction) promptForMissingValues(ctx context.Context, azdClient *azdext.AzdClient, flags *initFlags) error {
	if flags.manifestPointer == "" {
		resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Enter the location of the agent manifest",
				IgnoreHintKeys: true,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for agent manifest pointer: %w", err)
		}

		flags.manifestPointer = resp.Value
	}

	return nil
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

func (a *InitAction) parseAndSetProjectResourceId(ctx context.Context) error {
	foundryProject, err := extractProjectDetails(a.flags.projectResourceId)
	if err != nil {
		return fmt.Errorf("extracting project details: %w", err)
	}

	if err := a.setEnvVar(ctx, "AZURE_AI_PROJECT_ID", a.flags.projectResourceId); err != nil {
		return err
	}

	// Set the extracted values as environment variables
	if err := a.setEnvVar(ctx, "AZURE_RESOURCE_GROUP", foundryProject.ResourceGroupName); err != nil {
		return err
	}

	if err := a.setEnvVar(ctx, "AZURE_AI_ACCOUNT_NAME", foundryProject.AiAccountName); err != nil {
		return err
	}

	if err := a.setEnvVar(ctx, "AZURE_AI_PROJECT_NAME", foundryProject.AiProjectName); err != nil {
		return err
	}

	// Set the Microsoft Foundry endpoint URL
	aiFoundryEndpoint := fmt.Sprintf("https://%s.services.ai.azure.com/api/projects/%s", foundryProject.AiAccountName, foundryProject.AiProjectName)
	if err := a.setEnvVar(ctx, "AZURE_AI_PROJECT_ENDPOINT", aiFoundryEndpoint); err != nil {
		return err
	}

	aoaiEndpoint := fmt.Sprintf("https://%s.openai.azure.com/", foundryProject.AiAccountName)
	if err := a.setEnvVar(ctx, "AZURE_OPENAI_ENDPOINT", aoaiEndpoint); err != nil {
		return err
	}

	// Create FoundryProjectsClient and get connections
	foundryClient := azure.NewFoundryProjectsClient(foundryProject.AiAccountName, foundryProject.AiProjectName, a.credential)
	connections, err := foundryClient.GetAllConnections(ctx)
	if err != nil {
		fmt.Printf("Could not get Microsoft Foundry project connections to initialize AZURE_CONTAINER_REGISTRY_ENDPOINT: %v. Please set this environment variable manually.\n", err)
	} else {
		// Filter connections by ContainerRegistry type
		var acrConnections []azure.Connection
		var appInsightsConnections []azure.Connection
		for _, conn := range connections {
			switch conn.Type {
			case azure.ConnectionTypeContainerRegistry:
				acrConnections = append(acrConnections, conn)
			case azure.ConnectionTypeAppInsights:
				connWithCreds, err := foundryClient.GetConnectionWithCredentials(ctx, conn.Name)
				if err != nil {
					fmt.Printf("Could not get full details for Application Insights connection '%s': %v\n", conn.Name, err)
					continue
				}
				if connWithCreds != nil {
					conn = *connWithCreds
				}

				appInsightsConnections = append(appInsightsConnections, conn)
			}
		}

		if len(acrConnections) == 0 {
			fmt.Println(output.WithWarningFormat(
				"Agent deployment prerequisites not satisfied. To deploy this agent, you will need to " +
					"provision an Azure Container Registry (ACR) and grant the required permissions. " +
					"You can either do this manually before deployment, or use an infrastructure template. " +
					"See aka.ms/azdaiagent/docs for details."))

			resp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
				Options: &azdext.PromptOptions{
					Message: "If you have an ACR that you want to use with this agent, enter the azurecr.io endpoint for the ACR. " +
						"If you plan to provision one through the `azd provision` or `azd up` flow, leave blank.",
					IgnoreHintKeys: true,
				},
			})
			if err != nil {
				return fmt.Errorf("prompting for ACR endpoint: %w", err)
			}

			if resp.Value != "" {
				if err := a.setEnvVar(ctx, "AZURE_CONTAINER_REGISTRY_ENDPOINT", resp.Value); err != nil {
					return err
				}
			}
		} else {
			var selectedConnection *azure.Connection

			if len(acrConnections) == 1 {
				selectedConnection = &acrConnections[0]

				fmt.Printf("Using container registry connection: %s (%s)\n", selectedConnection.Name, selectedConnection.Target)
			} else {
				// Multiple connections found, prompt user to select
				fmt.Printf("Found %d container registry connections:\n", len(acrConnections))

				choices := make([]*azdext.SelectChoice, len(acrConnections))
				for i, conn := range acrConnections {
					choices[i] = &azdext.SelectChoice{
						Label: conn.Name,
						Value: fmt.Sprintf("%d", i),
					}
				}

				defaultIndex := int32(0)
				selectResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
					Options: &azdext.SelectOptions{
						Message:       "Select a container registry connection to use for this agent",
						Choices:       choices,
						SelectedIndex: &defaultIndex,
					},
				})
				if err != nil {
					fmt.Printf("failed to prompt for connection selection: %v\n", err)
				} else {
					selectedConnection = &acrConnections[int(*selectResp.Value)]
				}
			}

			if err := a.setEnvVar(ctx, "AZURE_CONTAINER_REGISTRY_ENDPOINT", selectedConnection.Target); err != nil {
				return err
			}
		}

		// Handle App Insights connections
		if len(appInsightsConnections) == 0 {
			fmt.Println(output.WithWarningFormat(
				"No Application Insights connection found. To enable telemetry for this agent, you will need to " +
					"provision an Application Insights resource and grant the required permissions. " +
					"You can either do this manually before deployment, or use an infrastructure template. " +
					"See aka.ms/azdaiagent/docs for details."))

			resp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
				Options: &azdext.PromptOptions{
					Message: "If you have an Application Insights resource that you want to use with this agent, enter the connection string. " +
						"If you plan to provision one through the `azd provision` or `azd up` flow, leave blank.",
					IgnoreHintKeys: true,
				},
			})
			if err != nil {
				return fmt.Errorf("prompting for Application Insights connection string: %w", err)
			}

			if resp.Value != "" {
				if err := a.setEnvVar(ctx, "APPLICATIONINSIGHTS_CONNECTION_STRING", resp.Value); err != nil {
					return err
				}
			}
		} else {
			var selectedConnection *azure.Connection

			if len(appInsightsConnections) == 1 {
				selectedConnection = &appInsightsConnections[0]

				fmt.Printf("Using Application Insights connection: %s (%s)\n", selectedConnection.Name, selectedConnection.Target)
			} else {
				// Multiple connections found, prompt user to select
				fmt.Printf("Found %d Application Insights connections:\n", len(appInsightsConnections))

				choices := make([]*azdext.SelectChoice, len(appInsightsConnections))
				for i, conn := range appInsightsConnections {
					choices[i] = &azdext.SelectChoice{
						Label: conn.Name,
						Value: fmt.Sprintf("%d", i),
					}
				}

				defaultIndex := int32(0)
				selectResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
					Options: &azdext.SelectOptions{
						Message:       "Select an Application Insights connection to use for this agent",
						Choices:       choices,
						SelectedIndex: &defaultIndex,
					},
				})
				if err != nil {
					fmt.Printf("failed to prompt for connection selection: %v\n", err)
				} else {
					selectedConnection = &appInsightsConnections[int(*selectResp.Value)]
				}
			}

			if selectedConnection != nil && selectedConnection.Credentials.Key != "" {
				if err := a.setEnvVar(ctx, "APPLICATIONINSIGHTS_CONNECTION_STRING", selectedConnection.Credentials.Key); err != nil {
					return err
				}
			}
		}
	}

	fmt.Printf("Successfully parsed and set environment variables from Microsoft Foundry project ID\n")
	return nil
}

func (a *InitAction) isLocalFilePath(path string) bool {
	// Check if it starts with http:// or https://
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return false
	} else if _, err := os.Stat(path); err == nil {
		return true
	}

	return false
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

type RegistryManifest struct {
	registryName    string
	manifestName    string
	manifestVersion string // Defaults to "" if not specified in URL
}

func (a *InitAction) isRegistryUrl(manifestPointer string) (bool, *RegistryManifest) {
	// Check if it matches the format "azureml://registries/{registryName}/agentmanifests/{manifestName}[/versions/{manifestVersion}]"
	if !strings.HasPrefix(manifestPointer, "azureml://") {
		return false, nil
	}

	// Remove the "azureml://" prefix
	path := strings.TrimPrefix(manifestPointer, "azureml://")

	// Split by "/" to get all path components
	parts := strings.Split(path, "/")

	// Should have either 4 parts (without version) or 6 parts (with version)
	// Format 1: "registries", registryName, "agentmanifests", manifestName
	// Format 2: "registries", registryName, "agentmanifests", manifestName, "versions", manifestVersion
	if len(parts) != 4 && len(parts) != 6 {
		return false, nil
	}

	// Validate the expected path structure for the first 4 parts
	if parts[0] != "registries" || parts[2] != "agentmanifests" {
		return false, nil
	}

	// All basic parts should be non-empty
	registryName := strings.TrimSpace(parts[1])
	manifestName := strings.TrimSpace(parts[3])

	if registryName == "" || manifestName == "" {
		return false, nil
	}

	var manifestVersion string

	// If we have 6 parts, validate the version structure
	if len(parts) == 6 {
		if parts[4] != "versions" {
			return false, nil
		}
		manifestVersion = strings.TrimSpace(parts[5])
		if manifestVersion == "" {
			return false, nil
		}
	} else {
		// If no version specified, default to ""
		manifestVersion = ""
	}

	return true, &RegistryManifest{
		registryName:    registryName,
		manifestName:    manifestName,
		manifestVersion: manifestVersion,
	}
}

func (a *InitAction) downloadAgentYaml(
	ctx context.Context, manifestPointer string, targetDir string) (*agent_yaml.AgentManifest, string, error) {
	if manifestPointer == "" {
		return nil, "", fmt.Errorf("The path to an agent manifest need to be provided (manifestPointer cannot be empty).")
	}

	var content []byte
	var err error
	var isGitHubUrl bool
	var urlInfo *GitHubUrlInfo
	var ghCli *github.Cli
	var console input.Console

	// Check if manifestPointer is a local file path or a URI
	if a.isLocalFilePath(manifestPointer) {
		// Handle local file path
		fmt.Printf("Reading agent.yaml from local file: %s\n", manifestPointer)
		content, err = os.ReadFile(manifestPointer)
		if err != nil {
			return nil, "", fmt.Errorf("reading local file %s: %w", manifestPointer, err)
		}

		// Parse the YAML content into genericManifest
		var genericManifest map[string]interface{}
		if err := yaml.Unmarshal(content, &genericManifest); err != nil {
			return nil, "", fmt.Errorf("parsing YAML from manifest file: %w", err)
		}

		var name string
		var ok bool
		if name, ok = genericManifest["name"].(string); !ok {
			name = ""
		}

		// Check if the manifest file is under current directory + "src"
		currentDir, _ := os.Getwd()
		srcDir := filepath.Join(currentDir, "src", name)
		absManifestPath, _ := filepath.Abs(manifestPointer)

		// Check if manifest is under src directory
		if strings.HasPrefix(absManifestPath, srcDir) {
			confirmResponse, err := a.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
				Options: &azdext.ConfirmOptions{
					Message:      "This operation will overwrite the provided manifest file. Do you want to continue?",
					DefaultValue: to.Ptr(false),
				},
			})
			if err != nil {
				return nil, "", fmt.Errorf("prompting for confirmation: %w", err)
			}
			if !*confirmResponse.Value {
				return nil, "", fmt.Errorf("operation cancelled by user")
			}
		}
	} else if a.isGitHubUrl(manifestPointer) {
		// Handle GitHub URLs using downloadGithubManifest
		fmt.Printf("Downloading agent.yaml from GitHub: %s\n", manifestPointer)
		isGitHubUrl = true

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
			return nil, "", fmt.Errorf("creating GitHub CLI: %w", err)
		}

		urlInfo, err = a.parseGitHubUrl(ctx, manifestPointer)
		if err != nil {
			return nil, "", err
		}

		apiPath := fmt.Sprintf("/repos/%s/contents/%s", urlInfo.RepoSlug, urlInfo.FilePath)
		if urlInfo.Branch != "" {
			fmt.Printf("Downloaded manifest from branch: %s\n", urlInfo.Branch)
			apiPath += fmt.Sprintf("?ref=%s", urlInfo.Branch)
		}

		contentStr, err := downloadGithubManifest(ctx, urlInfo, apiPath, ghCli, console)
		if err != nil {
			return nil, "", fmt.Errorf("downloading from GitHub: %w", err)
		}

		content = []byte(contentStr)
	} else if isRegistry, registryManifest := a.isRegistryUrl(manifestPointer); isRegistry {
		// Handle registry URLs

		// Create Azure credential
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, "", fmt.Errorf("failed to create Azure credential: %w", err)
		}

		manifestClient := registry_api.NewRegistryAgentManifestClient(registryManifest.registryName, cred)

		var versionResult *registry_api.Manifest
		if registryManifest.manifestVersion == "" {
			// No version specified, get latest version from GetAllLatest
			fmt.Printf("No version provided for manifest '%s', retrieving latest version\n", registryManifest.manifestName)

			allManifests, err := manifestClient.GetAllLatest(ctx)
			if err != nil {
				return nil, "", fmt.Errorf("getting latest manifests: %w", err)
			}

			// Find the manifest with matching name
			for _, manifest := range allManifests {
				if manifest.Name == registryManifest.manifestName {
					versionResult = &manifest
					break
				}
			}

			if versionResult == nil {
				return nil, "", fmt.Errorf("manifest '%s' not found in registry '%s'", registryManifest.manifestName, registryManifest.registryName)
			}
		} else {
			// Specific version requested
			fmt.Printf("Downloading agent.yaml from registry: %s\n", manifestPointer)

			manifest, err := manifestClient.GetManifest(ctx, registryManifest.manifestName, registryManifest.manifestVersion)
			if err != nil {
				return nil, "", fmt.Errorf("getting materialized manifest: %w", err)
			}
			versionResult = manifest
		}

		// Process the manifest into a maml format
		processedManifest, err := registry_api.ProcessRegistryManifest(ctx, versionResult, a.azdClient)
		if err != nil {
			return nil, "", fmt.Errorf("processing manifest with parameters: %w", err)
		}

		fmt.Println("Retrieved and processed manifest from registry")

		// Convert to YAML bytes for the content variable
		manifestBytes, err := yaml.Marshal(processedManifest)
		if err != nil {
			return nil, "", fmt.Errorf("marshaling agent manifest to YAML: %w", err)
		}
		content = manifestBytes
	}

	// Parse and validate the YAML content against AgentManifest structure
	agentManifest, err := agent_yaml.LoadAndValidateAgentManifest(content)
	if err != nil {
		return nil, "", fmt.Errorf("AgentManifest %w", err)
	}

	fmt.Println("✓ YAML content successfully validated against AgentManifest format")

	agentManifest, err = registry_api.ProcessManifestParameters(ctx, agentManifest, a.azdClient, a.flags.NoPrompt)
	if err != nil {
		return nil, "", fmt.Errorf("failed to process manifest parameters: %w", err)
	}

	agentManifest, deploymentDetails, err := a.ProcessModels(ctx, agentManifest)
	if err != nil {
		return nil, "", fmt.Errorf("failed to process model resources: %w", err)
	}

	a.deploymentDetails = deploymentDetails

	_, isPromptAgent := agentManifest.Template.(agent_yaml.PromptAgent)
	if isPromptAgent {
		agentManifest, err = agent_yaml.ProcessPromptAgentToolsConnections(ctx, agentManifest, a.azdClient)
		if err != nil {
			return nil, "", fmt.Errorf("failed to process prompt agent tools connections: %w", err)
		}
	}

	agentId := agentManifest.Name

	// Use targetDir if provided or set to local file pointer, otherwise default to "src/{agentId}"
	if targetDir == "" {
		targetDir = filepath.Join("src", agentId)
	}

	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, "", fmt.Errorf("creating target directory %s: %w", targetDir, err)
	}

	if a.isLocalFilePath(manifestPointer) {
		// Check if the template is a ContainerAgent
		_, isHostedContainer := agentManifest.Template.(agent_yaml.ContainerAgent)

		if isHostedContainer {
			// For container agents, copy the entire parent directory
			fmt.Println("Copying full directory for container agent")
			manifestDir := filepath.Dir(manifestPointer)
			err := copyDirectory(manifestDir, targetDir)
			if err != nil {
				return nil, "", fmt.Errorf("copying parent directory: %w", err)
			}
		}
	} else if isGitHubUrl {
		// Check if the template is a ContainerAgent
		_, isHostedContainer := agentManifest.Template.(agent_yaml.ContainerAgent)

		if isHostedContainer {
			// For container agents, download the entire parent directory
			fmt.Println("Downloading full directory for container agent")
			err := downloadParentDirectory(ctx, urlInfo, targetDir, ghCli, console)
			if err != nil {
				return nil, "", fmt.Errorf("downloading parent directory: %w", err)
			}
		}
	}

	content, err = yaml.Marshal(agentManifest.Template)
	if err != nil {
		return nil, "", fmt.Errorf("marshaling agent manifest to YAML after parameter processing: %w", err)
	}

	annotation := "# yaml-language-server: $schema=https://raw.githubusercontent.com/microsoft/AgentSchema/refs/heads/main/schemas/v1.0/ContainerAgent.yaml"
	agentFileContents := bytes.NewBufferString(annotation + "\n\n")
	_, err = agentFileContents.Write(content)
	if err != nil {
		return nil, "", fmt.Errorf("preparing new project file contents: %w", err)
	}

	// Save the file to the target directory
	filePath := filepath.Join(targetDir, "agent.yaml")
	if err := os.WriteFile(filePath, agentFileContents.Bytes(), osutil.PermissionFile); err != nil {
		return nil, "", fmt.Errorf("saving file to %s: %w", filePath, err)
	}

	fmt.Printf("Processed agent.yaml at %s\n", filePath)

	return agentManifest, targetDir, nil
}

func (a *InitAction) addToProject(ctx context.Context, targetDir string, agentManifest *agent_yaml.AgentManifest, host string) error {
	// Convert the template to bytes
	templateBytes, err := json.Marshal(agentManifest.Template)
	if err != nil {
		return fmt.Errorf("failed to marshal agent template to JSON: %w", err)
	}

	// Convert the bytes to a dictionary
	var templateDict map[string]interface{}
	if err := json.Unmarshal(templateBytes, &templateDict); err != nil {
		return fmt.Errorf("failed to unmarshal agent template from JSON: %w", err)
	}

	// Convert the dictionary to bytes
	dictJsonBytes, err := json.Marshal(templateDict)
	if err != nil {
		return fmt.Errorf("failed to marshal templateDict to JSON: %w", err)
	}

	// Convert the bytes to an Agent Definition
	var agentDef agent_yaml.AgentDefinition
	if err := json.Unmarshal(dictJsonBytes, &agentDef); err != nil {
		return fmt.Errorf("failed to unmarshal JSON to AgentDefinition: %w", err)
	}

	var serviceHost string

	switch host {
	case "containerapp":
		serviceHost = ContainerAppHost
	default:
		serviceHost = AiAgentHost
	}

	var agentConfig = project.ServiceTargetAgentConfig{}

	resourceDetails := []project.Resource{}
	switch agentDef.Kind {
	case agent_yaml.AgentKindHosted:
		// Handle tool resources that require connection names
		if agentManifest.Resources != nil {
			for _, resource := range agentManifest.Resources {
				// Try to cast to ToolResource
				if toolResource, ok := resource.(agent_yaml.ToolResource); ok {
					// Check if this is a resource that requires a connection name
					if toolResource.Id == "bing_grounding" || toolResource.Id == "azure_ai_search" {
						// Prompt the user for a connection name
						resp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
							Options: &azdext.PromptOptions{
								Message:        fmt.Sprintf("Enter a connection name for adding the resource %s to your Microsoft Foundry project", toolResource.Id),
								IgnoreHintKeys: true,
								DefaultValue:   toolResource.Id,
							},
						})
						if err != nil {
							return fmt.Errorf("prompting for connection name for %s: %w", toolResource.Id, err)
						}

						// Add to resource details
						resourceDetails = append(resourceDetails, project.Resource{
							Resource:       toolResource.Id,
							ConnectionName: resp.Value,
						})
					}
				}
				// Skip the resource if the cast fails
			}
		}

		// Prompt user for container settings
		containerSettings, err := a.populateContainerSettings(ctx)
		if err != nil {
			return fmt.Errorf("failed to populate container settings: %w", err)
		}
		agentConfig.Container = containerSettings
	}

	agentConfig.Deployments = a.deploymentDetails
	agentConfig.Resources = resourceDetails

	var agentConfigStruct *structpb.Struct
	if agentConfigStruct, err = project.MarshalStruct(&agentConfig); err != nil {
		return fmt.Errorf("failed to marshal agent config: %w", err)
	}

	serviceConfig := &azdext.ServiceConfig{
		Name:         strings.ReplaceAll(agentDef.Name, " ", ""),
		RelativePath: targetDir,
		Host:         serviceHost,
		Language:     "docker",
		Config:       agentConfigStruct,
	}

	// For hosted (container-based) agents, set remoteBuild to true by default
	if agentDef.Kind == agent_yaml.AgentKindHosted {
		serviceConfig.Docker = &azdext.DockerProjectOptions{
			RemoteBuild: true,
		}
	}

	req := &azdext.AddServiceRequest{Service: serviceConfig}

	if _, err := a.azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding agent service to project: %w", err)
	}

	fmt.Printf("\nAdded your agent as a service entry named '%s' under the file azure.yaml.\n", agentDef.Name)
	fmt.Printf("To provision and deploy the whole solution, use %s.\n", color.HiBlueString("azd up"))
	fmt.Printf(
		"If you already have your project provisioned with hosted agents requirements, "+
			"you can directly use %s.\n",
		color.HiBlueString("azd deploy %s", agentDef.Name))
	return nil
}

func (a *InitAction) populateContainerSettings(ctx context.Context) (*project.ContainerSettings, error) {
	// Default values
	defaultMemory := project.DefaultMemory
	defaultCpu := project.DefaultCpu
	defaultMinReplicas := fmt.Sprintf("%d", project.DefaultMinReplicas)
	defaultMaxReplicas := fmt.Sprintf("%d", project.DefaultMaxReplicas)

	// Prompt for memory allocation
	memoryResp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "Enter desired container memory allocation (e.g., '1Gi', '512Mi')",
			DefaultValue: defaultMemory,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("prompting for memory allocation: %w", err)
	}

	// Prompt for CPU allocation
	cpuResp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "Enter desired container CPU allocation (e.g., '1', '500m')",
			DefaultValue: defaultCpu,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("prompting for CPU allocation: %w", err)
	}

	// Prompt for minimum replicas
	minReplicasResp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "Enter desired container minimum number of replicas",
			DefaultValue: defaultMinReplicas,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("prompting for minimum replicas: %w", err)
	}

	// Prompt for maximum replicas
	maxReplicasResp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "Enter desired container maximum number of replicas",
			DefaultValue: defaultMaxReplicas,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("prompting for maximum replicas: %w", err)
	}

	// Convert string values to appropriate types
	minReplicas, err := strconv.Atoi(minReplicasResp.Value)
	if err != nil {
		return nil, fmt.Errorf("invalid minimum replicas value: %w", err)
	}

	maxReplicas, err := strconv.Atoi(maxReplicasResp.Value)
	if err != nil {
		return nil, fmt.Errorf("invalid maximum replicas value: %w", err)
	}

	// Validate that max replicas >= min replicas
	if maxReplicas < minReplicas {
		return nil, fmt.Errorf("maximum replicas (%d) must be greater than or equal to minimum replicas (%d)", maxReplicas, minReplicas)
	}

	return &project.ContainerSettings{
		Resources: &project.ResourceSettings{
			Memory: memoryResp.Value,
			Cpu:    cpuResp.Value,
		},
		Scale: &project.ScaleSettings{
			MinReplicas: minReplicas,
			MaxReplicas: maxReplicas,
		},
	}, nil
}

func downloadGithubManifest(
	ctx context.Context, urlInfo *GitHubUrlInfo, apiPath string, ghCli *github.Cli, console input.Console) (string, error) {
	// manifestPointer validation:
	// - accepts only URLs with the following format:
	//  - https://raw.<hostname>/<owner>/<repo>/refs/heads/<branch>/<path>/<file>.json
	//    - This url comes from a user clicking the `raw` button on a file in a GitHub repository (web view).
	//  - https://<hostname>/<owner>/<repo>/blob/<branch>/<path>/<file>.json
	//    - This url comes from a user browsing GitHub repository and copy-pasting the url from the browser.
	//  - https://api.<hostname>/repos/<owner>/<repo>/contents/<path>/<file>.json
	//    - This url comes from users familiar with the GitHub API. Usually for programmatic registration of templates.

	authResult, err := ghCli.GetAuthStatus(ctx, urlInfo.Hostname)
	if err != nil {
		return "", fmt.Errorf("failed to get auth status: %w", err)
	}
	if !authResult.LoggedIn {
		// ensure no spinner is shown when logging in, as this is interactive operation
		console.StopSpinner(ctx, "", input.Step)
		err := ghCli.Login(ctx, urlInfo.Hostname)
		if err != nil {
			return "", fmt.Errorf("failed to login: %w", err)
		}
		console.ShowSpinner(ctx, "Validating template source", input.Step)
	}

	content, err := ghCli.ApiCall(ctx, urlInfo.Hostname, apiPath, github.ApiCallOptions{
		Headers: []string{"Accept: application/vnd.github.v3.raw"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get content: %w", err)
	}

	return content, nil
}

// parseGitHubUrl extracts repository information from various GitHub URL formats using extension framework
func (a *InitAction) parseGitHubUrl(ctx context.Context, manifestPointer string) (*GitHubUrlInfo, error) {
	urlInfo, err := a.azdClient.Project().ParseGitHubUrl(ctx, &azdext.ParseGitHubUrlRequest{
		Url: manifestPointer,
	})
	if err != nil {
		return nil, err
	}

	return &GitHubUrlInfo{
		RepoSlug: urlInfo.RepoSlug,
		Branch:   urlInfo.Branch,
		FilePath: urlInfo.FilePath,
		Hostname: urlInfo.Hostname,
	}, nil
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

// func (a *InitAction) validateResources(ctx context.Context, agentYaml map[string]interface{}) error {
// 	fmt.Println("Reading model name from agent.yaml...")

// 	// Extract the model name from agentYaml
// 	agentModelName, ok := agentYaml["model"].(string)
// 	if !ok || agentModelName == "" {
// 		return fmt.Errorf("extracting model name from agent YAML: model name missing or empty")
// 	}

// 	fmt.Println("Reading current azd project resources...")

// 	// Check if the ai.project resource already exists and has the required model
// 	existingResourceName, err := a.checkResourceExistsAndHasModel(agentModelName)
// 	if err != nil {
// 		return fmt.Errorf("checking if ai.project resource has model '%s': %w", agentModelName, err)
// 	}

// 	if existingResourceName == "" {
// 		return a.addResource(ctx, agentModelName)
// 	}

// 	fmt.Printf("Validated: ai.project resource '%s' has required model '%s'\n", existingResourceName, agentModelName)
// 	return nil
// }

// // checkResourceExistsAndHasModel checks if the given ai.project resource has the specified model
// func (a *InitAction) checkResourceExistsAndHasModel(modelName string) (string, error) {
// 	// Look for ai.project resource
// 	var aiProjectResource *azdext.ComposedResource
// 	for _, resource := range a.composedResources {
// 		if resource.Type == "ai.project" {
// 			aiProjectResource = resource
// 			break
// 		}
// 	}

// 	if aiProjectResource == nil {
// 		fmt.Println("No 'ai.project' resource found in current azd project.")
// 		return "", nil
// 	}

// 	fmt.Println("'ai.project' resource found in current azd project. Checking for required model...")

// 	// Parse the resource config to check for models
// 	if len(aiProjectResource.Config) > 0 {
// 		var config map[string]interface{}
// 		if err := yaml.Unmarshal(aiProjectResource.Config, &config); err != nil {
// 			return "", fmt.Errorf("parsing resource config: %w", err)
// 		}

// 		// Check the models array - based on azure.yaml format: models[].name
// 		if models, ok := config["Models"].([]interface{}); ok {
// 			for _, model := range models {
// 				if modelObj, ok := model.(map[string]interface{}); ok {
// 					if name, ok := modelObj["Name"].(string); ok {
// 						if name == modelName {
// 							fmt.Printf("Found matching model: %s\n", name)
// 							return aiProjectResource.Name, nil
// 						}
// 					}
// 				}
// 			}
// 		}
// 	}

// 	fmt.Printf("Model '%s' not found in resource '%s'\n", modelName, aiProjectResource.Name)
// 	return "", nil
// }

// func (a *InitAction) addResource(ctx context.Context, agentModelName string) error {
// 	// Look for existing ai.project resource
// 	var aiProject *azdext.ComposedResource
// 	var aiProjectConfig *AiProjectResourceConfig

// 	for _, resource := range a.composedResources {
// 		if resource.Type == "ai.project" {
// 			aiProject = resource

// 			// Parse existing config if it exists
// 			if len(resource.Config) > 0 {
// 				if err := yaml.Unmarshal(resource.Config, &aiProjectConfig); err != nil {
// 					return fmt.Errorf("failed to unmarshal AI project config: %w", err)
// 				}
// 			}
// 			break
// 		}
// 	}

// 	// Create new ai.project resource if it doesn't exist
// 	if aiProject == nil {
// 		fmt.Println("Adding new 'ai.project' resource to azd project.")
// 		aiProject = &azdext.ComposedResource{
// 			Name: generateResourceName("ai-project", a.composedResources),
// 			Type: "ai.project",
// 		}
// 		aiProjectConfig = &AiProjectResourceConfig{}
// 	}

// 	// Prompt user for model details
// 	modelDetails, err := a.promptForModelDetails(ctx, agentModelName)
// 	if err != nil {
// 		return fmt.Errorf("failed to get model details: %w", err)
// 	}

// 	fmt.Println("Got model details, adding to ai.project resource.")
// 	// Convert the ai.AiModelDeployment to the map format expected by AiProjectResourceConfig
// 	defaultModel := map[string]interface{}{
// 		"name":    modelDetails.Name,
// 		"format":  modelDetails.Format,
// 		"version": modelDetails.Version,
// 		"sku": map[string]interface{}{
// 			"name":      modelDetails.Sku.Name,
// 			"usageName": modelDetails.Sku.UsageName,
// 			"capacity":  modelDetails.Sku.Capacity,
// 		},
// 	}
// 	aiProjectConfig.Models = append(aiProjectConfig.Models, defaultModel)

// 	// Marshal the config as JSON (since the struct has json tags)
// 	configJson, err := json.Marshal(aiProjectConfig)
// 	if err != nil {
// 		return fmt.Errorf("failed to marshal AI project config: %w", err)
// 	}

// 	// Update the resource config
// 	aiProject.Config = configJson

// 	// Add the resource to the project
// 	_, err = a.azdClient.Compose().AddResource(ctx, &azdext.AddResourceRequest{
// 		Resource: aiProject,
// 	})
// 	if err != nil {
// 		return fmt.Errorf("failed to add resource %s: %w", aiProject.Name, err)
// 	}

// 	fmt.Printf("Added AI project resource '%s' to azure.yaml\n", aiProject.Name)
// 	return nil
// }

// func (a *InitAction) promptForModelDetails(ctx context.Context, modelName string) (*ai.AiModelDeployment, error) {
// 	// Load the AI model catalog if not already loaded
// 	if err := a.loadAiCatalog(ctx); err != nil {
// 		return nil, err
// 	}

// 	// Check if the model exists in the catalog
// 	var model *ai.AiModel
// 	model, exists := a.modelCatalog[modelName]
// 	if !exists {
// 		return nil, fmt.Errorf("model '%s' not found in AI model catalog", modelName)
// 	}

// 	availableVersions, err := a.modelCatalogService.ListModelVersions(ctx, model)
// 	if err != nil {
// 		return nil, fmt.Errorf("listing versions for model '%s': %w", modelName, err)
// 	}

// 	availableSkus, err := a.modelCatalogService.ListModelSkus(ctx, model)
// 	if err != nil {
// 		return nil, fmt.Errorf("listing SKUs for model '%s': %w", modelName, err)
// 	}

// 	modelVersionSelection, err := selectFromList(
// 		ctx, a.console, "Which model version do you want to use?", availableVersions, nil)
// 	if err != nil {
// 		return nil, err
// 	}

// 	skuSelection, err := selectFromList(ctx, a.console, "Select model SKU", availableSkus, nil)
// 	if err != nil {
// 		return nil, err
// 	}

// 	deploymentOptions := ai.AiModelDeploymentOptions{
// 		Versions: []string{modelVersionSelection},
// 		Skus:     []string{skuSelection},
// 	}

// 	modelDeployment, err := a.modelCatalogService.GetModelDeployment(ctx, model, &deploymentOptions)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to get model deployment: %w", err)
// 	}

// 	return modelDeployment, nil
// }

func (a *InitAction) loadAiCatalog(ctx context.Context) error {
	if a.modelCatalog != nil {
		return nil
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:        "Loading the model catalog",
		ClearOnStop: true,
	})

	if err := spinner.Start(ctx); err != nil {
		return fmt.Errorf("failed to start spinner: %w", err)
	}

	aiModelCatalog, err := a.modelCatalogService.ListAllModels(ctx, a.azureContext.Scope.SubscriptionId, a.azureContext.Scope.Location)
	if err != nil {
		return fmt.Errorf("failed to load the model catalog: %w", err)
	}

	if err := spinner.Stop(ctx); err != nil {
		return err
	}

	a.modelCatalog = aiModelCatalog
	return nil
}

// // generateResourceName generates a unique resource name, similar to the AI builder pattern
// func generateResourceName(desiredName string, existingResources []*azdext.ComposedResource) string {
// 	resourceMap := map[string]struct{}{}
// 	for _, resource := range existingResources {
// 		resourceMap[resource.Name] = struct{}{}
// 	}

// 	if _, exists := resourceMap[desiredName]; !exists {
// 		return desiredName
// 	}
// 	// If the desired name already exists, append a number (always 2 digits) to the name
// 	nextIndex := 1
// 	for {
// 		newName := fmt.Sprintf("%s-%02d", desiredName, nextIndex)
// 		if _, exists := resourceMap[newName]; !exists {
// 			return newName
// 		}
// 		nextIndex++
// 	}
// }

func (a *InitAction) selectFromList(
	ctx context.Context, property string, options []string, defaultOpt string) (string, error) {

	if len(options) == 1 {
		fmt.Printf("Only one %s available: %s\n", property, options[0])
		return options[0], nil
	}

	slices.Sort(options)

	// Convert default value to string for comparison
	defaultStr := options[0]
	if defaultOpt != "" {
		defaultStr = defaultOpt
	}

	if a.flags.NoPrompt {
		fmt.Printf("No prompt mode enabled, selecting default %s: %s\n", property, defaultStr)
		return defaultStr, nil
	}

	// Create choices for the select prompt
	choices := make([]*azdext.SelectChoice, len(options))
	defaultIndex := int32(0)
	for i, val := range options {
		choices[i] = &azdext.SelectChoice{
			Value: val,
			Label: val,
		}
		if val == defaultStr {
			defaultIndex = int32(i)
		}
	}
	resp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       fmt.Sprintf("Select %s", property),
			Choices:       choices,
			SelectedIndex: &defaultIndex,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to prompt for enum value: %w", err)
	}

	return options[*resp.Value], nil
}

func (a *InitAction) setEnvVar(ctx context.Context, key, value string) error {
	_, err := a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: a.environment.Name,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("failed to set environment variable %s=%s: %w", key, value, err)
	}

	fmt.Printf("Set environment variable: %s=%s\n", key, value)
	return nil
}

func (a *InitAction) getModelDeploymentDetails(ctx context.Context, model agent_yaml.Model) (*project.Deployment, error) {
	resp, err := a.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: a.environment.Name,
		Key:     "AZURE_AI_PROJECT_ID",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get the environment variable AZURE_AI_PROJECT_ID from your azd environment: %w", err)
	}

	foundryProjectId := resp.Value
	if foundryProjectId != "" {
		// Extract subscription and account name from foundry project ID
		// Format: /subscriptions/{subscription}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{account}/projects/{project}
		parts := strings.Split(foundryProjectId, "/")
		var subscription, resourceGroup, accountName string

		if len(parts) >= 9 {
			subscription = parts[2]  // subscriptions/{subscription}
			resourceGroup = parts[4] // resourceGroups/{rg}
			accountName = parts[8]   // accounts/{account}
		}

		deploymentsClient, err := armcognitiveservices.NewDeploymentsClient(subscription, a.credential, azure.NewArmClientOptions())
		if err != nil {
			return nil, fmt.Errorf("failed to create deployments client: %w", err)
		}

		pager := deploymentsClient.NewListPager(resourceGroup, accountName, nil)
		var deployments []*armcognitiveservices.Deployment
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list deployments: %w", err)
			}
			deployments = append(deployments, page.Value...)
		}

		// Check for existing deployments that match the requested model
		matchingDeployments := make(map[string]*armcognitiveservices.Deployment)
		for _, deployment := range deployments {
			if deployment.Properties != nil && deployment.Properties.Model != nil {
				deployedModel := deployment.Properties.Model
				if deployedModel.Name != nil {
					if *deployedModel.Name == model.Id {
						matchingDeployments[*deployment.Name] = deployment
					}
				}
			}
		}

		// If we found matching deployments, prompt the user
		if len(matchingDeployments) > 0 {
			fmt.Printf("In your Microsoft Foundry project, found %d existing model deployment(s) matching your model %s.\n", len(matchingDeployments), model.Id)

			// Build options list with existing deployments plus "Create new deployment" option
			var options []string
			for deploymentName := range matchingDeployments {
				options = append(options, deploymentName)
			}
			options = append(options, "Create new model deployment")

			// Use selectFromList to choose between existing deployments or creating new one
			selection, err := a.selectFromList(ctx, "deployment", options, options[0])
			if err != nil {
				return nil, fmt.Errorf("failed to select deployment: %w", err)
			}

			// Check if user chose to create new deployment
			if selection != "Create new model deployment" {
				// User chose an existing deployment by name
				fmt.Printf("Using existing model deployment: %s\n", selection)

				// Get the selected deployment from the map and return its details
				if deployment, exists := matchingDeployments[selection]; exists {
					return &project.Deployment{
						Name: selection,
						Model: project.DeploymentModel{
							Name:    model.Id,
							Format:  *deployment.Properties.Model.Format,
							Version: *deployment.Properties.Model.Version,
						},
						Sku: project.DeploymentSku{
							Name:     *deployment.SKU.Name,
							Capacity: int(*deployment.SKU.Capacity),
						},
					}, nil
				}
			}
		}
	}

	modelDetails, err := a.getModelDetails(ctx, model.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to get model details: %w", err)
	}

	message := fmt.Sprintf("Enter model deployment name for model '%s' (defaults to model name)", model.Id)

	modelDeploymentInput, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        message,
			IgnoreHintKeys: true,
			DefaultValue:   model.Id,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for text value: %w", err)
	}

	modelDeployment := modelDeploymentInput.Value

	return &project.Deployment{
		Name: modelDeployment,
		Model: project.DeploymentModel{
			Name:    model.Id,
			Format:  modelDetails.Format,
			Version: modelDetails.Version,
		},
		Sku: project.DeploymentSku{
			Name:     modelDetails.Sku.Name,
			Capacity: int(modelDetails.Sku.Capacity),
		},
	}, nil
}

var defaultSkuPriority = []string{"GlobalStandard", "DataZoneStandard", "Standard"}

func (a *InitAction) getModelDetails(ctx context.Context, modelName string) (*ai.AiModelDeployment, error) {
	// Load the AI model catalog if not already loaded
	if err := a.loadAiCatalog(ctx); err != nil {
		return nil, err
	}

	// Check if the model exists in the catalog
	var model *ai.AiModel
	model, exists := a.modelCatalog[modelName]
	if !exists {
		return nil, fmt.Errorf("The model '%s' could not be found in the model catalog", modelName)
	}

	availableVersions, defaultVersion, err := a.modelCatalogService.ListModelVersions(ctx, model)
	if err != nil {
		return nil, fmt.Errorf("listing versions for model '%s': %w", modelName, err)
	}

	modelVersion, err := a.selectFromList(ctx, "model version", availableVersions, defaultVersion)
	if err != nil {
		return nil, err
	}

	availableSkus, err := a.modelCatalogService.ListModelSkus(ctx, model, modelVersion)
	if err != nil {
		return nil, fmt.Errorf("listing SKUs for model '%s': %w", modelName, err)
	}

	// Determine default SKU based on priority list
	defaultSku := ""
	for _, sku := range defaultSkuPriority {
		if slices.Contains(availableSkus, sku) {
			defaultSku = sku
			break
		}
	}

	skuSelection, err := a.selectFromList(ctx, "model SKU", availableSkus, defaultSku)
	if err != nil {
		return nil, err
	}

	deploymentOptions := ai.AiModelDeploymentOptions{
		Versions: []string{modelVersion},
		Skus:     []string{skuSelection},
	}

	modelDeployment, err := a.modelCatalogService.GetModelDeployment(ctx, model, &deploymentOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to get model deployment: %w", err)
	}

	if modelDeployment.Sku.Capacity == -1 {
		skuCapacity, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Selected model SKU has no default capacity. Please enter desired capacity",
				IgnoreHintKeys: true,
				Required:       true,
				DefaultValue:   "10",
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to prompt for text value: %w", err)
		}

		capacity, err := strconv.Atoi(skuCapacity.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid capacity value: %w", err)
		}
		modelDeployment.Sku.Capacity = int32(capacity)
	}

	return modelDeployment, nil
}

func (a *InitAction) ProcessModels(ctx context.Context, manifest *agent_yaml.AgentManifest) (*agent_yaml.AgentManifest, []project.Deployment, error) {
	// Convert the template to bytes
	templateBytes, err := yaml.Marshal(manifest.Template)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal agent template to YAML: %w", err)
	}

	// Convert the bytes to a dictionary
	var templateDict map[string]interface{}
	if err := yaml.Unmarshal(templateBytes, &templateDict); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal agent template from YAML: %w", err)
	}

	// Convert the dictionary to bytes
	dictJsonBytes, err := yaml.Marshal(templateDict)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal templateDict to YAML: %w", err)
	}

	// Convert the bytes to an Agent Definition
	var agentDef agent_yaml.AgentDefinition
	if err := yaml.Unmarshal(dictJsonBytes, &agentDef); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal YAML to AgentDefinition: %w", err)
	}

	deploymentDetails := []project.Deployment{}
	paramValues := registry_api.ParameterValues{}
	switch agentDef.Kind {
	case agent_yaml.AgentKindPrompt:
		agentDef := manifest.Template.(agent_yaml.PromptAgent)

		modelDeployment, err := a.getModelDeploymentDetails(ctx, agentDef.Model)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get model deployment details: %w", err)
		}
		deploymentDetails = append(deploymentDetails, *modelDeployment)
		paramValues["deploymentName"] = modelDeployment.Name
	case agent_yaml.AgentKindHosted:
		// Iterate over all models in the manifest for the container agent
		for _, resource := range manifest.Resources {
			// Convert the resource to bytes
			resourceBytes, err := yaml.Marshal(resource)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal resource to YAML: %w", err)
			}

			// Convert the bytes to an Agent Definition
			var resourceDef agent_yaml.Resource
			if err := yaml.Unmarshal(resourceBytes, &resourceDef); err != nil {
				return nil, nil, fmt.Errorf("failed to unmarshal YAML to Resource: %w", err)
			}

			if resourceDef.Kind == agent_yaml.ResourceKindModel {
				resource := resource.(agent_yaml.ModelResource)
				model := agent_yaml.Model{
					Id: resource.Id,
				}
				modelDeployment, err := a.getModelDeploymentDetails(ctx, model)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to get model deployment details: %w", err)
				}
				deploymentDetails = append(deploymentDetails, *modelDeployment)
				paramValues[resource.Name] = modelDeployment.Name
			}
		}
	}

	updatedManifest, err := registry_api.InjectParameterValuesIntoManifest(manifest, paramValues)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to inject deployment names into manifest: %w", err)
	}

	fmt.Println("Model deployment details processed and injected into agent definition. Deployment details can also be found in the JSON formatted AI_PROJECT_DEPLOYMENTS environment variable.")

	return updatedManifest, deploymentDetails, nil
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
