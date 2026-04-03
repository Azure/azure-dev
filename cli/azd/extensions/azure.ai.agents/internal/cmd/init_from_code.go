// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	posixpath "path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

type InitFromCodeAction struct {
	azdClient         *azdext.AzdClient
	flags             *initFlags
	projectConfig     *azdext.ProjectConfig
	azureContext      *azdext.AzureContext
	environment       *azdext.Environment
	credential        azcore.TokenCredential
	deploymentDetails []project.Deployment
	httpClient        *http.Client
}

// templateFileInfo represents a file from the GitHub template repository.
type templateFileInfo struct {
	Path     string // Relative path in the repo
	URL      string // Download URL for the file content
	Collides bool   // Whether the file already exists locally
}

func (a *InitFromCodeAction) Run(ctx context.Context) error {
	var err error
	a.projectConfig, err = a.ensureProject(ctx)
	if err != nil {
		return err
	}

	a.azureContext = &azdext.AzureContext{
		Scope:     &azdext.AzureScope{},
		Resources: []string{},
	}

	// If src path is absolute, convert it to relative path compared to the azd project path
	if a.flags.src != "" && filepath.IsAbs(a.flags.src) {
		projectResponse, err := a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return fmt.Errorf("failed to get project path: %w", err)
		}

		relPath, err := filepath.Rel(projectResponse.Project.Path, a.flags.src)
		if err != nil {
			return fmt.Errorf("failed to convert src path to relative path: %w", err)
		}
		a.flags.src = relPath
	}

	// No manifest pointer provided - process local agent code
	// Create a definition based on user prompts
	localDefinition, err := a.createDefinitionFromLocalAgent(ctx)
	if err != nil {
		return fmt.Errorf("failed to create definition from local agent: %w", err)
	}

	if localDefinition != nil {
		// Default src to current directory when not specified
		srcDir := a.flags.src
		if srcDir == "" {
			srcDir = "."
		}

		// Write the definition to a file in the src directory
		_, err := a.writeDefinitionToSrcDir(localDefinition, srcDir)
		if err != nil {
			return fmt.Errorf("failed to write definition to src directory: %w", err)
		}

		// Add the agent to the azd project (azure.yaml) services
		if err := a.addToProject(ctx, srcDir, localDefinition.Name); err != nil {
			return fmt.Errorf("failed to add agent to azure.yaml: %w", err)
		}

		if srcDir == "." {
			fmt.Printf("  %s  %s\n", color.GreenString("+"), color.GreenString("agent.yaml"))
		} else {
			fmt.Printf("  %s  %s\n", color.GreenString("+"), color.GreenString("%s/agent.yaml", srcDir))
		}

		fmt.Println("\nYou can customize environment variables, cpu, memory, and replica settings in the agent.yaml.")
		if projectID, _ := a.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: a.environment.Name,
			Key:     "AZURE_AI_PROJECT_ID",
		}); projectID != nil && projectID.Value != "" {
			fmt.Printf("Next steps: Run %s to deploy your agent to Microsoft Foundry.\n",
				color.HiBlueString("azd deploy %s", localDefinition.Name))
		} else {
			fmt.Printf("Next steps: Run %s to deploy your agent to Microsoft Foundry.\n",
				color.HiBlueString("azd up"))
		}
	}

	return nil
}

func (a *InitFromCodeAction) ensureProject(ctx context.Context) (*azdext.ProjectConfig, error) {
	projectResponse, err := a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Println("Let's get your project initialized.")

		if err := a.scaffoldTemplate(ctx, a.azdClient, "Azure-Samples/azd-ai-starter-basic", "main"); err != nil {
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("project initialization was cancelled")
			}
			return nil, exterrors.Dependency(
				exterrors.CodeScaffoldTemplateFailed,
				fmt.Sprintf("failed to scaffold template: %s", err),
				"",
			)
		}

		projectResponse, err = a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return nil, exterrors.Dependency(
				exterrors.CodeProjectNotFound,
				fmt.Sprintf("failed to get project after initialization: %s", err),
				"",
			)
		}

		fmt.Println()
	}

	if projectResponse.Project == nil {
		return nil, exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			"project not found",
			"",
		)
	}

	return projectResponse.Project, nil
}

// gitHubToken returns a GitHub personal access token from the environment, if available.
// It checks GITHUB_TOKEN first, then GH_TOKEN.
func gitHubToken() string {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	return os.Getenv("GH_TOKEN")
}

// setGitHubAuthHeader adds an Authorization header to the request if a GitHub token
// is available in the environment. This raises the rate limit from 60 to 5,000 requests/hour.
func setGitHubAuthHeader(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// scaffoldTemplate downloads a GitHub template repo into the current directory,
// checking for file collisions before writing. Files that don't collide are shown
// in green; colliding files are shown in yellow and the user is prompted for how
// to handle them.
func (a *InitFromCodeAction) scaffoldTemplate(ctx context.Context, azdClient *azdext.AzdClient, repoSlug string, branch string) error {
	// 1. Fetch the recursive file tree from GitHub
	ghToken := gitHubToken()

	apiUrl := fmt.Sprintf("https://api.github.com/repos/%s/git/trees/%s?recursive=1", repoSlug, branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiUrl, nil)
	if err != nil {
		return fmt.Errorf("creating tree request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	setGitHubAuthHeader(req, ghToken)

	//nolint:gosec // URL is explicitly constructed for GitHub API tree endpoint
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching repo tree: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf(
				"fetching repo tree: status %d (GitHub API rate limit may have been exceeded; "+
					"set GITHUB_TOKEN or GH_TOKEN environment variable to increase the limit)",
				resp.StatusCode,
			)
		}
		return fmt.Errorf("fetching repo tree: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading tree response: %w", err)
	}

	var treeResp struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"` // "blob" or "tree"
		} `json:"tree"`
	}
	if err := json.Unmarshal(body, &treeResp); err != nil {
		return fmt.Errorf("parsing tree response: %w", err)
	}

	// Collect only files (blobs) from the infra folder and azure.yaml
	var files []templateFileInfo
	for _, entry := range treeResp.Tree {
		if entry.Type != "blob" {
			continue
		}
		// Only include files in the infra folder or the azure.yaml file
		if !strings.HasPrefix(entry.Path, "infra/") && entry.Path != "azure.yaml" {
			continue
		}
		// Guard against path traversal or unexpected absolute paths.
		// Use posixpath (path) for URL-safe cleaning since GitHub returns forward-slash paths.
		cleanPath := posixpath.Clean(entry.Path)
		if posixpath.IsAbs(cleanPath) || strings.HasPrefix(cleanPath, "..") {
			return fmt.Errorf("invalid path in repository tree: %s", entry.Path)
		}
		downloadURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repoSlug, branch, cleanPath)
		collides := false
		if _, statErr := os.Stat(filepath.FromSlash(cleanPath)); statErr == nil {
			collides = true
		}
		files = append(files, templateFileInfo{
			Path:     cleanPath,
			URL:      downloadURL,
			Collides: collides,
		})
	}

	if len(files) == 0 {
		return fmt.Errorf("template repository %s has no files", repoSlug)
	}

	// Sort by path for consistent display
	slices.SortFunc(files, func(a, b templateFileInfo) int {
		return strings.Compare(a.Path, b.Path)
	})

	// 2. Classify into new and colliding
	var newFiles, collidingFiles []templateFileInfo
	for _, f := range files {
		if f.Collides {
			collidingFiles = append(collidingFiles, f)
		} else {
			newFiles = append(newFiles, f)
		}
	}

	// 3. Display the file list
	fmt.Print("\nThe following files will be created from the starter template:\n\n")
	for _, f := range files {
		if f.Collides {
			fmt.Printf("  %s  %s\n", color.YellowString("!"), color.YellowString(f.Path))
		} else {
			fmt.Printf("  %s  %s\n", color.GreenString("+"), color.GreenString(f.Path))
		}
	}
	fmt.Println()

	// 4. If there are collisions, show warning and prompt for resolution
	overwriteCollisions := false
	if len(collidingFiles) > 0 {
		fmt.Printf("%s %d file(s) already exist and would be overwritten.\n\n",
			color.YellowString("Warning:"), len(collidingFiles))

		conflictChoices := []*azdext.SelectChoice{
			{Label: "Overwrite existing files", Value: "overwrite"},
			{Label: "Skip existing files (keep my versions)", Value: "skip"},
			{Label: "Cancel", Value: "cancel"},
		}

		conflictResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: "How would you like to handle existing files?",
				Choices: conflictChoices,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for conflict resolution: %w", err)
		}

		selectedValue := conflictChoices[*conflictResp.Value].Value
		switch selectedValue {
		case "overwrite":
			overwriteCollisions = true
		case "skip":
			overwriteCollisions = false
		case "cancel":
			return fmt.Errorf("operation cancelled, no changes were made")
		}
	} else {
		// No collisions - confirm to proceed
		confirmResp, err := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      "Initialize the starter template?",
				DefaultValue: new(true),
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for confirmation: %w", err)
		}
		if !*confirmResp.Value {
			return fmt.Errorf("operation cancelled, no changes were made")
		}
	}

	// 5. Download and write files
	filesToWrite := newFiles
	if overwriteCollisions {
		filesToWrite = files
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:        fmt.Sprintf("Downloading template (%d files)...", len(filesToWrite)),
		ClearOnStop: true,
	})
	if err := spinner.Start(ctx); err != nil {
		return fmt.Errorf("starting spinner: %w", err)
	}

	for _, f := range filesToWrite {
		localPath := filepath.FromSlash(f.Path)

		// Create parent directories
		dir := filepath.Dir(localPath)
		if dir != "." {
			//nolint:gosec // scaffolded directories are intended to be readable/traversable
			if err := os.MkdirAll(dir, 0755); err != nil {
				_ = spinner.Stop(ctx)
				return fmt.Errorf("creating directory %s: %w", dir, err)
			}
		}

		// Download file content
		fileReq, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
		if err != nil {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("creating request for %s: %w", f.Path, err)
		}
		setGitHubAuthHeader(fileReq, ghToken)

		//nolint:gosec // URL is from GitHub tree API entries for the selected template
		fileResp, err := a.httpClient.Do(fileReq)
		if err != nil {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("downloading %s: %w", f.Path, err)
		}

		if fileResp.StatusCode != http.StatusOK {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("downloading %s: status %d", f.Path, fileResp.StatusCode)
		}

		content, err := io.ReadAll(fileResp.Body)
		_ = fileResp.Body.Close()
		if err != nil {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("reading %s: %w", f.Path, err)
		}

		//nolint:gosec // scaffolded files should remain readable by project tooling
		if err := os.WriteFile(localPath, content, 0644); err != nil {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("writing %s: %w", localPath, err)
		}
	}

	if err := spinner.Stop(ctx); err != nil {
		return fmt.Errorf("stopping spinner: %w", err)
	}

	skipped := len(files) - len(filesToWrite)
	if skipped > 0 {
		fmt.Printf("  Template initialized: %d file(s) written, %d file(s) skipped.\n", len(filesToWrite), skipped)
	} else {
		fmt.Printf("  Template initialized: %d file(s) written.\n", len(filesToWrite))
	}

	return nil
}

// createDefinitionFromLocalAgent creates a ContainerAgent for local agent code
// This is used when no manifest pointer is provided and we need to scaffold a new agent
func (a *InitFromCodeAction) createDefinitionFromLocalAgent(ctx context.Context) (*agent_yaml.ContainerAgent, error) {
	// Default agent name to sanitized cwd
	defaultName := "my-agent"
	if cwd, err := os.Getwd(); err == nil {
		defaultName = sanitizeAgentName(filepath.Base(cwd))
	}

	// Prompt user for agent name
	promptResp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "Enter a name for your agent",
			DefaultValue: defaultName,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("agent name prompt was cancelled")
		}
		return nil, fmt.Errorf("failed to prompt for agent name: %w", err)
	}
	agentName := promptResp.Value

	// Create the azd environment now that we have the agent name
	if a.environment == nil {
		envName := sanitizeAgentName(agentName + "-dev")
		env, err := createNewEnvironment(ctx, a.azdClient, envName)
		if err != nil {
			return nil, fmt.Errorf("failed to create azd environment: %w", err)
		}
		a.environment = env
		a.flags.env = envName
		fmt.Printf("  %s  %s\n", color.GreenString("+"), color.GreenString(".azure/%s/.env", envName))
	}

	// TODO: Prompt user for agent kind
	agentKind := agent_yaml.AgentKindHosted

	// Prompt user for supported protocols
	protocols, err := promptProtocols(ctx, a.azdClient.Prompt(), a.flags.NoPrompt, a.flags.protocols)
	if err != nil {
		return nil, err
	}

	// Ask user how they want to configure a model
	modelConfigChoices := []*azdext.SelectChoice{
		{Label: "Deploy a new model from the catalog", Value: "new"},
		{Label: "Select an existing model deployment from a Foundry project", Value: "existing"},
		{Label: "Skip model configuration", Value: "skip"},
	}

	var modelConfigChoice string
	if a.flags.projectResourceId == "" {
		defaultIndex := int32(0)
		modelConfigResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "How would you like to configure a model for your agent?",
				Choices:       modelConfigChoices,
				SelectedIndex: &defaultIndex,
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("model configuration choice was cancelled")
			}
			return nil, fmt.Errorf("failed to prompt for model configuration choice: %w", err)
		}
		modelConfigChoice = modelConfigChoices[*modelConfigResp.Value].Value
	} else {
		// If projectResourceId is provided, skip the prompt and default to existing deployment selection
		modelConfigChoice = "existing"

		a.azureContext.Scope.SubscriptionId = extractSubscriptionId(a.flags.projectResourceId)
	}

	var selectedModel *azdext.AiModel
	var existingDeployment *FoundryDeploymentInfo

	switch modelConfigChoice {
	case "new":
		// Path A: Deploy a new model from the catalog
		// Need subscription + location for model catalog
		newCred, err := ensureSubscriptionAndLocation(
			ctx, a.azdClient, a.azureContext, a.environment.Name,
			"Select an Azure subscription to look up available models and provision your Foundry project resources.",
		)
		if err != nil {
			return nil, err
		}
		a.credential = newCred

		selectedModel, err = selectNewModel(ctx, a.azdClient, a.azureContext, a.flags.model)
		if err != nil {
			return nil, fmt.Errorf("failed to select new model: %w", err)
		}

	case "existing":
		// Path B: Select an existing model deployment from a Foundry project
		// Need subscription to enumerate projects
		newCred, err := ensureSubscription(
			ctx, a.azdClient, a.azureContext, a.environment.Name,
			"Select an Azure subscription to look up available models and provision your Foundry project resources.",
		)
		if err != nil {
			return nil, err
		}
		a.credential = newCred

		// Select a Foundry project
		selectedProject, err := selectFoundryProject(ctx, a.azdClient, a.credential, a.azureContext, a.environment.Name, a.azureContext.Scope.SubscriptionId, a.flags.projectResourceId)
		if err != nil {
			return nil, err
		}

		if selectedProject == nil {
			// No projects found or user chose "Create new" → fall back to new model
			if a.azureContext.Scope.Location == "" {
				if err := ensureLocation(ctx, a.azdClient, a.azureContext, a.environment.Name); err != nil {
					return nil, err
				}
			}
			selectedModel, err = selectNewModel(ctx, a.azdClient, a.azureContext, a.flags.model)
			if err != nil {
				return nil, fmt.Errorf("failed to select new model: %w", err)
			}
		} else {
			// Select a deployment from the project
			deployment, err := selectModelDeployment(ctx, a.azdClient, a.credential, *selectedProject, a.flags.modelDeployment, "")
			if err != nil {
				return nil, err
			}

			if deployment != nil {
				existingDeployment = deployment
			} else {
				// User wants to create a new deployment — region locked to the project's location
				selectedModel, err = selectNewModel(ctx, a.azdClient, a.azureContext, a.flags.model)
				if err != nil {
					return nil, fmt.Errorf("failed to select new model: %w", err)
				}
			}
		}

	case "skip":
		// Path C: Skip model configuration entirely
	}

	// Create a minimal Agent Definition
	definition := &agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Name: agentName,
			Kind: agentKind,
		},
		Protocols: protocols,
		EnvironmentVariables: &[]agent_yaml.EnvironmentVariable{
			{
				Name:  "AZURE_OPENAI_ENDPOINT",
				Value: "${AZURE_OPENAI_ENDPOINT}",
			},
			{
				Name:  "AZURE_AI_PROJECT_ENDPOINT",
				Value: "${AZURE_AI_PROJECT_ENDPOINT}",
			},
		},
	}

	// Add model resource if a model was selected
	if existingDeployment != nil {
		// For existing deployments, store the deployment details directly
		a.deploymentDetails = append(a.deploymentDetails, project.Deployment{
			Name: existingDeployment.Name,
			Model: project.DeploymentModel{
				Name:    existingDeployment.ModelName,
				Format:  existingDeployment.ModelFormat,
				Version: existingDeployment.Version,
			},
			Sku: project.DeploymentSku{
				Name:     existingDeployment.SkuName,
				Capacity: existingDeployment.SkuCapacity,
			},
		})

		*definition.EnvironmentVariables = append(*definition.EnvironmentVariables, agent_yaml.EnvironmentVariable{
			Name:  "AZURE_AI_MODEL_DEPLOYMENT_NAME",
			Value: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		})

		if err := setEnvValue(ctx, a.azdClient, a.environment.Name, "AZURE_AI_MODEL_DEPLOYMENT_NAME", existingDeployment.Name); err != nil {
			return nil, fmt.Errorf("failed to set AZURE_AI_MODEL_DEPLOYMENT_NAME: %w", err)
		}
	} else if selectedModel != nil {
		modelDetails, err := resolveModelDeployment(ctx, a.azdClient, a.azureContext, selectedModel, a.azureContext.Scope.Location)
		if err != nil {
			return nil, fmt.Errorf("failed to get model deployment details: %w", err)
		}

		a.deploymentDetails = append(a.deploymentDetails, project.Deployment{
			Name: modelDetails.ModelName,
			Model: project.DeploymentModel{
				Name:    modelDetails.ModelName,
				Format:  modelDetails.Format,
				Version: modelDetails.Version,
			},
			Sku: project.DeploymentSku{
				Name:     modelDetails.Sku.Name,
				Capacity: int(modelDetails.Capacity),
			},
		})

		*definition.EnvironmentVariables = append(*definition.EnvironmentVariables, agent_yaml.EnvironmentVariable{
			Name:  "AZURE_AI_MODEL_DEPLOYMENT_NAME",
			Value: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		})

		if err := setEnvValue(ctx, a.azdClient, a.environment.Name, "AZURE_AI_MODEL_DEPLOYMENT_NAME", modelDetails.ModelName); err != nil {
			return nil, fmt.Errorf("failed to set AZURE_AI_MODEL_DEPLOYMENT_NAME: %w", err)
		}
	}

	return definition, nil
}

// sanitizeAgentName converts a string into a valid agent name:
// lowercase, replace non-alphanumeric with hyphens, collapse consecutive hyphens,
// strip leading/trailing hyphens, truncate to 63 chars.
func sanitizeAgentName(name string) string {
	name = strings.ToLower(name)
	// Replace any character that isn't a-z, 0-9, or hyphen with a hyphen
	re := regexp.MustCompile(`[^a-z0-9-]+`)
	name = re.ReplaceAllString(name, "-")
	// Collapse consecutive hyphens
	re = regexp.MustCompile(`-{2,}`)
	name = re.ReplaceAllString(name, "-")
	// Strip leading/trailing hyphens
	name = strings.Trim(name, "-")
	// Truncate to 63 chars
	if len(name) > 63 {
		name = name[:63]
		name = strings.TrimRight(name, "-")
	}
	if name == "" {
		name = "my-agent"
	}
	return name
}

// normalizeForFuzzyMatch strips common separator characters (hyphens, dots, spaces, underscores)
// and lowercases the string for fuzzy comparison.
func normalizeForFuzzyMatch(s string) string {
	s = strings.ToLower(s)
	re := regexp.MustCompile(`[-.\s_]+`)
	return re.ReplaceAllString(s, "")
}

// fuzzyFilterModels filters model names by a search term using normalized comparison.
// The search term and model names both have separators stripped before matching.
func fuzzyFilterModels(modelNames []string, searchTerm string) []string {
	if searchTerm == "" {
		return modelNames
	}
	normalizedSearch := normalizeForFuzzyMatch(searchTerm)
	if normalizedSearch == "" {
		return modelNames
	}

	// Build a regex pattern from the normalized search term
	pattern, err := regexp.Compile("(?i)" + regexp.QuoteMeta(normalizedSearch))
	if err != nil {
		// Fallback to simple contains if regex fails
		var matches []string
		for _, name := range modelNames {
			if strings.Contains(normalizeForFuzzyMatch(name), normalizedSearch) {
				matches = append(matches, name)
			}
		}
		return matches
	}

	var matches []string
	for _, name := range modelNames {
		if pattern.MatchString(normalizeForFuzzyMatch(name)) {
			matches = append(matches, name)
		}
	}
	return matches
}

// findDefaultModelIndex finds the index of gpt-4o in a sorted model list,
// falling back to the first gpt-4 match, or 0.
func findDefaultModelIndex(modelNames []string) int32 {
	// Look for exact gpt-4o first
	for i, name := range modelNames {
		if name == "gpt-4o" {
			return int32(i)
		}
	}
	// Fall back to first gpt-4 match
	for i, name := range modelNames {
		if strings.HasPrefix(name, "gpt-4") {
			return int32(i)
		}
	}
	return 0
}

// writeDefinitionToSrcDir writes a ContainerAgent to a YAML file in the src directory and returns the path
func (a *InitFromCodeAction) writeDefinitionToSrcDir(definition *agent_yaml.ContainerAgent, srcDir string) (string, error) {
	// Ensure the src directory exists
	//nolint:gosec // scaffold directory should be readable/traversable for project tools
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		return "", fmt.Errorf("creating src directory: %w", err)
	}

	// Create the definition file path
	definitionPath := filepath.Join(srcDir, "agent.yaml")

	// Marshal the definition to YAML
	content, err := yaml.Marshal(definition)
	if err != nil {
		return "", fmt.Errorf("marshaling definition to YAML: %w", err)
	}

	// Write to the file
	//nolint:gosec // generated manifest file should be readable by tooling and users
	if err := os.WriteFile(definitionPath, content, 0644); err != nil {
		return "", fmt.Errorf("writing definition to file: %w", err)
	}

	return definitionPath, nil
}

func (a *InitFromCodeAction) addToProject(ctx context.Context, targetDir string, agentName string) error {
	var agentConfig = project.ServiceTargetAgentConfig{}

	agentConfig.Container = &project.ContainerSettings{
		Resources: &project.ResourceSettings{
			Memory: project.DefaultMemory,
			Cpu:    project.DefaultCpu,
		},
	}

	if !isVNextEnabled(ctx, a.azdClient) {
		agentConfig.Container.Scale = &project.ScaleSettings{
			MinReplicas: project.DefaultMinReplicas,
			MaxReplicas: project.DefaultMaxReplicas,
		}
	}

	agentConfig.Deployments = a.deploymentDetails

	// Detect startup command from the project source directory
	startupCmd, err := resolveStartupCommandForInit(ctx, a.azdClient, a.projectConfig.Path, targetDir, a.flags.NoPrompt)
	if err != nil {
		return err
	}
	agentConfig.StartupCommand = startupCmd

	var agentConfigStruct *structpb.Struct
	if agentConfigStruct, err = project.MarshalStruct(&agentConfig); err != nil {
		return fmt.Errorf("failed to marshal agent config: %w", err)
	}

	serviceConfig := &azdext.ServiceConfig{
		Name:         strings.ReplaceAll(agentName, " ", ""),
		RelativePath: targetDir,
		Host:         AiAgentHost,
		Language:     "docker",
		Config:       agentConfigStruct,
	}

	// For hosted (container-based) agents, set remoteBuild to true by default
	serviceConfig.Docker = &azdext.DockerProjectOptions{
		RemoteBuild: true,
	}

	req := &azdext.AddServiceRequest{Service: serviceConfig}

	if _, err := a.azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding agent service to project: %w", err)
	}

	fmt.Printf("\nAdded your agent as a service entry named '%s' under the file azure.yaml.\n", agentName)
	return nil
}

// protocolInfo pairs a protocol name with the default version used when generating agent.yaml.
type protocolInfo struct {
	Name    string
	Version string
}

// knownProtocols lists the protocols offered during init, in display order.
var knownProtocols = []protocolInfo{
	{Name: "responses", Version: "v1"},
	{Name: "invocations", Version: "v0.0.1"},
}

// promptProtocols asks the user which protocols their agent supports.
// When flagProtocols is non-empty the prompt is skipped and those values are used directly.
// When noPrompt is true and no flag values are provided, defaults to [responses/v1].
func promptProtocols(
	ctx context.Context,
	promptClient azdext.PromptServiceClient,
	noPrompt bool,
	flagProtocols []string,
) ([]agent_yaml.ProtocolVersionRecord, error) {
	// Build a lookup from protocol name → version for known protocols.
	versionOf := make(map[string]string, len(knownProtocols))
	for _, p := range knownProtocols {
		versionOf[p.Name] = p.Version
	}

	// If explicit flag values were provided, use them directly (with dedup).
	if len(flagProtocols) > 0 {
		seen := make(map[string]bool, len(flagProtocols))
		records := make([]agent_yaml.ProtocolVersionRecord, 0, len(flagProtocols))
		for _, name := range flagProtocols {
			if seen[name] {
				continue
			}
			seen[name] = true

			version, ok := versionOf[name]
			if !ok {
				return nil, exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					fmt.Sprintf("unknown protocol %q; supported values: %s",
						name, knownProtocolNames()),
					fmt.Sprintf("Use one of the supported protocol values: %s", knownProtocolNames()),
				)
			}
			records = append(records, agent_yaml.ProtocolVersionRecord{
				Protocol: name,
				Version:  version,
			})
		}
		return records, nil
	}

	// Non-interactive mode: default to responses.
	if noPrompt {
		return []agent_yaml.ProtocolVersionRecord{
			{Protocol: "responses", Version: "v1"},
		}, nil
	}

	// Build multi-select choices; "responses" is pre-selected.
	choices := make([]*azdext.MultiSelectChoice, 0, len(knownProtocols))
	for _, p := range knownProtocols {
		choices = append(choices, &azdext.MultiSelectChoice{
			Value:    p.Name,
			Label:    p.Name,
			Selected: p.Name == "responses",
		})
	}

	resp, err := promptClient.MultiSelect(ctx, &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message:     "Which protocols does your agent support?",
			Choices:     choices,
			HelpMessage: "Use arrow keys to move, space to toggle, enter to confirm",
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("protocol selection was cancelled")
		}
		return nil, fmt.Errorf("failed to prompt for protocols: %w", err)
	}

	// Collect selected protocols.
	var records []agent_yaml.ProtocolVersionRecord
	for _, choice := range resp.Values {
		if choice.Selected {
			version, ok := versionOf[choice.Value]
			if !ok {
				return nil, exterrors.Internal(
					"prompt_protocols",
					fmt.Sprintf("unexpected protocol %q returned from prompt", choice.Value),
				)
			}
			records = append(records, agent_yaml.ProtocolVersionRecord{
				Protocol: choice.Value,
				Version:  version,
			})
		}
	}

	if len(records) == 0 {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			"at least one protocol must be selected",
			"Select at least one protocol for your agent.",
		)
	}

	return records, nil
}

// knownProtocolNames returns a comma-separated list of known protocol names.
func knownProtocolNames() string {
	names := make([]string, 0, len(knownProtocols))
	for _, p := range knownProtocols {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}
