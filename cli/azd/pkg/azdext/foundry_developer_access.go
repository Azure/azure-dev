// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/google/uuid"
)

const (
	// SkipFoundryRoleAssignmentsEnvKey disables the developer access check when set to a true value,
	// for environments where Foundry access is managed externally.
	SkipFoundryRoleAssignmentsEnvKey = "AZD_AGENT_SKIP_ROLE_ASSIGNMENTS"

	foundryProjectIDEnvKey = "AZURE_AI_PROJECT_ID"

	// RoleFoundryUser is the Foundry User (formerly Azure AI User) role definition ID.
	RoleFoundryUser = "53ca6127-db72-4b80-b1b0-d745d6d5456d"

	// Roles whose dataActions cover Microsoft.CognitiveServices/*, which includes the AIServices
	// namespace used by Foundry data-plane operations. Owner and Contributor are management-plane
	// only and are intentionally absent; so is Azure AI Developer, whose dataActions do not cover
	// the AIServices namespace.
	roleCognitiveServicesUser = "a97b65f3-24c7-4388-baec-2e87135dc908"
	roleFoundryProjectManager = "eadc314b-1a2d-4efa-be10-5d325db5065e"
	roleFoundryOwner          = "c883944f-8b7b-4483-af10-35834be79c4a"
)

// foundryDataPlaneRoles lists the roles sufficient to use the Foundry data plane.
var foundryDataPlaneRoles = []string{
	RoleFoundryUser,
	roleCognitiveServicesUser, // same dataActions; assigned by older Bicep provisions
	roleFoundryProjectManager,
	roleFoundryOwner,
}

// FoundryDeveloperAccessOptions configures EnsureFoundryDeveloperAccess.
type FoundryDeveloperAccessOptions struct {
	// Writer receives progress and warning output. Output is discarded when nil.
	Writer io.Writer
}

// foundryAccessChecked records the projects already handled in this process so that repeated
// callers (for example per-service deploy events) do not re-query role assignments.
var (
	foundryAccessMu      sync.Mutex
	foundryAccessChecked = map[string]struct{}{}
)

// EnsureFoundryDeveloperAccess verifies that the current identity can use the Foundry data plane of
// the project in the current azd environment, and assigns Foundry User when it is missing.
//
// It is intended to run before Foundry data-plane writes (for example creating a toolbox or deploying
// an agent), not before read-only operations. Missing access never fails the caller: a denied
// assignment is reported as a warning so work can proceed when access is managed externally.
// An error is returned only when the azd host cannot be queried.
func EnsureFoundryDeveloperAccess(
	ctx context.Context,
	azdClient *AzdClient,
	opts FoundryDeveloperAccessOptions,
) error {
	out := opts.Writer
	if out == nil {
		out = io.Discard
	}

	envClient := azdClient.Environment()
	envResp, err := envClient.GetCurrent(ctx, &EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}
	envName := envResp.Environment.Name

	skipValue := ""
	if skipResp, err := envClient.GetValue(ctx, &GetEnvRequest{
		EnvName: envName,
		Key:     SkipFoundryRoleAssignmentsEnvKey,
	}); err == nil {
		skipValue = skipResp.Value
	}
	if skipValue == "" {
		skipValue = os.Getenv(SkipFoundryRoleAssignmentsEnvKey)
	}
	if skip, parseErr := strconv.ParseBool(skipValue); parseErr == nil && skip {
		fmt.Fprintf(out, "  (-) Skipping developer RBAC pre-flight check (%s is set)\n",
			SkipFoundryRoleAssignmentsEnvKey)
		return nil
	}

	projectResp, err := envClient.GetValue(ctx, &GetEnvRequest{
		EnvName: envName,
		Key:     foundryProjectIDEnvKey,
	})
	if err != nil || projectResp.Value == "" {
		// Without the project ID there is nothing to check; the operation reports a clearer error.
		return nil
	}
	projectID := projectResp.Value

	scope, err := parseFoundryProjectScope(projectID)
	if err != nil {
		return nil
	}

	foundryAccessMu.Lock()
	_, done := foundryAccessChecked[strings.ToLower(projectID)]
	foundryAccessMu.Unlock()
	if done {
		return nil
	}

	tenantResp, err := azdClient.Account().LookupTenant(ctx, &LookupTenantRequest{
		SubscriptionId: scope.subscriptionID,
	})
	if err != nil {
		return nil
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResp.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil
	}

	principal, err := azdClient.Account().GetCurrentPrincipal(ctx, &GetCurrentPrincipalRequest{
		SubscriptionId: scope.subscriptionID,
	})
	if err != nil || strings.TrimSpace(principal.GetObjectId()) == "" {
		fmt.Fprintln(out, "  ⚠ Could not resolve current principal — skipping developer RBAC pre-flight check")
		return nil
	}
	var principalType armauthorization.PrincipalType
	switch principal.GetPrincipalType() {
	case PrincipalType_PRINCIPAL_TYPE_USER:
		principalType = armauthorization.PrincipalTypeUser
	case PrincipalType_PRINCIPAL_TYPE_SERVICE_PRINCIPAL:
		principalType = armauthorization.PrincipalTypeServicePrincipal
	default:
		fmt.Fprintln(out, "  ⚠ Unsupported current principal type — skipping developer RBAC pre-flight check")
		return nil
	}

	client, err := armauthorization.NewRoleAssignmentsClient(scope.subscriptionID, cred, nil)
	if err != nil {
		fmt.Fprintf(out, "  ⚠ Could not create role assignments client: %s\n", err)
		return nil
	}

	ensureFoundryUser(ctx, &roleAssignmentsAdapter{client: client}, out, scope, principal.GetObjectId(), principalType)

	foundryAccessMu.Lock()
	foundryAccessChecked[strings.ToLower(projectID)] = struct{}{}
	foundryAccessMu.Unlock()
	return nil
}

type foundryProjectScope struct {
	subscriptionID string
	accountName    string
	projectName    string
	projectScope   string
}

// parseFoundryProjectScope extracts the pieces of a Foundry project resource ID:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{account}/projects/{project}
func parseFoundryProjectScope(projectResourceID string) (*foundryProjectScope, error) {
	parts := strings.Split(projectResourceID, "/")
	scope := &foundryProjectScope{projectScope: projectResourceID}
	hasResourceGroup := false
	for i, part := range parts {
		if i+1 >= len(parts) {
			continue
		}
		switch strings.ToLower(part) {
		case "subscriptions":
			scope.subscriptionID = parts[i+1]
		case "resourcegroups":
			hasResourceGroup = parts[i+1] != ""
		case "accounts":
			scope.accountName = parts[i+1]
		case "projects":
			scope.projectName = parts[i+1]
		}
	}
	if scope.subscriptionID == "" || !hasResourceGroup || scope.accountName == "" || scope.projectName == "" {
		return nil, fmt.Errorf("invalid Foundry project resource ID: %s", projectResourceID)
	}
	return scope, nil
}

// roleAssignmentsAPI is the subset of role-assignment operations used by the access check.
type roleAssignmentsAPI interface {
	hasAnyRole(ctx context.Context, principalID string, roleIDs []string, scope string) (bool, error)
	assignRole(
		ctx context.Context, principalID, roleID, scope string, principalType armauthorization.PrincipalType,
	) error
}

// ensureFoundryUser grants Foundry User when the principal has no sufficient data-plane role.
// All failures are reported as warnings.
func ensureFoundryUser(
	ctx context.Context,
	api roleAssignmentsAPI,
	out io.Writer,
	scope *foundryProjectScope,
	principalID string,
	principalType armauthorization.PrincipalType,
) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Developer RBAC pre-flight check")
	fmt.Fprintf(out, "  Developer: %s (%s)\n", principalID, principalType)

	hasAccess, err := api.hasAnyRole(ctx, principalID, foundryDataPlaneRoles, scope.projectScope)
	if err != nil {
		fmt.Fprintf(out, "  ⚠ Could not check Foundry User role: %s\n", err)
		return
	}
	if hasAccess {
		fmt.Fprintln(out, "  ✓ Foundry User on Foundry Project")
		return
	}

	fmt.Fprintln(out, "  Foundry User role not found — attempting to auto-assign...")
	if err := api.assignRole(ctx, principalID, RoleFoundryUser, scope.projectScope, principalType); err != nil {
		if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
			respErr.StatusCode == http.StatusForbidden {
			fmt.Fprintf(out,
				"  ⚠ Your identity (%s) does not have the 'Foundry User' role on the Foundry Project %s/%s "+
					"and auto-assign was denied.\n"+
					"    Ask a subscription Owner or User Access Administrator to assign the role:\n"+
					"      az role assignment create --assignee %s --role %s --scope %q"+
					"  # Foundry User (formerly Azure AI User)\n",
				principalID, scope.accountName, scope.projectName,
				principalID, RoleFoundryUser, scope.projectScope,
			)
			return
		}
		fmt.Fprintf(out, "  ⚠ Foundry User auto-assign failed (non-auth error): %s — continuing\n", err)
		return
	}
	fmt.Fprintln(out, "  ✓ Foundry User auto-assigned to developer identity")
}

type roleAssignmentsAdapter struct {
	client *armauthorization.RoleAssignmentsClient
}

// hasAnyRole reports whether the principal holds any of roleIDs at scope, including inherited assignments.
func (a *roleAssignmentsAdapter) hasAnyRole(
	ctx context.Context, principalID string, roleIDs []string, scope string,
) (bool, error) {
	filter := fmt.Sprintf("assignedTo('%s')", principalID)
	pager := a.client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
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
			for _, id := range roleIDs {
				if strings.HasSuffix(*assignment.Properties.RoleDefinitionID, "/roleDefinitions/"+id) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func (a *roleAssignmentsAdapter) assignRole(
	ctx context.Context,
	principalID, roleID, scope string,
	principalType armauthorization.PrincipalType,
) error {
	subscriptionID := foundryScopeSubscription(scope)
	params := armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			RoleDefinitionID: new(fmt.Sprintf(
				"/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", subscriptionID, roleID)),
			PrincipalID:   new(principalID),
			PrincipalType: &principalType,
		},
	}
	if _, err := a.client.Create(ctx, scope, uuid.New().String(), params, nil); err != nil {
		if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
			(respErr.StatusCode == http.StatusConflict || respErr.ErrorCode == "RoleAssignmentExists") {
			return nil
		}
		return fmt.Errorf("failed to create role assignment: %w", err)
	}
	return nil
}

func foundryScopeSubscription(scope string) string {
	parts := strings.Split(scope, "/")
	for i, part := range parts {
		if strings.EqualFold(part, "subscriptions") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
