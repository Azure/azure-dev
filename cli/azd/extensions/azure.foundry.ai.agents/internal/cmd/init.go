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
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type initFlags struct {
	manifestPointer string
	src             string
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
	//console           input.Console
	//credential        azcore.TokenCredential
	//modelCatalog      map[string]*ai.AiModel
	//modelCatalogService *ai.ModelCatalogService
	projectConfig *azdext.ProjectConfig
}

// GitHubUrlInfo holds parsed information from a GitHub URL
type GitHubUrlInfo struct {
	RepoSlug string
	Branch   string
	FilePath string
	Hostname string
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

			azureContext, projectConfig, err := ensureAzureContext(ctx, azdClient)
			if err != nil {
				return fmt.Errorf("failed to ensure azure context: %w", err)
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
				azureContext: azureContext,
				// composedResources:   getComposedResourcesResponse.Resources,
				// console: console,
				// credential:          credential,
				// modelCatalogService: ai.NewModelCatalogService(credential),
				projectConfig: projectConfig,
			}

			if err := action.Run(ctx, flags); err != nil {
				return fmt.Errorf("failed to run start action: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&flags.manifestPointer, "", "m", "",
		"Pointer to the manifest to use for the agent")

	cmd.Flags().StringVarP(&flags.src, "src", "s", "",
		"[Optional] Directory to download the agent yaml to (defaults to 'src/<agent-id>')")

	return cmd
}

func (a *InitAction) Run(ctx context.Context, flags *initFlags) error {
	color.Green("Initializing AI agent project...")
	fmt.Println()

	// Validate command flags
	if err := a.validateFlags(flags); err != nil {
		return err
	}

	// Prompt for any missing input values
	if err := a.promptForMissingValues(ctx, a.azdClient, flags); err != nil {
		return fmt.Errorf("collecting required information: %w", err)
	}

	fmt.Println("Configuration:")
	fmt.Printf("  URI: %s\n", flags.manifestPointer)
	fmt.Println()

	// Download agent.yaml file from the provided URI and save it to project's "agents" directory
	var agentYaml map[string]interface{}
	var targetDir string
	var err error

	agentYaml, targetDir, err = a.downloadAgentYaml(ctx, flags.manifestPointer, flags.src)
	if err != nil {
		return fmt.Errorf("downloading agent.yaml: %w", err)
	}

	agentId, ok := agentYaml["id"].(string)
	if !ok || agentId == "" {
		return fmt.Errorf("extracting id from agent YAML: id missing or empty")
	}
	agentKind, ok := agentYaml["kind"].(string)
	if !ok || agentKind == "" {
		return fmt.Errorf("extracting kind from agent YAML: kind missing or empty")
	}
	agentModelName, ok := agentYaml["model"].(string)
	if !ok || agentModelName == "" {
		return fmt.Errorf("extracting model name from agent YAML: model name missing or empty")
	}

	// Add the agent to the azd project (azure.yaml) services
	if err := a.addToProject(ctx, targetDir, agentId, agentKind); err != nil {
		return fmt.Errorf("failed to add agent to azure.yaml: %w", err)
	}

	// Update environment with necessary env vars
	if err := a.updateEnvironment(ctx, agentKind, agentModelName); err != nil {
		return fmt.Errorf("failed to update environment: %w", err)
	}

	// Populate the "resources" section of the azure.yaml
	// TODO: Add back in once we move forward with composability support
	// if err := a.validateResources(ctx, agentYaml); err != nil {
	// 	return fmt.Errorf("updating resources in azure.yaml: %w", err)
	// }

	color.Green("\nAI agent project initialized successfully!")
	return nil
}

func ensureProject(ctx context.Context, azdClient *azdext.AzdClient) (*azdext.ProjectConfig, error) {
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Println("Lets get your project initialized.")

		// We don't have a project yet
		// Dispatch a workflow to init the project and create a new environment
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
		if _, err := url.ParseRequestURI(flags.manifestPointer); err != nil {
			return fmt.Errorf("invalid URI '%s': %w", flags.manifestPointer, err)
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
	return strings.HasPrefix(hostname, "raw.") ||
		strings.HasPrefix(hostname, "api.") ||
		strings.Contains(hostname, "github")
}

func (a *InitAction) downloadAgentYaml(
	ctx context.Context, manifestPointer string, targetDir string) (map[string]interface{}, string, error) {
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
	} else if a.isGitHubUrl(manifestPointer) {
		// Handle GitHub URLs using downloadGithubManifest
		fmt.Printf("Downloading agent.yaml from GitHub: %s\n", manifestPointer)

		// Create a simple console and command runner for GitHub CLI
		commandRunner := exec.NewCommandRunner(&exec.RunnerOptions{
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		})

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

		ghCli, err := github.NewGitHubCli(ctx, console, commandRunner)
		if err != nil {
			return nil, "", fmt.Errorf("creating GitHub CLI: %w", err)
		}

		urlInfo, err := parseGitHubUrl(manifestPointer)
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

		// Parse the YAML content into a map
		var yamlData map[string]interface{}
		if err := yaml.Unmarshal(content, &yamlData); err != nil {
			return nil, "", fmt.Errorf("parsing YAML content: %w", err)
		}

		agentKind, ok := yamlData["kind"].(string)
		if !ok {
			return nil, "", fmt.Errorf("missing or invalid 'kind' field in YAML content")
		}

		if agentKind == "hosted" {
			// For hosted agents, download the entire parent directory
			agentId, ok := yamlData["id"].(string)
			if !ok {
				return nil, "", fmt.Errorf("missing or invalid 'id' field in YAML content")
			}

			// Use targetDir if provided or set to local file pointer, otherwise default to "src/{agentId}"
			if targetDir == "" {
				targetDir = filepath.Join("src", agentId)
			}

			// Create target directory if it doesn't exist
			if err := os.MkdirAll(targetDir, 0755); err != nil {
				return nil, "", fmt.Errorf("creating target directory %s: %w", targetDir, err)
			}

			err := downloadParentDirectory(ctx, urlInfo, targetDir, ghCli, console)
			if err != nil {
				return nil, "", fmt.Errorf("downloading parent directory: %w", err)
			}
		}
	}

	// Parse the YAML content into a map
	var yamlData map[string]interface{}
	if err := yaml.Unmarshal(content, &yamlData); err != nil {
		return nil, "", fmt.Errorf("parsing YAML content: %w", err)
	}

	agentId, ok := yamlData["id"].(string)
	if !ok {
		return nil, "", fmt.Errorf("missing or invalid 'id' field in YAML content")
	}

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
	return yamlData, targetDir, nil
}

func (a *InitAction) addToProject(ctx context.Context, targetDir string, agentId string, agentKind string) error {
	var host string
	switch agentKind {
	case "container":
		host = "containerapp"
	default:
		host = "foundry.agent"
	}

	serviceConfig := &azdext.ServiceConfig{
		Name:         agentId,
		RelativePath: targetDir,
		Host:         host,
		Language:     "python",
	}

	req := &azdext.AddServiceRequest{Service: serviceConfig}

	if _, err := a.azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding agent service to project: %w", err)
	}

	fmt.Printf("Added service '%s' to azure.yaml\n", agentId)
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

func (a *InitAction) updateEnvironment(ctx context.Context, agentKind string, agentModelName string) error {
	fmt.Printf("Updating environment variables for agent kind: %s\n", agentKind)

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
	switch agentKind {
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
	if err := a.setEnvVar(ctx, envName, "AZURE_AI_FOUNDRY_MODEL_NAME", agentModelName); err != nil {
		return err
	}

	fmt.Printf("Successfully updated environment variables for agent kind: %s\n", agentKind)
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
