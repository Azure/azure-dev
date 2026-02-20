// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"
	"azureaiagent/internal/project"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
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
	modelCatalog      map[string]*azdext.AiModel
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
		return fmt.Errorf("failed to ensure project: %w", err)
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
		if err := a.addToProject(ctx, srcDir, localDefinition.Name, a.flags.host); err != nil {
			return fmt.Errorf("failed to add agent to azure.yaml: %w", err)
		}

		if srcDir == "." {
			fmt.Printf("  %s  %s\n", color.GreenString("+"), color.GreenString("agent.yaml"))
		} else {
			fmt.Printf("  %s  %s\n", color.GreenString("+"), color.GreenString("%s/agent.yaml", srcDir))
		}

		fmt.Println("\nYou can customize environment variables, cpu, memory, and replica settings in the agent.yaml.")
		fmt.Printf("Next steps: Run %s to deploy your agent to Microsoft Foundry.\n", color.HiBlueString("azd up"))
	}

	return nil
}

func (a *InitFromCodeAction) ensureProject(ctx context.Context) (*azdext.ProjectConfig, error) {
	projectResponse, err := a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Println("Lets get your project initialized.")

		if err := a.scaffoldTemplate(ctx, a.azdClient, "Azure-Samples/azd-ai-starter-basic", "trangevi/existing-acr"); err != nil {
			return nil, fmt.Errorf("failed to scaffold template: %w", err)
		}

		projectResponse, err = a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
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

// scaffoldTemplate downloads a GitHub template repo into the current directory,
// checking for file collisions before writing. Files that don't collide are shown
// in green; colliding files are shown in yellow and the user is prompted for how
// to handle them.
func (a *InitFromCodeAction) scaffoldTemplate(ctx context.Context, azdClient *azdext.AzdClient, repoSlug string, branch string) error {
	// 1. Fetch the recursive file tree from GitHub
	apiUrl := fmt.Sprintf("https://api.github.com/repos/%s/git/trees/%s?recursive=1", repoSlug, branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiUrl, nil)
	if err != nil {
		return fmt.Errorf("creating tree request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching repo tree: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
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
		downloadURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repoSlug, branch, entry.Path)
		collides := false
		if _, statErr := os.Stat(entry.Path); statErr == nil {
			collides = true
		}
		files = append(files, templateFileInfo{
			Path:     entry.Path,
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
				DefaultValue: to.Ptr(true),
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
		// Create parent directories
		dir := filepath.Dir(f.Path)
		if dir != "." {
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

		fileResp, err := a.httpClient.Do(fileReq)
		if err != nil {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("downloading %s: %w", f.Path, err)
		}

		content, err := io.ReadAll(fileResp.Body)
		fileResp.Body.Close()
		if err != nil {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("reading %s: %w", f.Path, err)
		}

		if fileResp.StatusCode != http.StatusOK {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("downloading %s: status %d", f.Path, fileResp.StatusCode)
		}

		if err := os.WriteFile(f.Path, content, 0644); err != nil {
			_ = spinner.Stop(ctx)
			return fmt.Errorf("writing %s: %w", f.Path, err)
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
			Message:      "Enter a name for your agent:",
			DefaultValue: defaultName,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for agent name: %w", err)
	}
	agentName := promptResp.Value

	// Create the azd environment now that we have the agent name
	if a.environment == nil {
		if err := a.createEnvironment(ctx, agentName+"-dev"); err != nil {
			return nil, fmt.Errorf("failed to create azd environment: %w", err)
		}
	}

	// TODO: Prompt user for agent kind
	agentKind := agent_yaml.AgentKindHosted

	// Ask user how they want to configure a model
	modelConfigChoices := []*azdext.SelectChoice{
		{Label: "Deploy a new model from the catalog", Value: "new"},
		{Label: "Select an existing model deployment from a Foundry project", Value: "existing"},
		{Label: "Skip model configuration", Value: "skip"},
	}

	var modelConfigChoice string
	if a.flags.projectResourceId == "" {
		modelConfigResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: "How would you like to configure a model for your agent?",
				Choices: modelConfigChoices,
			},
		})
		if err != nil {
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
		if err := a.ensureSubscriptionAndLocation(ctx); err != nil {
			return nil, err
		}

		selectedModel, err = a.selectNewModel(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to select new model: %w", err)
		}

	case "existing":
		// Path B: Select an existing model deployment from a Foundry project
		// Need subscription to enumerate projects
		if err := a.ensureSubscription(ctx); err != nil {
			return nil, err
		}

		spinnerText := "Searching for Foundry projects in your subscription..."
		if a.flags.projectResourceId != "" {
			spinnerText = "Getting details on the provided Foundry project..."
		}

		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text:        spinnerText,
			ClearOnStop: true,
		})
		if err := spinner.Start(ctx); err != nil {
			return nil, fmt.Errorf("failed to start spinner: %w", err)
		}

		projects, err := a.listFoundryProjects(ctx, a.azureContext.Scope.SubscriptionId)
		if stopErr := spinner.Stop(ctx); stopErr != nil {
			return nil, stopErr
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list Foundry projects: %w", err)
		}

		if len(projects) == 0 {
			fmt.Println("No Foundry projects found in your subscription. Falling back to deploying a new model.")
			// Fall back to new model flow
			if err := a.ensureSubscriptionAndLocation(ctx); err != nil {
				return nil, err
			}

			selectedModel, err = a.selectNewModel(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to select new model: %w", err)
			}
		} else {
			var selectedIdx int32
			if a.flags.projectResourceId == "" {
				// Let user pick a Foundry project
				projectChoices := make([]*azdext.SelectChoice, len(projects)+1)
				for i, p := range projects {
					projectChoices[i] = &azdext.SelectChoice{
						Label: fmt.Sprintf("%s / %s (%s)", p.AccountName, p.ProjectName, p.Location),
						Value: fmt.Sprintf("%d", i),
					}
				}
				projectChoices = append(projectChoices, &azdext.SelectChoice{
					Label: "Create a new Foundry project",
					Value: "__create_new__",
				})

				projectResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
					Options: &azdext.SelectOptions{
						Message: "Select a Foundry project:",
						Choices: projectChoices,
					},
				})
				if err != nil {
					return nil, fmt.Errorf("failed to prompt for project selection: %w", err)
				}

				selectedIdx = *projectResp.Value
			} else {
				// If projectResourceId is provided, find the matching project and set selectedIdx accordingly
				selectedIdx = -1
				for i, p := range projects {
					if p.ResourceId == a.flags.projectResourceId {
						selectedIdx = int32(i)
						break
					}
				}
				if selectedIdx == -1 {
					return nil, fmt.Errorf("provided projectResourceId does not match any Foundry projects in the subscription")
				}
			}

			if selectedIdx < int32(len(projects)) {
				// User selected an existing Foundry project
				selectedProject := projects[selectedIdx]

				// Set the Foundry project context
				a.azureContext.Scope.Location = selectedProject.Location
				a.setEnvVar(ctx, "AZURE_LOCATION", selectedProject.Location)

				err := a.processExistingFoundryProject(ctx, selectedProject)
				if err != nil {
					return nil, fmt.Errorf("failed to set Foundry project context: %w", err)
				}

				spinner := ux.NewSpinner(&ux.SpinnerOptions{
					Text:        "Searching for model deployments in your Foundry Project...",
					ClearOnStop: true,
				})
				if err := spinner.Start(ctx); err != nil {
					return nil, fmt.Errorf("failed to start spinner: %w", err)
				}

				// List deployments in selected project
				deployments, err := a.listProjectDeployments(ctx, selectedProject.SubscriptionId, selectedProject.ResourceGroupName, selectedProject.AccountName)
				if stopErr := spinner.Stop(ctx); stopErr != nil {
					return nil, stopErr
				}
				if err != nil {
					return nil, fmt.Errorf("failed to list deployments: %w", err)
				}

				if len(deployments) == 0 {
					fmt.Println("No existing deployments found. You can create a new model deployment.")
				}

				// Build choices: existing deployments + "Create a new model deployment"
				deployChoices := make([]*azdext.SelectChoice, 0, len(deployments)+1)
				for _, d := range deployments {
					label := fmt.Sprintf("%s (%s v%s, %s)", d.Name, d.ModelName, d.Version, d.SkuName)
					deployChoices = append(deployChoices, &azdext.SelectChoice{
						Label: label,
						Value: d.Name,
					})
				}
				deployChoices = append(deployChoices, &azdext.SelectChoice{
					Label: "Create a new model deployment",
					Value: "__create_new__",
				})

				deployResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
					Options: &azdext.SelectOptions{
						Message: "Select a model deployment:",
						Choices: deployChoices,
					},
				})
				if err != nil {
					return nil, fmt.Errorf("failed to prompt for deployment selection: %w", err)
				}

				selectedIdx := *deployResp.Value
				if selectedIdx < int32(len(deployments)) {
					// User selected an existing deployment
					d := deployments[selectedIdx]
					existingDeployment = &d
					fmt.Printf("Model deployment name: %s\n", d.Name)
				} else {
					// User wants to create a new deployment — region locked to the project's location
					selectedModel, err = a.selectNewModel(ctx)
					if err != nil {
						return nil, fmt.Errorf("failed to select new model: %w", err)
					}
				}
			} else {
				// User wants a new Foundry project
				if err := a.ensureLocation(ctx); err != nil {
					return nil, err
				}

				selectedModel, err = a.selectNewModel(ctx)
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
		Protocols: []agent_yaml.ProtocolVersionRecord{
			{
				Protocol: "responses",
				Version:  "v1",
			},
		},
		EnvironmentVariables: &[]agent_yaml.EnvironmentVariable{
			{
				Name:  "AZURE_OPENAI_ENDPOINT",
				Value: "${AZURE_OPENAI_ENDPOINT}",
			},
		},
	}

	// Add model resource if a model was selected
	if selectedModel != nil {
		if existingDeployment == nil {
			modelDetails, err := a.resolveModelDeploymentNoPrompt(ctx, selectedModel, a.azureContext.Scope.Location)
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
				Value: modelDetails.ModelName,
			})
		} else {
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
				Value: existingDeployment.Name,
			})
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

// createEnvironment creates a new azd environment with the given name and sets
// it on the InitFromCodeAction so subsequent calls can use it.
func (a *InitFromCodeAction) createEnvironment(ctx context.Context, envName string) error {
	envName = sanitizeAgentName(envName)

	workflow := &azdext.Workflow{
		Name: "env new",
		Steps: []*azdext.WorkflowStep{
			{Command: &azdext.WorkflowCommand{Args: []string{"env", "new", envName}}},
		},
	}

	_, err := a.azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
		Workflow: workflow,
	})
	if err != nil {
		return fmt.Errorf("failed to create environment %s: %w", envName, err)
	}

	fmt.Printf("  %s  %s\n", color.GreenString("+"), color.GreenString(".azure/%s/.env", envName))

	a.flags.env = envName
	env := getExistingEnvironment(ctx, a.flags, a.azdClient)
	if env == nil {
		return fmt.Errorf("environment %s was created but could not be found", envName)
	}

	a.environment = env
	return nil
}

// ensureSubscriptionAndLocation prompts for subscription and location if not already set,
// with messaging that explains these are needed for model lookup and Foundry project resources.
func (a *InitFromCodeAction) ensureSubscriptionAndLocation(ctx context.Context) error {
	if a.azureContext.Scope.SubscriptionId == "" {
		err := a.ensureSubscription(ctx)
		if err != nil {
			return err
		}
	}

	if a.azureContext.Scope.Location == "" {
		err := a.ensureLocation(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

// ensureSubscription prompts for subscription only if not already set.
func (a *InitFromCodeAction) ensureSubscription(ctx context.Context) error {
	if a.azureContext.Scope.SubscriptionId == "" {
		fmt.Println("Select an Azure subscription to look up available models and provision your Foundry project resources.")

		subscriptionResponse, err := a.azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
		if err != nil {
			return fmt.Errorf("failed to prompt for subscription: %w", err)
		}

		a.azureContext.Scope.SubscriptionId = subscriptionResponse.Subscription.Id
		a.azureContext.Scope.TenantId = subscriptionResponse.Subscription.TenantId
	} else {
		tenantResponse, err := a.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
			SubscriptionId: a.azureContext.Scope.SubscriptionId,
		})
		if err != nil {
			return fmt.Errorf("failed to lookup tenant: %w", err)
		}
		a.azureContext.Scope.TenantId = tenantResponse.TenantId
	}

	// Persist to environment
	_, err := a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: a.environment.Name,
		Key:     "AZURE_SUBSCRIPTION_ID",
		Value:   a.azureContext.Scope.SubscriptionId,
	})
	if err != nil {
		return fmt.Errorf("failed to set AZURE_SUBSCRIPTION_ID in environment: %w", err)
	}

	_, err = a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: a.environment.Name,
		Key:     "AZURE_TENANT_ID",
		Value:   a.azureContext.Scope.TenantId,
	})
	if err != nil {
		return fmt.Errorf("failed to set AZURE_TENANT_ID in environment: %w", err)
	}

	// Refresh credential with the tenant
	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   a.azureContext.Scope.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return fmt.Errorf("failed to create azure credential: %w", err)
	}
	a.credential = credential

	return nil
}

func (a *InitFromCodeAction) ensureLocation(ctx context.Context) error {
	fmt.Println("Select an Azure location. This determines which models are available and where your Foundry project resources will be deployed.")

	locationResponse, err := a.azdClient.Prompt().PromptLocation(ctx, &azdext.PromptLocationRequest{
		AzureContext: a.azureContext,
	})
	if err != nil {
		return fmt.Errorf("failed to prompt for location: %w", err)
	}

	a.azureContext.Scope.Location = locationResponse.Location.Name

	_, err = a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: a.environment.Name,
		Key:     "AZURE_LOCATION",
		Value:   a.azureContext.Scope.Location,
	})
	if err != nil {
		return fmt.Errorf("failed to set AZURE_LOCATION in environment: %w", err)
	}

	return nil
}

func (a *InitFromCodeAction) selectNewModel(ctx context.Context) (*azdext.AiModel, error) {
	var err error
	if a.modelCatalog == nil {
		spinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text:        "Loading model catalog...",
			ClearOnStop: true,
		})
		if err := spinner.Start(ctx); err != nil {
			return nil, fmt.Errorf("failed to start spinner: %w", err)
		}

		a.loadAiCatalog(ctx)
		if stopErr := spinner.Stop(ctx); stopErr != nil {
			return nil, stopErr
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list models from catalog: %w", err)
		}
	}

	var modelNames []string
	for modelName := range a.modelCatalog {
		modelNames = append(modelNames, modelName)
	}

	// selectedModel, err := a.promptForModelWithSearch(ctx, modelNames)
	// if err != nil {
	// 	return "", err
	// }

	promptReq := &azdext.PromptAiModelRequest{
		AzureContext: a.azureContext,
		SelectOptions: &azdext.SelectOptions{
			Message: "Select a model",
		},
		Quota: &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 1,
		},
		Filter: &azdext.AiModelFilterOptions{
			Locations: []string{a.azureContext.Scope.Location},
		},
	}

	modelResp, err := a.azdClient.Prompt().PromptAiModel(ctx, promptReq)
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for model selection: %w", err)
	}

	selectedModel := modelResp.Model

	return selectedModel, nil
}

// promptForModelWithSearch prompts the user with a text search field, then shows a filtered Select list.
// Returns the selected model name.
func (a *InitFromCodeAction) promptForModelWithSearch(ctx context.Context, modelNames []string) (string, error) {
	for {
		// Prompt user for a search term
		searchResp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message: "Search for a model (e.g., gpt-4o) or press Enter to see all models:",
			},
		})
		if err != nil {
			return "", fmt.Errorf("failed to prompt for model search: %w", err)
		}

		filtered := fuzzyFilterModels(modelNames, searchResp.Value)
		if len(filtered) == 0 {
			fmt.Printf("No models matching '%s'. Please try again.\n", searchResp.Value)
			continue
		}

		slices.Sort(filtered)

		defaultIndex := findDefaultModelIndex(filtered)

		choices := make([]*azdext.SelectChoice, len(filtered))
		for i, name := range filtered {
			choices[i] = &azdext.SelectChoice{
				Label: name,
				Value: name,
			}
		}

		modelResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "Select a model:",
				Choices:       choices,
				SelectedIndex: &defaultIndex,
			},
		})
		if err != nil {
			return "", fmt.Errorf("failed to prompt for model selection: %w", err)
		}

		return filtered[*modelResp.Value], nil
	}
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

// FoundryProjectInfo holds information about a discovered Foundry project
type FoundryProjectInfo struct {
	SubscriptionId    string
	ResourceGroupName string
	AccountName       string
	ProjectName       string
	Location          string
	ResourceId        string
}

// extractSubscriptionId extracts the subscription ID from an Azure resource ID.
func extractSubscriptionId(resourceId string) string {
	parts := strings.Split(resourceId, "/")
	for i, part := range parts {
		if strings.EqualFold(part, "subscriptions") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// extractResourceGroup extracts the resource group name from an Azure resource ID.
func extractResourceGroup(resourceId string) string {
	parts := strings.Split(resourceId, "/")
	for i, part := range parts {
		if strings.EqualFold(part, "resourceGroups") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// listFoundryProjects enumerates all Foundry projects in a subscription by listing
// CognitiveServices accounts and their projects.
func (a *InitFromCodeAction) listFoundryProjects(ctx context.Context, subscriptionId string) ([]FoundryProjectInfo, error) {
	accountsClient, err := armcognitiveservices.NewAccountsClient(subscriptionId, a.credential, azure.NewArmClientOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to create accounts client: %w", err)
	}

	projectsClient, err := armcognitiveservices.NewProjectsClient(subscriptionId, a.credential, azure.NewArmClientOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to create projects client: %w", err)
	}

	var results []FoundryProjectInfo

	// List all CognitiveServices accounts
	accountPager := accountsClient.NewListPager(nil)
	for accountPager.More() {
		page, err := accountPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list accounts: %w", err)
		}

		for _, account := range page.Value {
			if account.Kind == nil {
				continue
			}
			// Only include Foundry-compatible account types
			kind := strings.ToLower(*account.Kind)
			if kind != "aiservices" && kind != "openai" {
				continue
			}

			// Extract resource group from the account's ID
			accountId := ""
			if account.ID != nil {
				accountId = *account.ID
			}
			rgName := extractResourceGroup(accountId)
			if rgName == "" {
				continue
			}
			accountName := ""
			if account.Name != nil {
				accountName = *account.Name
			}
			accountLocation := ""
			if account.Location != nil {
				accountLocation = *account.Location
			}

			// List projects under this account
			projectPager := projectsClient.NewListPager(rgName, accountName, nil)
			for projectPager.More() {
				projectPage, err := projectPager.NextPage(ctx)
				if err != nil {
					// Skip accounts we can't list projects for (permissions, etc.)
					break
				}
				for _, proj := range projectPage.Value {
					projName := ""
					if proj.Name != nil {
						// ARM returns nested resource names like "accountName/projectName"
						// Extract just the project name (last segment)
						fullName := *proj.Name
						if idx := strings.LastIndex(fullName, "/"); idx != -1 {
							projName = fullName[idx+1:]
						} else {
							projName = fullName
						}
					}
					projLocation := accountLocation
					if proj.Location != nil {
						projLocation = *proj.Location
					}
					resourceId := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s/projects/%s",
						subscriptionId, rgName, accountName, projName)

					results = append(results, FoundryProjectInfo{
						SubscriptionId:    subscriptionId,
						ResourceGroupName: rgName,
						AccountName:       accountName,
						ProjectName:       projName,
						Location:          projLocation,
						ResourceId:        resourceId,
					})
				}
			}
		}
	}

	return results, nil
}

// FoundryDeploymentInfo holds information about an existing model deployment in a Foundry project.
type FoundryDeploymentInfo struct {
	Name        string
	ModelName   string
	ModelFormat string
	Version     string
	SkuName     string
	SkuCapacity int
}

// listProjectDeployments lists all model deployments in a Foundry project (account).
func (a *InitFromCodeAction) listProjectDeployments(ctx context.Context, subscriptionId, resourceGroup, accountName string) ([]FoundryDeploymentInfo, error) {
	deploymentsClient, err := armcognitiveservices.NewDeploymentsClient(subscriptionId, a.credential, azure.NewArmClientOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to create deployments client: %w", err)
	}

	pager := deploymentsClient.NewListPager(resourceGroup, accountName, nil)
	var results []FoundryDeploymentInfo
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list deployments: %w", err)
		}
		for _, deployment := range page.Value {
			info := FoundryDeploymentInfo{}
			if deployment.Name != nil {
				info.Name = *deployment.Name
			}
			if deployment.Properties != nil && deployment.Properties.Model != nil {
				m := deployment.Properties.Model
				if m.Name != nil {
					info.ModelName = *m.Name
				}
				if m.Format != nil {
					info.ModelFormat = *m.Format
				}
				if m.Version != nil {
					info.Version = *m.Version
				}
			}
			if deployment.SKU != nil {
				if deployment.SKU.Name != nil {
					info.SkuName = *deployment.SKU.Name
				}
				if deployment.SKU.Capacity != nil {
					info.SkuCapacity = int(*deployment.SKU.Capacity)
				}
			}
			results = append(results, info)
		}
	}
	return results, nil
}

func (a *InitFromCodeAction) setEnvVar(ctx context.Context, key, value string) error {
	_, err := a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: a.environment.Name,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("failed to set environment variable %s=%s: %w", key, value, err)
	}

	return nil
}

// lookupAcrResourceId finds the resource ID for an ACR given its login server endpoint
func (a *InitFromCodeAction) lookupAcrResourceId(ctx context.Context, subscriptionId string, loginServer string) (string, error) {
	// Extract registry name from login server (e.g., "myregistry" from "myregistry.azurecr.io")
	registryName := strings.Split(loginServer, ".")[0]

	client, err := armcontainerregistry.NewRegistriesClient(subscriptionId, a.credential, azure.NewArmClientOptions())
	if err != nil {
		return "", fmt.Errorf("failed to create container registry client: %w", err)
	}

	// List all registries and find the matching one
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to list registries: %w", err)
		}
		for _, registry := range page.Value {
			if registry.Name != nil && strings.EqualFold(*registry.Name, registryName) {
				if registry.ID != nil {
					return *registry.ID, nil
				}
			}
		}
	}

	return "", fmt.Errorf("container registry '%s' not found in subscription", registryName)
}

// writeDefinitionToSrcDir writes a ContainerAgent to a YAML file in the src directory and returns the path
func (a *InitFromCodeAction) writeDefinitionToSrcDir(definition *agent_yaml.ContainerAgent, srcDir string) (string, error) {
	// Ensure the src directory exists
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
	if err := os.WriteFile(definitionPath, content, 0644); err != nil {
		return "", fmt.Errorf("writing definition to file: %w", err)
	}

	return definitionPath, nil
}

func (a *InitFromCodeAction) addToProject(ctx context.Context, targetDir string, agentName string, host string) error {
	var agentConfig = project.ServiceTargetAgentConfig{}

	agentConfig.Container = &project.ContainerSettings{
		Resources: &project.ResourceSettings{
			Memory: project.DefaultMemory,
			Cpu:    project.DefaultCpu,
		},
		Scale: &project.ScaleSettings{
			MinReplicas: project.DefaultMinReplicas,
			MaxReplicas: project.DefaultMaxReplicas,
		},
	}

	agentConfig.Deployments = a.deploymentDetails

	var agentConfigStruct *structpb.Struct
	var err error
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
	fmt.Printf("To provision and deploy the whole solution, use %s.\n", color.HiBlueString("azd up"))
	fmt.Printf(
		"If you already have your project provisioned with hosted agents requirements, "+
			"you can directly use %s.\n",
		color.HiBlueString("azd deploy %s", agentName))
	return nil
}

// func (a *InitFromCodeAction) getModelDeploymentDetails(ctx context.Context, modelName string) (*ai.AiModelDeployment, error) {
// 	var model *ai.AiModel
// 	model, _ = a.modelCatalog[modelName]

// 	_, defaultVersion, err := a.modelCatalogService.ListModelVersions(ctx, model, a.azureContext.Scope.Location)
// 	if err != nil {
// 		return nil, fmt.Errorf("listing versions for model '%s': %w", model.Name, err)
// 	}

// 	availableSkus, err := a.modelCatalogService.ListModelSkus(ctx, model, a.azureContext.Scope.Location, defaultVersion)
// 	if err != nil {
// 		return nil, fmt.Errorf("listing SKUs for model '%s': %w", model.Name, err)
// 	}

// 	// Determine default SKU based on priority list
// 	defaultSku := ""
// 	for _, sku := range defaultSkuPriority {
// 		if slices.Contains(availableSkus, sku) {
// 			defaultSku = sku
// 			break
// 		}
// 	}

// 	deploymentOptions := ai.AiModelDeploymentOptions{
// 		Versions: []string{defaultVersion},
// 		Skus:     []string{defaultSku},
// 	}

// 	modelDeployment, err := a.modelCatalogService.GetModelDeployment(ctx, model, &deploymentOptions)
// 	if err != nil {
// 		return nil, fmt.Errorf("failed to get model deployment: %w", err)
// 	}

// 	if modelDeployment.Sku.Capacity == -1 {
// 		modelDeployment.Sku.Capacity = 10
// 	}

// 	return modelDeployment, nil
// }

func (a *InitFromCodeAction) processExistingFoundryProject(ctx context.Context, foundryProject FoundryProjectInfo) error {

	if err := a.setEnvVar(ctx, "AZURE_AI_PROJECT_ID", foundryProject.ResourceId); err != nil {
		return err
	}

	// Set the extracted values as environment variables
	if err := a.setEnvVar(ctx, "AZURE_RESOURCE_GROUP", foundryProject.ResourceGroupName); err != nil {
		return err
	}

	if err := a.setEnvVar(ctx, "AZURE_AI_ACCOUNT_NAME", foundryProject.AccountName); err != nil {
		return err
	}

	if err := a.setEnvVar(ctx, "AZURE_AI_PROJECT_NAME", foundryProject.ProjectName); err != nil {
		return err
	}

	// Set the Microsoft Foundry endpoint URL
	aiFoundryEndpoint := fmt.Sprintf("https://%s.services.ai.azure.com/api/projects/%s", foundryProject.AccountName, foundryProject.ProjectName)
	if err := a.setEnvVar(ctx, "AZURE_AI_PROJECT_ENDPOINT", aiFoundryEndpoint); err != nil {
		return err
	}

	aoaiEndpoint := fmt.Sprintf("https://%s.openai.azure.com/", foundryProject.AccountName)
	if err := a.setEnvVar(ctx, "AZURE_OPENAI_ENDPOINT", aoaiEndpoint); err != nil {
		return err
	}

	// Create FoundryProjectsClient and get connections
	foundryClient := azure.NewFoundryProjectsClient(foundryProject.AccountName, foundryProject.ProjectName, a.credential)
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
			fmt.Println("\n" +
				"An Azure Container Registry (ACR) is required\n\n" +
				"Foundry Hosted Agents need an Azure Container Registry to store container images before deployment.\n\n" +
				"You can:\n" +
				"  • Use an existing ACR\n" +
				"  • Or create a new one from the template during 'azd up'\n\n" +
				"Learn more: aka.ms/azdaiagent/docs")

			resp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
				Options: &azdext.PromptOptions{
					Message:        "Enter your ACR login server (e.g., myregistry.azurecr.io), or leave blank to create a new one",
					IgnoreHintKeys: true,
				},
			})
			if err != nil {
				return fmt.Errorf("prompting for ACR endpoint: %w", err)
			}

			if resp.Value != "" {
				// Look up the ACR resource ID from the login server
				resourceId, err := a.lookupAcrResourceId(ctx, a.azureContext.Scope.SubscriptionId, resp.Value)
				if err != nil {
					return fmt.Errorf("failed to lookup ACR resource ID: %w", err)
				}

				if err := a.setEnvVar(ctx, "AZURE_CONTAINER_REGISTRY_ENDPOINT", resp.Value); err != nil {
					return err
				}
				if err := a.setEnvVar(ctx, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID", resourceId); err != nil {
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

			if err := a.setEnvVar(ctx, "AZURE_AI_PROJECT_ACR_CONNECTION_NAME", selectedConnection.Name); err != nil {
				return err
			}

			if err := a.setEnvVar(ctx, "AZURE_CONTAINER_REGISTRY_ENDPOINT", selectedConnection.Target); err != nil {
				return err
			}
		}

		// Handle App Insights connections
		if len(appInsightsConnections) == 0 {
			fmt.Println("\n" +
				"Application Insights (optional)\n\n" +
				"Enable telemetry to collect logs, traces, and diagnostics for this agent.\n\n" +
				"You can:\n" +
				"  • Use an existing Application Insights resource\n" +
				"  • Or create a new one during 'azd up'\n\n" +
				"Docs: aka.ms/azdaiagent/docs")

			// First prompt for resource ID
			resourceIdResp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
				Options: &azdext.PromptOptions{
					Message:        "Enter your Application Insights resource ID, or leave blank to create a new one",
					IgnoreHintKeys: true,
				},
			})
			if err != nil {
				return fmt.Errorf("prompting for Application Insights resource ID: %w", err)
			}

			if resourceIdResp.Value != "" {
				if err := a.setEnvVar(ctx, "APPLICATIONINSIGHTS_RESOURCE_ID", resourceIdResp.Value); err != nil {
					return err
				}

				// If user provided resource ID, also prompt for connection string
				connStrResp, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
					Options: &azdext.PromptOptions{
						Message:        "Enter your Application Insights connection string",
						IgnoreHintKeys: true,
					},
				})
				if err != nil {
					return fmt.Errorf("prompting for Application Insights connection string: %w", err)
				}

				if connStrResp.Value != "" {
					if err := a.setEnvVar(ctx, "APPLICATIONINSIGHTS_CONNECTION_STRING", connStrResp.Value); err != nil {
						return err
					}
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
				if err := a.setEnvVar(ctx, "APPLICATIONINSIGHTS_CONNECTION_NAME", selectedConnection.Name); err != nil {
					return err
				}

				if err := a.setEnvVar(ctx, "APPLICATIONINSIGHTS_CONNECTION_STRING", selectedConnection.Credentials.Key); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (a *InitFromCodeAction) loadAiCatalog(ctx context.Context) error {
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

	modelResp, err := a.azdClient.Ai().ListModels(ctx, &azdext.ListModelsRequest{
		AzureContext: a.azureContext,
	})
	stopErr := spinner.Stop(ctx)
	if err != nil {
		return fmt.Errorf("failed to load the model catalog: %w", err)
	}
	if stopErr != nil {
		return stopErr
	}

	a.modelCatalog = mapModelsByName(modelResp.Models)

	return nil
}

func (a *InitFromCodeAction) resolveModelDeploymentNoPrompt(
	ctx context.Context,
	model *azdext.AiModel,
	location string,
) (*azdext.AiModelDeployment, error) {
	resolveResp, err := a.azdClient.Ai().ResolveModelDeployments(ctx, &azdext.ResolveModelDeploymentsRequest{
		AzureContext: a.azureContext,
		ModelName:    model.Name,
		Options: &azdext.AiModelDeploymentOptions{
			Locations: []string{location},
		},
		Quota: &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 1,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve model deployment: %w", err)
	}

	if len(resolveResp.Deployments) == 0 {
		return nil, fmt.Errorf("no deployment candidates found for model '%s' in location '%s'", model.Name, location)
	}

	orderedCandidates := slices.Clone(resolveResp.Deployments)
	defaultVersions := make(map[string]struct{}, len(model.Versions))
	for _, version := range model.Versions {
		if version.IsDefault {
			defaultVersions[version.Version] = struct{}{}
		}
	}

	slices.SortFunc(orderedCandidates, func(a, b *azdext.AiModelDeployment) int {
		_, aDefault := defaultVersions[a.Version]
		_, bDefault := defaultVersions[b.Version]
		if aDefault != bDefault {
			if aDefault {
				return -1
			}
			return 1
		}

		aSkuPriority := skuPriority(a.Sku.Name)
		bSkuPriority := skuPriority(b.Sku.Name)
		if aSkuPriority != bSkuPriority {
			if aSkuPriority < bSkuPriority {
				return -1
			}
			return 1
		}

		if cmp := strings.Compare(a.Version, b.Version); cmp != 0 {
			return cmp
		}

		if cmp := strings.Compare(a.Sku.Name, b.Sku.Name); cmp != 0 {
			return cmp
		}

		return strings.Compare(a.Sku.UsageName, b.Sku.UsageName)
	})

	for _, candidate := range orderedCandidates {
		capacity, ok := resolveNoPromptCapacity(candidate)
		if !ok {
			continue
		}

		return cloneDeploymentWithCapacity(candidate, capacity), nil
	}

	return nil, fmt.Errorf("no deployment candidates found for model '%s' with a valid non-interactive capacity", model.Name)
}
