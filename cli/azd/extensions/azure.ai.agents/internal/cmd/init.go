// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/registry_api"
	"azureaiagent/internal/pkg/azure"
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
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

type initFlags struct {
	*rootFlagsDefinition
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
	console              input.Console
	credential           azcore.TokenCredential
	modelCatalog         map[string]*azdext.AiModel
	locationWarningShown bool
	projectConfig        *azdext.ProjectConfig
	environment          *azdext.Environment
	flags                *initFlags
	deploymentDetails    []project.Deployment
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

func newInitCommand(rootFlags *rootFlagsDefinition) *cobra.Command {
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
				azdClient: azdClient,
				// azureClient:         azure.NewAzureClient(credential),
				azureContext: azureContext,
				// composedResources:   getComposedResourcesResponse.Resources,
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

		// Environment creation is handled separately in ensureEnvironment
		initArgs := []string{"init", "--minimal"}

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
	var useGhCli bool = false

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

		if name != "" {
			// Check if the manifest file is under current directory + "src/<name>"
			currentDir, err := os.Getwd()
			if err != nil {
				return nil, "", fmt.Errorf("getting current directory: %w", err)
			}
			srcDir := filepath.Join(currentDir, "src", name)
			absManifestPath, err := filepath.Abs(manifestPointer)
			if err != nil {
				return nil, "", fmt.Errorf("getting absolute path for manifest %s: %w", manifestPointer, err)
			}

			// Check if manifest is under src directory
			if isSubpath(absManifestPath, srcDir) {
				confirmResponse, err := a.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
					Options: &azdext.ConfirmOptions{
						Message:      "This operation will overwrite the provided manifest file. Continue?",
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
		}
	} else if a.isGitHubUrl(manifestPointer) {
		// Handle GitHub URLs using downloadGithubManifest
		// manifestPointer validation:
		// - accepts only URLs with the following format:
		//  - https://raw.<hostname>/<owner>/<repo>/refs/heads/<branch>/<path>/<file>.json
		//    - This url comes from a user clicking the `raw` button on a file in a GitHub repository (web view).
		//  - https://<hostname>/<owner>/<repo>/blob/<branch>/<path>/<file>.json
		//    - This url comes from a user browsing GitHub repository and copy-pasting the url from the browser.
		//  - https://api.<hostname>/repos/<owner>/<repo>/contents/<path>/<file>.json
		//    - This url comes from users familiar with the GitHub API. Usually for programmatic registration of templates.

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

		ghCli = github.NewGitHubCli(console, commandRunner)
		if err := ghCli.EnsureInstalled(ctx); err != nil {
			return nil, "", fmt.Errorf("ensuring gh is installed: %w", err)
		}

		var contentStr string
		// First try naive parsing assuming branch is a single word. This allows users to not have to authenticate
		// with gh CLI for public repositories.
		urlInfo = a.parseGitHubUrlNaive(manifestPointer)
		if urlInfo != nil {
			// Construct GitHub Contents API URL with ref query parameter
			fileApiUrl := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", urlInfo.RepoSlug, urlInfo.FilePath)
			if urlInfo.Branch != "" {
				escapedBranch := url.QueryEscape(urlInfo.Branch)
				fileApiUrl += fmt.Sprintf("?ref=%s", escapedBranch)
			}
			fmt.Printf("Attempting to download manifest from '%s' in repository '%s', branch '%s'\n", urlInfo.FilePath, urlInfo.RepoSlug, urlInfo.Branch)

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileApiUrl, nil)
			if err == nil {
				req.Header.Set("Accept", "application/vnd.github.v3.raw")
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					defer resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						bodyBytes, readErr := io.ReadAll(resp.Body)
						if readErr == nil {
							contentStr = string(bodyBytes)
							fmt.Printf("Downloaded manifest from branch: %s\n", urlInfo.Branch)
						}
					}
				}
			}
			if contentStr == "" {
				fmt.Printf("Warning: naive GitHub URL parsing failed to download manifest\n")
				fmt.Println("Proceeding with full parsing and download logic...")
			}
		}

		if contentStr == "" {
			// Fall back to complex parsing via azd GitHub CLI handling
			useGhCli = true
			urlInfo, err = a.parseGitHubUrl(ctx, manifestPointer)
			if err != nil {
				return nil, "", err
			}

			apiPath := fmt.Sprintf("/repos/%s/contents/%s", urlInfo.RepoSlug, urlInfo.FilePath)
			if urlInfo.Branch != "" {
				fmt.Printf("Downloaded manifest from branch: %s\n", urlInfo.Branch)
				apiPath += fmt.Sprintf("?ref=%s", urlInfo.Branch)
			}

			contentStr, err = downloadGithubManifest(ctx, urlInfo, apiPath, ghCli)
			if err != nil {
				return nil, "", fmt.Errorf("downloading from GitHub: %w", err)
			}
		}

		content = []byte(contentStr)
	} else if isRegistry, registryManifest := a.isRegistryUrl(manifestPointer); isRegistry {
		// Handle registry URLs
		manifestClient := registry_api.NewRegistryAgentManifestClient(registryManifest.registryName, a.credential)

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
	} else {
		// If we reach here, the manifest pointer didn't match any known type
		return nil, "", fmt.Errorf(
			"manifest pointer '%s' is not a valid local file path, GitHub URL, or registry URL",
			manifestPointer,
		)
	}

	// Parse and validate the YAML content against AgentManifest structure
	agentManifest, err := agent_yaml.LoadAndValidateAgentManifest(content)
	if err != nil {
		return nil, "", err
	}

	fmt.Println("✓ YAML content successfully validated against AgentManifest format")

	agentId := agentManifest.Name

	// Use targetDir if provided, otherwise default to "src/{agentId}"
	if targetDir == "" {
		targetDir = filepath.Join("src", agentId)
	}

	// Safety checks for local container-based agents should happen before prompting for model SKU, etc.
	if a.isLocalFilePath(manifestPointer) {
		if _, isContainerAgent := agentManifest.Template.(agent_yaml.ContainerAgent); isContainerAgent {
			if err := a.validateLocalContainerAgentCopy(ctx, manifestPointer, targetDir); err != nil {
				return nil, "", err
			}
		}
	}

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

	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, "", fmt.Errorf("creating target directory %s: %w", targetDir, err)
	}

	if a.isLocalFilePath(manifestPointer) {
		// Check if the template is a ContainerAgent
		_, isHostedContainer := agentManifest.Template.(agent_yaml.ContainerAgent)

		if isHostedContainer {
			// For container agents, copy the entire parent directory.
			// If the manifest already lives in the target directory (re-init), skip the copy.
			manifestDir := filepath.Dir(manifestPointer)
			srcAbs, err := filepath.Abs(manifestDir)
			if err != nil {
				return nil, "", fmt.Errorf("resolving manifest directory %s: %w", manifestDir, err)
			}
			dstAbs, err := filepath.Abs(targetDir)
			if err != nil {
				return nil, "", fmt.Errorf("resolving target directory %s: %w", targetDir, err)
			}
			if !isSamePath(srcAbs, dstAbs) {
				fmt.Println("Copying full directory for container agent")
				err := copyDirectory(manifestDir, targetDir)
				if err != nil {
					return nil, "", fmt.Errorf("copying parent directory: %w", err)
				}
			}
		}
	} else if isGitHubUrl {
		// Check if the template is a ContainerAgent
		_, isHostedContainer := agentManifest.Template.(agent_yaml.ContainerAgent)

		if isHostedContainer {
			// For container agents, download the entire parent directory
			fmt.Println("Downloading full directory for container agent")
			err := downloadParentDirectory(ctx, urlInfo, targetDir, ghCli, console, useGhCli)
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
	ctx context.Context, urlInfo *GitHubUrlInfo, apiPath string, ghCli *github.Cli) (string, error) {
	// This method assumes that either the repo is public, or the user has already been prompted to log in to the github cli
	// through our use of the underlying azd logic.

	content, err := ghCli.ApiCall(ctx, urlInfo.Hostname, apiPath, github.ApiCallOptions{
		Headers: []string{"Accept: application/vnd.github.v3.raw"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get content: %w", err)
	}

	return content, nil
}

// parseGitHubUrlNaive attempts to parse a GitHub URL assuming a simple single-word branch name.
// Returns nil if the URL doesn't match the expected pattern.
// Expected formats:
//   - https://github.com/{owner}/{repo}/blob/{branch}/{path}
//   - https://raw.githubusercontent.com/{owner}/{repo}/refs/heads/{branch}/{path}
func (a *InitAction) parseGitHubUrlNaive(manifestPointer string) *GitHubUrlInfo {
	// Parse URL to properly handle query parameters and fragments
	parsedURL, err := url.Parse(manifestPointer)
	if err != nil {
		return nil
	}

	// Try parsing github.com/blob format: https://github.com/{owner}/{repo}/blob/{branch}/{path}
	if parsedURL.Host == "github.com" && strings.Contains(parsedURL.Path, "/blob/") {
		hostname := "github.com"

		// Split by /blob/
		parts := strings.SplitN(parsedURL.Path, "/blob/", 2)
		if len(parts) != 2 {
			return nil
		}

		// Extract repo slug (owner/repo) from the first part
		repoPath := strings.TrimPrefix(parts[0], "/")
		repoSlug := repoPath

		// The second part is {branch}/{file-path}
		branchAndPath := parts[1]
		slashIndex := strings.Index(branchAndPath, "/")
		if slashIndex == -1 {
			return nil
		}

		branch := branchAndPath[:slashIndex]
		filePath := branchAndPath[slashIndex+1:]

		// Only use naive parsing if branch looks like a simple single word (no slashes)
		if strings.Contains(branch, "/") {
			return nil
		}

		return &GitHubUrlInfo{
			RepoSlug: repoSlug,
			Branch:   branch,
			FilePath: filePath,
			Hostname: hostname,
		}
	}

	// Try parsing raw.githubusercontent.com format: https://raw.githubusercontent.com/{owner}/{repo}/refs/heads/{branch}/{path}
	if parsedURL.Host == "raw.githubusercontent.com" {
		hostname := "github.com" // API calls still use github.com

		// Remove leading slash from path
		pathPart := strings.TrimPrefix(parsedURL.Path, "/")

		// Split path: {owner}/{repo}/refs/heads/{branch}/{file-path}
		parts := strings.SplitN(pathPart, "/", 3) // owner, repo, rest
		if len(parts) < 3 {
			return nil
		}

		repoSlug := parts[0] + "/" + parts[1]
		rest := parts[2]

		// Check for refs/heads/ prefix
		if strings.HasPrefix(rest, "refs/heads/") {
			rest = strings.TrimPrefix(rest, "refs/heads/")
			slashIndex := strings.Index(rest, "/")
			if slashIndex == -1 {
				return nil
			}

			branch := rest[:slashIndex]
			filePath := rest[slashIndex+1:]

			// Only use naive parsing if branch looks like a simple single word
			if strings.Contains(branch, "/") {
				return nil
			}

			return &GitHubUrlInfo{
				RepoSlug: repoSlug,
				Branch:   branch,
				FilePath: filePath,
				Hostname: hostname,
			}
		}
	}

	return nil
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
	ctx context.Context, urlInfo *GitHubUrlInfo, targetDir string, ghCli *github.Cli, console input.Console, useGhCli bool) error {

	// Get parent directory by removing the filename from the file path
	pathParts := strings.Split(urlInfo.FilePath, "/")
	if len(pathParts) <= 1 {
		fmt.Println("The file agent.yaml is at repository root, no parent directory to download")
		return nil
	}

	parentDirPath := strings.Join(pathParts[:len(pathParts)-1], "/")
	fmt.Printf("Downloading parent directory '%s' from repository '%s', branch '%s'\n", parentDirPath, urlInfo.RepoSlug, urlInfo.Branch)

	// Download directory contents
	if useGhCli {
		if err := downloadDirectoryContents(ctx, urlInfo.Hostname, urlInfo.RepoSlug, parentDirPath, urlInfo.Branch, targetDir, ghCli, console); err != nil {
			return fmt.Errorf("failed to download directory contents with GH CLI: %w", err)
		}
	} else {
		if err := downloadDirectoryContentsWithoutGhCli(ctx, urlInfo.RepoSlug, parentDirPath, urlInfo.Branch, targetDir); err != nil {
			return fmt.Errorf("failed to download directory contents without GH CLI: %w", err)
		}
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

func downloadDirectoryContentsWithoutGhCli(
	ctx context.Context, repoSlug string, dirPath string, branch string, localPath string) error {

	// Get directory contents using GitHub API directly
	apiUrl := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repoSlug, dirPath)
	if branch != "" {
		apiUrl += fmt.Sprintf("?ref=%s", branch)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiUrl, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to get directory contents: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get directory contents: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read directory contents response: %w", err)
	}

	// Parse the directory contents JSON
	var dirContents []map[string]interface{}
	if err := json.Unmarshal(body, &dirContents); err != nil {
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
			// Download file using GitHub Contents API with raw accept header
			fmt.Printf("Downloading file: %s\n", itemPath)
			fileURL := &url.URL{
				Scheme: "https",
				Host:   "api.github.com",
				Path:   fmt.Sprintf("/repos/%s/contents/%s", repoSlug, itemPath),
			}
			if branch != "" {
				query := url.Values{}
				query.Set("ref", branch)
				fileURL.RawQuery = query.Encode()
			}

			fileReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL.String(), nil)
			if err != nil {
				return fmt.Errorf("failed to create file request %s: %w", itemPath, err)
			}
			fileReq.Header.Set("Accept", "application/vnd.github.v3.raw")

			fileResp, err := http.DefaultClient.Do(fileReq)
			if err != nil {
				return fmt.Errorf("failed to download file %s: %w", itemPath, err)
			}
			defer fileResp.Body.Close()

			if fileResp.StatusCode != http.StatusOK {
				return fmt.Errorf("failed to download file %s: status %d", itemPath, fileResp.StatusCode)
			}

			fileContent, err := io.ReadAll(fileResp.Body)
			if err != nil {
				return fmt.Errorf("failed to read file content %s: %w", itemPath, err)
			}

			if err := os.WriteFile(itemLocalPath, fileContent, 0644); err != nil {
				return fmt.Errorf("failed to write file %s: %w", itemLocalPath, err)
			}
		} else if itemType == "dir" {
			// Recursively download subdirectory
			fmt.Printf("Downloading directory: %s\n", itemPath)
			if err := os.MkdirAll(itemLocalPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", itemLocalPath, err)
			}

			// Recursively download directory contents
			if err := downloadDirectoryContentsWithoutGhCli(ctx, repoSlug, itemPath, branch, itemLocalPath); err != nil {
				return fmt.Errorf("failed to download subdirectory %s: %w", itemPath, err)
			}
		}
	}

	return nil
}
