// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azure.ai.projects/internal/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ResourceGroupLocationRuleID is the stable identifier for the resource-group
// location-mismatch provision validation check. It doubles as both the
// registration RuleID and the result DiagnosticId.
//
// The identifier keeps its original azure.ai.agents prefix so existing
// consumers do not break while ownership moves to azure.ai.projects.
const ResourceGroupLocationRuleID = "azure.ai.agents.resource_group_location_mismatch"

// ResourceGroupLocationCheck is a provider-agnostic "provision" validation check
// (azdext.ValidationCheckTypeProvision) that detects an immutable resource-group
// region conflict before provisioning starts. It is registered under the
// provision check type — rather than the Bicep-only "arm-provision" type —
// because the azure.ai.projects extension provisions through its own
// microsoft.foundry provider, which never triggers arm-provision checks.
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

	// resourceGroupLocation resolves the region of an existing resource group.
	// It is a field so tests can inject a fake without Azure access; production
	// code uses armResourceGroupLocation (set by the constructor).
	resourceGroupLocation resourceGroupLocationLookup
}

// resourceGroupLocationLookup resolves the Azure region of an existing resource
// group. It returns (location, found, err): found=false means the group does not
// exist (or its region is unknown), which is not a conflict; a non-nil err is
// treated as non-blocking by the caller (the check never blocks on its own
// inability to determine the situation).
type resourceGroupLocationLookup func(
	ctx context.Context, subscriptionID, resourceGroup string,
) (location string, found bool, err error)

// NewResourceGroupLocationCheck creates a new ResourceGroupLocationCheck.
func NewResourceGroupLocationCheck(azdClient *azdext.AzdClient) *ResourceGroupLocationCheck {
	c := &ResourceGroupLocationCheck{azdClient: azdClient}
	c.resourceGroupLocation = c.armResourceGroupLocation
	return c
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

	// Skip brownfield (bring-your-own) projects. When the Foundry service sets
	// `endpoint:`, the microsoft.foundry provider connects to that existing
	// project and provisions nothing — it creates no resource group and derives
	// its target from AZURE_AI_PROJECT_ID, ignoring AZURE_RESOURCE_GROUP. Running
	// this check there would compare AZURE_LOCATION against an unrelated, stale
	// resource group of the same name and could wrongly block provisioning while
	// suggesting the deletion of a resource group that has nothing to do with the
	// deployment.
	if c.isBrownfieldFoundryProject(ctx) {
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
	//
	// The context accessors return the environment value verbatim
	// (provisionValidationContext sources them from Environment.Getenv without
	// trimming), so trim here too. Otherwise a persisted value with surrounding
	// whitespace would bypass the trimmed envValueOrEmpty fallback and either
	// produce a false mismatch (AZURE_LOCATION) or target the wrong resource
	// group name and silently skip the check — while the Foundry provider, which
	// trims, provisions into the real group.
	subscriptionID, ok := valCtx.SubscriptionID()
	subscriptionID = strings.TrimSpace(subscriptionID)
	if !ok || subscriptionID == "" {
		subscriptionID = envValueOrEmpty(ctx, envClient, envName, "AZURE_SUBSCRIPTION_ID")
	}

	location, ok := valCtx.EnvLocation()
	location = strings.TrimSpace(location)
	if !ok || location == "" {
		location = envValueOrEmpty(ctx, envClient, envName, "AZURE_LOCATION")
	}
	if subscriptionID == "" || location == "" {
		// Without both values there is nothing to compare; provision will prompt for them.
		return empty, nil
	}

	resourceGroup, ok := valCtx.ResourceGroup()
	resourceGroup = strings.TrimSpace(resourceGroup)
	if !ok || resourceGroup == "" {
		resourceGroup = envValueOrEmpty(ctx, envClient, envName, "AZURE_RESOURCE_GROUP")
	}
	if resourceGroup == "" {
		// Mirror azd's default when AZURE_RESOURCE_GROUP is unset, reusing the
		// same helper the foundry provisioning path uses so the check stays in
		// sync if the default naming convention ever changes.
		resourceGroup = defaultResourceGroupName(envName)
	}

	existingLocation, found, err := c.resourceGroupLocation(ctx, subscriptionID, resourceGroup)
	if err != nil || !found {
		// Non-critical: a missing resource group (404), auth/tenant failure, or any
		// other lookup error is treated as non-blocking so the check never blocks
		// provisioning on its own inability to determine the situation.
		return empty, nil
	}

	return evaluateResourceGroupLocation(resourceGroup, existingLocation, location, subscriptionID), nil
}

// armResourceGroupLocation is the production resourceGroupLocationLookup. It
// resolves the resource group's region via ARM, using the azd credential scoped
// to the subscription's tenant. Every failure mode is non-blocking: it returns
// found=false (or an error the caller ignores) so the check never blocks
// provisioning on its own inability to determine the situation.
func (c *ResourceGroupLocationCheck) armResourceGroupLocation(
	ctx context.Context, subscriptionID, resourceGroup string,
) (string, bool, error) {
	tenantResponse, err := c.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subscriptionID,
	})
	if err != nil {
		return "", false, err // Non-critical: can't resolve tenant.
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResponse.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return "", false, err // Non-critical: auth issues will surface during provision.
	}

	rgClient, err := armresources.NewResourceGroupsClient(subscriptionID, cred, azure.NewArmClientOptions())
	if err != nil {
		return "", false, err // Non-critical: can't create the client.
	}

	getResp, err := rgClient.Get(ctx, resourceGroup, nil)
	if err != nil {
		// A 404 means the resource group does not exist yet — provision will create it in
		// AZURE_LOCATION, so there is no conflict. Any other error is treated as non-blocking.
		return "", false, err
	}

	if getResp.Location == nil {
		return "", false, nil
	}

	return *getResp.Location, true, nil
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
			"az group delete --name %q --subscription %s",
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

// isBrownfieldFoundryProject reports whether the current project's Foundry
// service is brownfield — i.e. it sets `endpoint:` to connect to an existing
// Foundry project. It reuses the same helpers the provider uses so the two stay
// in sync: findFoundryProjectService locates the service and foundryServiceEndpoint
// reports whether `endpoint:` is set. It is best-effort: if the project or
// azure.yaml cannot be read, or no Foundry service is found, it returns false so
// the check proceeds (the greenfield path, which is where the region conflict can
// actually occur).
func (c *ResourceGroupLocationCheck) isBrownfieldFoundryProject(ctx context.Context) bool {
	resp, err := c.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || resp.GetProject() == nil {
		return false
	}

	// ProjectConfig.Path is the project directory that contains azure.yaml.
	rawYAML, err := os.ReadFile(filepath.Join(resp.GetProject().GetPath(), "azure.yaml"))
	if err != nil {
		return false
	}

	svcName, err := findFoundryProjectService(rawYAML)
	if err != nil {
		return false
	}

	return foundryServiceEndpoint(rawYAML, svcName) != ""
}

// envValueOrEmpty returns the trimmed value of key in the named azd environment,
// or an empty string when it is unset or cannot be read. Trimming matches
// FoundryProvisioningProvider.envValue and prevents a stray leading/trailing
// space in AZURE_LOCATION or AZURE_RESOURCE_GROUP from producing a
// false-positive region mismatch (the comparison uses strings.EqualFold, which
// does not trim). The distinct name avoids confusion with that method, which
// has different error semantics (it propagates the error rather than swallowing
// it).
func envValueOrEmpty(ctx context.Context, envClient azdext.EnvironmentServiceClient, envName, key string) string {
	resp, err := envClient.GetValue(ctx, &azdext.GetEnvRequest{EnvName: envName, Key: key})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(resp.Value)
}
