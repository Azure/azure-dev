// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/braydonk/yaml"
)

// serviceIsPromptAgent reports whether the service config describes a prompt
// (kind=managed) agent. Prompt agents carry a populated `promptAgent` block in
// their azure.yaml service config; hosted/workflow agents leave it nil.
func serviceIsPromptAgent(serviceConfig *azdext.ServiceConfig) bool {
	if serviceConfig == nil || serviceConfig.Config == nil {
		return false
	}
	var cfg ServiceTargetAgentConfig
	if err := UnmarshalStruct(serviceConfig.Config, &cfg); err != nil {
		return false
	}
	return cfg.PromptAgent != nil
}

// isPromptAgentService reports whether the provider's current service is a
// prompt agent.
func (p *AgentServiceTargetProvider) isPromptAgentService() bool {
	return serviceIsPromptAgent(p.serviceConfig)
}

// promptAgentSettings extracts and validates the prompt-agent harness settings
// from the service config, applying environment-variable overrides.
func (p *AgentServiceTargetProvider) promptAgentSettings() (*PromptAgentSettings, error) {
	var cfg ServiceTargetAgentConfig
	if err := UnmarshalStruct(p.serviceConfig.Config, &cfg); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("failed to parse service config: %s", err),
			"check the service configuration in azure.yaml",
		)
	}
	if cfg.PromptAgent == nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"service config is missing the promptAgent block",
			"re-run `azd ai agent init` to scaffold the prompt agent service",
		)
	}
	cfg.PromptAgent.ApplyEnvOverrides()
	if err := cfg.PromptAgent.Validate(); err != nil {
		return nil, err
	}
	return cfg.PromptAgent, nil
}

// loadPromptAgentDefinition reads the agent.yaml as a bare ManagedAgent.
//
// Convention: when the YAML omits inline `instructions:`, a sibling
// `instructions.md` (next to agent.yaml) is used as the agent's instructions.
// Inline `instructions:` always takes precedence over the file.
func (p *AgentServiceTargetProvider) loadPromptAgentDefinition() (agent_yaml.ManagedAgent, error) {
	data, err := os.ReadFile(p.agentDefinitionPath)
	if err != nil {
		return agent_yaml.ManagedAgent{}, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to read agent manifest file: %s", err),
			"verify the agent.yaml file exists and is readable",
		)
	}
	if err := validatePromptAgentRawFields(data); err != nil {
		return agent_yaml.ManagedAgent{}, err
	}
	var managed agent_yaml.ManagedAgent
	if err := yaml.Unmarshal(data, &managed); err != nil {
		return agent_yaml.ManagedAgent{}, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent.yaml is not a valid prompt agent: %s", err),
			"fix the agent.yaml to match the prompt (managed) agent schema",
		)
	}
	if !strings.EqualFold(string(managed.Kind), string(agent_yaml.AgentKindManaged)) {
		return agent_yaml.ManagedAgent{}, exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			fmt.Sprintf("agent.yaml declares kind %q, expected managed", managed.Kind),
			"use kind: managed for prompt agents",
		)
	}

	// Convention: fall back to a sibling instructions.md when instructions are
	// not declared inline. Inline instructions win.
	if strings.TrimSpace(managed.Instructions) == "" {
		instructionsPath := filepath.Join(filepath.Dir(p.agentDefinitionPath), promptInstructionsFileName)
		if content, readErr := os.ReadFile(instructionsPath); readErr == nil {
			managed.Instructions = string(content)
		}
	}

	return managed, nil
}

// promptInstructionsFileName is the conventional sidecar file whose contents
// become the prompt agent's instructions when none are declared inline.
const promptInstructionsFileName = "instructions.md"

// containerOnlyPromptFields lists agent.yaml keys that are only meaningful for
// hosted (container) agents and are therefore rejected for kind: prompt.
var containerOnlyPromptFields = []string{
	"image",
	"protocols",
	"agent_endpoint",
	"agent_card",
	"code_configuration",
	"docker",
	"runtime",
	"startupCommand",
	"startup_command",
}

// validatePromptAgentRawFields rejects container-only fields on a prompt agent.
//
// The YAML decoder silently drops unknown fields, so a probe decode into a
// generic map is used to detect container-only keys that the typed ManagedAgent
// would otherwise ignore, surfacing a clear error instead of silently ignoring
// misplaced configuration.
func validatePromptAgentRawFields(data []byte) error {
	var probe map[string]any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		// A malformed document is reported by the typed decode with a better
		// message; don't duplicate the error here.
		return nil
	}
	for _, field := range containerOnlyPromptFields {
		if _, ok := probe[field]; ok {
			return exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("field %q is not valid for a prompt (kind: managed) agent", field),
				"remove container-only fields (image, protocols, code_configuration, ...) "+
					"or use kind: hosted for container agents",
			)
		}
	}
	return nil
}

// deployPromptAgent creates (or updates) the prompt agent on the managed
// harness and registers the resulting agent identity in the azd environment.
// It is the prompt-agent analogue of deployHostedAgent, dispatched from
// Deploy() when the service is a prompt agent.
func (p *AgentServiceTargetProvider) deployPromptAgent(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic in deployPromptAgent: %v\n%s\n", r, debug.Stack())
			panic(r)
		}
	}()

	managed, err := p.loadPromptAgentDefinition()
	if err != nil {
		return nil, err
	}

	settings, err := p.promptAgentSettings()
	if err != nil {
		return nil, err
	}

	// Overlay the provisioned Foundry project values from the azd environment
	// onto any settings still at their default placeholder. This makes the
	// "create a new Foundry project" init path work: `azd up` provisions the
	// project, and the deploy targets it. The overlay is a no-op unless the azd
	// environment actually holds a resolved project (AZURE_AI_PROJECT_NAME),
	// so the local-dev fake tuple is preserved when no project was provisioned.
	projectScopedTarget := false
	if env, envErr := p.azdEnvValues(ctx); envErr == nil {
		mappedFromProjectID, mapErr := ResolvePromptTargetFromEnv(settings, env)
		if mapErr != nil {
			return nil, mapErr
		}
		projectScopedTarget = mappedFromProjectID
		if projectScopedTarget {
			fmt.Fprintf(
				os.Stderr,
				"Resolved managed prompt target from AZURE_AI_PROJECT_ID: subscription=%q resourceGroup=%q workspace=%q.\n",
				settings.SubscriptionID,
				settings.ResourceGroup,
				settings.Workspace,
			)
		}

		// When the service already has an explicit non-placeholder workspace,
		// trust it and avoid the RG-wide discovery path entirely.
		workspaceKnown := strings.TrimSpace(settings.Workspace) != "" &&
			settings.Workspace != DefaultPromptWorkspace

		if !workspaceKnown && !projectScopedTarget {
			if ws, ok := p.resolvePromptWorkspaceFromAzure(ctx, settings, env); ok {
				if !strings.EqualFold(ws, settings.Workspace) {
					fmt.Fprintf(os.Stderr, "Resolved prompt workspace to %q (was %q).\n", ws, settings.Workspace)
					settings.Workspace = ws
				}
			} else {
				// No AML workspace found — provision one. The managed harness API
				// requires Microsoft.MachineLearningServices/workspaces/{name} to exist.
				if progress != nil {
					progress(fmt.Sprintf("Workspace %q not found; provisioning an AML workspace now", settings.Workspace))
				}
				if createErr := ensurePromptWorkspaceExists(ctx, settings, env, progress); createErr != nil {
					fmt.Fprintf(os.Stderr, "Warning: AML workspace provisioning failed: %v\n", createErr)
				}
			}
		} else if workspaceKnown && !projectScopedTarget {
			// No AML workspace found — provision one. The managed harness API
			// Keep the explicit workspace from azure.yaml / env and skip discovery.
			fmt.Fprintf(os.Stderr, "Using configured prompt workspace %q.\n", settings.Workspace)
		}
	}

	// Resolve the prompt agent's dependency graph. This validates the whole
	// graph (model + instructions, and — as later stages land — folders,
	// connections, and skills) and resolves convention-based dependencies,
	// enriching the definition before the create request is built. Env values
	// are best-effort; a nil map simply means nothing to overlay.
	graphEnv, _ := p.azdEnvValues(ctx)
	if err := p.resolvePromptAgentGraph(ctx, &managed, settings, graphEnv, progress); err != nil {
		return nil, err
	}

	request, err := agent_yaml.CreateManagedAgentAPIRequest(managed, nil)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent.yaml is not a valid prompt agent: %s", err),
			"ensure agent.yaml declares a non-empty model and instructions",
		)
	}

	client, err := NewPromptAgentClient(settings)
	if err != nil {
		return nil, err
	}

	if progress != nil {
		progress("Creating prompt agent on the harness")
	}
	headers := map[string]string{
		"x-model-endpoint": settings.EffectiveModelEndpoint(),
	}
	agent, err := client.CreateAgentWithHeaders(ctx, request, settings.EffectiveAPIVersion(), headers)
	if err != nil && isWorkspaceNotFoundError(err) && !projectScopedTarget {
		// Workspace provisioning may not have finished or may have raced; retry once.
		if env2, envErr2 := p.azdEnvValues(ctx); envErr2 == nil {
			if createErr := ensurePromptWorkspaceExists(ctx, settings, env2, progress); createErr == nil {
				fmt.Fprintf(os.Stderr, "Retrying agent creation after workspace provisioning.\n")
				if client2, clientErr := NewPromptAgentClient(settings); clientErr == nil {
					agent, err = client2.CreateAgentWithHeaders(ctx, request, settings.EffectiveAPIVersion(), headers)
				}
			}
		}
	}
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)
	}

	latest := agent.Versions.Latest
	if latest.Status != "active" {
		polled, pollErr := p.waitForPromptAgentActive(ctx, client, request.Name, settings, progress)
		if pollErr != nil {
			return nil, pollErr
		}
		latest = *polled
	} else {
		fmt.Fprintf(os.Stderr, "Prompt agent %q version %s is already active.\n", request.Name, latest.Version)
	}

	if err := p.registerPromptAgentEnvVars(ctx, serviceConfig, request.Name, latest.Version, settings); err != nil {
		return nil, err
	}

	if progress != nil {
		progress("Prompt agent deployed")
	}
	return &azdext.ServiceDeployResult{}, nil
}

// ProjectEndpointAPIVersion is the api-version used by the Foundry project
// data-plane managed agent endpoints
// (https://<account>.services.ai.azure.com/api/projects/<project>/agents?api-version=v1).
const ProjectEndpointAPIVersion = "v1"

// ResolvePromptTargetFromEnv applies azd environment-derived overrides to the
// prompt settings so both deploy and the lifecycle commands (show/invoke/list/
// delete) target the same managed agent route.
//
// It resolves the Foundry project data-plane endpoint
// (https://<account>.services.ai.azure.com/api/projects/<project>), preferring
// the value already on the settings (set via interactive init) and otherwise
// falling back to AZURE_AI_PROJECT_ENDPOINT in the azd environment (covers
// --no-prompt and the provisioned-project path). When a project endpoint is
// available it becomes the authoritative routing target, the api-version is
// normalized to v1, and the model endpoint is derived from the account host.
//
// It returns true when a project-scoped target was resolved.
func ResolvePromptTargetFromEnv(settings *PromptAgentSettings, env map[string]string) (bool, error) {
	if settings == nil || env == nil {
		return false, nil
	}
	settings.OverlayAzdProjectEnv(env)
	mapped, err := overlayPromptSettingsFromProjectResourceID(settings, env)
	if err != nil {
		return false, err
	}

	// Prefer the config-supplied project endpoint (interactive init); otherwise
	// read it from the azd environment (--no-prompt / provisioned project).
	if strings.TrimSpace(settings.ProjectEndpoint) == "" {
		if pe := strings.TrimSpace(env["AZURE_AI_PROJECT_ENDPOINT"]); pe != "" {
			settings.ProjectEndpoint = pe
		}
	}

	if pe := strings.TrimSpace(settings.ProjectEndpoint); pe != "" {
		// The project data-plane contract uses api-version=v1.
		settings.APIVersion = ProjectEndpointAPIVersion
		// x-model-endpoint targets the account host backing the project.
		if u, perr := url.Parse(pe); perr == nil && u.Host != "" {
			if strings.TrimSpace(settings.ModelEndpoint) == "" ||
				strings.EqualFold(strings.TrimSpace(settings.ModelEndpoint), DefaultPromptModelEndpoint) {
				settings.ModelEndpoint = u.Scheme + "://" + u.Host
			}
		}
		return true, nil
	}

	return mapped, nil
}

func overlayPromptSettingsFromProjectResourceID(settings *PromptAgentSettings, env map[string]string) (bool, error) {
	if settings == nil || env == nil {
		return false, nil
	}

	projectResourceID := strings.TrimSpace(env["AZURE_AI_PROJECT_ID"])
	if projectResourceID == "" {
		return false, nil
	}

	parsedResource, err := arm.ParseResourceID(projectResourceID)
	if err != nil {
		return false, exterrors.Validation(
			exterrors.CodeInvalidAiProjectId,
			fmt.Sprintf("failed to parse AZURE_AI_PROJECT_ID: %s", err),
			"verify AZURE_AI_PROJECT_ID points to a Foundry project ARM resource ID",
		)
	}

	if parsedResource.Parent == nil || !strings.Contains(string(parsedResource.ResourceType.Type), "/") {
		return false, exterrors.Validation(
			exterrors.CodeInvalidAiProjectId,
			fmt.Sprintf("AZURE_AI_PROJECT_ID is not a Foundry project resource ID: %q", projectResourceID),
			"set AZURE_AI_PROJECT_ID to a Microsoft.CognitiveServices/accounts/projects resource ID",
		)
	}

	settings.SubscriptionID = parsedResource.SubscriptionID
	settings.ResourceGroup = parsedResource.ResourceGroupName

	if parsedResource.Parent != nil {
		accountName := strings.TrimSpace(parsedResource.Parent.Name)
		if accountName != "" {
			// Managed CreateAgent routes are workspace-scoped. For Foundry projects,
			// the backing AML workspace name follows: <account>@<project>@AML.
			settings.Workspace = fmt.Sprintf("%s@%s@AML", accountName, parsedResource.Name)
			sameAsDefault := strings.TrimSpace(settings.ModelEndpoint) == "" ||
				strings.EqualFold(strings.TrimSpace(settings.ModelEndpoint), DefaultPromptModelEndpoint)
			if sameAsDefault {
				settings.ModelEndpoint = fmt.Sprintf("https://%s.services.ai.azure.com", accountName)
			}
		} else {
			settings.Workspace = parsedResource.Name
		}
	} else {
		settings.Workspace = parsedResource.Name
	}

	return true, nil
}

// waitForPromptAgentActive polls the harness GetAgent endpoint until the
// agent's latest version reaches a terminal status. It returns the active
// version object, or a typed error on failure/timeout.
func (p *AgentServiceTargetProvider) waitForPromptAgentActive(
	ctx context.Context,
	client *agent_api.ManagedAgentClient,
	agentName string,
	settings *PromptAgentSettings,
	progress azdext.ProgressReporter,
) (*agent_api.AgentVersionObject, error) {
	const pollInterval = 5 * time.Second
	const pollTimeout = 5 * time.Minute

	deadline := time.Now().Add(pollTimeout)
	attempt := 0
	if progress != nil {
		progress("Waiting for prompt agent to become active")
	}

	var lastStatus string
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("deployment cancelled: %w", ctx.Err())
		case <-time.After(pollInterval):
		}

		attempt++
		if progress != nil {
			progress(fmt.Sprintf("Polling prompt agent status (attempt %d)", attempt))
		}

		agent, err := client.GetAgent(ctx, agentName, settings.EffectiveAPIVersion())
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: poll failed: %s\n", err)
			continue
		}
		latest := agent.Versions.Latest
		lastStatus = latest.Status

		switch latest.Status {
		case "active":
			fmt.Fprintf(os.Stderr, "Prompt agent version %s is active!\n", latest.Version)
			return &latest, nil
		case "failed":
			errMsg := "prompt agent deployment failed"
			if latest.Error != nil {
				errMsg = fmt.Sprintf(
					"prompt agent deployment failed: [%s] %s", latest.Error.Code, latest.Error.Message,
				)
			}
			return nil, exterrors.Internal(exterrors.CodeAgentCreateFailed, errMsg)
		default:
			fmt.Fprintf(os.Stderr, "  Status: %s...\n", latest.Status)
		}
	}

	if lastStatus == "" {
		lastStatus = "unknown"
	}
	return nil, exterrors.Internal(
		exterrors.CodeAgentCreateFailed,
		fmt.Sprintf("prompt agent deployment timed out (last status: %s); check status with 'azd ai agent show'", lastStatus),
	)
}

// registerPromptAgentEnvVars stores the deployed prompt agent's identity and
// harness invocation endpoint in the azd environment, mirroring the hosted
// AGENT_{KEY}_* convention so downstream commands (show/invoke) resolve.
func (p *AgentServiceTargetProvider) registerPromptAgentEnvVars(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	agentName, version string,
	settings *PromptAgentSettings,
) error {
	if agentName == "" {
		return fmt.Errorf("agent name is empty; cannot register environment variables")
	}

	serviceKey := p.getServiceKey(serviceConfig.Name)
	endpoint := promptAgentResponsesEndpoint(settings)
	envVars := map[string]string{
		fmt.Sprintf("AGENT_%s_NAME", serviceKey):     agentName,
		fmt.Sprintf("AGENT_%s_VERSION", serviceKey):  version,
		fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey): endpoint,
	}

	for key, value := range envVars {
		if _, err := p.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: p.env.Name,
			Key:     key,
			Value:   value,
		}); err != nil {
			return fmt.Errorf("failed to set environment variable %s: %w", key, err)
		}
	}
	return nil
}

// promptAgentResponsesEndpoint builds the Responses URL the harness exposes for
// invoking a prompt agent. When a Foundry project data-plane endpoint is
// configured it is used directly; otherwise it falls back to the legacy
// workspace-rooted route. Best-effort: returns the base URL when neither can be
// built.
func promptAgentResponsesEndpoint(settings *PromptAgentSettings) string {
	if pe := strings.TrimSpace(settings.ProjectEndpoint); pe != "" {
		return strings.TrimRight(pe, "/") + "/openai/v1/responses"
	}
	prefix, err := agent_api.BuildWorkspaceRoutePrefix(
		settings.SubscriptionID, settings.ResourceGroup, settings.Workspace,
	)
	if err != nil {
		return settings.BaseURL
	}
	return strings.TrimRight(settings.BaseURL, "/") + prefix + "/openai/responses?api-version=" +
		settings.EffectiveAPIVersion()
}

// azdEnvValues returns the current azd environment as a key/value map. Used to
// overlay provisioned Foundry project values onto the prompt settings at
// deploy time.
func (p *AgentServiceTargetProvider) azdEnvValues(ctx context.Context) (map[string]string, error) {
	resp, err := p.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: p.env.Name,
	})
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(resp.KeyValues))
	for _, kv := range resp.KeyValues {
		values[kv.Key] = kv.Value
	}
	return values, nil
}

// resolvePromptWorkspaceFromAzure discovers a valid AML workspace name for
// managed prompt routes from the target resource group.
//
// Selection order:
//  1. Keep the configured workspace when it already exists.
//  2. Prefer env-derived candidates that exist (project/account names).
//  3. Use the only workspace in the RG when exactly one exists.
func (p *AgentServiceTargetProvider) resolvePromptWorkspaceFromAzure(
	ctx context.Context,
	settings *PromptAgentSettings,
	env map[string]string,
) (string, bool) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "Warning: workspace discovery panicked: %v\n", r)
		}
	}()

	if settings == nil {
		return "", false
	}

	// Prompt agents skip the hosted credential-init path so p.credential is nil.
	// Fall back to the prompt harness credential so workspace discovery works.
	var cred azcore.TokenCredential = p.credential
	if cred == nil {
		cred = promptCredential()
	}
	if cred == nil {
		return "", false
	}

	resourcesClient, err := armresources.NewClient(settings.SubscriptionID, cred, azure.NewArmClientOptions())
	if err != nil {
		return "", false
	}

	pager := resourcesClient.NewListByResourceGroupPager(settings.ResourceGroup, &armresources.ClientListByResourceGroupOptions{
		Filter: new("resourceType eq 'Microsoft.MachineLearningServices/workspaces'"),
	})

	workspaceNames := []string{}
	for pager.More() {
		page, pageErr := pager.NextPage(ctx)
		if pageErr != nil {
			return "", false
		}
		for _, resource := range page.Value {
			if resource == nil || resource.Name == nil {
				continue
			}
			name := strings.TrimSpace(*resource.Name)
			if name == "" {
				continue
			}
			workspaceNames = append(workspaceNames, name)
		}
	}

	if len(workspaceNames) == 0 {
		return "", false
	}

	containsFold := func(target string) bool {
		return slices.ContainsFunc(workspaceNames, func(n string) bool { return strings.EqualFold(n, strings.TrimSpace(target)) })
	}

	if containsFold(settings.Workspace) {
		return settings.Workspace, true
	}

	candidates := []string{
		strings.TrimSpace(env["AZURE_AI_PROJECT_NAME"]),
		strings.TrimSpace(env["AZURE_AI_ACCOUNT_NAME"]),
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if containsFold(candidate) {
			return candidate, true
		}
	}

	if len(workspaceNames) == 1 {
		return workspaceNames[0], true
	}

	return "", false
}

func isWorkspaceNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		if strings.EqualFold(strings.TrimSpace(respErr.ErrorCode), "WorkspaceNotFound") {
			return true
		}
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "workspacenotfound") ||
		strings.Contains(msg, "workspace not found")
}
