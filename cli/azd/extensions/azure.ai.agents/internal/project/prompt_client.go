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
	PromptBaseURLEnvVar         = "AZD_MANAGED_AGENT_BASE_URL"
	PromptSubscriptionEnvVar    = "AZD_MANAGED_AGENT_SUBSCRIPTION_ID"
	PromptResourceGroupEnvVar   = "AZD_MANAGED_AGENT_RESOURCE_GROUP"
	PromptWorkspaceEnvVar       = "AZD_MANAGED_AGENT_WORKSPACE"
	PromptProjectEndpointEnvVar = "AZD_MANAGED_AGENT_PROJECT_ENDPOINT"
	PromptAPIVersionEnvVar      = "AZD_MANAGED_AGENT_API_VERSION"
	PromptModelEndpointEnvVar   = "AZD_MANAGED_AGENT_MODEL_ENDPOINT"
	// PromptNoAuthEnvVar, when truthy, skips attaching a bearer token to
	// harness requests. Use it only against a harness that runs with auth
	// fully bypassed; by default a cognitive-services token is attached.
	PromptNoAuthEnvVar = "AZD_MANAGED_AGENT_NO_AUTH"
)

// DefaultPromptBaseURL is the public managed prompt-agent control plane base
// URL prefix. The deploy path appends /<region> (from AZURE_LOCATION) when
// this default is still in use.
const DefaultPromptBaseURL = "https://ai.azure.com/api"

// Default ARM workspace tuple placeholders used when prompt init runs in
// non-guided mode. Guided init and env overlays replace these with real
// provisioned values.
const (
	DefaultPromptSubscriptionID = "00000000-0000-0000-0000-000000000001"
	DefaultPromptResourceGroup  = "test-rg"
	DefaultPromptWorkspace      = "test-ws"
)

// DefaultPromptAPIVersion is the api-version query parameter sent on every
// prompt-agent request.
const DefaultPromptAPIVersion = "2025-05-15-preview"

// DefaultPromptModelEndpoint is the model gateway the harness calls to reach
// the LLM. It is sent on invoke (Responses) requests via the x-model-endpoint
// header.
const DefaultPromptModelEndpoint = "https://va-dev-fdp-resource.services.ai.azure.com"

// PromptAgentSettings captures the harness connection details for a prompt
// (kind=managed) agent. It is stored in the azure.yaml service config block
// (ServiceTargetAgentConfig.PromptAgent) and resolved at deploy/invoke time.
type PromptAgentSettings struct {
	// BaseURL is the harness origin (scheme + host [+ port]). Required.
	BaseURL string `json:"baseUrl"`

	// SubscriptionID is the Azure subscription containing the workspace.
	SubscriptionID string `json:"subscriptionId"`

	// ResourceGroup is the Azure resource group containing the workspace.
	ResourceGroup string `json:"resourceGroup"`

	// Workspace is the Azure ML / Foundry workspace name.
	Workspace string `json:"workspace"`

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
		BaseURL:        DefaultPromptBaseURL,
		SubscriptionID: DefaultPromptSubscriptionID,
		ResourceGroup:  DefaultPromptResourceGroup,
		Workspace:      DefaultPromptWorkspace,
		APIVersion:     DefaultPromptAPIVersion,
		ModelEndpoint:  DefaultPromptModelEndpoint,
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
	if strings.TrimSpace(s.BaseURL) == "" {
		missing = append(missing, "baseUrl")
	}
	if strings.TrimSpace(s.SubscriptionID) == "" {
		missing = append(missing, "subscriptionId")
	}
	if strings.TrimSpace(s.ResourceGroup) == "" {
		missing = append(missing, "resourceGroup")
	}
	if strings.TrimSpace(s.Workspace) == "" {
		missing = append(missing, "workspace")
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

// EffectiveModelEndpoint returns the configured model endpoint, falling back
// to the package-level default when empty.
func (s *PromptAgentSettings) EffectiveModelEndpoint() string {
	if s == nil || strings.TrimSpace(s.ModelEndpoint) == "" {
		return DefaultPromptModelEndpoint
	}
	return s.ModelEndpoint
}

// ApplyEnvOverrides updates any non-empty environment variables into the
// settings. Env vars trump stored values so a developer can temporarily
// retarget the harness without editing azure.yaml.
func (s *PromptAgentSettings) ApplyEnvOverrides() {
	if s == nil {
		return
	}
	if v := strings.TrimSpace(os.Getenv(PromptBaseURLEnvVar)); v != "" {
		s.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv(PromptSubscriptionEnvVar)); v != "" {
		s.SubscriptionID = v
	}
	if v := strings.TrimSpace(os.Getenv(PromptResourceGroupEnvVar)); v != "" {
		s.ResourceGroup = v
	}
	if v := strings.TrimSpace(os.Getenv(PromptWorkspaceEnvVar)); v != "" {
		s.Workspace = v
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

// NewPromptAgentClient constructs a ManagedAgentClient from the given prompt
// settings. Environment overrides are applied first, then the settings are
// validated. Set AZD_MANAGED_AGENT_NO_AUTH=true to skip attaching a bearer
// token.
//
// Routing target:
//   - When ProjectEndpoint is set, all operations target the Foundry project
//     data-plane: https://<account>.services.ai.azure.com/api/projects/<project>/agents?api-version=v1
//   - Otherwise it falls back to the legacy workspace-rooted management route.
func NewPromptAgentClient(settings *PromptAgentSettings) (*agent_api.ManagedAgentClient, error) {
	if settings == nil {
		return nil, fmt.Errorf("NewPromptAgentClient: settings is nil")
	}
	settings.ApplyEnvOverrides()
	if err := settings.Validate(); err != nil {
		return nil, err
	}

	baseURL := settings.BaseURL
	var prefix string
	if pe := strings.TrimSpace(settings.ProjectEndpoint); pe != "" {
		b, p, err := agent_api.SplitProjectEndpoint(pe)
		if err != nil {
			return nil, err
		}
		baseURL, prefix = b, p
	} else {
		p, err := agent_api.BuildWorkspaceRoutePrefix(
			settings.SubscriptionID, settings.ResourceGroup, settings.Workspace,
		)
		if err != nil {
			return nil, fmt.Errorf("building workspace route prefix: %w", err)
		}
		prefix = p
	}

	return agent_api.NewManagedAgentClient(agent_api.ManagedAgentClientOptions{
		BaseURL:     baseURL,
		RoutePrefix: prefix,
		Credential:  promptCredential(),
		Scopes:      promptScopesForBaseURL(baseURL),
	})
}

// promptScopesForBaseURL selects auth scopes by target endpoint.
//
// Public endpoints use audience-specific tokens:
//   - ai.azure.com and <region>.api.azureml.ms use AI audience tokens.
//   - management.azure.com uses ARM audience tokens.
//
// Local/custom harness endpoints continue to use cognitive-services scope.
func promptScopesForBaseURL(baseURL string) []string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err == nil {
		host := strings.ToLower(parsed.Hostname())
		if strings.HasSuffix(host, "ai.azure.com") || strings.HasSuffix(host, ".api.azureml.ms") {
			return []string{"https://ai.azure.com/.default"}
		}
		if strings.HasSuffix(host, "management.azure.com") {
			return []string{"https://management.azure.com/.default"}
		}
	}

	return []string{"https://cognitiveservices.azure.com/.default"}
}

// promptCredential returns the bearer-token credential to attach to harness
// requests, or nil when AZD_MANAGED_AGENT_NO_AUTH is truthy.
//
// Credential-construction failures are surfaced as nil so the underlying HTTP
// error from the service (401/403) becomes the user-visible failure mode —
// that error is more actionable than a generic "failed to create credential"
// wrap.
func promptCredential() azcore.TokenCredential {
	if isTruthyEnvValue(os.Getenv(PromptNoAuthEnvVar)) {
		return nil
	}
	c, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{},
	)
	if err == nil {
		return c
	}

	// Fall back to Azure CLI tokens when azd credential construction is not
	// available in the current process context.
	azCred, azErr := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{})
	if azErr == nil {
		return azCred
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
	if strings.TrimSpace(s.BaseURL) == DefaultPromptBaseURL {
		if location := strings.ToLower(strings.TrimSpace(env["AZURE_LOCATION"])); location != "" {
			s.BaseURL = fmt.Sprintf("%s/%s", DefaultPromptBaseURL, location)
		}
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
	if strings.TrimSpace(s.Workspace) == "" || s.Workspace == DefaultPromptWorkspace {
		if v := strings.TrimSpace(env["AZURE_AI_PROJECT_NAME"]); v != "" {
			s.Workspace = v
		}
	}
	if strings.TrimSpace(s.ModelEndpoint) == "" || s.ModelEndpoint == DefaultPromptModelEndpoint {
		if v := strings.TrimSpace(env["AZURE_AI_ACCOUNT_NAME"]); v != "" {
			s.ModelEndpoint = fmt.Sprintf("https://%s.services.ai.azure.com", v)
		}
	}
}
