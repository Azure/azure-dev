// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ResolvePromptCredential creates a tenant-aware credential for the Foundry
// project subscription, matching the prompt deployment path.
func ResolvePromptCredential(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	settings *PromptAgentSettings,
) (*azidentity.AzureDeveloperCLICredential, error) {
	if settings == nil || strings.TrimSpace(settings.SubscriptionID) == "" {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAzureSubscription,
			"AZURE_SUBSCRIPTION_ID is required for prompt agent authentication",
			"run `azd provision` to resolve the Foundry project subscription",
		)
	}
	tenant, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: settings.SubscriptionID,
	})
	if err != nil {
		return nil, exterrors.Auth(
			exterrors.CodeTenantLookupFailed,
			fmt.Sprintf("failed to get tenant for subscription %s: %s", settings.SubscriptionID, err),
			"verify your Azure login with `azd auth login`",
		)
	}
	credential := promptCredential(tenant.TenantId)
	if credential == nil {
		return nil, exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			"failed to create a credential for prompt agent operations",
			"run `azd auth login` to authenticate",
		)
	}
	return credential, nil
}

// PromptAgentSettings captures the harness connection details for a prompt agent.
//
// Values are resolved from the active azd environment. Keeping this
// environment-specific routing state out of azure.yaml allows the project to be
// reused across Foundry projects unchanged.
type PromptAgentSettings struct {
	// SubscriptionID is the Azure subscription containing the Foundry project.
	SubscriptionID string `json:"subscriptionId,omitempty"`

	// ResourceGroup is the Azure resource group containing the Foundry project.
	ResourceGroup string `json:"resourceGroup,omitempty"`

	// ProjectEndpoint is the Foundry project data-plane root
	// (https://<account>.services.ai.azure.com/api/projects/<project>). When set,
	// it is the authoritative routing target for ALL managed agent operations
	// (CRUD and Responses) and supersedes the legacy workspace tuple. It is
	// populated from the interactive init selection or, in --no-prompt flows,
	// from FOUNDRY_PROJECT_ENDPOINT in the azd environment.
	ProjectEndpoint string `json:"projectEndpoint,omitempty"`

	// ModelEndpoint is the model gateway the harness calls to reach the LLM.
	// Sent on invoke requests via the x-model-endpoint header.
	ModelEndpoint string `json:"modelEndpoint,omitempty"`
}

// DefaultPromptAgentSettings returns unresolved prompt-agent settings. Init may
// scaffold these before provision; deployment resolves real values from azd.
func DefaultPromptAgentSettings() PromptAgentSettings {
	return PromptAgentSettings{}
}

// overlay copies every non-empty field of src onto s, leaving s's existing
// value in place where src is empty.
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
			"run `azd up` to populate the Foundry project environment values",
		)
	}
	if err := validateAuthenticatedPromptEndpoint(s.ProjectEndpoint); err != nil {
		return err
	}
	return nil
}

func validateAuthenticatedPromptEndpoint(endpoint string) error {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" ||
		!strings.HasSuffix(strings.ToLower(u.Hostname()), ".services.ai.azure.com") {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"authenticated prompt agent endpoint must be an HTTPS Foundry project URL",
			"use https://<account>.services.ai.azure.com/api/projects/<project>",
		)
	}
	return nil
}

// EffectiveModelEndpoint returns the model gateway to advertise to the
// harness. An explicitly configured ModelEndpoint wins. Otherwise the resolved
// Foundry project endpoint is used, because the model deployments this agent
// references live in the user's own project — falling straight through to the
// shared development default would send every user's traffic at a resource
// they cannot access.
func (s *PromptAgentSettings) EffectiveModelEndpoint() string {
	if s == nil {
		return ""
	}
	if v := strings.TrimSpace(s.ModelEndpoint); v != "" {
		return v
	}
	if pe := strings.TrimSpace(s.ProjectEndpoint); pe != "" {
		// Trim the /api/projects/<name> suffix: the model gateway is the
		// account origin, not the project-scoped data-plane route.
		if u, err := url.Parse(pe); err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	return ""
}

// NewPromptAgentClient constructs the unified project-scoped agent client.
func NewPromptAgentClient(
	settings *PromptAgentSettings,
	credentials ...azcore.TokenCredential,
) (*agent_api.AgentClient, error) {
	if settings == nil {
		return nil, fmt.Errorf("NewPromptAgentClient: settings is nil")
	}
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
// requests.
//
// Credential-construction failures are surfaced as nil so the underlying HTTP
// error from the service (401/403) becomes the user-visible failure mode —
// that error is more actionable than a generic "failed to create credential"
// wrap.
func promptCredential(tenantID string) *azidentity.AzureDeveloperCLICredential {
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

// OverlayAzdProjectEnv fills unresolved harness target fields from the
// provisioned azd project environment values.
//
// Real values resolved at init time (a user-selected Foundry project) are
// non-default and are preserved. This makes the "create a new Foundry project"
// init path work end-to-end: `azd up` provisions the project and writes the
// AZURE_* env vars, and the deploy then targets that provisioned project.
//
// env is the azd environment key/value map; missing keys are ignored.
func (s *PromptAgentSettings) OverlayAzdProjectEnv(env map[string]string) {
	if s == nil || env == nil {
		return
	}
	if strings.TrimSpace(s.SubscriptionID) == "" {
		if v := strings.TrimSpace(env["AZURE_SUBSCRIPTION_ID"]); v != "" {
			s.SubscriptionID = v
		}
	}
	if strings.TrimSpace(s.ResourceGroup) == "" {
		if v := strings.TrimSpace(env["AZURE_RESOURCE_GROUP"]); v != "" {
			s.ResourceGroup = v
		}
	}
	if strings.TrimSpace(s.ModelEndpoint) == "" {
		if v := strings.TrimSpace(env["AZURE_AI_ACCOUNT_NAME"]); v != "" {
			s.ModelEndpoint = fmt.Sprintf("https://%s.services.ai.azure.com", v)
		}
	}
}
