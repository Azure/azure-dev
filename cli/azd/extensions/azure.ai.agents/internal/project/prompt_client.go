// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// Environment-variable overrides for the prompt-agent (managed) harness
// client. When set, these take precedence over the corresponding fields in
// the azure.yaml service config so developers can temporarily retarget the
// harness without editing the project file.
const (
	PromptSubscriptionEnvVar    = "AZD_MANAGED_AGENT_SUBSCRIPTION_ID"
	PromptResourceGroupEnvVar   = "AZD_MANAGED_AGENT_RESOURCE_GROUP"
	PromptProjectEndpointEnvVar = "AZD_MANAGED_AGENT_PROJECT_ENDPOINT"
	PromptAPIVersionEnvVar      = "AZD_MANAGED_AGENT_API_VERSION"
	PromptModelEndpointEnvVar   = "AZD_MANAGED_AGENT_MODEL_ENDPOINT"
	// PromptNoAuthEnvVar, when truthy, skips attaching a bearer token to
	// harness requests. Use it only against a harness that runs with auth
	// fully bypassed; by default a cognitive-services token is attached.
	PromptNoAuthEnvVar = "AZD_MANAGED_AGENT_NO_AUTH"
)

const (
	DefaultPromptSubscriptionID = "00000000-0000-0000-0000-000000000001"
	DefaultPromptResourceGroup  = "test-rg"
)

// DefaultPromptAPIVersion is the api-version query parameter sent on every
// prompt-agent request.
const DefaultPromptAPIVersion = "2025-05-15-preview"

// DefaultPromptModelEndpoint is the model gateway the harness calls to reach
// the LLM. It is sent on invoke (Responses) requests via the x-model-endpoint
// header.
//
// This is a private development resource and is only a last-resort fallback:
// EffectiveModelEndpoint prefers the user's own resolved Foundry project
// endpoint, which is the correct gateway for anyone outside the dev
// subscription. Do not rely on this constant being reachable.
const DefaultPromptModelEndpoint = "https://va-dev-fdp-resource.services.ai.azure.com"

// PromptAgentSettings captures the harness connection details for a prompt
// (kind=managed) agent. It is stored in the azure.yaml service config block
// (ServiceTargetAgentConfig.PromptAgent) and resolved at deploy/invoke time.
//
// `azd ai agent init` writes every field as a ${VAR} reference rather than a
// literal, so azure.yaml carries no subscription, resource group, or workspace
// of its own and can be copied between Foundry projects unchanged. Deploy
// expands the references against the azd environment and falls back to the
// built-in defaults for any variable that is unset.
//
// Every field is omitempty so a field with nothing to say is left out entirely.
// Persisting empty strings would put a shape into azure.yaml that carries no
// information but looks like configuration a developer must fill in, and
// overlay() treats an empty value as "not configured" in either case.
type PromptAgentSettings struct {
	// SubscriptionID is the Azure subscription containing the workspace.
	SubscriptionID string `json:"subscriptionId,omitempty"`

	// ResourceGroup is the Azure resource group containing the workspace.
	ResourceGroup string `json:"resourceGroup,omitempty"`

	// ProjectEndpoint is the Foundry project data-plane root
	// (https://<account>.services.ai.azure.com/api/projects/<project>). When set,
	// it is the authoritative routing target for ALL managed agent operations
	// (CRUD and Responses) and supersedes the legacy workspace tuple. It is
	// populated from the interactive init selection or, in --no-prompt flows,
	// from AZURE_AI_PROJECT_ENDPOINT in the azd environment.
	ProjectEndpoint string `json:"projectEndpoint,omitempty"`

	// APIVersion is the api-version query parameter sent on every request.
	// Defaults to DefaultPromptAPIVersion when empty.
	APIVersion string `json:"apiVersion,omitempty"`

	// ModelEndpoint is the model gateway the harness calls to reach the LLM.
	// Sent on invoke requests via the x-model-endpoint header. Defaults to
	// DefaultPromptModelEndpoint when empty.
	ModelEndpoint string `json:"modelEndpoint,omitempty"`
}

// DefaultPromptAgentSettings returns settings populated with public managed
// prompt-agent defaults plus placeholder workspace tuple values used by
// non-guided init.
func DefaultPromptAgentSettings() PromptAgentSettings {
	return PromptAgentSettings{
		SubscriptionID: DefaultPromptSubscriptionID,
		ResourceGroup:  DefaultPromptResourceGroup,
		APIVersion:     DefaultPromptAPIVersion,
		ModelEndpoint:  DefaultPromptModelEndpoint,
	}
}

// overlay copies every non-empty field of src onto s, leaving s's existing
// value in place where src is empty. It lets a partially populated (or empty)
// promptAgent block in azure.yaml layer over DefaultPromptAgentSettings without
// blanking the defaults.
func (s *PromptAgentSettings) overlay(src *PromptAgentSettings) {
	if s == nil || src == nil {
		return
	}
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&s.SubscriptionID, src.SubscriptionID},
		{&s.ResourceGroup, src.ResourceGroup},
		{&s.ProjectEndpoint, src.ProjectEndpoint},
		{&s.APIVersion, src.APIVersion},
		{&s.ModelEndpoint, src.ModelEndpoint},
	} {
		if v := strings.TrimSpace(f.src); v != "" {
			*f.dst = v
		}
	}
}

// Validate reports a typed error when any required field is empty.
func (s *PromptAgentSettings) Validate() error {
	if s == nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"prompt agent settings are not configured",
			"re-run `azd ai agent init` to scaffold the prompt agent service",
		)
	}
	var missing []string
	if strings.TrimSpace(s.SubscriptionID) == "" {
		missing = append(missing, "subscriptionId")
	}
	if strings.TrimSpace(s.ResourceGroup) == "" {
		missing = append(missing, "resourceGroup")
	}
	if strings.TrimSpace(s.ProjectEndpoint) == "" {
		missing = append(missing, "projectEndpoint")
	}
	if len(missing) > 0 {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("prompt agent config is missing required fields: %s", strings.Join(missing, ", ")),
			"edit the promptAgent block in azure.yaml, or re-run `azd ai agent init`",
		)
	}
	return nil
}

// EffectiveAPIVersion returns the configured api-version, falling back to the
// package-level default when empty.
func (s *PromptAgentSettings) EffectiveAPIVersion() string {
	if s == nil || strings.TrimSpace(s.APIVersion) == "" {
		return DefaultPromptAPIVersion
	}
	return strings.TrimSpace(s.APIVersion)
}

// EffectiveModelEndpoint returns the model gateway to advertise to the
// harness. An explicitly configured ModelEndpoint wins. Otherwise the resolved
// Foundry project endpoint is used, because the model deployments this agent
// references live in the user's own project — falling straight through to the
// shared development default would send every user's traffic at a resource
// they cannot access.
func (s *PromptAgentSettings) EffectiveModelEndpoint() string {
	if s == nil {
		return DefaultPromptModelEndpoint
	}
	if v := strings.TrimSpace(s.ModelEndpoint); v != "" && v != DefaultPromptModelEndpoint {
		return v
	}
	if pe := strings.TrimSpace(s.ProjectEndpoint); pe != "" {
		// Trim the /api/projects/<name> suffix: the model gateway is the
		// account origin, not the project-scoped data-plane route.
		if u, err := url.Parse(pe); err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	if strings.TrimSpace(s.ModelEndpoint) != "" {
		return s.ModelEndpoint
	}
	return DefaultPromptModelEndpoint
}

// ApplyEnvOverrides updates any non-empty environment variables into the
// settings. Env vars trump stored values so a developer can temporarily
// retarget the harness without editing azure.yaml.
func (s *PromptAgentSettings) ApplyEnvOverrides() {
	if s == nil {
		return
	}
	if v := strings.TrimSpace(os.Getenv(PromptSubscriptionEnvVar)); v != "" {
		s.SubscriptionID = v
	}
	if v := strings.TrimSpace(os.Getenv(PromptResourceGroupEnvVar)); v != "" {
		s.ResourceGroup = v
	}
	if v := strings.TrimSpace(os.Getenv(PromptProjectEndpointEnvVar)); v != "" {
		s.ProjectEndpoint = v
	}
	if v := strings.TrimSpace(os.Getenv(PromptAPIVersionEnvVar)); v != "" {
		s.APIVersion = v
	}
	if v := strings.TrimSpace(os.Getenv(PromptModelEndpointEnvVar)); v != "" {
		s.ModelEndpoint = v
	}
}

// NewPromptAgentClient constructs the unified project-scoped agent client.
func NewPromptAgentClient(
	settings *PromptAgentSettings,
	credentials ...azcore.TokenCredential,
) (*agent_api.AgentClient, error) {
	if settings == nil {
		return nil, fmt.Errorf("NewPromptAgentClient: settings is nil")
	}
	settings.ApplyEnvOverrides()
	if err := settings.Validate(); err != nil {
		return nil, err
	}

	projectEndpoint := strings.TrimRight(strings.TrimSpace(settings.ProjectEndpoint), "/")
	if projectEndpoint == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"a Foundry project endpoint is required for prompt agent operations",
			"run `azd up` to provision a Foundry project",
		)
	}
	var credential azcore.TokenCredential
	if len(credentials) > 0 {
		credential = credentials[0]
	} else {
		credential = promptCredential("")
	}
	return agent_api.NewAgentClient(projectEndpoint, credential), nil
}

// promptScopesForBaseURL selects auth scopes by target endpoint.
//
// Public endpoints use audience-specific tokens:
//   - ai.azure.com and <region>.api.azureml.ms use AI audience tokens.
//   - management.azure.com uses ARM audience tokens.
//
// Local/custom harness endpoints continue to use cognitive-services scope.
// promptCredential returns the bearer-token credential to attach to harness
// requests, or nil when AZD_MANAGED_AGENT_NO_AUTH is truthy.
//
// Credential-construction failures are surfaced as nil so the underlying HTTP
// error from the service (401/403) becomes the user-visible failure mode —
// that error is more actionable than a generic "failed to create credential"
// wrap.
func promptCredential(tenantID string) *azidentity.AzureDeveloperCLICredential {
	if isTruthyEnvValue(os.Getenv(PromptNoAuthEnvVar)) {
		return nil
	}
	c, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   tenantID,
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err == nil {
		return c
	}

	return nil
}

// isTruthyEnvValue reports whether an environment-variable value should be
// treated as "on" (true/1/yes/on, case-insensitive).
func isTruthyEnvValue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// OverlayAzdProjectEnv fills any harness target field still at its package
// default placeholder from the provisioned azd project environment values.
//
// Real values resolved at init time (a user-selected Foundry project) are
// non-default and are preserved. This makes the "create a new Foundry project"
// init path work end-to-end: `azd up` provisions the project and writes the
// AZURE_* env vars, and the deploy then targets that provisioned project.
//
// The overlay is atomic on the presence of a resolved project: when the azd
// environment has no AZURE_AI_PROJECT_NAME (e.g. a scaffold that was never
// provisioned), nothing is changed and placeholder defaults are preserved.
// env is the azd environment key/value map; missing keys are ignored.
func (s *PromptAgentSettings) OverlayAzdProjectEnv(env map[string]string) {
	if s == nil || env == nil {
		return
	}
	// Gate on a resolved/provisioned project. Without one there is nothing to
	// overlay and placeholder tuple values must be preserved.
	if strings.TrimSpace(env["AZURE_AI_PROJECT_NAME"]) == "" {
		return
	}
	if strings.TrimSpace(s.SubscriptionID) == "" || s.SubscriptionID == DefaultPromptSubscriptionID {
		if v := strings.TrimSpace(env["AZURE_SUBSCRIPTION_ID"]); v != "" {
			s.SubscriptionID = v
		}
	}
	if strings.TrimSpace(s.ResourceGroup) == "" || s.ResourceGroup == DefaultPromptResourceGroup {
		if v := strings.TrimSpace(env["AZURE_RESOURCE_GROUP"]); v != "" {
			s.ResourceGroup = v
		}
	}
	if strings.TrimSpace(s.ModelEndpoint) == "" || s.ModelEndpoint == DefaultPromptModelEndpoint {
		if v := strings.TrimSpace(env["AZURE_AI_ACCOUNT_NAME"]); v != "" {
			s.ModelEndpoint = fmt.Sprintf("https://%s.services.ai.azure.com", v)
		}
	}
}
