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
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/registry_api"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

type initFlags struct {
	*rootFlagsDefinition
	projectResourceId string
	modelDeployment   string
	model             string
	manifestPointer   string
	src               string
	env               string
	protocols         []string
}

// AiProjectResourceConfig represents the configuration for an AI project resource
type AiProjectResourceConfig struct {
	Models []map[string]any `json:"models,omitempty"`
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
	models        *modelSelector

	deploymentDetails []project.Deployment
	httpClient        *http.Client
}

// modelSelector encapsulates the dependencies needed for model selection and
// deployment resolution during init. It avoids constructing partial InitAction
// structs when only the model-selection call chain is needed.
type modelSelector struct {
	azdClient    *azdext.AzdClient
	azureContext *azdext.AzureContext
	environment  *azdext.Environment
	flags        *initFlags

	modelCatalog         map[string]*azdext.AiModel
	locationWarningShown bool
}

func (a *InitAction) getModelSelector() *modelSelector {
	if a.models == nil {
		a.models = &modelSelector{
			azdClient:    a.azdClient,
			azureContext: a.azureContext,
			environment:  a.environment,
			flags:        a.flags,
		}
	}
	return a.models
}

// GitHubUrlInfo holds parsed information from a GitHub URL
type GitHubUrlInfo struct {
	RepoSlug string
	Branch   string
	FilePath string
	Hostname string
}

const AiAgentHost = "azure.ai.agent"
const agentsV2ModelCapability = "agentsV2"

// checkAiModelServiceAvailable is a temporary check to ensure the azd host supports
// required gRPC services. Remove once azd core enforces requiredAzdVersion.
func checkAiModelServiceAvailable(ctx context.Context, azdClient *azdext.AzdClient) error {
	_, err := azdClient.Ai().ListModels(ctx, &azdext.ListModelsRequest{})
	if err == nil {
		return nil
	}

	if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
		return exterrors.Compatibility(
			exterrors.CodeIncompatibleAzdVersion,
			"this version of the azure.ai.agents extension is incompatible with your installed version of azd.",
			"upgrade azd to the latest version (https://aka.ms/azd/upgrade) and retry",
		)
	}

	return nil
}

// runInitFromManifest sets up Azure context, credentials, console, and runs the
// InitAction for a given manifest pointer. This is the shared code path used when
// initializing from a manifest URL/path (the -m flag, agent template, or azd template
// that contains an agent manifest).
func runInitFromManifest(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
) error {
	// Ensure project and environment exist (no subscription/location prompting yet)
	projectConfig, err := ensureProject(ctx, flags, azdClient)
	if err != nil {
		return err
	}

	// Get or create environment
	env := getExistingEnvironment(ctx, flags.env, azdClient)
	if env == nil {
		fmt.Println("Lets create a new default azd environment for your project.")
		env, err = createNewEnvironment(ctx, azdClient, flags.env)
		if err != nil {
			return err
		}
	}

	// Load whatever Azure context values already exist in the environment
	azureContext, err := loadAzureContext(ctx, azdClient, env.Name)
	if err != nil {
		return err
	}

	// Create credential with whatever tenant is available (may be empty → default tenant)
	credential, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   azureContext.Scope.TenantId,
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("failed to create Azure credential: %s", err),
			"run 'azd auth login' to authenticate",
		)
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
		environment:   env,
		flags:         flags,
		httpClient:    httpClient,
	}

	return action.Run(ctx)
}

func newInitCommand(rootFlags *rootFlagsDefinition) *cobra.Command {
	flags := &initFlags{
		rootFlagsDefinition: rootFlags,
	}

	cmd := &cobra.Command{
		Use:   "init [-m <manifest pointer>] [--src <source directory>]",
		Short: fmt.Sprintf("Initialize a new AI agent project. %s", color.YellowString("(Preview)")),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			printBanner(cmd.OutOrStdout())

			ctx := azdext.WithAccessToken(cmd.Context())

			setupDebugLogging(cmd.Flags())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return exterrors.Internal(exterrors.CodeAzdClientFailed, fmt.Sprintf("failed to create azd client: %s", err))
			}
			defer azdClient.Close()

			if err := checkAiModelServiceAvailable(ctx, azdClient); err != nil {
				return err
			}

			// Wait for debugger if AZD_EXT_DEBUG is set
			if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
					return nil
				}
				return fmt.Errorf("failed waiting for debugger: %w", err)
			}

			var httpClient = &http.Client{
				Timeout: 30 * time.Second,
			}

			// Auto-detect an existing agent manifest in the target directory
			// when no --manifest flag was provided.
			if flags.manifestPointer == "" {
				checkDir := flags.src
				if checkDir == "" {
					checkDir = "."
				}
				detected, detectErr := detectLocalManifest(checkDir)
				if detectErr != nil {
					return fmt.Errorf("checking for existing manifest: %w", detectErr)
				}
				if detected != "" {
					useExisting := flags.NoPrompt
					if !flags.NoPrompt {
						confirmResp, promptErr := azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
							Options: &azdext.ConfirmOptions{
								Message: fmt.Sprintf(
									"An existing agent manifest was found at %q. Use it?",
									detected,
								),
								DefaultValue: new(true),
							},
						})
						if promptErr != nil {
							if exterrors.IsCancellation(promptErr) {
								return exterrors.Cancelled("initialization was cancelled")
							}
							return fmt.Errorf("prompting for manifest detection: %w", promptErr)
						}
						useExisting = *confirmResp.Value
					}
					if useExisting {
						flags.manifestPointer = detected
						if flags.src == "" {
							flags.src = checkDir
						}
					}
				}
			}

			if flags.manifestPointer != "" {
				if err := runInitFromManifest(ctx, flags, azdClient, httpClient); err != nil {
					if exterrors.IsCancellation(err) {
						return exterrors.Cancelled("initialization was cancelled")
					}
					return err
				}
			} else {
				// No manifest provided - prompt user for init mode
				initMode, err := promptInitMode(ctx, azdClient)
				if err != nil {
					if exterrors.IsCancellation(err) {
						return exterrors.Cancelled("initialization was cancelled")
					}
					return err
				}

				switch initMode {
				case initModeTemplate:
					// User chose to start from a template - select one
					selectedTemplate, err := promptAgentTemplate(ctx, azdClient, httpClient, flags.NoPrompt)
					if err != nil {
						if exterrors.IsCancellation(err) {
							return exterrors.Cancelled("initialization was cancelled")
						}
						return err
					}

					switch selectedTemplate.EffectiveType() {
					case TemplateTypeAzd:
						// Full azd template - dispatch azd init -t <repo>
						initArgs := []string{"init", "-t", selectedTemplate.Source}
						if flags.env != "" {
							initArgs = append(initArgs, "--environment", flags.env)
						} else {
							cwd, err := os.Getwd()
							if err == nil {
								sanitizedDirectoryName := sanitizeAgentName(filepath.Base(cwd))
								initArgs = append(
									initArgs, "--environment", sanitizedDirectoryName+"-dev",
								)
							}
						}

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
							if exterrors.IsCancellation(err) {
								return exterrors.Cancelled("initialization was cancelled")
							}
							return exterrors.Dependency(
								exterrors.CodeProjectInitFailed,
								fmt.Sprintf(
									"failed to initialize project from template: %s", err,
								),
								"",
							)
						}

						fmt.Printf(
							"\nProject initialized from template: %s\n",
							selectedTemplate.Title,
						)

						// Search for an agent manifest in the scaffolded project
						cwd, err := os.Getwd()
						if err != nil {
							return fmt.Errorf("getting current directory: %w", err)
						}

						manifestPath, err := findAgentManifest(cwd)
						if err != nil {
							return fmt.Errorf("searching for agent manifest: %w", err)
						}

						if manifestPath != "" {
							flags.manifestPointer = manifestPath
							if err := runInitFromManifest(ctx, flags, azdClient, httpClient); err != nil {
								if exterrors.IsCancellation(err) {
									return exterrors.Cancelled("initialization was cancelled")
								}
								return err
							}
						} else {
							fmt.Println("No agent manifest found in the scaffolded project.")
						}

					default:
						// Agent manifest template - use existing -m flow
						flags.manifestPointer = selectedTemplate.Source
						if err := runInitFromManifest(ctx, flags, azdClient, httpClient); err != nil {
							if exterrors.IsCancellation(err) {
								return exterrors.Cancelled("initialization was cancelled")
							}
							return err
						}
					}

				default:
					// initModeFromCode - use existing code in current directory
					action := &InitFromCodeAction{
						azdClient:  azdClient,
						flags:      flags,
						httpClient: httpClient,
					}

					if err := action.Run(ctx); err != nil {
						if exterrors.IsCancellation(err) {
							return exterrors.Cancelled("initialization was cancelled")
						}
						return err
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&flags.projectResourceId, "project-id", "p", "",
		"Existing Microsoft Foundry Project Id to initialize your azd environment with")

	cmd.Flags().StringVarP(&flags.modelDeployment, "model-deployment", "d", "",
		"Name of an existing model deployment to use from the Foundry project. Only used when paired with an existing Foundry project, either via --project-id or interactive prompts")

	cmd.Flags().StringVar(&flags.model, "model", "",
		"Name of the AI model to use (e.g., 'gpt-4o'). If not specified, defaults to 'gpt-4.1-mini'. Mutually exclusive with --model-deployment, with --model-deployment being used if both are provided")

	cmd.Flags().StringVarP(&flags.manifestPointer, "manifest", "m", "",
		"Path or URI to an agent manifest to add to your azd project")

	cmd.Flags().StringVarP(&flags.src, "src", "s", "",
		"Directory to download the agent definition to (defaults to 'src/<agent-id>')")

	cmd.Flags().StringVarP(&flags.env, "environment", "e", "", "The name of the azd environment to use.")

	cmd.Flags().StringSliceVar(&flags.protocols, "protocol", nil,
		"Protocols supported by the agent (e.g., 'responses', 'invocations'). Can be specified multiple times.")

	return cmd
}

func (a *InitAction) Run(ctx context.Context) error {

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

	// If --manifest is given
	if a.flags.manifestPointer != "" {
		// Validate that the manifest pointer is either a valid URL or existing file path
		isValidURL := false
		isValidFile := false

		if _, err := url.ParseRequestURI(a.flags.manifestPointer); err == nil {
			isValidURL = true
		} else if _, fileErr := os.Stat(a.flags.manifestPointer); fileErr == nil {
			isValidFile = true
		}

		if !isValidURL && !isValidFile {
			return exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("agent manifest pointer is invalid: '%s' is neither a valid URI nor an existing file path", a.flags.manifestPointer),
				"provide a valid URL or an existing local agent.yaml/agent.yml path",
			)
		}

		// Download/read agent.yaml file from the provided URI or file path
		agentManifest, targetDir, err := a.downloadAgentYaml(ctx, a.flags.manifestPointer, a.flags.src)
		if err != nil {
			return fmt.Errorf("downloading agent.yaml: %w", err)
		}

		// Model configuration: prompt user for "use existing" vs "deploy new"
		agentManifest, err = a.configureModelChoice(ctx, agentManifest)
		if err != nil {
			return fmt.Errorf("configuring model choice: %w", err)
		}

		// Prompt for manifest parameters (e.g. tool credentials) after project selection
		agentManifest, err = registry_api.ProcessManifestParameters(
			ctx, agentManifest, a.azdClient, a.flags.NoPrompt,
		)
		if err != nil {
			return fmt.Errorf("failed to process manifest parameters: %w", err)
		}

		// Inject toolbox MCP endpoint env vars into hosted agent definitions
		// so agent.yaml is self-documenting about what env vars will be set.
		if err := injectToolboxEnvVarsIntoDefinition(agentManifest); err != nil {
			return fmt.Errorf("injecting toolbox env vars: %w", err)
		}

		// Write the final agent.yaml to disk (after deployment names have been injected)
		if err := writeAgentDefinitionFile(targetDir, agentManifest); err != nil {
			return fmt.Errorf("writing agent definition: %w", err)
		}

		// Add the agent to the azd project (azure.yaml) services
		if err := a.addToProject(ctx, targetDir, agentManifest); err != nil {
			return fmt.Errorf("failed to add agent to azure.yaml: %w", err)
		}

		color.Green("\nAI agent definition added to your azd project successfully!")
	}

	return nil
}

func ensureProject(ctx context.Context, flags *initFlags, azdClient *azdext.AzdClient) (*azdext.ProjectConfig, error) {
	projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		fmt.Println("Let's get your project initialized.")

		// Environment creation is handled separately in ensureEnvironment
		initArgs := []string{"init", "-t", "Azure-Samples/azd-ai-starter-basic"}
		if flags.env != "" {
			initArgs = append(initArgs, "--environment", flags.env)
		} else {
			cwd, err := os.Getwd()
			if err == nil {
				sanitizedDirectoryName := sanitizeAgentName(filepath.Base(cwd))
				initArgs = append(initArgs, "--environment", sanitizedDirectoryName+"-dev")
			}
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
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("project initialization was cancelled")
			}
			return nil, exterrors.Dependency(
				exterrors.CodeProjectInitFailed,
				fmt.Sprintf("failed to initialize project: %s", err),
				"",
			)
		}

		projectResponse, err = azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
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

func getExistingEnvironment(ctx context.Context, envName string, azdClient *azdext.AzdClient) *azdext.Environment {
	var env *azdext.Environment
	if envName == "" {
		if envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
			env = envResponse.Environment
		}
	} else {
		if envResponse, err := azdClient.Environment().Get(ctx, &azdext.GetEnvironmentRequest{
			Name: envName,
		}); err == nil {
			env = envResponse.Environment
		}
	}

	return env
}

// manifestHasModelResources returns true if the manifest contains any model resources
// that need deployment configuration. Prompt agents always have a model. Hosted agents
// only need model config if they have resources with kind "model".
func manifestHasModelResources(manifest *agent_yaml.AgentManifest) bool {
	if _, ok := manifest.Template.(agent_yaml.PromptAgent); ok {
		return true
	}

	if manifest.Resources != nil {
		for _, resource := range manifest.Resources {
			if _, ok := resource.(agent_yaml.ModelResource); ok {
				return true
			}
		}
	}

	return false
}

// configureModelChoice presents the "use existing / deploy new" model configuration choice
// and establishes the necessary Azure context (subscription, location, project) before
// ProcessModels is called. This defers subscription/location prompting until we know
// which path the user wants.
func (a *InitAction) configureModelChoice(
	ctx context.Context, agentManifest *agent_yaml.AgentManifest,
) (*agent_yaml.AgentManifest, error) {
	// If --project-id is provided, validate the ARM format and extract the subscription ID
	// so ensureSubscription can skip the prompt and just resolve the tenant
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
	}

	// If the manifest has no model resources, skip the model configuration prompt
	// but still ensure subscription and location are set for agent creation.
	// When --project-id is provided, use the existing project to derive location
	// and configure Foundry env vars (ACR, AppInsights, etc.) instead of prompting.
	if !manifestHasModelResources(agentManifest) {
		if a.flags.projectResourceId != "" {
			newCred, err := ensureSubscription(
				ctx, a.azdClient, a.azureContext, a.environment.Name,
				"Select an Azure subscription to provision your agent and Foundry project resources.",
			)
			if err != nil {
				return nil, err
			}
			a.credential = newCred

			selectedProject, err := selectFoundryProject(
				ctx, a.azdClient, a.credential, a.azureContext, a.environment.Name,
				a.azureContext.Scope.SubscriptionId, a.flags.projectResourceId,
			)
			if err != nil {
				return nil, err
			}

			if selectedProject == nil {
				return nil, fmt.Errorf("foundry project not found: %s", a.flags.projectResourceId)
			}
		} else {
			newCred, err := ensureSubscriptionAndLocation(
				ctx, a.azdClient, a.azureContext, a.environment.Name,
				"Select an Azure subscription to provision your agent and Foundry project resources.",
			)
			if err != nil {
				return nil, err
			}
			a.credential = newCred
		}

		return agentManifest, nil
	}

	modelConfigChoices := []*azdext.SelectChoice{
		{Label: "Deploy new model(s) from the catalog", Value: "new"},
		{Label: "Use existing model deployment(s) from a Foundry project", Value: "existing"},
	}

	var modelConfigChoice string

	if a.flags.projectResourceId != "" {
		// --project-id provided: auto-select "existing" path
		modelConfigChoice = "existing"
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

	switch modelConfigChoice {
	case "existing":
		// Ensure subscription for project listing
		newCred, err := ensureSubscription(
			ctx, a.azdClient, a.azureContext, a.environment.Name,
			"Select an Azure subscription to look up available models and provision your Foundry project resources.",
		)
		if err != nil {
			return nil, err
		}
		a.credential = newCred

		// Select a Foundry project (sets AZURE_AI_PROJECT_ID, ACR, AppInsights env vars)
		selectedProject, err := selectFoundryProject(
			ctx, a.azdClient, a.credential, a.azureContext, a.environment.Name,
			a.azureContext.Scope.SubscriptionId, a.flags.projectResourceId,
		)
		if err != nil {
			return nil, err
		}

		if selectedProject == nil {
			// No existing project selected (no projects found or user chose "Create new") → fall back to "deploy new" path
			_, _ = color.New(color.Faint).Println(
				"No existing Foundry project was selected. Falling back to deploying a new model.",
			)
			if err := ensureLocation(ctx, a.azdClient, a.azureContext, a.environment.Name); err != nil {
				return nil, err
			}
		}

	case "new":
		// Ensure subscription + location for model catalog
		newCred, err := ensureSubscriptionAndLocation(
			ctx, a.azdClient, a.azureContext, a.environment.Name,
			"Select an Azure subscription to look up available models and provision your Foundry project resources.",
		)
		if err != nil {
			return nil, err
		}
		a.credential = newCred
	}

	// Now process models — getModelDeploymentDetails will branch based on AZURE_AI_PROJECT_ID
	agentManifest, deploymentDetails, err := a.ProcessModels(ctx, agentManifest)
	if err != nil {
		return nil, fmt.Errorf("failed to process model resources: %w", err)
	}
	a.deploymentDetails = deploymentDetails

	return agentManifest, nil
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
		return nil, "", fmt.Errorf("the path to an agent manifest needs to be provided (manifestPointer cannot be empty)")
	}

	var content []byte
	var err error
	var isGitHubUrl bool
	var urlInfo *GitHubUrlInfo
	var ghCli *github.Cli
	var console input.Console
	useGhCli := false

	// Check if manifestPointer is a local file path or a URI
	if a.isLocalFilePath(manifestPointer) {
		// Handle local file path
		fmt.Printf("Reading agent.yaml from local file: %s\n", manifestPointer)
		//nolint:gosec // manifest path is an explicit user-provided local path
		content, err = os.ReadFile(manifestPointer)
		if err != nil {
			return nil, "", exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("reading local file %s: %s", manifestPointer, err),
				"verify the file path exists and is readable",
			)
		}

		// Parse the YAML content into genericManifest
		var genericManifest map[string]any
		if err := yaml.Unmarshal(content, &genericManifest); err != nil {
			return nil, "", exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("parsing YAML from manifest file: %s", err),
				"verify the manifest file contains valid YAML",
			)
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
						DefaultValue: new(false),
					},
				})
				if err != nil {
					return nil, "", fmt.Errorf("prompting for confirmation: %w", err)
				}
				if !*confirmResponse.Value {
					return nil, "", exterrors.Cancelled("operation cancelled by user")
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
			return nil, "", exterrors.Dependency(
				exterrors.CodeGitHubDownloadFailed,
				fmt.Sprintf("ensuring gh is installed: %s", err),
				"install the GitHub CLI (gh) from https://cli.github.com",
			)
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
				//nolint:gosec // URL is constrained to GitHub API endpoint built from parsed GitHub URL
				resp, err := a.httpClient.Do(req)
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
				return nil, "", exterrors.Dependency(
					exterrors.CodeGitHubDownloadFailed,
					fmt.Sprintf("downloading from GitHub: %s", err),
					"verify the URL points to a valid agent.yaml file in the repository",
				)
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
		return nil, "", exterrors.Validation(
			exterrors.CodeInvalidManifestPointer,
			fmt.Sprintf("manifest pointer '%s' is not a valid local file path, GitHub URL, or registry URL", manifestPointer),
			"provide a valid URL or an existing local agent.yaml/agent.yml path",
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

	_, isPromptAgent := agentManifest.Template.(agent_yaml.PromptAgent)
	if isPromptAgent {
		agentManifest, err = agent_yaml.ProcessPromptAgentToolsConnections(ctx, agentManifest, a.azdClient)
		if err != nil {
			return nil, "", fmt.Errorf("failed to process prompt agent tools connections: %w", err)
		}
	}

	// Create target directory if it doesn't exist
	//nolint:gosec // project scaffold directory should be readable and traversable
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
			err := downloadParentDirectory(ctx, urlInfo, targetDir, ghCli, console, useGhCli, a.httpClient)
			if err != nil {
				return nil, "", exterrors.Dependency(
					exterrors.CodeGitHubDownloadFailed,
					fmt.Sprintf("downloading parent directory: %s", err),
					"verify the URL points to a valid repository and you have access",
				)
			}
		}
	}

	return agentManifest, targetDir, nil
}

// writeAgentDefinitionFile writes the agent definition to disk as agent.yaml in targetDir.
// This should be called after all parameter/deployment injection is complete so the on-disk
// file has fully resolved values (no `{{...}}` placeholders).
func writeAgentDefinitionFile(targetDir string, agentManifest *agent_yaml.AgentManifest) error {
	content, err := yaml.Marshal(agentManifest.Template)
	if err != nil {
		return fmt.Errorf("marshaling agent manifest to YAML: %w", err)
	}

	annotation := "# yaml-language-server: $schema=https://raw.githubusercontent.com/microsoft/AgentSchema/refs/heads/main/schemas/v1.0/ContainerAgent.yaml"
	agentFileContents := bytes.NewBufferString(annotation + "\n\n")
	if _, err = agentFileContents.Write(content); err != nil {
		return fmt.Errorf("preparing agent.yaml file contents: %w", err)
	}

	filePath := filepath.Join(targetDir, "agent.yaml")
	if err := os.WriteFile(filePath, agentFileContents.Bytes(), osutil.PermissionFile); err != nil {
		return fmt.Errorf("saving file to %s: %w", filePath, err)
	}

	fmt.Printf("Processed agent.yaml at %s\n", filePath)
	return nil
}

func (a *InitAction) addToProject(ctx context.Context, targetDir string, agentManifest *agent_yaml.AgentManifest) error {
	// Convert the template to bytes
	templateBytes, err := json.Marshal(agentManifest.Template)
	if err != nil {
		return fmt.Errorf("failed to marshal agent template to JSON: %w", err)
	}

	// Convert the bytes to a dictionary
	var templateDict map[string]any
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

	// Process toolbox resources from the manifest
	toolboxes, toolConnections, credEnvVars, err := extractToolboxAndConnectionConfigs(agentManifest)
	if err != nil {
		return err
	}
	agentConfig.Toolboxes = toolboxes
	agentConfig.ToolConnections = toolConnections

	// Persist credential values as azd environment variables so they are
	// resolved at provision/deploy time instead of stored in azure.yaml.
	for envKey, envVal := range credEnvVars {
		if _, setErr := a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: a.environment.Name,
			Key:     envKey,
			Value:   envVal,
		}); setErr != nil {
			return fmt.Errorf("storing credential env var %s: %w", envKey, setErr)
		}
	}

	// Process connection resources from the manifest
	connections, connCredEnvVars, err := extractConnectionConfigs(agentManifest)
	if err != nil {
		return err
	}
	agentConfig.Connections = connections

	// Store connection credential env vars alongside toolbox ones
	for envKey, envVal := range connCredEnvVars {
		if _, setErr := a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: a.environment.Name,
			Key:     envKey,
			Value:   envVal,
		}); setErr != nil {
			return fmt.Errorf("storing credential env var %s: %w", envKey, setErr)
		}
	}

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
		Name:         strings.ReplaceAll(agentDef.Name, " ", ""),
		RelativePath: targetDir,
		Host:         AiAgentHost,
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
	if projectID, _ := a.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: a.environment.Name,
		Key:     "AZURE_AI_PROJECT_ID",
	}); projectID != nil && projectID.Value != "" {
		fmt.Printf("To deploy your agent, use %s.\n", color.HiBlueString("azd deploy %s", agentDef.Name))
	} else {
		fmt.Printf("To provision and deploy the whole solution, use %s.\n", color.HiBlueString("azd up"))
	}
	return nil
}

func (a *InitAction) populateContainerSettings(ctx context.Context) (*project.ContainerSettings, error) {
	choices := make([]*azdext.SelectChoice, len(project.ResourceTiers))
	for i, t := range project.ResourceTiers {
		choices[i] = &azdext.SelectChoice{
			Label: t.String(),
			Value: fmt.Sprintf("%d", i),
		}
	}

	defaultIndex := int32(0)
	resp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "Select container resource allocation (CPU and Memory) for your agent. You can adjust these settings later in the azure.yaml file if needed.",
			Choices:       choices,
			SelectedIndex: &defaultIndex,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("prompting for container resources: %w", err)
	}

	selected := project.ResourceTiers[*resp.Value]

	containerSettings := &project.ContainerSettings{
		Resources: &project.ResourceSettings{
			Memory: selected.Memory,
			Cpu:    selected.Cpu,
		},
	}

	if !isVNextEnabled(ctx, a.azdClient) {
		containerSettings.Scale = &project.ScaleSettings{
			MinReplicas: project.DefaultMinReplicas,
			MaxReplicas: project.DefaultMaxReplicas,
		}
	}

	return containerSettings, nil
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

		branch, filePath, ok := strings.Cut(parts[1], "/")
		if !ok {
			return nil
		}

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
		if rest, ok := strings.CutPrefix(rest, "refs/heads/"); ok {
			branch, filePath, ok := strings.Cut(rest, "/")
			if !ok {
				return nil
			}

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
	ctx context.Context, urlInfo *GitHubUrlInfo, targetDir string, ghCli *github.Cli, console input.Console, useGhCli bool, httpClient *http.Client) error {

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
		if err := downloadDirectoryContentsWithoutGhCli(ctx, urlInfo.RepoSlug, parentDirPath, urlInfo.Branch, targetDir, httpClient); err != nil {
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
	var dirContents []map[string]any
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
			fmt.Printf("%s\n", color.New(color.Faint).Sprintf("Downloading file: %s", itemPath))
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

			//nolint:gosec // downloaded project files are intended to be readable by project tooling
			if err := os.WriteFile(itemLocalPath, []byte(fileContent), 0644); err != nil {
				return fmt.Errorf("failed to write file %s: %w", itemLocalPath, err)
			}
		} else if itemType == "dir" {
			// Recursively download subdirectory
			fmt.Printf("Downloading directory: %s\n", itemPath)
			//nolint:gosec // scaffolded directories are intended to be readable/traversable
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
	ctx context.Context, repoSlug string, dirPath string, branch string, localPath string, httpClient *http.Client) error {

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

	//nolint:gosec // URL is explicitly constructed for GitHub contents API
	resp, err := httpClient.Do(req)
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
	var dirContents []map[string]any
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
			fmt.Printf("%s\n", color.New(color.Faint).Sprintf("Downloading file: %s", itemPath))
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

			//nolint:gosec // URL is explicitly constructed for GitHub contents API
			fileResp, err := httpClient.Do(fileReq)
			if err != nil {
				return fmt.Errorf("failed to download file %s: %w", itemPath, err)
			}

			if fileResp.StatusCode != http.StatusOK {
				return fmt.Errorf("failed to download file %s: status %d", itemPath, fileResp.StatusCode)
			}

			fileContent, err := io.ReadAll(fileResp.Body)
			_ = fileResp.Body.Close()
			if err != nil {
				return fmt.Errorf("failed to read file content %s: %w", itemPath, err)
			}

			//nolint:gosec // downloaded project files are intended to be readable by project tooling
			if err := os.WriteFile(itemLocalPath, fileContent, 0644); err != nil {
				return fmt.Errorf("failed to write file %s: %w", itemLocalPath, err)
			}
		} else if itemType == "dir" {
			// Recursively download subdirectory
			fmt.Printf("Downloading directory: %s\n", itemPath)
			//nolint:gosec // scaffolded directories are intended to be readable/traversable
			if err := os.MkdirAll(itemLocalPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", itemLocalPath, err)
			}

			// Recursively download directory contents
			if err := downloadDirectoryContentsWithoutGhCli(ctx, repoSlug, itemPath, branch, itemLocalPath, httpClient); err != nil {
				return fmt.Errorf("failed to download subdirectory %s: %w", itemPath, err)
			}
		}
	}

	return nil
}

// extractToolboxAndConnectionConfigs extracts toolbox resource definitions from the agent manifest
// and converts them into project.Toolbox config entries and project.ToolConnection entries.
// Tools with a target/authType also produce connection entries for Bicep provisioning.
// Built-in tools (bing_grounding, azure_ai_search, etc.) produce toolbox tools but no connections.
func extractToolboxAndConnectionConfigs(
	manifest *agent_yaml.AgentManifest,
) ([]project.Toolbox, []project.ToolConnection, map[string]string, error) {
	if manifest == nil || manifest.Resources == nil {
		return nil, nil, nil, nil
	}

	var toolboxes []project.Toolbox
	var connections []project.ToolConnection
	// credentialEnvVars maps generated env var names to their raw values so
	// the caller can persist them in the azd environment.
	credentialEnvVars := map[string]string{}

	for _, resource := range manifest.Resources {
		tbResource, ok := resource.(agent_yaml.ToolboxResource)
		if !ok {
			continue
		}

		description := tbResource.Description

		if len(tbResource.Tools) == 0 {
			return nil, nil, nil, fmt.Errorf(
				"toolbox resource '%s' is missing required 'tools'",
				tbResource.Name,
			)
		}

		var tools []map[string]any
		for _, rawTool := range tbResource.Tools {
			toolMap, ok := rawTool.(map[string]any)
			if !ok {
				return nil, nil, nil, fmt.Errorf(
					"toolbox resource '%s' has invalid tool entry: expected object",
					tbResource.Name,
				)
			}

			// Manifest and API both use "type" for tool kind
			toolType, _ := toolMap["type"].(string)

			target, _ := toolMap["target"].(string)
			if target == "" {
				// No target — either a built-in tool or a pre-configured tool
				// that already has project_connection_id. Pass through as-is.
				result := make(map[string]any, len(toolMap))
				for k, v := range toolMap {
					result[k] = v
				}
				tools = append(tools, result)
				continue
			}

			// External tools with target/authType need a connection
			toolName, _ := toolMap["name"].(string)
			authType, _ := toolMap["authType"].(string)
			credentials, _ := toolMap["credentials"].(map[string]any)

			connName := toolName
			if connName == "" {
				connName = tbResource.Name + "-" + toolType
			}

			conn := project.ToolConnection{
				Name:     connName,
				Category: "RemoteTool",
				Target:   target,
				AuthType: authType,
			}

			// Extract credentials, storing raw values as env vars and
			// replacing them with ${VAR} references in the config.
			if len(credentials) > 0 {
				creds := make(map[string]any, len(credentials))
				for k, v := range credentials {
					envVar := credentialEnvVarName(connName, k)
					credentialEnvVars[envVar] = fmt.Sprintf("%v", v)
					creds[k] = fmt.Sprintf("${%s}", envVar)
				}

				conn.Credentials = creds
			}

			connections = append(connections, conn)

			// Preserve all tool fields, replacing consumed connection fields
			// with the project_connection_id reference.
			tool := make(map[string]any, len(toolMap))
			for k, v := range toolMap {
				tool[k] = v
			}
			tool["type"] = toolType
			tool["project_connection_id"] = connName
			delete(tool, "target")
			delete(tool, "authType")
			delete(tool, "credentials")
			tools = append(tools, tool)
		}

		toolboxes = append(toolboxes, project.Toolbox{
			Name:        tbResource.Name,
			Description: description,
			Tools:       tools,
		})
	}

	return toolboxes, connections, credentialEnvVars, nil
}

// credentialEnvVarName builds a deterministic env var name for a connection
// credential key, e.g. ("github-copilot", "clientSecret") → "TOOL_GITHUB_COPILOT_CLIENTSECRET".
// All non-alphanumeric characters are replaced with underscores and consecutive
// underscores are collapsed to produce a valid [A-Z0-9_]+ environment variable name.
var nonAlphanumRe = regexp.MustCompile(`[^A-Z0-9]+`)

func credentialEnvVarName(connName, key string) string {
	s := "TOOL_" + strings.ToUpper(connName) + "_" + strings.ToUpper(key)
	return nonAlphanumRe.ReplaceAllString(s, "_")
}

// injectToolboxEnvVarsIntoDefinition adds TOOLBOX_{NAME}_MCP_ENDPOINT entries
// to the environment_variables section of a hosted agent definition for each toolbox
// resource in the manifest. Returns an error if two toolboxes produce the same
// environment variable name or if the key already exists in the definition.
func injectToolboxEnvVarsIntoDefinition(manifest *agent_yaml.AgentManifest) error {
	if manifest == nil || manifest.Resources == nil {
		return nil
	}

	containerAgent, ok := manifest.Template.(agent_yaml.ContainerAgent)
	if !ok {
		return nil
	}

	// Collect toolbox resource names
	var toolboxNames []string
	for _, resource := range manifest.Resources {
		if tbResource, ok := resource.(agent_yaml.ToolboxResource); ok {
			toolboxNames = append(toolboxNames, tbResource.Name)
		}
	}
	if len(toolboxNames) == 0 {
		return nil
	}

	if containerAgent.EnvironmentVariables == nil {
		envVars := []agent_yaml.EnvironmentVariable{}
		containerAgent.EnvironmentVariables = &envVars
	}

	existingNames := make(map[string]bool, len(*containerAgent.EnvironmentVariables))
	for _, ev := range *containerAgent.EnvironmentVariables {
		existingNames[ev.Name] = true
	}

	for _, tbName := range toolboxNames {
		envKey := toolboxMCPEndpointEnvKey(tbName)
		if existingNames[envKey] {
			return fmt.Errorf(
				"duplicate toolbox environment variable %q (from toolbox %q)",
				envKey, tbName,
			)
		}
		existingNames[envKey] = true
		*containerAgent.EnvironmentVariables = append(
			*containerAgent.EnvironmentVariables,
			agent_yaml.EnvironmentVariable{
				Name:  envKey,
				Value: fmt.Sprintf("${%s}", envKey),
			},
		)
	}

	manifest.Template = containerAgent
	return nil
}

// extractConnectionConfigs extracts connection resource definitions from the agent manifest
// and converts them into project.Connection config entries. Credential values are externalized
// to environment variables and replaced with ${VAR} references in the returned connections.
func extractConnectionConfigs(
	manifest *agent_yaml.AgentManifest,
) ([]project.Connection, map[string]string, error) {
	if manifest == nil || manifest.Resources == nil {
		return nil, nil, nil
	}

	var connections []project.Connection
	credentialEnvVars := map[string]string{}

	for _, resource := range manifest.Resources {
		connResource, ok := resource.(agent_yaml.ConnectionResource)
		if !ok {
			continue
		}

		creds := maps.Clone(connResource.Credentials)
		authType := string(connResource.AuthType)

		// Surface credentials.type to top-level authType when not explicitly set.
		// This must happen before externalization so we capture the raw value.
		if authType == "" && len(creds) > 0 {
			if credType, ok := creds["type"].(string); ok && credType != "" {
				authType = credType
				delete(creds, "type")
			}
		}

		// Externalize credential values to env vars and replace with ${VAR} references.
		if len(creds) > 0 {
			externalizedCreds := make(map[string]any, len(creds))
			for k, v := range creds {
				envVar := credentialEnvVarName(connResource.Name, k)
				credentialEnvVars[envVar] = fmt.Sprintf("%v", v)
				externalizedCreds[k] = fmt.Sprintf("${%s}", envVar)
			}
			creds = externalizedCreds
		}

		conn := project.Connection{
			Name:                        connResource.Name,
			Category:                    string(connResource.Category),
			Target:                      connResource.Target,
			AuthType:                    authType,
			Credentials:                 creds,
			Metadata:                    connResource.Metadata,
			ExpiryTime:                  connResource.ExpiryTime,
			IsSharedToAll:               connResource.IsSharedToAll,
			SharedUserList:              connResource.SharedUserList,
			PeRequirement:               connResource.PeRequirement,
			PeStatus:                    connResource.PeStatus,
			UseWorkspaceManagedIdentity: connResource.UseWorkspaceManagedIdentity,
			Error:                       connResource.Error,
			AuthorizationUrl:            connResource.AuthorizationUrl,
			TokenUrl:                    connResource.TokenUrl,
			RefreshUrl:                  connResource.RefreshUrl,
			Scopes:                      connResource.Scopes,
			Audience:                    connResource.Audience,
			ConnectorName:               connResource.ConnectorName,
		}

		connections = append(connections, conn)
	}

	return connections, credentialEnvVars, nil
}
