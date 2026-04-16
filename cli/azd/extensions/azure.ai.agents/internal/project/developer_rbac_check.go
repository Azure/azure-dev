// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
)

const (
	// Broad roles that imply full access (superset of any specific role).
	roleOwner       = "8e3af657-a8ff-443c-a75c-2fe8c4bcb635"
	roleContributor = "b24988ac-6180-42a0-ab88-20f7382dd24c"

	// ACR-specific roles that grant push/build access.
	roleAcrPush                                = "8311e382-0749-4cb8-b61a-304f252e45ec"
	roleContainerRegistryTasksContributor      = "fb382eab-e894-4461-af04-94435c366c3f"
	roleContainerRegistryRepositoryContributor = "2efddaa5-3f1f-4df3-97df-af3f13818f4c"

	// AI-specific roles that grant agent management access.
	roleAzureAIDeveloper = "64702f94-c441-49e6-a78b-ef80e0188fee"
)

// sufficientACRRoles lists every role that grants enough ACR access to build
// and push container images. Order: broadest first for early exit.
var sufficientACRRoles = []string{
	roleOwner,
	roleContributor,
	roleAcrPush,
	roleContainerRegistryTasksContributor,
	roleContainerRegistryRepositoryContributor,
}

// sufficientAIUserRoles lists every role that grants enough Foundry Project
// access to create and run agents.
var sufficientAIUserRoles = []string{
	roleOwner,
	roleContributor,
	roleAzureAIUser,
	roleAzureAIDeveloper,
}

// CheckDeveloperRBAC verifies that the currently authenticated developer has the required
// RBAC roles for deploying hosted agents:
//   - Azure AI User on the Foundry Project (to create and run agents)
//   - Container Registry Tasks Contributor OR Container Registry Repository Contributor
//     on the ACR (to build images via remote build and push container images)
//
// Returns nil if all checks pass, or a structured error with suggestions on failure.
func CheckDeveloperRBAC(ctx context.Context, azdClient *azdext.AzdClient) error {
	azdEnvClient := azdClient.Environment()
	cEnvResponse, err := azdEnvClient.GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}
	envResponse, err := azdEnvClient.GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: cEnvResponse.Environment.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get environment values: %w", err)
	}

	azdEnv := make(map[string]string, len(envResponse.KeyValues))
	for _, kv := range envResponse.KeyValues {
		azdEnv[kv.Key] = kv.Value
	}

	if isRoleAssignmentsSkipped(azdEnv) {
		fmt.Println("  (-) Skipping developer RBAC pre-flight check (AZD_AGENT_SKIP_ROLE_ASSIGNMENTS is set)")
		return nil
	}

	projectResourceID := azdEnv["AZURE_AI_PROJECT_ID"]
	if projectResourceID == "" {
		// Can't check RBAC without the project ID; deployment will fail later with a clearer message.
		return nil
	}

	info, err := parseAgentIdentityInfo(projectResourceID)
	if err != nil {
		return nil // Non-critical: let deployment handle parse errors.
	}

	tenantResponse, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: info.SubscriptionID,
	})
	if err != nil {
		return nil // Non-critical: can't resolve tenant for pre-flight check.
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResponse.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil // Non-critical: auth issues will surface during deploy.
	}

	fmt.Println()
	fmt.Println("Developer RBAC pre-flight check")

	// Get the developer's principal ID via Graph API.
	graphClient, err := graphsdk.NewGraphClient(cred, nil)
	if err != nil {
		fmt.Println("  ⚠ Could not create Graph client — skipping RBAC pre-flight check")
		return nil
	}

	userProfile, err := graphClient.Me().Get(ctx)
	if err != nil {
		fmt.Println("  ⚠ Could not retrieve user profile — skipping RBAC pre-flight check")
		return nil
	}

	principalID := userProfile.Id
	fmt.Printf("  Developer: %s (%s)\n", userProfile.DisplayName, principalID)

	// Check 1: Azure AI User (or superset role) on Foundry Project scope.
	hasAIAccess, err := hasAnyRoleAssignment(ctx, cred, principalID, sufficientAIUserRoles, info.ProjectScope)
	if err != nil {
		fmt.Printf("  ⚠ Could not check AI User role: %s\n", err)
	} else if !hasAIAccess {
		return exterrors.Auth(
			exterrors.CodeDeveloperMissingAIUserRole,
			fmt.Sprintf(
				"your identity (%s) does not have the 'Azure AI User' role on the Foundry Project %s/%s",
				userProfile.DisplayName, info.AccountName, info.ProjectName,
			),
			fmt.Sprintf(
				"ask a subscription Owner or User Access Administrator to assign the 'Azure AI User' role "+
					"to your identity on the Foundry Project scope:\n"+
					"  az role assignment create --assignee %s --role \"Azure AI User\" --scope %q",
				principalID, info.ProjectScope,
			),
		)
	} else {
		fmt.Println("  ✓ Azure AI User on Foundry Project")
	}

	// Check 2: ACR role — any role that grants push/build access.
	acrEndpoint := azdEnv["AZURE_CONTAINER_REGISTRY_ENDPOINT"]
	if acrEndpoint == "" {
		fmt.Println("  ⚠ AZURE_CONTAINER_REGISTRY_ENDPOINT not set — skipping ACR role check")
		return nil
	}

	// Prefer the persisted ARM resource ID (set during init); fall back to listing registries.
	acrResourceID := azdEnv["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]
	if acrResourceID == "" {
		acrResourceID, err = resolveACRResourceID(ctx, cred, info.SubscriptionID, acrEndpoint)
		if err != nil {
			fmt.Printf("  ⚠ Could not resolve ACR resource ID: %s — skipping ACR role check\n", err)
			return nil
		}
	}

	hasACRAccess, err := hasAnyRoleAssignment(ctx, cred, principalID, sufficientACRRoles, acrResourceID)
	if err != nil {
		fmt.Printf("  ⚠ Could not check ACR role: %s\n", err)
		return nil
	}

	if !hasACRAccess {
		acrName := strings.TrimSuffix(normalizeLoginServer(acrEndpoint), ".azurecr.io")
		return exterrors.Auth(
			exterrors.CodeDeveloperMissingACRRole,
			fmt.Sprintf(
				"your identity (%s) does not have the required role on the Container Registry '%s' "+
					"to build and push container images",
				userProfile.DisplayName, acrName,
			),
			fmt.Sprintf(
				"ask a subscription Owner or User Access Administrator to assign one of these roles "+
					"to your identity on the Container Registry scope:\n"+
					"  • Owner or Contributor (broad access)\n"+
					"  • AcrPush (push and pull images)\n"+
					"  • Container Registry Tasks Contributor (remote build)\n"+
					"  • Container Registry Repository Contributor (repository operations)\n\n"+
					"  az role assignment create --assignee %s --role \"AcrPush\" --scope %q",
				principalID, acrResourceID,
			),
		)
	}

	fmt.Println("  ✓ Container Registry role on ACR")
	fmt.Println()
	return nil
}

// hasAnyRoleAssignment checks whether the given principal has any of the specified roles
// at the given scope (including inherited assignments from parent scopes).
// Uses server-side filtering by principal to reduce API load on large subscriptions.
func hasAnyRoleAssignment(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	principalID string,
	roleIDs []string,
	scope string,
) (bool, error) {
	subscriptionID := extractSubscriptionID(scope)
	if subscriptionID == "" {
		return false, fmt.Errorf("could not extract subscription ID from scope: %s", scope)
	}

	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create role assignments client: %w", err)
	}

	// Build a set of suffixes to match against.
	suffixes := make(map[string]bool, len(roleIDs))
	for _, id := range roleIDs {
		suffixes[fmt.Sprintf("/roleDefinitions/%s", id)] = true
	}

	// Use server-side assignedTo filter to only return assignments for this principal.
	filter := fmt.Sprintf("assignedTo('%s')", principalID)
	pager := client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: &filter,
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, fmt.Errorf("failed to list role assignments: %w", err)
		}
		for _, assignment := range page.Value {
			if assignment.Properties == nil || assignment.Properties.RoleDefinitionID == nil {
				continue
			}
			roleDefID := *assignment.Properties.RoleDefinitionID
			for suffix := range suffixes {
				if strings.HasSuffix(roleDefID, suffix) {
					return true, nil
				}
			}
		}
	}

	return false, nil
}

// normalizeLoginServer strips protocol prefixes, trailing slashes, and lowercases
// an ACR login server endpoint for consistent comparison.
func normalizeLoginServer(loginServer string) string {
	s := loginServer
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) > len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
			s = s[len(prefix):]
			break
		}
	}
	return strings.ToLower(strings.TrimSuffix(s, "/"))
}

// resolveACRResourceID finds the ARM resource ID for an Azure Container Registry
// given its login server endpoint (e.g., "myregistry.azurecr.io").
func resolveACRResourceID(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	subscriptionID string,
	loginServer string,
) (string, error) {
	loginServer = normalizeLoginServer(loginServer)

	client, err := armcontainerregistry.NewRegistriesClient(subscriptionID, cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create ACR client: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to list container registries: %w", err)
		}
		for _, registry := range page.Value {
			if registry.Properties != nil &&
				registry.Properties.LoginServer != nil &&
				strings.EqualFold(*registry.Properties.LoginServer, loginServer) {
				if registry.ID != nil {
					return *registry.ID, nil
				}
			}
		}
	}

	return "", fmt.Errorf("container registry with login server '%s' not found in subscription %s", loginServer, subscriptionID)
}
