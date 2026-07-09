// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"strings"

	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ResourceGroupLocationRuleID is the stable identifier for the resource-group
// location-mismatch provision validation check.
const ResourceGroupLocationRuleID = "resource_group_location_mismatch"

// ResourceGroupLocationCheck is a provider-agnostic "provision" validation check
// (azdext.ValidationCheckTypeProvision) that detects an immutable resource-group
// region conflict before provisioning starts. It is registered under the
// provision check type — rather than the Bicep-only "local-preflight" type —
// because the azure.ai.agents extension provisions through its own
// microsoft.foundry provider, which never triggers local-preflight checks.
//
// The azure.ai.agents extension writes a stable, salted resource group name to
// AZURE_RESOURCE_GROUP at init. If AZURE_LOCATION is later changed (for example by
// re-running init, adopting a Foundry project in a different region, or a manual
// `azd env set`) while that resource group already exists in the original region,
// the subscription-scoped provisioning deployment fails with the ARM error
// "InvalidResourceGroupLocation" — a resource group's region cannot be changed.
//
// Surfacing the conflict here turns a slow, cryptic deploy-time failure into fast,
// actionable guidance during azd's validation phase.
type ResourceGroupLocationCheck struct {
	azdClient *azdext.AzdClient
}

// NewResourceGroupLocationCheck creates a new ResourceGroupLocationCheck.
func NewResourceGroupLocationCheck(azdClient *azdext.AzdClient) *ResourceGroupLocationCheck {
	return &ResourceGroupLocationCheck{azdClient: azdClient}
}

// Validate implements azdext.ValidationCheckProvider.
//
// The check is best-effort: any inability to determine the situation (missing
// environment values, auth failure, resource group not found, or an API error)
// yields no results so that validation is never blocked by the check itself.
// Only a definitive mismatch — the resource group exists in a different region —
// produces a blocking error result.
func (c *ResourceGroupLocationCheck) Validate(
	ctx context.Context,
	valCtx *azdext.ValidationContext,
	_ *azdext.ValidationCheckRequest,
) (*azdext.ValidationCheckResponse, error) {
	empty := &azdext.ValidationCheckResponse{}

	// The "provision" check type is dispatched for every project whenever this
	// extension is installed, but the InvalidResourceGroupLocation conflict this
	// check guards against is specific to the microsoft.foundry provisioning
	// provider — it runs a subscription-scoped deployment that CREATES the
	// resource group at AZURE_LOCATION. For any other provider (core Bicep,
	// Terraform, or an unrelated project that merely has this extension
	// installed) an existing resource group in a different region is perfectly
	// valid, so running the check there would raise a false positive. Skip
	// unless the current project actually provisions through microsoft.foundry.
	if !c.usesFoundryProvider(ctx) {
		return empty, nil
	}

	envClient := c.azdClient.Environment()
	envResp, err := envClient.GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return empty, nil // Non-critical: can't resolve the environment.
	}
	envName := envResp.Environment.Name

	// Prefer the values supplied in the lean "provision" validation context;
	// fall back to reading the azd environment directly. Context values are
	// best-effort and may be empty on a cold first run (dispatch happens before
	// the provider resolves and prompts for subscription/location/resource group).
	subscriptionID, ok := valCtx.SubscriptionID()
	if !ok || subscriptionID == "" {
		subscriptionID = envValue(ctx, envClient, envName, "AZURE_SUBSCRIPTION_ID")
	}

	location, ok := valCtx.EnvLocation()
	if !ok || location == "" {
		location = envValue(ctx, envClient, envName, "AZURE_LOCATION")
	}
	if subscriptionID == "" || location == "" {
		// Without both values there is nothing to compare; provision will prompt for them.
		return empty, nil
	}

	resourceGroup, ok := valCtx.ResourceGroup()
	if !ok || resourceGroup == "" {
		resourceGroup = envValue(ctx, envClient, envName, "AZURE_RESOURCE_GROUP")
	}
	if resourceGroup == "" {
		// Mirror azd's default when AZURE_RESOURCE_GROUP is unset.
		resourceGroup = fmt.Sprintf("rg-%s", envName)
	}

	tenantResponse, err := c.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subscriptionID,
	})
	if err != nil {
		return empty, nil // Non-critical: can't resolve tenant.
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResponse.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return empty, nil // Non-critical: auth issues will surface during provision.
	}

	rgClient, err := armresources.NewResourceGroupsClient(subscriptionID, cred, azure.NewArmClientOptions())
	if err != nil {
		return empty, nil // Non-critical: can't create the client.
	}

	getResp, err := rgClient.Get(ctx, resourceGroup, nil)
	if err != nil {
		// A 404 means the resource group does not exist yet — provision will create it in
		// AZURE_LOCATION, so there is no conflict. Any other error is treated as non-blocking.
		return empty, nil
	}

	if getResp.Location == nil {
		return empty, nil
	}

	return evaluateResourceGroupLocation(resourceGroup, *getResp.Location, location, subscriptionID), nil
}

// evaluateResourceGroupLocation compares an existing resource group's region against the
// requested AZURE_LOCATION. It returns an empty response when the regions match
// (case-insensitively) and a blocking error result with remediation guidance when they
// differ. The comparison and message construction are isolated here so they can be
// unit-tested without Azure access.
func evaluateResourceGroupLocation(
	resourceGroup, existingLocation, requestedLocation, subscriptionID string,
) *azdext.ValidationCheckResponse {
	if strings.EqualFold(existingLocation, requestedLocation) {
		return &azdext.ValidationCheckResponse{}
	}

	message := fmt.Sprintf(
		"Resource group %q already exists in region %q, but AZURE_LOCATION is set to %q. "+
			"A resource group's region cannot be changed, so provisioning would fail with "+
			"the ARM error \"InvalidResourceGroupLocation\".",
		resourceGroup, existingLocation, requestedLocation,
	)

	suggestion := fmt.Sprintf(
		"Resolve the conflict before provisioning by choosing one of the following:\n"+
			"  • Use the existing region:          azd env set AZURE_LOCATION %s\n"+
			"  • Target a different resource group: azd env set AZURE_RESOURCE_GROUP <new-name>\n"+
			"  • Delete the resource group if it is no longer needed: "+
			"az group delete --name %s --subscription %s",
		existingLocation, resourceGroup, subscriptionID,
	)

	return &azdext.ValidationCheckResponse{
		Results: []*azdext.ValidationCheckResult{
			{
				Severity:     azdext.ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_ERROR,
				DiagnosticId: ResourceGroupLocationRuleID,
				Message:      message,
				Suggestion:   suggestion,
			},
		},
	}
}

// usesFoundryProvider reports whether the current azd project provisions through
// the microsoft.foundry provider (azure.yaml `infra.provider: microsoft.foundry`).
// It is best-effort: when the project configuration cannot be read it returns
// false so the check is skipped rather than risk a false positive on a project
// that has nothing to do with Foundry agents.
func (c *ResourceGroupLocationCheck) usesFoundryProvider(ctx context.Context) bool {
	resp, err := c.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return false
	}
	return resp.GetProject().GetInfra().GetProvider() == FoundryProviderName
}

// envValue returns the value of key in the named azd environment, or an empty string when
// it is unset or cannot be read.
func envValue(ctx context.Context, envClient azdext.EnvironmentServiceClient, envName, key string) string {
	resp, err := envClient.GetValue(ctx, &azdext.GetEnvRequest{EnvName: envName, Key: key})
	if err != nil {
		return ""
	}
	return resp.Value
}
