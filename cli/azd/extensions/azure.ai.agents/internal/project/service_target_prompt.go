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
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/braydonk/yaml"
	"google.golang.org/protobuf/types/known/structpb"
)

// ServiceIsPromptAgent reports whether the service config describes a prompt
// (kind=prompt) agent.
//
// The definition is carried inline on the service entry, so `kind` names the
// flavor directly. A `$ref:` include is merged onto the entry by
// resolveServiceConfig before this runs, so a definition that lives in its own
// file is classified the same way.
func ServiceIsPromptAgent(serviceConfig *azdext.ServiceConfig) bool {
	if serviceConfig == nil {
		return false
	}
	for _, props := range []*structpb.Struct{
		serviceConfig.GetAdditionalProperties(),
		serviceConfig.GetConfig(),
	} {
		if kind := structKind(props); kind != "" {
			return strings.EqualFold(kind, string(agent_yaml.AgentKindPrompt))
		}
	}
	// Projects scaffolded before the definition moved inline declare no kind on
	// the service entry and are identified by their promptAgent config block.
	if serviceConfig.Config == nil {
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
	return ServiceIsPromptAgent(p.serviceConfig)
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

// ResolvePromptAgentSettings produces the settings that address the harness.
//
// The Foundry target is read from the azd environment, which is the only thing
// that knows it: `azd provision` writes the subscription, resource group,
// workspace, and project endpoint there, and they change per environment. That
// is why azure.yaml carries no promptAgent block — the values would be either a
// copy of the environment or a set of ${VAR} references pointing back at it.
//
// A hand-authored promptAgent block still wins, layered on top, so a developer
// can pin a field or set one of the advanced knobs (apiVersion, modelEndpoint)
// that the environment does not carry. Its ${VAR} references are expanded
// against the same environment first, keeping older projects working unchanged.
// Process-environment AZD_MANAGED_AGENT_* overrides are applied last.
func ResolvePromptAgentSettings(
	configured *PromptAgentSettings,
	env map[string]string,
) (*PromptAgentSettings, error) {
	expanded, err := expandPromptAgentSettings(configured, env)
	if err != nil {
		return nil, err
	}
	settings := DefaultPromptAgentSettings()
	settings.overlay(promptAgentSettingsFromEnv(env))
	settings.overlay(expanded)
	settings.ApplyEnvOverrides()
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return &settings, nil
}

// promptAgentSettingsFromEnv reads the Foundry target out of the azd
// environment using the standard variable names `azd provision` records.
//
// Only the fields the environment actually knows are returned; an unset
// variable is left empty so overlay() keeps the default in place, which is what
// lets a project be cloned and inspected before it has been provisioned.
func promptAgentSettingsFromEnv(env map[string]string) *PromptAgentSettings {
	if env == nil {
		return nil
	}
	return &PromptAgentSettings{
		SubscriptionID:  strings.TrimSpace(env["AZURE_SUBSCRIPTION_ID"]),
		ResourceGroup:   strings.TrimSpace(env["AZURE_RESOURCE_GROUP"]),
		ProjectEndpoint: strings.TrimSpace(env["AZURE_AI_PROJECT_ENDPOINT"]),
	}
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
		"subscriptionId":  &expanded.SubscriptionID,
		"resourceGroup":   &expanded.ResourceGroup,
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

// loadPromptAgentDefinition returns the service's prompt-agent definition.
//
// The definition is normally inline on the azure.yaml service entry, which is
// what `azd ai agent init` scaffolds. agentDefinitionPath is set only when the
// definition lives in its own file — a `$ref:` include, the AGENT_DEFINITION_PATH
// override, or the legacy agent.yaml convention — and that file is then the
// authority, because it is also what anchors the skills/ and vector-assets/
// convention folders.
func (p *AgentServiceTargetProvider) loadPromptAgentDefinition() (agent_yaml.PromptAgent, error) {
	if p.agentDefinitionPath == "" {
		promptDef, found, err := PromptAgentFromResolvedService(p.serviceConfig, p.projectPath)
		if err != nil {
			return agent_yaml.PromptAgent{}, err
		}
		if !found {
			return agent_yaml.PromptAgent{}, exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("service %q carries no prompt agent definition", p.serviceConfig.GetName()),
				"add the agent definition to the service entry in azure.yaml, "+
					"or re-run `azd ai agent init`",
			)
		}
		return promptDef, nil
	}

	data, err := os.ReadFile(p.agentDefinitionPath)
	if err != nil {
		return agent_yaml.PromptAgent{}, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to read agent manifest file: %s", err),
			"verify the agent definition file exists and is readable",
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
	// A failed env read is fatal because the provisioned Foundry project endpoint
	// is resolved from this environment.
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

	if _, mapErr := ResolvePromptTargetFromEnv(settings, env); mapErr != nil {
		return nil, mapErr
	}
	if strings.TrimSpace(settings.ProjectEndpoint) == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeInvalidServiceConfig,
			"a Foundry project endpoint is required to deploy a prompt agent",
			"run `azd provision` to create the Foundry project",
		)
	}
	if err := p.ensurePromptCredential(ctx, settings); err != nil {
		return nil, err
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

	client, err := NewPromptAgentClient(settings, p.credential)
	if err != nil {
		return nil, err
	}

	if progress != nil {
		progress("Creating prompt agent on the harness")
	}
	headers := promptAgentRequestHeaders(&managed, settings)
	agent, err := p.createOrUpdatePromptAgent(ctx, client, request, settings, headers)
	if err != nil {
		return nil, promptCreateError(err, &managed)
	}

	latest := agent.Versions.Latest
	if latest.Status != "active" {
		polled, pollErr := p.waitForPromptAgentActive(ctx, client, request.Name, settings, headers, progress)
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

func (p *AgentServiceTargetProvider) ensurePromptCredential(
	ctx context.Context,
	settings *PromptAgentSettings,
) error {
	if isTruthyEnvValue(os.Getenv(PromptNoAuthEnvVar)) {
		p.credential = nil
		return nil
	}
	if p.credential != nil {
		return nil
	}
	if strings.TrimSpace(settings.SubscriptionID) == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingAzureSubscription,
			"AZURE_SUBSCRIPTION_ID is required for prompt agent authentication",
			"run `azd provision` to resolve the Foundry project subscription",
		)
	}
	tenant, err := p.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: settings.SubscriptionID,
	})
	if err != nil {
		return exterrors.Auth(
			exterrors.CodeTenantLookupFailed,
			fmt.Sprintf("failed to get tenant for subscription %s: %s", settings.SubscriptionID, err),
			"verify your Azure login with `azd auth login`",
		)
	}
	p.tenantId = tenant.TenantId
	p.credential = promptCredential(p.tenantId)
	if p.credential == nil {
		return exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			"failed to create a credential for prompt agent deployment",
			"run `azd auth login` to authenticate",
		)
	}
	return nil
}

func promptAgentRequestHeaders(
	managed *agent_yaml.PromptAgent,
	settings *PromptAgentSettings,
) map[string]string {
	headers := map[string]string{"x-model-endpoint": settings.EffectiveModelEndpoint()}
	if managed != nil && managed.HarnessType() == agent_api.ManagedAgentHarnessGitHubCopilot {
		headers["Foundry-Features"] = "GitHubCopilot=V1Preview"
	}
	return headers
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
			sameAsDefault := strings.TrimSpace(settings.ModelEndpoint) == "" ||
				strings.EqualFold(strings.TrimSpace(settings.ModelEndpoint), DefaultPromptModelEndpoint)
			if sameAsDefault {
				settings.ModelEndpoint = fmt.Sprintf("https://%s.services.ai.azure.com", accountName)
			}
		}
	}

	return true, nil
}

// waitForPromptAgentActive polls the harness GetAgent endpoint until the
// agent's latest version reaches a terminal status. It returns the active
// version object, or a typed error on failure/timeout.
func (p *AgentServiceTargetProvider) waitForPromptAgentActive(
	ctx context.Context,
	client *agent_api.AgentClient,
	agentName string,
	settings *PromptAgentSettings,
	headers map[string]string,
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

		agent, err := client.GetAgentWithHeaders(ctx, agentName, settings.EffectiveAPIVersion(), headers)
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
	if version == "" {
		return fmt.Errorf("agent version is empty; cannot register environment variables")
	}

	serviceKey := p.getServiceKey(serviceConfig.Name)
	endpoint := promptAgentResponsesEndpoint(settings)
	versionKey := fmt.Sprintf("AGENT_%s_VERSION", serviceKey)
	envVars := []azdext.SetEnvRequest{
		{EnvName: p.env.Name, Key: versionKey, Value: ""},
		{EnvName: p.env.Name, Key: fmt.Sprintf("AGENT_%s_NAME", serviceKey), Value: agentName},
		{EnvName: p.env.Name, Key: fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey), Value: endpoint},
	}
	if storeName, ok := bindings[memoryStoreBindingKey].(string); ok && strings.TrimSpace(storeName) != "" {
		envVars = append(envVars, azdext.SetEnvRequest{
			EnvName: p.env.Name,
			Key:     fmt.Sprintf("AGENT_%s_MEMORY_STORE_NAME", serviceKey),
			Value:   storeName,
		})
	}
	envVars = append(envVars,
		azdext.SetEnvRequest{
			EnvName: p.env.Name,
			Key:     envkey.AgentProjectEndpoint(serviceConfig.Name),
			Value:   strings.TrimRight(settings.ProjectEndpoint, "/"),
		},
		azdext.SetEnvRequest{EnvName: p.env.Name, Key: versionKey, Value: version},
	)

	for i := range envVars {
		if _, err := p.azdClient.Environment().SetValue(ctx, &envVars[i]); err != nil {
			return fmt.Errorf("failed to set environment variable %s: %w", envVars[i].Key, err)
		}
	}
	return nil
}

// promptAgentResponsesEndpoint builds the project-scoped Responses URL.
func promptAgentResponsesEndpoint(settings *PromptAgentSettings) string {
	if pe := strings.TrimSpace(settings.ProjectEndpoint); pe != "" {
		return strings.TrimRight(pe, "/") + "/openai/v1/responses"
	}
	return ""
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
	client *agent_api.AgentClient,
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
