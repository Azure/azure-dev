// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/fatih/color"
)

type InitFromCodeAction struct {
	azdClient         *azdext.AzdClient
	flags             *initFlags
	projectConfig     *azdext.ProjectConfig
	azureContext      *azdext.AzureContext
	environment       *azdext.Environment
	credential        azcore.TokenCredential
	deploymentDetails []project.Deployment
	needsProvision    bool
	httpClient        *http.Client

	// selectedFoundryProject holds the existing Foundry project resolved during
	// init (nil when creating a new project). It carries NetworkInjected so
	// addToProject can disable remote build for VNET-injected accounts
	// without issuing a second account read.
	selectedFoundryProject *FoundryProjectInfo
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

	// Validate code deploy flags
	if err := validateCodeDeployInput(
		a.flags.noPrompt, a.flags.deployMode, a.flags.runtime, a.flags.entryPoint, a.flags.depResolution,
	); err != nil {
		return err
	}

	// Default src to current directory when not specified
	srcDir := a.flags.src
	if srcDir == "" {
		srcDir = "."
	}

	// Guard against silently overwriting an existing agent definition. Reached
	// when the user declined the reuse prompt in RunE or bypassed it; we still
	// refuse in --no-prompt and confirm interactively.
	if existing, statErr := findExistingAgentYaml(srcDir); statErr == nil && existing != "" {
		displayPath, relErr := filepath.Rel(srcDir, existing)
		if relErr != nil || displayPath == "" {
			displayPath = existing
		}
		if a.flags.noPrompt {
			return exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("%s already exists at %q", displayPath, existing),
				fmt.Sprintf(
					"delete or move the existing %s, or run interactively to confirm overwrite",
					displayPath,
				),
			)
		}

		confirmResp, err := a.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      fmt.Sprintf("An agent definition already exists at %q. Overwrite?", displayPath),
				DefaultValue: new(false),
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return exterrors.Cancelled("overwrite confirmation was cancelled")
			}
			return fmt.Errorf("prompting for overwrite confirmation: %w", err)
		}
		if !*confirmResp.Value {
			return exterrors.Cancelled(fmt.Sprintf("%s already exists; overwrite declined", displayPath))
		}
	}

	// No manifest pointer provided - process local agent code
	// Create a definition based on user prompts
	localDefinition, err := a.createDefinitionFromLocalAgent(ctx)
	if err != nil {
		return fmt.Errorf("failed to create definition from local agent: %w", err)
	}

	if localDefinition != nil {

		// Generate .agentignore. The agent definition is written into the
		// azure.yaml service entry below, not to an on-disk agent.yaml.
		if err := a.writeAgentIgnoreToSrcDir(srcDir); err != nil {
			return fmt.Errorf("failed to write .agentignore: %w", err)
		}

		// Add the agent to the azd project (azure.yaml) services
		isCodeDeploy := localDefinition.CodeConfiguration != nil
		if err := a.addToProject(ctx, srcDir, localDefinition, isCodeDeploy); err != nil {
			return fmt.Errorf("failed to add agent to azure.yaml: %w", err)
		}

		// Run post-init validations (advisory warnings only)
		validatePostInit(srcDir, localDefinition.CodeConfiguration)

		fmt.Println("\nYou can customize environment variables and other settings " +
			"in the agent service entry in azure.yaml.")

		// Delegate the trailing Next: block to the shared nextstep
		// resolver — the same path used by the manifest-driven init
		// flow (see InitAction.addToProject). The resolver inspects
		// the current azd environment, the pending-provision signal,
		// each agent.yaml's references to user-supplied variables,
		// and emits context-aware guidance (`azd provision` when infra
		// outputs are unset or pending, `azd env set <KEY>` lines when
		// agent.yaml references unset user-supplied variables, or
		// `azd ai agent run` when everything is configured). All paths
		// terminate with the deploy hint. State-assembly errors are
		// intentionally ignored: the resolver degrades gracefully on
		// partial state per the design spec.
		state, _ := nextstep.AssembleState(ctx, a.azdClient)
		_ = printAllNextIfTerminal(os.Stdout, nextstep.ResolveAfterInit(state, readmeExistsForProject(ctx, a.azdClient)))
	}

	return nil
}

func (a *InitFromCodeAction) ensureProject(ctx context.Context) (*azdext.ProjectConfig, error) {
	projectResponse, err := a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Println("Let's get your project initialized.")

		// Derive an environment name when the user didn't pass --environment.
		// from-code runs in the user's existing code directory, so the
		// default uses the cwd basename.
		envName := a.flags.env
		if envName == "" {
			cwd, cwdErr := os.Getwd()
			base := "agent"
			if cwdErr == nil {
				base = filepath.Base(cwd)
			}
			base = sanitizeAgentName(base)
			if len(base) > 59 {
				base = strings.TrimRight(base[:59], "-")
			}
			envName = base + "-dev"
		}

		// Scaffold a minimal project locally via azd-core. Runs in cwd
		// (no -C) since from-code starts inside the user's existing code
		// directory. `writeFoundryProvider` below stamps the provider name
		// onto the resulting azure.yaml.
		initArgs := []string{
			"init", "--minimal", "--no-prompt",
			"--environment", envName,
		}
		workflow := &azdext.Workflow{
			Name: "init",
			Steps: []*azdext.WorkflowStep{
				{Command: &azdext.WorkflowCommand{Args: initArgs}},
			},
		}
		if _, err := a.azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
			Workflow: workflow,
		}); err != nil {
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("project initialization was cancelled")
			}
			return nil, exterrors.Dependency(
				exterrors.CodeProjectInitFailed,
				fmt.Sprintf("failed to initialize project: %s", err),
				"",
			)
		}

		if err := writeFoundryProvider(ctx, a.azdClient); err != nil {
			return nil, err
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

// createDefinitionFromLocalAgent creates a ContainerAgent for local agent code
// This is used when no manifest pointer is provided and we need to scaffold a new agent
func (a *InitFromCodeAction) createDefinitionFromLocalAgent(ctx context.Context) (*agent_yaml.ContainerAgent, error) {
	// Default agent name to sanitized cwd
	defaultName := "my-agent"
	if cwd, err := os.Getwd(); err == nil {
		defaultName = sanitizeAgentName(filepath.Base(cwd))
	}

	agentName, err := resolveInitAgentName(ctx, a.azdClient, a.flags, defaultName)
	if err != nil {
		return nil, err
	}

	// Create the azd environment now that we have the agent name
	if a.environment == nil {
		if env := getExistingEnvironment(ctx, a.flags.env, a.azdClient); env != nil {
			a.environment = env
			a.flags.env = env.Name
		} else {
			envName := sanitizeAgentName(agentName + "-dev")
			env, err := createNewEnvironment(ctx, a.azdClient, envName)
			if err != nil {
				return nil, fmt.Errorf("failed to create azd environment: %w", err)
			}
			a.environment = env
			a.flags.env = envName
			fmt.Printf("  %s  %s\n", color.GreenString("+"), color.GreenString(".azure/%s/.env", envName))
		}
	}
	if a.azureContext == nil || a.azureContext.Scope == nil ||
		(a.azureContext.Scope.SubscriptionId == "" &&
			a.azureContext.Scope.TenantId == "" &&
			a.azureContext.Scope.Location == "") {
		azureContext, err := loadAzureContext(ctx, a.azdClient, a.environment.Name)
		if err != nil {
			return nil, err
		}
		a.azureContext = azureContext
	}
	// TODO: Prompt user for agent kind
	agentKind := agent_yaml.AgentKindHosted

	// Prompt user for deploy mode (container vs code)
	// Code deploy is available for Python and .NET projects
	srcDir := a.flags.src
	if srcDir == "" {
		srcDir, _ = os.Getwd()
	}
	showCodeDeploy := isPythonProject(srcDir) || isDotnetProject(srcDir)
	deployMode, err := promptDeployMode(ctx, a.azdClient, a.flags.noPrompt, showCodeDeploy, a.flags.deployMode, false)
	if err != nil {
		return nil, err
	}

	// If code deploy, prompt for code configuration details
	var codeConfig *agent_yaml.CodeConfiguration
	if deployMode == "code" {
		codeConfig, err = a.promptCodeConfiguration(ctx, a.flags.src)
		if err != nil {
			return nil, err
		}
	}

	// Prompt user for supported protocols
	protocols, err := promptProtocols(ctx, a.azdClient.Prompt(), a.flags.noPrompt, a.flags.protocols)
	if err != nil {
		return nil, err
	}

	// Step 1: Foundry project selection
	var selectedProject *FoundryProjectInfo
	deferredAzureContext := false
	if a.flags.projectResourceId != "" {
		projectDetails, err := extractProjectDetails(a.flags.projectResourceId)
		if err != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidProjectResourceId,
				fmt.Sprintf("invalid --project-id value: %s", err),
				"Provide a valid Foundry project resource ID in the format:\n"+
					"/subscriptions/<SUBSCRIPTION_ID>/resourceGroups/<RESOURCE_GROUP>/providers/"+
					"Microsoft.CognitiveServices/accounts/<ACCOUNT_NAME>/projects/<PROJECT_NAME>",
			)
		}
		a.azureContext.Scope.SubscriptionId = projectDetails.SubscriptionId

		newCred, err := ensureSubscription(
			ctx, a.azdClient, a.azureContext, a.environment.Name,
			"Select an Azure subscription to find existing Foundry projects.",
		)
		if err != nil {
			return nil, err
		}
		a.credential = newCred

		proj, err := selectFoundryProject(
			ctx, a.azdClient, a.credential, a.azureContext, a.environment.Name,
			a.azureContext.Scope.SubscriptionId, a.flags.projectResourceId,
			deployMode == "code",
			true, // bicepless
		)
		if err != nil {
			return nil, err
		}
		if proj == nil {
			return nil, fmt.Errorf("specified foundry project was not found or is not eligible for the current configuration: %s", a.flags.projectResourceId)
		}
		selectedProject = proj

		if err := setEnvValue(ctx, a.azdClient, a.environment.Name, "USE_EXISTING_AI_PROJECT", "true"); err != nil {
			return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
		}
	} else if shouldDeferInitAzureContext(a.flags.noPrompt, a.azureContext) {
		// In headless init, missing Azure values should not block local scaffold generation.
		// Defer project/model setup and print the values required before provisioning.
		if err := configureDeferredInitAzureContext(
			ctx, a.azdClient, a.environment.Name, a.azureContext, false,
		); err != nil {
			return nil, err
		}
		deferredAzureContext = true
	} else if a.flags.noPrompt {
		newCred, err := configureNewProjectForNoPrompt(
			ctx, a.azdClient, a.environment.Name, a.azureContext,
			"Select an Azure subscription to look up available models and provision your Foundry project resources.",
		)
		if err != nil {
			return nil, err
		}
		a.credential = newCred
	} else {
		projectChoices := []*azdext.SelectChoice{
			{Label: "Use an existing Foundry project", Value: "existing"},
			{Label: "Create a new Foundry project", Value: "new"},
		}

		defaultIdx := int32(0)
		projectResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "Select a Foundry project to host your agent and any models or tools it uses.",
				Choices:       projectChoices,
				SelectedIndex: &defaultIdx,
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("project selection was cancelled")
			}
			return nil, exterrors.FromPrompt(err, "failed to prompt for Foundry project configuration choice")
		}

		switch projectChoices[*projectResp.Value].Value {
		case "existing":
			newCred, err := ensureSubscription(
				ctx, a.azdClient, a.azureContext, a.environment.Name,
				"Select an Azure subscription to find existing Foundry projects.",
			)
			if err != nil {
				return nil, err
			}
			a.credential = newCred

			proj, err := selectFoundryProject(
				ctx, a.azdClient, a.credential, a.azureContext, a.environment.Name,
				a.azureContext.Scope.SubscriptionId, "",
				deployMode == "code",
				true, // bicepless
			)
			if err != nil {
				return nil, err
			}

			if proj == nil {
				fmt.Println(output.WithGrayFormat(
					"No existing Foundry project was selected. Falling back to creating new resources.",
				))
				if err := setEnvValue(ctx, a.azdClient, a.environment.Name, "USE_EXISTING_AI_PROJECT", "false"); err != nil {
					return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
				}
				if err := ensureLocation(ctx, a.azdClient, a.azureContext, a.environment.Name); err != nil {
					return nil, err
				}
			} else {
				selectedProject = proj
				if err := setEnvValue(ctx, a.azdClient, a.environment.Name, "USE_EXISTING_AI_PROJECT", "true"); err != nil {
					return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
				}
			}
		default:
			newCred, err := ensureSubscriptionAndLocation(
				ctx, a.azdClient, a.azureContext, a.environment.Name,
				"Select an Azure subscription to look up available models and provision your Foundry project resources.",
			)
			if err != nil {
				return nil, err
			}
			a.credential = newCred

			if err := setEnvValue(ctx, a.azdClient, a.environment.Name, "USE_EXISTING_AI_PROJECT", "false"); err != nil {
				return nil, fmt.Errorf("failed to set USE_EXISTING_AI_PROJECT: %w", err)
			}
		}
	}

	// Step 2: Model configuration
	a.selectedFoundryProject = selectedProject
	var modelConfigChoices []*azdext.SelectChoice
	if selectedProject != nil {
		modelConfigChoices = []*azdext.SelectChoice{
			{Label: "Use an existing model deployment", Value: "existing"},
			{Label: "Deploy a new model from the catalog", Value: "new"},
			{Label: "Skip model configuration", Value: "skip"},
		}
	} else {
		modelConfigChoices = []*azdext.SelectChoice{
			{Label: "Deploy a new model from the catalog", Value: "new"},
			{Label: "Skip model configuration", Value: "skip"},
		}
	}

	modelConfigChoice := "skip"
	if a.flags.noPrompt {
		if selectedProject != nil && a.flags.modelDeployment != "" {
			modelConfigChoice = "existing"
		} else if a.flags.model != "" && !deferredAzureContext {
			modelConfigChoice = "new"
		}
		if deferredAzureContext && (a.flags.model != "" || a.flags.modelDeployment != "") {
			fmt.Printf("%s", output.WithWarningFormat(
				"Model configuration was deferred because Azure environment values are missing.\n",
			))
			fmt.Println(output.WithGrayFormat(
				"Set the missing values, then re-run init with your model options or configure deployments in azure.yaml.",
			))
		}
	} else {
		defaultIndex := int32(0)
		modelConfigResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "How would you like to configure model(s) for your agent?",
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
	}

	var selectedModel *azdext.AiModel
	var existingDeployment *FoundryDeploymentInfo

	switch modelConfigChoice {
	case "new":
		if a.azureContext.Scope.Location == "" {
			if err := ensureLocation(ctx, a.azdClient, a.azureContext, a.environment.Name); err != nil {
				return nil, err
			}
		}
		selectedModel, err = selectNewModel(ctx, a.azdClient, a.azureContext, a.flags.model)
		if err != nil {
			return nil, fmt.Errorf("failed to select new model: %w", err)
		}

	case "existing":
		if selectedProject == nil {
			return nil, fmt.Errorf("cannot select existing deployment without a Foundry project")
		}
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

	case "skip":
		// Skip model configuration entirely
	}

	// Create a minimal Agent Definition
	// Note: FOUNDRY_PROJECT_ENDPOINT and other FOUNDRY_* env vars are automatically
	// injected into hosted agent containers by the platform, so we don't need to
	// add them to agent.yaml. For local development, `azd ai agent run` translates
	// azd environment values to FOUNDRY_* env vars.
	definition := &agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Name: agentName,
			Kind: agentKind,
		},
		Protocols:         protocols,
		CodeConfiguration: codeConfig,
	}

	// An activity agent additionally advertises the friendly "activity" endpoint
	// guarded by BotServiceRbac. We compose this into any existing agent_endpoint
	// rather than overwriting it, so Activity can coexist with the other
	// protocols the agent selected (responses/invocations). Injecting it here
	// mirrors the manifest-based init so the generated azure.yaml is identical and
	// `azd deploy` provisions the Azure Bot connector. Phase 1 covers the simple
	// use case; digital-worker is a Phase 2 addition. No-op for non-activity agents.
	if project.IsActivityProtocol(*definition) {
		definition.AgentEndpoint = project.ComposeActivityAgentEndpoint(
			definition.AgentEndpoint, definition.Protocols,
		)
	}

	// Add model resource if a model was selected
	if existingDeployment != nil {
		// Existing deployment: reference it by name only. Per REFERENCE.md an
		// existing deployment is NOT declared under azure.ai.project.deployments:
		// (azd does not create/upsert it) — it is referenced via the agent env
		// var and verified at deploy time. So do not append to a.deploymentDetails.
		definition.EnvironmentVariables = appendEnvVar(definition.EnvironmentVariables, agent_yaml.EnvironmentVariable{
			Name:  "AZURE_AI_MODEL_DEPLOYMENT_NAME",
			Value: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		})

		if err := setEnvValue(ctx, a.azdClient, a.environment.Name, "AZURE_AI_MODEL_DEPLOYMENT_NAME", existingDeployment.Name); err != nil {
			return nil, fmt.Errorf("failed to set AZURE_AI_MODEL_DEPLOYMENT_NAME: %w", err)
		}

		// Existing deployment chosen — clear any prior
		// model_deployment tag so re-init that swaps from
		// new-deployment back to existing doesn't leave the
		// trailer stuck on `azd provision`.
		if err := updatePendingModelDeploymentSignal(
			ctx, a.azdClient, a.environment.Name, true, false,
		); err != nil {
			log.Printf("warning: failed to update model_deployment provision signal: %v", err)
		}
	} else if selectedModel != nil {
		modelDetails, err := a.resolveSelectedModelDeployment(ctx, selectedModel)
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
		a.needsProvision = true

		definition.EnvironmentVariables = appendEnvVar(definition.EnvironmentVariables, agent_yaml.EnvironmentVariable{
			Name:  "AZURE_AI_MODEL_DEPLOYMENT_NAME",
			Value: "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		})

		if err := setEnvValue(ctx, a.azdClient, a.environment.Name, "AZURE_AI_MODEL_DEPLOYMENT_NAME", modelDetails.ModelName); err != nil {
			return nil, fmt.Errorf("failed to set AZURE_AI_MODEL_DEPLOYMENT_NAME: %w", err)
		}

		// New model deployment configured — record that the
		// post-init trailer should suggest `azd provision`. See
		// pending_provision.go for the lifecycle contract: this
		// tag is cleared by postprovisionHandler after a
		// successful provision.
		if err := updatePendingModelDeploymentSignal(
			ctx, a.azdClient, a.environment.Name, true, true,
		); err != nil {
			log.Printf("warning: failed to update model_deployment provision signal: %v", err)
		}
	}

	agentName, err = resolveExistingAgentNameConflict(
		ctx,
		a.azdClient,
		a.environment,
		a.credential,
		a.flags.noPrompt,
		agentName,
	)
	if err != nil {
		return nil, err
	}
	definition.Name = agentName

	return definition, nil
}

func (a *InitFromCodeAction) resolveSelectedModelDeployment(
	ctx context.Context,
	model *azdext.AiModel,
) (*azdext.AiModelDeployment, error) {
	deployments, err := resolveModelDeployments(ctx, a.azdClient, a.azureContext, model, a.azureContext.Scope.Location)
	if err == nil {
		if candidate := selectBestModelDeploymentCandidate(model, deployments); candidate != nil {
			return candidate, nil
		}
	}

	if err != nil && !isRecoverableDeploymentSelectionError(err) {
		return nil, exterrors.FromAiService(err, exterrors.CodeModelResolutionFailed)
	}

	selector := &modelSelector{
		azdClient:    a.azdClient,
		azureContext: a.azureContext,
		environment:  a.environment,
		flags:        a.flags,
	}

	// allowSkip=false: in this recovery path the user already explicitly chose
	// the model via selectNewModel earlier, so offering "Skip" would be confusing.
	return selector.getModelDetails(ctx, model.Name, false)
}

// appendEnvVar appends an environment variable to a possibly-nil slice pointer,
// initializing it if needed.
func appendEnvVar(
	envVars *[]agent_yaml.EnvironmentVariable,
	envVar agent_yaml.EnvironmentVariable,
) *[]agent_yaml.EnvironmentVariable {
	if envVars == nil {
		return &[]agent_yaml.EnvironmentVariable{envVar}
	}
	*envVars = append(*envVars, envVar)
	return envVars
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
			return boundedInt32Index(i)
		}
	}
	// Fall back to first gpt-4 match
	for i, name := range modelNames {
		if strings.HasPrefix(name, "gpt-4") {
			return boundedInt32Index(i)
		}
	}
	return 0
}

// writeAgentIgnoreToSrcDir generates a default .agentignore in srcDir if one
// does not already exist. The agent definition itself is written into the
// azure.yaml service entry (not an on-disk agent.yaml); .agentignore is still
// needed to scope code-deploy ZIP packaging.
func (a *InitFromCodeAction) writeAgentIgnoreToSrcDir(srcDir string) error {
	//nolint:gosec // scaffold directory should be readable/traversable for project tools
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		return fmt.Errorf("creating src directory: %w", err)
	}

	agentIgnorePath := filepath.Join(srcDir, ".agentignore")
	if _, err := os.Stat(agentIgnorePath); os.IsNotExist(err) {
		if err := os.WriteFile(agentIgnorePath, []byte(project.DefaultAgentIgnoreContent()), osutil.PermissionFile); err != nil {
			return fmt.Errorf("writing .agentignore: %w", err)
		}
	}

	return nil
}

func (a *InitFromCodeAction) addToProject(
	ctx context.Context,
	targetDir string,
	definition *agent_yaml.ContainerAgent,
	isCodeDeploy bool,
) error {
	agentName := definition.Name
	// If targetDir is ".", resolve the actual relative path from the project root to cwd.
	// This ensures azure.yaml gets the correct "project:" value when init is run from a subdirectory.
	if targetDir == "." {
		if cwd, err := os.Getwd(); err == nil && a.projectConfig != nil && a.projectConfig.Path != "" {
			if relPath, err := filepath.Rel(a.projectConfig.Path, cwd); err == nil && relPath != "." {
				targetDir = filepath.ToSlash(relPath)
			}
		}
	}

	var agentConfig = project.ServiceTargetAgentConfig{}

	// Both code and container modes need container resources for local run
	agentConfig.Container = &project.ContainerSettings{
		Resources: &project.ResourceSettings{
			Memory: project.DefaultMemory,
			Cpu:    project.DefaultCpu,
		},
	}

	agentConfig.Deployments = a.deploymentDetails

	// Detect startup command (container deploy only; code deploy does not use startupCommand)
	if !isCodeDeploy {
		startupCmd, err := resolveStartupCommandForInit(ctx, a.azdClient, a.projectConfig.Path, targetDir, a.flags.noPrompt)
		if err != nil {
			return err
		}
		agentConfig.StartupCommand = startupCmd
	}

	// Move the model deployments out of the agent config into a sibling
	// azure.ai.project service, emitted after the agent service below.
	resourceDeployments := agentConfig.Deployments
	agentConfig.Deployments = nil

	// Embed the agent definition (formerly written to agent.yaml) as
	// service-level properties on the azure.ai.agent entry, merged with the
	// remaining agent config (container settings, startup command).
	agentProps, err := project.AgentDefinitionToServiceProperties(*definition, &agentConfig)
	if err != nil {
		return err
	}

	language := "python"
	if !isCodeDeploy {
		language = "docker"
	} else if definition.CodeConfiguration != nil &&
		strings.HasPrefix(definition.CodeConfiguration.Runtime, "dotnet_") {
		language = "csharp"
	}

	serviceConfig := &azdext.ServiceConfig{
		Name:                 strings.ReplaceAll(agentName, " ", ""),
		RelativePath:         targetDir,
		Host:                 AiAgentHost,
		Language:             language,
		Image:                definition.Image,
		AdditionalProperties: agentProps,
	}

	// For hosted container-based agents, enable remote build by default. It is
	// silently disabled when the target Foundry account has VNET network injection
	// configured, since it cannot reach a registry in the VNET.
	if !isCodeDeploy {
		networkInjected := a.selectedFoundryProject != nil && a.selectedFoundryProject.NetworkInjected
		serviceConfig.Docker = &azdext.DockerProjectOptions{RemoteBuild: !networkInjected}
	}

	// Set AZD_AGENT_SKIP_ACR so Bicep knows whether to create a container registry.
	// Set before AddService so env state is consistent even if AddService fails.
	if err := setACREnvVar(ctx, a.azdClient, a.environment.Name, isCodeDeploy); err != nil {
		return err
	}

	req := &azdext.AddServiceRequest{Service: serviceConfig}

	if _, err := a.azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding agent service to project: %w", err)
	}

	// Emit the sibling azure.ai.project service carrying the model deployments
	// and wire the agent's uses: to it. A selected existing project contributes
	// its endpoint so provision reuses it instead of creating a new project.
	agentServiceName := strings.ReplaceAll(agentName, " ", "")
	if err := emitResourceServices(
		ctx, a.azdClient, agentServiceName,
		projectNameHint(ctx, a.azdClient, a.environment.Name, a.selectedFoundryProject),
		a.selectedFoundryProject.Endpoint(),
		resourceDeployments, nil, nil,
	); err != nil {
		return err
	}

	fmt.Printf("\nAdded your agent as a service entry named '%s' under the file azure.yaml.\n", agentName)
	return nil
}

// promptCodeConfiguration prompts the user for code deploy configuration settings.
func (a *InitFromCodeAction) promptCodeConfiguration(ctx context.Context, srcDir string) (*agent_yaml.CodeConfiguration, error) {
	return promptCodeConfig(ctx, a.azdClient, srcDir, a.flags.noPrompt, codeDeployOptions{
		runtime:       a.flags.runtime,
		entryPoint:    a.flags.entryPoint,
		depResolution: a.flags.depResolution,
	}, false)
}

// protocolInfo pairs a protocol name with the default version used when generating agent.yaml.
type protocolInfo struct {
	Name    string
	Version string
}

// knownProtocols lists the protocols offered during init, in display order.
var knownProtocols = []protocolInfo{
	{Name: "responses", Version: "2.0.0"},
	{Name: "invocations", Version: "1.0.0"},
	{Name: "invocations_ws", Version: "2.0.0"},
	// "activity" is the canonical protocol name (legacy alias: "activity_protocol").
	// The version selects the platform's internal container route ("v1"/"1.0.0" ->
	// /api/messages, "2.0.0" -> /activity/messages), but that hop is Bot Service ->
	// container inside the platform: the client, the Bot Service messaging endpoint,
	// and the agent sample are all unaffected by the choice. "2.0.0" is the service's
	// official/recommended version ("1.0.0" is accepted but deprecated going forward),
	// so new agents default to it, matching the latest-version convention responses uses.
	{Name: "activity", Version: "2.0.0"},
}

// promptProtocols asks the user which protocols their agent supports.
// When flagProtocols is non-empty the prompt is skipped and those values are used directly.
// When noPrompt is true and no flag values are provided, defaults to [responses/2.0.0].
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
			{Protocol: "responses", Version: "2.0.0"},
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

// promptDeployMode asks the user to choose between code deploy and container deploy.
// When deployModeFlag is set, it is used directly (for --no-prompt with explicit flag).
// When noPrompt is true and no flag is provided, defaults to "code".
// When showCodeDeploy is false and no explicit flag overrides, code deploy is not offered.
func promptDeployMode(ctx context.Context, azdClient *azdext.AzdClient, noPrompt bool, showCodeDeploy bool, deployModeFlag string, userProvidedManifest bool) (string, error) {
	// Resolution precedence:
	//   1. Explicit flag (--deploy-mode) — always wins
	//   2. !showCodeDeploy — container is the only option (not Python/.NET)
	//   3. userProvidedManifest — auto-select "code" (opinionated default;
	//      triggered by -m flag OR interactive template selection)
	//   4. noPrompt — "code" (default deploy mode)
	//   5. Interactive prompt

	// Explicit flag takes precedence
	if deployModeFlag != "" {
		switch deployModeFlag {
		case "container", "code":
			return deployModeFlag, nil
		default:
			return "", exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("invalid --deploy-mode value %q; must be 'container' or 'code'", deployModeFlag),
				"Use --deploy-mode container or --deploy-mode code",
			)
		}
	}

	if !showCodeDeploy {
		return "container", nil
	}

	// When the user provided a manifest explicitly (-m), auto-select the
	// opinionated default (code) without prompting. Users who want
	// container deploy with -m can pass --deploy-mode container explicitly.
	if userProvidedManifest {
		log.Printf("Auto-selected deploy mode: code (use --deploy-mode container for container deploy)")
		return "code", nil
	}

	if noPrompt {
		return "code", nil
	}

	deployModeChoices := []*azdext.SelectChoice{
		{Label: "Source Code (ZIP upload)", Value: "code"},
		{Label: "Container Image (Docker)", Value: "container"},
	}

	defaultIdx := int32(0) // Code deploy is the default
	deployModeResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "How would you like to deploy your agent?",
			Choices:       deployModeChoices,
			SelectedIndex: &defaultIdx,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", exterrors.Cancelled("deploy mode selection was cancelled")
		}
		return "", fmt.Errorf("failed to prompt for deploy mode: %w", err)
	}
	return deployModeChoices[*deployModeResp.Value].Value, nil
}

// detectDefaultEntryPoint returns a sensible default entry point based on the runtime and source directory.
// TODO: reuse this logic in the `run` command (tracked as future work item).
func detectDefaultEntryPoint(srcDir, runtime string) string {
	if strings.HasPrefix(runtime, "dotnet_") {
		// Look for .csproj file and derive DLL name from <AssemblyName> or project filename
		entries, err := os.ReadDir(srcDir)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".csproj") {
					dllName := strings.TrimSuffix(e.Name(), ".csproj") + ".dll"
					// Try to parse <AssemblyName> from the csproj
					csprojPath := filepath.Join(srcDir, e.Name())
					if data, readErr := os.ReadFile(csprojPath); readErr == nil { //nolint:gosec // path from user project
						if asmName := extractAssemblyName(string(data)); asmName != "" {
							dllName = asmName + ".dll"
						}
					}
					return dllName
				}
			}
		}
		return "App.dll"
	}

	// Python default
	if _, err := os.Stat(filepath.Join(srcDir, "app.py")); err == nil {
		return "app.py"
	}
	return "main.py"
}

// extractAssemblyName parses the <AssemblyName> property from a .csproj file content.
// Returns empty string if not found.
func extractAssemblyName(csprojContent string) string {
	const startTag = "<AssemblyName>"
	const endTag = "</AssemblyName>"
	start := strings.Index(csprojContent, startTag)
	if start < 0 {
		return ""
	}
	start += len(startTag)
	end := strings.Index(csprojContent[start:], endTag)
	if end < 0 {
		return ""
	}
	name := strings.TrimSpace(csprojContent[start : start+end])
	if name == "" || strings.ContainsAny(name, "$()") {
		// Skip MSBuild property references like $(MSBuildProjectName)
		return ""
	}
	return name
}

// codeDeployOptions holds optional flag overrides for code deploy configuration.
type codeDeployOptions struct {
	runtime       string
	entryPoint    string
	depResolution string
}

// promptCodeConfig prompts for code deploy configuration (runtime, entry point,
// dependency resolution). When noPrompt is true, flags or defaults are used without prompting.
func promptCodeConfig(ctx context.Context, azdClient *azdext.AzdClient, srcDir string, noPrompt bool, opts codeDeployOptions, userProvidedManifest bool) (*agent_yaml.CodeConfiguration, error) {
	if srcDir == "" {
		srcDir = "."
	}

	// Prompt for runtime — filter choices based on detected project type
	var runtimeChoices []*azdext.SelectChoice
	isDotnet := isDotnetProject(srcDir)
	isPython := isPythonProject(srcDir)

	if isDotnet && !isPython {
		runtimeChoices = []*azdext.SelectChoice{
			{Label: ".NET 10", Value: "dotnet_10"},
		}
	} else if isPython && !isDotnet {
		runtimeChoices = []*azdext.SelectChoice{
			{Label: "Python 3.13", Value: "python_3_13"},
			{Label: "Python 3.14", Value: "python_3_14"},
		}
	} else {
		// Mixed or unknown — show all options
		runtimeChoices = []*azdext.SelectChoice{
			{Label: "Python 3.13", Value: "python_3_13"},
			{Label: "Python 3.14", Value: "python_3_14"},
			{Label: ".NET 10", Value: "dotnet_10"},
		}
	}

	var runtime string
	if opts.runtime != "" {
		runtime = opts.runtime
	} else if noPrompt || userProvidedManifest {
		if isDotnet && !isPython {
			runtime = "dotnet_10"
		} else {
			runtime = "python_3_13"
		}
		log.Printf("Auto-detected runtime: %s", runtime)
	} else {
		defaultIdx := int32(0) // First item in the filtered list
		runtimeResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "Select the runtime for your agent",
				Choices:       runtimeChoices,
				SelectedIndex: &defaultIdx,
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("runtime selection was cancelled")
			}
			return nil, fmt.Errorf("failed to prompt for runtime: %w", err)
		}
		runtime = runtimeChoices[*runtimeResp.Value].Value
	}

	// Prompt for entry point
	defaultEntryPoint := detectDefaultEntryPoint(srcDir, runtime)

	var entryPoint string
	if opts.entryPoint != "" {
		entryPoint = opts.entryPoint
	} else if noPrompt || userProvidedManifest {
		entryPoint = defaultEntryPoint
		log.Printf("Auto-detected entry point: %s", entryPoint)
	} else {
		entryPointResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:      "Enter the file path for the entry point of the agent",
				DefaultValue: defaultEntryPoint,
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("entry point prompt was cancelled")
			}
			return nil, fmt.Errorf("failed to prompt for entry point: %w", err)
		}
		entryPoint = entryPointResp.Value
	}

	// Prompt for dependency resolution
	depResChoices := []*azdext.SelectChoice{
		{Label: "Remote build (dependencies installed on server during deployment)", Value: "remote_build"},
		{Label: "Bundled (dependencies pre-installed locally and included in ZIP)", Value: "bundled"},
	}

	var depResolution string
	if opts.depResolution != "" {
		depResolution = opts.depResolution
	} else if noPrompt || userProvidedManifest {
		depResolution = "remote_build"
		log.Printf("Defaulted dependency resolution to remote_build")
	} else {
		depDefaultIdx := int32(0)
		depResResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "How should dependencies be resolved?",
				Choices:       depResChoices,
				SelectedIndex: &depDefaultIdx,
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("dependency resolution selection was cancelled")
			}
			return nil, fmt.Errorf("failed to prompt for dependency resolution: %w", err)
		}
		depResolution = depResChoices[*depResResp.Value].Value
	}

	return &agent_yaml.CodeConfiguration{
		Runtime:              runtime,
		EntryPoint:           entryPoint,
		DependencyResolution: &depResolution,
	}, nil
}

// isPythonProject returns true if the directory appears to be a Python project,
// determined by the presence of requirements.txt or any .py file.
func isPythonProject(dir string) bool {
	if dir == "" {
		dir = "."
	}
	// Check for requirements.txt
	if _, err := os.Stat(filepath.Join(dir, "requirements.txt")); err == nil {
		return true
	}
	// Check for any .py file (shallow scan)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") {
			return true
		}
	}
	return false
}

// isDotnetProject returns true if the directory contains a .csproj file.
// NOTE: .fsproj (F#) is not yet supported by the packaging path (packageDotnetBundled/detectDefaultEntryPoint).
func isDotnetProject(dir string) bool {
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".csproj") {
			return true
		}
	}
	return false
}
