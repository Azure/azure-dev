// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
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
//
// `azd ai agent init` writes every promptAgent field as a ${VAR} reference so
// azure.yaml stays portable, so the block is expanded against env (the azd
// environment, falling back to the process environment) before it is layered
// over the defaults. A reference whose variable is unset expands to "" and
// therefore leaves the corresponding default in place, which is what lets a
// project be cloned into an environment that has not been provisioned yet.
// Projects that carry literal values keep working -- expansion leaves a string
// with no ${...} in it untouched.
func (p *AgentServiceTargetProvider) promptAgentSettings(env map[string]string) (*PromptAgentSettings, error) {
	var cfg ServiceTargetAgentConfig
	if err := UnmarshalStruct(p.serviceConfig.Config, &cfg); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("failed to parse service config: %s", err),
			"check the service configuration in azure.yaml",
		)
	}
	return ResolvePromptAgentSettings(cfg.PromptAgent, env)
}

// ResolvePromptAgentSettings turns a raw promptAgent block from azure.yaml into
// settings that can address the harness: ${VAR} references are expanded against
// env, the result is layered over the defaults, process-environment overrides
// are applied, and the whole is validated.
//
// Every caller that talks to the harness must go through this. The block is
// written with ${...} references so azure.yaml stays portable, which means the
// raw config carries literal "${AZURE_AI_PROJECT_ENDPOINT}" strings — usable as
// a URL only after expansion. Skipping this step fails at the point of use with
// a message about a malformed URL rather than a missing variable.
func ResolvePromptAgentSettings(
	configured *PromptAgentSettings,
	env map[string]string,
) (*PromptAgentSettings, error) {
	expanded, err := expandPromptAgentSettings(configured, env)
	if err != nil {
		return nil, err
	}
	settings := DefaultPromptAgentSettings()
	settings.overlay(expanded)
	settings.ApplyEnvOverrides()
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return &settings, nil
}

// expandPromptAgentSettings returns a copy of src with ${VAR} references in
// every field resolved against env, falling back to the process environment for
// variables the azd environment does not define. A nil src returns nil.
func expandPromptAgentSettings(
	src *PromptAgentSettings,
	env map[string]string,
) (*PromptAgentSettings, error) {
	if src == nil {
		return nil, nil
	}
	lookup := func(name string) string {
		if v, ok := env[name]; ok {
			return v
		}
		v, _ := os.LookupEnv(name)
		return v
	}
	expanded := *src
	for name, field := range map[string]*string{
		"baseUrl":         &expanded.BaseURL,
		"subscriptionId":  &expanded.SubscriptionID,
		"resourceGroup":   &expanded.ResourceGroup,
		"workspace":       &expanded.Workspace,
		"projectEndpoint": &expanded.ProjectEndpoint,
		"apiVersion":      &expanded.APIVersion,
		"modelEndpoint":   &expanded.ModelEndpoint,
	} {
		value, err := ExpandEnv(strings.TrimSpace(*field), lookup)
		if err != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("failed to expand promptAgent.%s: %s", name, err),
				"check the ${VAR} references in the promptAgent block in azure.yaml",
			)
		}
		*field = strings.TrimSpace(value)
	}
	return &expanded, nil
}

// expandPromptAgentPolicies resolves ${VAR} references in the agent's
// policies[].raiPolicyName against the azd environment.
//
// A Responsible AI policy is addressed by its full ARM resource ID, which
// embeds a subscription, resource group and account. `azd ai agent init` writes
// ${RAI_POLICY_ID} rather than that ID so the scaffold can be copied to another
// subscription unchanged, and the generated `rai` infrastructure layer exports
// the concrete value at provision time.
//
// An unresolved reference is fatal rather than silently empty: dropping the
// policy would publish an agent without the guardrails its manifest declares.
func expandPromptAgentPolicies(managed *agent_yaml.PromptAgent, env map[string]string) error {
	lookup := func(name string) string {
		if value, ok := env[name]; ok {
			return value
		}
		value, _ := os.LookupEnv(name)
		return value
	}

	for i := range managed.Policies {
		policy := &managed.Policies[i]
		if policy.Type != agent_yaml.PolicyTypeRai {
			continue
		}
		raw := strings.TrimSpace(policy.RaiPolicyName)
		if raw == "" {
			continue
		}
		expanded, err := ExpandEnv(raw, lookup)
		if err != nil {
			return exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("failed to expand policies[%d].raiPolicyName: %s", i, err),
				"check the ${VAR} references in the policies block in agent.yaml",
			)
		}
		expanded = strings.TrimSpace(expanded)
		if expanded == "" {
			return exterrors.Dependency(
				exterrors.CodeRaiPolicyNotFound,
				fmt.Sprintf("policies[%d].raiPolicyName is %q, but that value is not set in the azd environment",
					i, raw),
				"run `azd provision` to create the policy declared by your infrastructure, or set the "+
					"variable to the policy's full ARM resource ID with `azd env set "+
					raiPolicyEnvVarName+" <resource id>`",
			)
		}
		policy.RaiPolicyName = expanded
	}
	return nil
}

// raiPolicyEnvVarName is the variable `azd ai agent init` records the resolved
// Responsible AI policy ID under. Named here so the deploy-time suggestion
// above points at the same variable the scaffold writes.
const raiPolicyEnvVarName = "RAI_POLICY_ID"

// promptCreateError converts a failed agent create into an actionable error.
//
// Guardrails get a suggestion of their own. The managed harness has been
// observed to reject rai_config outright while the same policy is accepted by a
// plain prompt agent, and the service returns a generic bad request that names
// neither rai_config nor the policy. Without this, the only visible difference
// between "your policy is wrong" and "this harness does not take policies yet"
// is a message that mentions neither.
func promptCreateError(err error, managed *agent_yaml.PromptAgent) error {
	converted := exterrors.ServiceFromAzure(err, exterrors.OpCreateAgent)

	local, ok := errors.AsType[*azdext.LocalError](converted)
	if !ok || !declaresRaiPolicy(managed) {
		return converted
	}

	suggestion := "This agent declares a Responsible AI policy. Verify the policy ID is correct and " +
		"reachable from this account, then re-run. If the policy is valid, the harness may not accept " +
		"policies yet — remove the policies block from agent.yaml to confirm, and deploy without " +
		"'harness:' to apply the policy as a plain prompt agent."
	if managed.HarnessType() == "" {
		suggestion = "This agent declares a Responsible AI policy. Verify the policy ID is correct and " +
			"reachable from this account, then re-run."
	}
	if local.Suggestion != "" {
		suggestion = local.Suggestion + " " + suggestion
	}
	local.Suggestion = suggestion
	return local
}

// declaresRaiPolicy reports whether the agent binds a Responsible AI policy.
func declaresRaiPolicy(managed *agent_yaml.PromptAgent) bool {
	if managed == nil {
		return false
	}
	for _, policy := range managed.Policies {
		if policy.Type == agent_yaml.PolicyTypeRai && strings.TrimSpace(policy.RaiPolicyName) != "" {
			return true
		}
	}
	return false
}

// resolvedPromptAgentSettings returns the prompt-agent settings with the same
// azd environment-derived target resolution deployPromptAgent applies. Read-only
// callers (Endpoints, GetTargetResource) must use this rather than
// promptAgentSettings: a non-guided init stores a placeholder
// subscription/resource-group/workspace tuple in azure.yaml and only the azd
// environment knows the real Foundry target, so the raw settings would report
// `test-rg`/`test-ws` even after a successful deploy.
func (p *AgentServiceTargetProvider) resolvedPromptAgentSettings(
	ctx context.Context,
) (*PromptAgentSettings, error) {
	env, err := p.azdEnvValues(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the azd environment: %w", err)
	}
	settings, err := p.promptAgentSettings(env)
	if err != nil {
		return nil, err
	}
	if _, err := ResolvePromptTargetFromEnv(settings, env); err != nil {
		return nil, err
	}
	return settings, nil
}

// loadPromptAgentDefinition reads the agent.yaml as a bare PromptAgent.
func (p *AgentServiceTargetProvider) loadPromptAgentDefinition() (agent_yaml.PromptAgent, error) {
	data, err := os.ReadFile(p.agentDefinitionPath)
	if err != nil {
		return agent_yaml.PromptAgent{}, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to read agent manifest file: %s", err),
			"verify the agent.yaml file exists and is readable",
		)
	}
	if err := validatePromptAgentRawFields(data); err != nil {
		return agent_yaml.PromptAgent{}, err
	}
	var promptDef agent_yaml.PromptAgent
	if err := yaml.Unmarshal(data, &promptDef); err != nil {
		return agent_yaml.PromptAgent{}, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent.yaml is not a valid prompt agent: %s", err),
			"fix the agent.yaml to match the prompt agent schema",
		)
	}
	if !strings.EqualFold(string(promptDef.Kind), string(agent_yaml.AgentKindPrompt)) {
		return agent_yaml.PromptAgent{}, exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			fmt.Sprintf("agent.yaml declares kind %q, expected prompt", promptDef.Kind),
			"use kind: prompt for prompt agents",
		)
	}

	return promptDef, nil
}

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
// generic map is used to detect container-only keys that the typed PromptAgent
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
				fmt.Sprintf("field %q is not valid for a prompt (kind: prompt) agent", field),
				"remove container-only fields (image, protocols, code_configuration, ...) "+
					"or use kind: hosted for container agents",
			)
		}
	}
	// `harness` used to be the harness name on its own. It is an object now, so
	// the typed decode below would reject a string with a decoder-level type
	// error that names neither the old shape nor the new one.
	if harness, ok := probe["harness"].(string); ok {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent.yaml sets harness to the string %q, but harness is now a block", harness),
			fmt.Sprintf("replace it with:\n  harness:\n    type: %s", promptHarnessTypeFor(harness)),
		)
	}
	return nil
}

// promptHarnessTypeFor maps an old bare harness name to the type to write in the
// new block, so the suggestion above is copy-pasteable even when the name itself
// was also renamed.
func promptHarnessTypeFor(harness string) string {
	harness = strings.TrimSpace(harness)
	if replacement, removed := agent_api.RemovedManagedAgentHarnesses[harness]; removed {
		return replacement
	}
	return harness
}

// deployPromptAgent creates (or updates) the prompt agent on the managed
// harness and registers the resulting agent identity in the azd environment.
// It is the prompt-agent analogue of deployHostedAgent, dispatched from
// Deploy() when the service is a prompt agent.
func (p *AgentServiceTargetProvider) deployPromptAgent(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	progress azdext.ProgressReporter,
) (result *azdext.ServiceDeployResult, err error) {
	// Convert an unexpected panic into a deploy error. Deploy handlers are
	// expected to return errors to azd; re-panicking would tear down the whole
	// extension process and azd would surface a transport failure instead of an
	// actionable deploy error.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in deployPromptAgent: %v\n%s", r, debug.Stack())
			result = nil
			err = fmt.Errorf("unexpected error deploying prompt agent: %v", r)
		}
	}()

	managed, err := p.loadPromptAgentDefinition()
	if err != nil {
		return nil, err
	}

	// The azd environment is read before the settings because azure.yaml states
	// the promptAgent block as ${VAR} references that resolve against it.
	//
	// A failed env read is fatal: skipping it would also skip
	// ResolvePromptTargetFromEnv and its AZURE_AI_PROJECT_ID validation, leaving
	// the placeholder tuple in place so the create call goes out against a
	// workspace that never existed and the user sees WorkspaceNotFound instead of
	// the real cause.
	env, err := p.azdEnvValues(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the azd environment: %w", err)
	}

	settings, err := p.promptAgentSettings(env)
	if err != nil {
		return nil, err
	}

	// Guardrails are stated as ${RAI_POLICY_ID} so the project stays portable;
	// resolve them against the azd environment before anything validates the
	// shape of the value.
	if err := expandPromptAgentPolicies(&managed, env); err != nil {
		return nil, err
	}

	// Overlay the provisioned Foundry project values from the azd environment
	// onto any settings still at their default placeholder. This makes the
	// "create a new Foundry project" init path work: `azd up` provisions the
	// project, and the deploy targets it. The overlay is a no-op unless the azd
	// environment actually holds a resolved project (AZURE_AI_PROJECT_NAME),
	// so the local-dev fake tuple is preserved when no project was provisioned.

	projectScopedTarget := false
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
		// Keep the explicit workspace from azure.yaml / env and skip discovery.
		fmt.Fprintf(os.Stderr, "Using configured prompt workspace %q.\n", settings.Workspace)
	}

	// Resolve the prompt agent's dependency graph. This validates the whole
	// graph (model + instructions, and — as later stages land — folders,
	// connections, and skills) and resolves convention-based dependencies,
	// enriching the definition before the create request is built.
	bindings, err := p.resolvePromptAgentGraph(ctx, &managed, settings, env, progress)
	if err != nil {
		return nil, err
	}

	request, err := agent_yaml.CreatePromptAgentAPIRequest(managed, nil)
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
	agent, err := p.createOrUpdatePromptAgent(ctx, client, request, settings, headers)
	if err != nil && isWorkspaceNotFoundError(err) && !projectScopedTarget {
		// Workspace provisioning may not have finished or may have raced; retry once.
		if env2, envErr2 := p.azdEnvValues(ctx); envErr2 == nil {
			if createErr := ensurePromptWorkspaceExists(ctx, settings, env2, progress); createErr == nil {
				fmt.Fprintf(os.Stderr, "Retrying agent creation after workspace provisioning.\n")
				if client2, clientErr := NewPromptAgentClient(settings); clientErr == nil {
					agent, err = p.createOrUpdatePromptAgent(ctx, client2, request, settings, headers)
				}
			}
		}
	}
	if err != nil {
		return nil, promptCreateError(err, &managed)
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

	if err := p.registerPromptAgentEnvVars(ctx, serviceConfig, request.Name, latest.Version, settings, bindings); err != nil {
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

// promptProjectEndpointEnvKeys lists the azd environment keys that may carry
// the Foundry project data-plane endpoint, in precedence order.
//
// FOUNDRY_PROJECT_ENDPOINT is what the microsoft.foundry provisioning provider
// and `azd ai agent init` write today; AZURE_AI_PROJECT_ENDPOINT is the older
// name still emitted by hand-authored infra/ templates. Both must be honored:
// reading only the latter leaves ProjectEndpoint empty after a greenfield
// provision, and the deploy then falls back to the legacy workspace-rooted
// harness route, which 404s because a Foundry project is not an AML workspace.
var promptProjectEndpointEnvKeys = []string{
	"AZURE_AI_PROJECT_ENDPOINT",
	"FOUNDRY_PROJECT_ENDPOINT",
}

// ResolvePromptTargetFromEnv applies azd environment-derived overrides to the
// prompt settings so both deploy and the lifecycle commands (show/invoke/list/
// delete) target the same managed agent route.
//
// It resolves the Foundry project data-plane endpoint
// (https://<account>.services.ai.azure.com/api/projects/<project>), preferring
// the value already on the settings (set via interactive init) and otherwise
// falling back to the azd environment (covers --no-prompt and the provisioned-
// project path). When a project endpoint is available it becomes the
// authoritative routing target, the api-version is normalized to v1, and the
// model endpoint is derived from the account host.
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
		for _, key := range promptProjectEndpointEnvKeys {
			if pe := strings.TrimSpace(env[key]); pe != "" {
				settings.ProjectEndpoint = pe
				break
			}
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
// bindings carries ids resolved by the deploy graph that must survive into the
// next deploy (currently the vector store id).
func (p *AgentServiceTargetProvider) registerPromptAgentEnvVars(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	agentName, version string,
	settings *PromptAgentSettings,
	bindings map[string]any,
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
	if storeID, ok := bindings[vectorStoreBindingKey].(string); ok && strings.TrimSpace(storeID) != "" {
		envVars[fmt.Sprintf("AGENT_%s_VECTOR_STORE_ID", serviceKey)] = storeID
	}
	if storeName, ok := bindings[memoryStoreBindingKey].(string); ok && strings.TrimSpace(storeName) != "" {
		envVars[fmt.Sprintf("AGENT_%s_MEMORY_STORE_NAME", serviceKey)] = storeName
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
//
// It calls ensureEnv first: the prompt-agent branches of Endpoints and
// GetTargetResource return before ensureDeployContext runs, so p.env would
// otherwise still be nil and dereferencing it would panic the handler.
func (p *AgentServiceTargetProvider) azdEnvValues(ctx context.Context) (map[string]string, error) {
	if err := p.ensureEnv(ctx); err != nil {
		return nil, err
	}
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
	// No panic recovery here on purpose. A panic in discovery used to be
	// converted into ("", false), which the caller cannot tell apart from "no
	// workspace exists" -- so a crash silently became a request to provision a
	// new workspace. It masked a nil credential reaching armresources.NewClient
	// for the entire life of this function. deployPromptAgent already recovers
	// at the RPC boundary and reports the panic as a deploy error.

	if settings == nil {
		return "", false
	}

	// Prompt agents skip the hosted credential-init path so p.credential is nil.
	// Fall back to the prompt harness credential so workspace discovery works.
	//
	// The nil check is on the concrete pointer, not on the interface. Assigning a
	// nil *AzureDeveloperCLICredential into azcore.TokenCredential produces an
	// interface that is non-nil but carries a nil pointer, so comparing the
	// interface to nil never succeeds and the fallback below never runs.
	var cred azcore.TokenCredential
	if p.credential != nil {
		cred = p.credential
	}
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

// isAgentConflictError reports whether err is a 409 Conflict from the managed
// agent create endpoint, which the harness returns when an agent with the same
// name already exists.
func isAgentConflictError(err error) bool {
	if err == nil {
		return false
	}
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		if respErr.StatusCode == http.StatusConflict {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(respErr.ErrorCode), "conflict") {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists")
}

// createOrUpdatePromptAgent publishes the agent definition, creating the agent
// on first deploy and publishing a new version on subsequent deploys.
//
// Managed (prompt) agents are versioned: the create endpoint (POST /agents)
// only succeeds for a brand-new agent and returns 409 Conflict once the agent
// exists. Re-deploys therefore fall back to the update endpoint
// (POST /agents/{name}), which appends a new version. This makes `azd deploy`
// idempotent: the first run creates the agent, and every later run bumps its
// version.
func (p *AgentServiceTargetProvider) createOrUpdatePromptAgent(
	ctx context.Context,
	client *agent_api.ManagedAgentClient,
	request *agent_api.CreateAgentRequest,
	settings *PromptAgentSettings,
	headers map[string]string,
) (*agent_api.AgentObject, error) {
	apiVersion := settings.EffectiveAPIVersion()

	agent, err := client.CreateAgentWithHeaders(ctx, request, apiVersion, headers)
	if err == nil {
		return agent, nil
	}
	if !isAgentConflictError(err) {
		return nil, err
	}

	// The agent already exists — publish a new version instead.
	fmt.Fprintf(os.Stderr, "Agent %q already exists; publishing a new version.\n", request.Name)
	updateReq := &agent_api.UpdateAgentRequest{
		CreateAgentVersionRequest: request.CreateAgentVersionRequest,
	}
	return client.UpdateAgentWithHeaders(ctx, request.Name, updateReq, apiVersion, headers)
}
