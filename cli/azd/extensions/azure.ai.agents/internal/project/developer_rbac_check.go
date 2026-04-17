// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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

	// Roles that grant Microsoft.Authorization/roleAssignments/write.
	// Note: roleContributor is intentionally excluded — its notActions explicitly
	// block Microsoft.Authorization/*/Write.
	roleUserAccessAdministrator = "18d7d88d-d35e-4fb5-a5c3-7773c20a72d9"
	roleRBACAdministrator       = "f58310d9-a9f6-439a-9e8d-f62e7b41a168"

	// Classic ACR roles that grant push/build access.
	roleAcrPush                                = "8311e382-0749-4cb8-b61a-304f252e45ec"
	roleContainerRegistryTasksContributor      = "fb382eab-e894-4461-af04-94435c366c3f"
	roleContainerRegistryRepositoryContributor = "2efddaa5-3f1f-4df3-97df-af3f13818f4c"

	// ABAC repository-scoped role that grants write access.
	// Required for ABAC-mode registries (roleAssignmentMode == AbacRepositoryPermissions).
	roleAcrRepositoryWriter = "2a1e307c-b015-4ebd-883e-5b7698a07328"

	// AI-specific roles that grant agent management access.
	roleAzureAIDeveloper = "64702f94-c441-49e6-a78b-ef80e0188fee"
)

// sufficientACRRoles lists every role that grants enough ACR access to build
// and push container images on classic-mode registries. Order: broadest first for early exit.
var sufficientACRRoles = []string{
	roleOwner,
	roleContributor,
	roleAcrPush,
	roleContainerRegistryTasksContributor,
	roleContainerRegistryRepositoryContributor,
}

// sufficientACRAbacRoles lists roles sufficient for ABAC-mode registries
// (roleAssignmentMode == AbacRepositoryPermissions). Classic roles such as AcrPush and
// Contributor do not grant repository-scoped dataActions on ABAC registries.
var sufficientACRAbacRoles = []string{
	roleOwner,
	roleAcrRepositoryWriter,
	roleContainerRegistryRepositoryContributor, // superset of RepositoryWriter
}

// sufficientAIUserRoles lists every role that grants enough Foundry Project
// access to create and run agents.
var sufficientAIUserRoles = []string{
	roleOwner,
	roleContributor,
	roleAzureAIUser,
	roleAzureAIDeveloper,
}

// sufficientRoleAssignWriteRoles lists every role that grants
// Microsoft.Authorization/roleAssignments/write on Azure Resource Manager.
// Required for the postdeploy hook to assign Azure AI User to agent service principals.
// Note: roleContributor is intentionally excluded — Contributor's notActions explicitly
// block Microsoft.Authorization/*/Write.
var sufficientRoleAssignWriteRoles = []string{
	roleOwner,
	roleUserAccessAdministrator,
	roleRBACAdministrator,
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
		// Attempt to auto-assign Azure AI User to the developer. This succeeds when the
		// developer has Owner, User Access Administrator, or RBAC Administrator.
		fmt.Println("  Azure AI User role not found — attempting to auto-assign...")
		if _, assignErr := assignRoleToIdentity(
			ctx, cred, principalID, roleAzureAIUser,
			"Azure AI User → Foundry Project", info.ProjectScope,
			armauthorization.PrincipalTypeUser,
		); assignErr != nil {
			// Only treat 403 as a hard RBAC failure — transient errors (throttling, network) are non-blocking.
			if respErr, ok := errors.AsType[*azcore.ResponseError](assignErr); ok &&
				respErr.StatusCode == http.StatusForbidden {
				return exterrors.Auth(
					exterrors.CodeDeveloperMissingAIUserRole,
					fmt.Sprintf(
						"your identity (%s) does not have the 'Azure AI User' role on the Foundry Project %s/%s "+
							"and auto-assign was denied: %s",
						userProfile.DisplayName, info.AccountName, info.ProjectName, assignErr,
					),
					fmt.Sprintf(
						"ask a subscription Owner or User Access Administrator to assign the 'Azure AI User' role "+
							"to your identity on the Foundry Project scope:\n"+
							"  az role assignment create --assignee %s --role \"Azure AI User\" --scope %q",
						principalID, info.ProjectScope,
					),
				)
			}
			fmt.Printf("  ⚠ Azure AI User auto-assign failed (non-auth error): %s — continuing\n", assignErr)
		} else {
			fmt.Println("  ✓ Azure AI User auto-assigned to developer identity")
		}
	} else {
		fmt.Println("  ✓ Azure AI User on Foundry Project")
	}

	// Check 2: roleAssignments/write capability on Foundry Project scope.
	// Required for the postdeploy hook to assign Azure AI User to agent service principals.
	// Note: Contributor cannot write role assignments (it is excluded from sufficientRoleAssignWriteRoles).
	hasRoleWrite, err := hasAnyRoleAssignment(ctx, cred, principalID, sufficientRoleAssignWriteRoles, info.ProjectScope)
	if err != nil {
		fmt.Printf("  ⚠ Could not check role-assignment-write capability: %s\n", err)
	} else if !hasRoleWrite {
		return exterrors.Auth(
			exterrors.CodeDeveloperMissingRoleAssignWriteRole,
			fmt.Sprintf(
				"your identity (%s) does not have the permission to write role assignments on the "+
					"Foundry Project %s/%s — this is required for the postdeploy step to assign "+
					"'Azure AI User' to agent service principals",
				userProfile.DisplayName, info.AccountName, info.ProjectName,
			),
			fmt.Sprintf(
				"ask a subscription Owner or User Access Administrator to assign one of these roles "+
					"to your identity on the Foundry Project scope:\n"+
					"  • Owner\n"+
					"  • User Access Administrator\n"+
					"  • Role Based Access Control Administrator\n\n"+
					"  az role assignment create --assignee %s "+
					"--role \"Role Based Access Control Administrator\" --scope %q\n\n"+
					"Alternatively, if role assignments are managed externally:\n"+
					"  AZD_AGENT_SKIP_ROLE_ASSIGNMENTS=true",
				principalID, info.ProjectScope,
			),
		)
	} else {
		fmt.Println("  ✓ Role assignment write on Foundry Project")
	}

	// Check 3: ACR role — must branch on registry mode (ABAC vs classic).
	acrEndpoint := azdEnv["AZURE_CONTAINER_REGISTRY_ENDPOINT"]
	if acrEndpoint == "" {
		fmt.Println("  ⚠ AZURE_CONTAINER_REGISTRY_ENDPOINT not set — skipping ACR role check")
		return nil
	}

	// When AZURE_CONTAINER_REGISTRY_RESOURCE_ID is set (populated during init), use a targeted
	// RegistriesClient.Get() call — O(1) instead of paging all registries in the subscription.
	var (
		acrResourceID string
		isAbac        bool
	)
	if rid := azdEnv["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]; rid != "" {
		acrResourceID, isAbac, err = resolveACRInfoByResourceID(ctx, cred, rid)
	} else {
		acrResourceID, isAbac, err = resolveACRInfo(ctx, cred, info.SubscriptionID, acrEndpoint)
	}
	if err != nil {
		fmt.Printf("  ⚠ Could not resolve ACR resource info: %s — skipping ACR role check\n", err)
		return nil
	}

	acrRolesToCheck := sufficientACRRoles
	if isAbac {
		acrRolesToCheck = sufficientACRAbacRoles
	}

	hasACRAccess, err := hasAnyRoleAssignment(ctx, cred, principalID, acrRolesToCheck, acrResourceID)
	if err != nil {
		fmt.Printf("  ⚠ Could not check ACR role: %s\n", err)
		return nil
	}

	if !hasACRAccess {
		acrName := strings.TrimSuffix(normalizeLoginServer(acrEndpoint), ".azurecr.io")
		if isAbac {
			return exterrors.Auth(
				exterrors.CodeDeveloperMissingACRRole,
				fmt.Sprintf(
					"your identity (%s) does not have the required role on the ABAC-mode Container Registry '%s' "+
						"to push container images",
					userProfile.DisplayName, acrName,
				),
				fmt.Sprintf(
					"ask a subscription Owner or User Access Administrator to assign one of these roles "+
						"to your identity on the Container Registry scope:\n"+
						"  • Owner (broad access)\n"+
						"  • Container Registry Repository Writer (ABAC push)\n"+
						"  • Container Registry Repository Contributor (superset of Writer)\n\n"+
						"  az role assignment create --assignee %s "+
						"--role \"Container Registry Repository Writer\" --scope %q",
					principalID, acrResourceID,
				),
			)
		}
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

	if isAbac {
		fmt.Println("  ✓ Container Registry role on ACR (ABAC mode)")
	} else {
		fmt.Println("  ✓ Container Registry role on ACR")
	}
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

// resolveACRInfoByResourceID fetches ABAC mode and validates the resource ID for a registry
// using a single targeted ARM Get() call — O(1) vs paging all registries.
// resourceID must be a full ARM resource ID:
// /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.ContainerRegistry/registries/{name}
func resolveACRInfoByResourceID(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	resourceID string,
) (string, bool, error) {
	// Parse resource group and registry name from the ARM resource ID.
	parts := strings.Split(resourceID, "/")
	var resourceGroup, registryName, subscriptionID string
	for i, p := range parts {
		switch strings.ToLower(p) {
		case "subscriptions":
			if i+1 < len(parts) {
				subscriptionID = parts[i+1]
			}
		case "resourcegroups":
			if i+1 < len(parts) {
				resourceGroup = parts[i+1]
			}
		case "registries":
			if i+1 < len(parts) {
				registryName = parts[i+1]
			}
		}
	}
	if subscriptionID == "" || resourceGroup == "" || registryName == "" {
		return "", false, fmt.Errorf("could not parse subscription, resource group, or registry name from resource ID: %s", resourceID)
	}

	client, err := armcontainerregistry.NewRegistriesClient(subscriptionID, cred, nil)
	if err != nil {
		return "", false, fmt.Errorf("failed to create ACR client: %w", err)
	}

	resp, err := client.Get(ctx, resourceGroup, registryName, nil)
	if err != nil {
		return "", false, fmt.Errorf("failed to get container registry: %w", err)
	}

	if resp.ID == nil {
		return "", false, fmt.Errorf("registry response missing ID for resource: %s", resourceID)
	}

	abac := resp.Properties != nil &&
		resp.Properties.RoleAssignmentMode != nil &&
		*resp.Properties.RoleAssignmentMode == armcontainerregistry.RoleAssignmentModeAbacRepositoryPermissions

	return *resp.ID, abac, nil
}

// resolveACRInfo finds the ARM resource ID and ABAC mode for an Azure Container Registry
// given its login server endpoint (e.g., "myregistry.azurecr.io").
// isAbac is true when the registry uses roleAssignmentMode == AbacRepositoryPermissions.
func resolveACRInfo(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	subscriptionID string,
	loginServer string,
) (resourceID string, isAbac bool, err error) {
	loginServer = normalizeLoginServer(loginServer)

	client, err := armcontainerregistry.NewRegistriesClient(subscriptionID, cred, nil)
	if err != nil {
		return "", false, fmt.Errorf("failed to create ACR client: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", false, fmt.Errorf("failed to list container registries: %w", err)
		}
		for _, registry := range page.Value {
			if registry.Properties != nil &&
				registry.Properties.LoginServer != nil &&
				strings.EqualFold(*registry.Properties.LoginServer, loginServer) {
				if registry.ID != nil {
					abac := registry.Properties.RoleAssignmentMode != nil &&
						*registry.Properties.RoleAssignmentMode == armcontainerregistry.RoleAssignmentModeAbacRepositoryPermissions
					return *registry.ID, abac, nil
				}
			}
		}
	}

	return "", false, fmt.Errorf("container registry with login server '%s' not found in subscription %s", loginServer, subscriptionID)
}
