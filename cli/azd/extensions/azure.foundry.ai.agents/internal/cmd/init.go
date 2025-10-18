// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type initFlags struct {
	projectResourceId string
	manifestPointer   string
	src               string
}

// AiProjectResourceConfig represents the configuration for an AI project resource
type AiProjectResourceConfig struct {
	Models []map[string]interface{} `json:"models,omitempty"`
}

type InitAction struct {
	azdClient *azdext.AzdClient
	//azureClient       *azure.AzureClient
	// azureContext *azdext.AzureContext
	//composedResources []*azdext.ComposedResource
	//console           input.Console
	//credential        azcore.TokenCredential
	//modelCatalog      map[string]*ai.AiModel
	//modelCatalogService *ai.ModelCatalogService
	projectConfig *azdext.ProjectConfig
	environment    *azdext.Environment
}

func newInitCommand() *cobra.Command {
	flags := &initFlags{}

	cmd := &cobra.Command{
		Use:   "init [-m <manifest pointer>] [--src <source directory>]",
		Short: "Initialize a new AI agent project.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			//azureContext, projectConfig, err := ensureAzureContext(ctx, azdClient)
			projectConfig, err := ensureProject(ctx, azdClient)
			if err != nil {
				return fmt.Errorf("failed to ground into a project context: %w", err)
			}

			environment, err := ensureEnvironment(ctx, azdClient)
			if err != nil {
				return fmt.Errorf("failed to ground into an environment: %w", err)
			}

			// getComposedResourcesResponse, err := azdClient.Compose().ListResources(ctx, &azdext.EmptyRequest{})
			// if err != nil {
			// 	return fmt.Errorf("failed to get composed resources: %w", err)
			// }

			// credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
			// 	TenantID:                   azureContext.Scope.TenantId,
			// 	AdditionallyAllowedTenants: []string{"*"},
			// })
			// if err != nil {
			// 	return fmt.Errorf("failed to create azure credential: %w", err)
			// }

			// console := input.NewConsole(
			// 	false, // noPrompt
			// 	true,  // isTerminal
			// 	input.Writers{Output: os.Stdout},
			// 	input.ConsoleHandles{
			// 		Stderr: os.Stderr,
			// 		Stdin:  os.Stdin,
			// 		Stdout: os.Stdout,
			// 	},
			// 	nil, // formatter
			// 	nil, // externalPromptCfg
			// )

			action := &InitAction{
				azdClient: azdClient,
				// azureClient:         azure.NewAzureClient(credential),
				// azureContext: azureContext,
				// composedResources:   getComposedResourcesResponse.Resources,
				// console: console,
				// credential:          credential,
				// modelCatalogService: ai.NewModelCatalogService(credential),
				projectConfig: projectConfig,
				environment:   environment,
			}

			if err := action.Run(ctx, flags); err != nil {
				return fmt.Errorf("failed to run start action: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&flags.projectResourceId, "project-id", "p", "",
		"Azure AI Foundry Project Id to set your environment to")

	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "",
		"Path or URI to an agent manifest to add to your project")

	cmd.Flags().StringVarP(&flags.src, "src", "s", "",
		"[Optional] Directory to download the agent yaml to (defaults to 'src/<agent-id>')")

	return cmd
}

func (a *InitAction) Run(ctx context.Context, flags *initFlags) error {
	color.Green("Initializing AI agent project...")
	fmt.Println()

	// If --project-id is given
	if flags.projectResourceId != "" {
		// projectResourceId is a string of the format 
		// /subscriptions/[AZURE_SUBSCRIPTION]/resourceGroups/[AZURE_RESOURCE_GROUP]/providers/Microsoft.CognitiveServices/accounts/[AI_ACCOUNT_NAME]/projects/[AI_PROJECT_NAME]
		// extract each of those fields from the string, issue an error if it doesn't match the format
		fmt.Println("Setting up your azd environment to use the provided AI Foundry project resource ID...")
		if err := a.parseAndSetProjectResourceId(ctx, flags.projectResourceId); err != nil {
			return fmt.Errorf("failed to parse project resource ID: %w", err)
		}

		color.Green("\nAI agent project initialized successfully!")
	}

	// If --manifest is given
	if flags.manifestPointer != "" {
		// Validate that the manifest pointer is either a valid URL or existing file path
		isValidURL := false
		isValidFile := false

		if _, err := url.ParseRequestURI(flags.manifestPointer); err == nil {
			isValidURL = true
		} else if _, fileErr := os.Stat(flags.manifestPointer); fileErr == nil {
			isValidFile = true
		}

		if !isValidURL && !isValidFile {
			return fmt.Errorf("manifest pointer '%s' is neither a valid URI nor an existing file path", flags.manifestPointer)
		}

		// Download/read agent.yaml file from the provided URI or file path and save it to project's "agents" directory
		agentManifest, targetDir, err := a.downloadAgentYaml(ctx, flags.manifestPointer, flags.src)
		if err != nil {
			return fmt.Errorf("downloading agent.yaml: %w", err)
		}

		// Add the agent to the azd project (azure.yaml) services
		if err := a.addToProject(ctx, targetDir, agentManifest); err != nil {
			return fmt.Errorf("failed to add agent to azure.yaml: %w", err)
		}

		// Update environment with necessary env vars
		if err := a.updateEnvironment(ctx, agentManifest); err != nil {
			return fmt.Errorf("failed to update environment: %w", err)
		}

		color.Green("\nAI agent added to your project successfully!")
	}

	// Validate command flags
	// if err := a.validateFlags(flags); err != nil {
	// 	return err
	// }

	// Prompt for any missing input values
	// if err := a.promptForMissingValues(ctx, a.azdClient, flags); err != nil {
	// 	return fmt.Errorf("collecting required information: %w", err)
	// }

	return nil
}

func ensureProject(ctx context.Context, azdClient *azdext.AzdClient) (*azdext.ProjectConfig, error) {
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Println("Lets get your project initialized.")

		// We don't have a project yet
		// Dispatch a workflow to init the project
		workflow := &azdext.Workflow{
			Name: "init",
			Steps: []*azdext.WorkflowStep{
				{Command: &azdext.WorkflowCommand{Args: []string{"init"}}},
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

func ensureEnvironment(ctx context.Context, azdClient *azdext.AzdClient) (*azdext.Environment, error) {
	envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Println("Lets create a new default environment for your project.")

		// We don't have a project yet
		// Dispatch a workflow to init the project and create a new environment
		workflow := &azdext.Workflow{
			Name: "env new",
			Steps: []*azdext.WorkflowStep{
				{Command: &azdext.WorkflowCommand{Args: []string{"env", "new"}}},
			},
		}

		_, err = azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
			Workflow: workflow,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create new environment: %w", err)
		}

		envResponse, err = azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return nil, fmt.Errorf("failed to get current environment: %w", err)
		}

		fmt.Println()
	}

	if envResponse.Environment == nil {
		return nil, fmt.Errorf("environment not found")
	}

	return envResponse.Environment, nil
}

func ensureAzureContext(
	ctx context.Context,
	azdClient *azdext.AzdClient,
) (*azdext.AzureContext, *azdext.ProjectConfig, error) {
	project, err := ensureProject(ctx, azdClient)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to ensure environment: %w", err)
	}

	env, err := ensureEnvironment(ctx, azdClient)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to ensure environment: %w", err)
	}

	envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: env.Name,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get environment values: %w", err)
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
			return nil, nil, fmt.Errorf("failed to prompt for subscription: %w", err)
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
			return nil, nil, fmt.Errorf("failed to set tenant ID in environment: %w", err)
		}

		// Set the tenant ID in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Name,
			Key:     "AZURE_SUBSCRIPTION_ID",
			Value:   azureContext.Scope.SubscriptionId,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set subscription ID in environment: %w", err)
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
			return nil, nil, fmt.Errorf("failed to prompt for location: %w", err)
		}

		azureContext.Scope.Location = locationResponse.Location.Name

		// Set the location in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Name,
			Key:     "AZURE_LOCATION",
			Value:   azureContext.Scope.Location,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set location in environment: %w", err)
		}
	}

	return azureContext, project, nil
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
				Message:        "Enter the location of the agent manifest:",
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

func (a *InitAction) parseAndSetProjectResourceId(ctx context.Context, projectResourceId string) error {
	// Define the regex pattern for the project resource ID
	pattern := `^/subscriptions/([^/]+)/resourceGroups/([^/]+)/providers/Microsoft\.CognitiveServices/accounts/([^/]+)/projects/([^/]+)$`
	
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("failed to compile regex pattern: %w", err)
	}

	matches := regex.FindStringSubmatch(projectResourceId)
	if matches == nil || len(matches) != 5 {
		return fmt.Errorf("project resource ID does not match expected format: /subscriptions/[SUBSCRIPTION]/resourceGroups/[RESOURCE_GROUP]/providers/Microsoft.CognitiveServices/accounts/[AI_ACCOUNT]/projects/[AI_PROJECT]")
	}

	// Extract the components
	subscriptionId := matches[1]
	resourceGroupName := matches[2]
	aiAccountName := matches[3]
	aiProjectName := matches[4]

	// Get current environment
	envResponse, err := a.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}

	if envResponse.Environment == nil {
		return fmt.Errorf("no current environment found")
	}

	envName := envResponse.Environment.Name

	// Set the extracted values as environment variables
	if err := a.setEnvVar(ctx, envName, "AZURE_SUBSCRIPTION_ID", subscriptionId); err != nil {
		return err
	}

	if err := a.setEnvVar(ctx, envName, "AZURE_RESOURCE_GROUP", resourceGroupName); err != nil {
		return err
	}

	if err := a.setEnvVar(ctx, envName, "AZURE_AI_ACCOUNT_NAME", aiAccountName); err != nil {
		return err
	}

	if err := a.setEnvVar(ctx, envName, "AZURE_AI_PROJECT_NAME", aiProjectName); err != nil {
		return err
	}

	// Set the AI Foundry endpoint URL
	aiFoundryEndpoint := fmt.Sprintf("https://%s.services.ai.azure.com/api/projects/%s", aiAccountName, aiProjectName)
	if err := a.setEnvVar(ctx, envName, "AZURE_AI_FOUNDRY_PROJECT_ENDPOINT", aiFoundryEndpoint); err != nil {
		return err
	}

	fmt.Printf("Successfully parsed and set environment variables from project resource ID\n")
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

func (a *InitAction) downloadAgentYaml(
	ctx context.Context, manifestPointer string, targetDir string) (*agent_yaml.AgentManifest, string, error) {
	if manifestPointer == "" {
		return nil, "", fmt.Errorf("manifestPointer cannot be empty")
	}

	var content []byte
	var err error

	// Check if manifestPointer is a local file path or a URI
	if a.isLocalFilePath(manifestPointer) {
		// Handle local file path
		fmt.Printf("Reading agent.yaml from local file: %s\n", manifestPointer)
		content, err = os.ReadFile(manifestPointer)
		if err != nil {
			return nil, "", fmt.Errorf("reading local file %s: %w", manifestPointer, err)
		}
		targetDir = filepath.Dir(manifestPointer)
	} else {
		// Handle URI - existing logic
		fmt.Printf("Downloading agent.yaml from URI: %s\n", manifestPointer)

		// Download the file from the URI
		req, err := http.NewRequestWithContext(ctx, "GET", manifestPointer, nil)
		if err != nil {
			return nil, "", fmt.Errorf("creating request for URI %s: %w", manifestPointer, err)
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("downloading file from URI %s: %w", manifestPointer, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("failed to download file: HTTP %d", resp.StatusCode)
		}

		// Read the response body
		content, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, "", fmt.Errorf("reading response body: %w", err)
		}
	}

	// Parse and validate the YAML content against AgentManifest structure
	agentManifest, err := agent_yaml.LoadAndValidateAgentManifest(content)
	if err != nil {
		return nil, "", fmt.Errorf("AgentManifest %w", err)
	}

	fmt.Println("✓ YAML content successfully validated against AgentManifest format")

	agentId := agentManifest.Agent.Name

	// Use targetDir if provided or set to local file pointer, otherwise default to "src/{agentId}"
	if targetDir == "" {
		targetDir = filepath.Join("src", agentId)
	}

	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, "", fmt.Errorf("creating target directory %s: %w", targetDir, err)
	}

	// Save the file to the target directory
	filePath := filepath.Join(targetDir, "agent.yaml")
	if err := os.WriteFile(filePath, content, osutil.PermissionFile); err != nil {
		return nil, "", fmt.Errorf("saving file to %s: %w", filePath, err)
	}

	fmt.Printf("Processed agent.yaml at %s\n", filePath)
	
	return agentManifest, targetDir, nil
}

func (a *InitAction) addToProject(ctx context.Context, targetDir string, agentManifest *agent_yaml.AgentManifest) error {
	var host string
	switch agentManifest.Agent.Kind {
	case "container":
		host = "containerapp"
	default:
		host = "foundry.agent"
	}

	serviceConfig := &azdext.ServiceConfig{
		Name:         agentManifest.Agent.Name,
		RelativePath: targetDir,
		Host:         host,
		Language:     "python",
	}

	req := &azdext.AddServiceRequest{Service: serviceConfig}

	if _, err := a.azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding agent service to project: %w", err)
	}

	fmt.Printf("Added service '%s' to azure.yaml\n", agentManifest.Agent.Name)
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

// func (a *InitAction) loadAiCatalog(ctx context.Context) error {
// 	if a.modelCatalog != nil {
// 		return nil
// 	}

// 	spinner := ux.NewSpinner(&ux.SpinnerOptions{
// 		Text:        "Loading AI Model Catalog",
// 		ClearOnStop: true,
// 	})

// 	if err := spinner.Start(ctx); err != nil {
// 		return fmt.Errorf("failed to start spinner: %w", err)
// 	}

// 	aiModelCatalog, err := a.modelCatalogService.ListAllModels(ctx, a.azureContext.Scope.SubscriptionId)
// 	if err != nil {
// 		return fmt.Errorf("failed to load AI model catalog: %w", err)
// 	}

// 	if err := spinner.Stop(ctx); err != nil {
// 		return err
// 	}

// 	a.modelCatalog = aiModelCatalog
// 	return nil
// }

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

// func selectFromList(
// 	ctx context.Context, console input.Console, q string, options []string, defaultOpt *string) (string, error) {

// 	if len(options) == 1 {
// 		return options[0], nil
// 	}

// 	defOpt := options[0]

// 	if defaultOpt != nil {
// 		defOpt = *defaultOpt
// 	}

// 	slices.Sort(options)
// 	selectedIndex, err := console.Select(ctx, input.ConsoleOptions{
// 		Message:      q,
// 		Options:      options,
// 		DefaultValue: defOpt,
// 	})

// 	if err != nil {
// 		return "", err
// 	}

// 	chosen := options[selectedIndex]
// 	return chosen, nil
// }

func (a *InitAction) updateEnvironment(ctx context.Context, agentManifest *agent_yaml.AgentManifest) error {
	fmt.Printf("Updating environment variables for agent kind: %s\n", agentManifest.Agent.Kind)

	// Get current environment
	envResponse, err := a.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}

	if envResponse.Environment == nil {
		return fmt.Errorf("no current environment found")
	}

	envName := envResponse.Environment.Name

	// Set environment variables based on agent kind
	switch agentManifest.Agent.Kind {
	case "hosted":
		// Set environment variables for hosted agents
		if err := a.setEnvVar(ctx, envName, "ENABLE_HOSTED_AGENTS", "true"); err != nil {
			return err
		}
	case "container":
		// Set environment variables for foundry agents
		if err := a.setEnvVar(ctx, envName, "ENABLE_CONTAINER_AGENTS", "true"); err != nil {
			return err
		}
	}

	// Model information should be set regardless of agent kind
	if err := a.setEnvVar(ctx, envName, "AZURE_AI_FOUNDRY_MODEL_NAME", agentManifest.Agent.Model.Id); err != nil {
		return err
	}

	fmt.Printf("Successfully updated environment variables for agent kind: %s\n", agentManifest.Agent.Kind)
	return nil
}

func (a *InitAction) setEnvVar(ctx context.Context, envName, key, value string) error {
	_, err := a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("failed to set environment variable %s=%s: %w", key, value, err)
	}

	fmt.Printf("Set environment variable: %s=%s\n", key, value)
	return nil
}

