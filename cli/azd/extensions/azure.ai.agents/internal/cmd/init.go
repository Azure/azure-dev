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
	"slices"
	"strconv"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/registry_api"
	"azureaiagent/internal/pkg/azure/ai"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
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
		Short: "Initialize a new AI agent project.",
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
		"Azure AI Foundry Project Id to set your environment to")

	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "",
		"Path or URI to an agent manifest to add to your project")

	cmd.Flags().StringVarP(&flags.src, "src", "s", "",
		"[Optional] Directory to download the agent yaml to (defaults to 'src/<agent-id>')")

	cmd.Flags().StringVarP(&flags.host, "host", "", "",
		"[Optional] For container based agents, can override the default host to target a container app instead. Accepted values: 'containerapp'")

	cmd.Flags().StringVarP(&flags.env, "environment", "e", "", "The name of the environment to use.")

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
		fmt.Println("Setting up your azd environment to use the provided AI Foundry project resource ID...")
		if err := a.parseAndSetProjectResourceId(ctx); err != nil {
			return fmt.Errorf("failed to parse project resource ID: %w", err)
		}

		color.Green("\nAI agent project initialized successfully!")
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
			return fmt.Errorf("manifest pointer '%s' is neither a valid URI nor an existing file path", a.flags.manifestPointer)
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

		color.Green("\nAI agent added to your project successfully!")
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
	// Get specified or current environment if it exists
	existingEnv := getExistingEnvironment(ctx, flags, azdClient)
	if existingEnv == nil {
		// Dispatch `azd env new` to create a new environment with interactive flow
		fmt.Println("Lets create a new default environment for your project.")

		envArgs := []string{"env", "new"}
		if flags.env != "" {
			envArgs = append(envArgs, flags.env)
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
			return nil, fmt.Errorf("failed to create new environment: %w", err)
		}

		// Re-fetch the environment after creation
		existingEnv = getExistingEnvironment(ctx, flags, azdClient)
		if existingEnv == nil {
			return nil, fmt.Errorf("environment not found")
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
			return nil, nil, nil, fmt.Errorf("failed to set tenant ID in environment: %w", err)
		}

		// Set the tenant ID in the environment
		_, err = azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: env.Name,
			Key:     "AZURE_SUBSCRIPTION_ID",
			Value:   azureContext.Scope.SubscriptionId,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to set subscription ID in environment: %w", err)
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
			return nil, nil, nil, fmt.Errorf("failed to set location in environment: %w", err)
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

func (a *InitAction) parseAndSetProjectResourceId(ctx context.Context) error {
	// Define the regex pattern for the project resource ID
	pattern := `^/subscriptions/([^/]+)/resourceGroups/([^/]+)/providers/Microsoft\.CognitiveServices/accounts/([^/]+)/projects/([^/]+)$`

	regex, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("failed to compile regex pattern: %w", err)
	}

	matches := regex.FindStringSubmatch(a.flags.projectResourceId)
	if matches == nil || len(matches) != 5 {
		return fmt.Errorf("project resource ID does not match expected format: /subscriptions/[SUBSCRIPTION]/resourceGroups/[RESOURCE_GROUP]/providers/Microsoft.CognitiveServices/accounts/[AI_ACCOUNT]/projects/[AI_PROJECT]")
	}

	// Extract the components
	subscriptionId := matches[1]
	resourceGroupName := matches[2]
	aiAccountName := matches[3]
	aiProjectName := matches[4]

	// Set the extracted values as environment variables
	if err := a.setEnvVar(ctx, "AZURE_SUBSCRIPTION_ID", subscriptionId); err != nil {
		return err
	}

	if err := a.setEnvVar(ctx, "AZURE_RESOURCE_GROUP", resourceGroupName); err != nil {
		return err
	}

	if err := a.setEnvVar(ctx, "AZURE_AI_ACCOUNT_NAME", aiAccountName); err != nil {
		return err
	}

	if err := a.setEnvVar(ctx, "AZURE_AI_PROJECT_NAME", aiProjectName); err != nil {
		return err
	}

	// Set the AI Foundry endpoint URL
	aiFoundryEndpoint := fmt.Sprintf("https://%s.services.ai.azure.com/api/projects/%s", aiAccountName, aiProjectName)
	if err := a.setEnvVar(ctx, "AZURE_AI_FOUNDRY_PROJECT_ENDPOINT", aiFoundryEndpoint); err != nil {
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
		return nil, "", fmt.Errorf("manifestPointer cannot be empty")
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
		targetDir = filepath.Dir(manifestPointer)
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

		urlInfo, err = parseGitHubUrl(manifestPointer)
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

	if isGitHubUrl {
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

	// Save the file to the target directory
	filePath := filepath.Join(targetDir, "agent.yaml")
	if err := os.WriteFile(filePath, content, osutil.PermissionFile); err != nil {
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

	deploymentDetails := []project.Deployment{}
	resourceDetails := []project.Resource{}
	switch agentDef.Kind {
	case agent_yaml.AgentKindPrompt:
		agentDef := agentManifest.Template.(agent_yaml.PromptAgent)

		modelDeployment, err := a.getModelDeploymentDetails(ctx, agentDef.Model)
		if err != nil {
			return fmt.Errorf("failed to get model deployment details: %w", err)
		}
		deploymentDetails = append(deploymentDetails, *modelDeployment)
	case agent_yaml.AgentKindHosted:
		// Iterate over all models in the manifest for the container agent
		for _, resource := range agentManifest.Resources {
			// Convert the resource to bytes
			resourceBytes, err := json.Marshal(resource)
			if err != nil {
				return fmt.Errorf("failed to marshal resource to JSON: %w", err)
			}

			// Convert the bytes to an Agent Definition
			var resourceDef agent_yaml.Resource
			if err := json.Unmarshal(resourceBytes, &resourceDef); err != nil {
				return fmt.Errorf("failed to unmarshal JSON to Resource: %w", err)
			}

			if resourceDef.Kind == agent_yaml.ResourceKindModel {
				resource := resource.(agent_yaml.ModelResource)
				model := agent_yaml.Model{
					Id: resource.Id,
				}
				modelDeployment, err := a.getModelDeploymentDetails(ctx, model)
				if err != nil {
					return fmt.Errorf("failed to get model deployment details: %w", err)
				}
				deploymentDetails = append(deploymentDetails, *modelDeployment)
			}
		}

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
								Message:        fmt.Sprintf("Enter connection name for %s resource:", toolResource.Id),
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

		// Hard code for now
		// TODO: Add env var handling in the future
		containerSettings := &project.ContainerSettings{
			Resources: &project.ResourceSettings{
				Memory: "2Gi",
				Cpu:    "1",
			},
			Scale: &project.ScaleSettings{
				MinReplicas: 1,
				MaxReplicas: 3,
			},
		}
		agentConfig.Container = containerSettings
	}

	agentConfig.Deployments = deploymentDetails
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

	fmt.Printf("Added service '%s' to azure.yaml\n", agentDef.Name)
	return nil
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

func downloadParentDirectory(
	ctx context.Context, urlInfo *GitHubUrlInfo, targetDir string, ghCli *github.Cli, console input.Console) error {

	// Get parent directory by removing the filename from the file path
	pathParts := strings.Split(urlInfo.FilePath, "/")
	if len(pathParts) <= 1 {
		fmt.Println("Agent.yaml is at repository root, no parent directory to download")
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
		Text:        "Loading AI Model Catalog",
		ClearOnStop: true,
	})

	if err := spinner.Start(ctx); err != nil {
		return fmt.Errorf("failed to start spinner: %w", err)
	}

	aiModelCatalog, err := a.modelCatalogService.ListAllModels(ctx, a.azureContext.Scope.SubscriptionId, a.azureContext.Scope.Location)
	if err != nil {
		return fmt.Errorf("failed to load AI model catalog: %w", err)
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
		Key:     "AZURE_AI_FOUNDRY_PROJECT_ID",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get foundry project ID: %w", err)
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

		deploymentsClient, err := armcognitiveservices.NewDeploymentsClient(subscription, a.credential, nil)
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
			fmt.Printf("Found %d existing deployment(s) for model %s.\n", len(matchingDeployments), model.Id)

			// Build options list with existing deployments plus "Create new deployment" option
			var options []string
			for deploymentName := range matchingDeployments {
				options = append(options, deploymentName)
			}
			options = append(options, "Create new deployment")

			// Use selectFromList to choose between existing deployments or creating new one
			selection, err := a.selectFromList(ctx, "deployment", options, options[0])
			if err != nil {
				return nil, fmt.Errorf("failed to select deployment: %w", err)
			}

			// Check if user chose to create new deployment
			if selection != "Create new deployment" {
				// User chose an existing deployment by name
				fmt.Printf("Using existing deployment: %s\n", selection)

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

	message := fmt.Sprintf("Enter model deployment name for model '%s' (defaults to model name):", model.Id)

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
		return nil, fmt.Errorf("model '%s' not found in AI model catalog", modelName)
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
				DefaultValue:   "1",
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
