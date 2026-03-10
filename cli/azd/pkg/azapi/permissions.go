// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

// PermissionsService checks whether the current user has specific Azure RBAC permissions
// at a given scope (e.g. subscription).
// It works by listing the role assignments for the principal and then inspecting the role
// definitions to determine whether a required action (such as
// "Microsoft.Authorization/roleAssignments/write") is allowed.
type PermissionsService struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
}

// NewPermissionsService creates a new PermissionsService.
func NewPermissionsService(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
) *PermissionsService {
	return &PermissionsService{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
	}
}

// HasRequiredPermissions checks whether the given principal has all the specified
// permissions at the subscription scope. Each required permission should be an Azure
// resource provider action string such as
// "Microsoft.Authorization/roleAssignments/write".
//
// The check is performed by:
//  1. Listing all role assignments for the principal on the subscription.
//  2. Retrieving the role definition for each assignment.
//  3. Checking that the required actions are included in the allowed actions
//     and not excluded by NotActions.
func (s *PermissionsService) HasRequiredPermissions(
	ctx context.Context,
	subscriptionId string,
	principalId string,
	requiredActions []string,
) (bool, error) {
	credential, err := s.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return false, fmt.Errorf("getting credential for subscription %s: %w", subscriptionId, err)
	}

	// Create a role assignments client to list the principal's role assignments at subscription scope.
	roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(
		subscriptionId, credential, s.armClientOptions,
	)
	if err != nil {
		return false, fmt.Errorf("creating role assignments client: %w", err)
	}

	// Create a role definitions client to retrieve the definition for each assignment.
	roleDefinitionsClient, err := armauthorization.NewRoleDefinitionsClient(credential, s.armClientOptions)
	if err != nil {
		return false, fmt.Errorf("creating role definitions client: %w", err)
	}

	// Collect all role definition IDs assigned to this principal at subscription scope.
	// Use assignedTo() filter which is supported by the API and also captures
	// role assignments inherited through group membership.
	subscriptionScope := fmt.Sprintf("/subscriptions/%s", subscriptionId)
	filter := fmt.Sprintf("assignedTo('%s')", principalId)
	pager := roleAssignmentsClient.NewListForScopePager(
		subscriptionScope,
		&armauthorization.RoleAssignmentsClientListForScopeOptions{
			Filter: to.Ptr(filter),
		},
	)

	roleDefinitionIDs := []string{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, fmt.Errorf("listing role assignments for principal %s: %w", principalId, err)
		}
		for _, ra := range page.Value {
			if ra.Properties != nil && ra.Properties.RoleDefinitionID != nil {
				roleDefinitionIDs = append(roleDefinitionIDs, *ra.Properties.RoleDefinitionID)
			}
		}
	}

	if len(roleDefinitionIDs) == 0 {
		return false, nil
	}

	// Build the set of allowed actions from all role definitions.
	allowedActions, err := s.collectAllowedActions(ctx, roleDefinitionsClient, roleDefinitionIDs)
	if err != nil {
		return false, err
	}

	// Check that every required action is covered.
	for _, required := range requiredActions {
		if !isActionAllowed(required, allowedActions) {
			return false, nil
		}
	}

	return true, nil
}

// allowedActionSet represents the collected allowed and denied actions from role definitions.
type allowedActionSet struct {
	actions    []string
	notActions []string
}

// collectAllowedActions retrieves the allowed/denied actions from all specified role definitions.
func (s *PermissionsService) collectAllowedActions(
	ctx context.Context,
	client *armauthorization.RoleDefinitionsClient,
	roleDefinitionIDs []string,
) (*allowedActionSet, error) {
	result := &allowedActionSet{}

	for _, rdID := range roleDefinitionIDs {
		resp, err := client.GetByID(ctx, rdID, nil)
		if err != nil {
			return nil, fmt.Errorf("getting role definition %s: %w", rdID, err)
		}

		if resp.Properties == nil || resp.Properties.Permissions == nil {
			continue
		}

		for _, perm := range resp.Properties.Permissions {
			if perm.Actions != nil {
				for _, action := range perm.Actions {
					if action != nil {
						result.actions = append(result.actions, *action)
					}
				}
			}
			if perm.NotActions != nil {
				for _, notAction := range perm.NotActions {
					if notAction != nil {
						result.notActions = append(result.notActions, *notAction)
					}
				}
			}
		}
	}

	return result, nil
}

// isActionAllowed checks whether the given action is matched by any allowed action
// and not excluded by any NotAction. Action matching supports the wildcard "*".
func isActionAllowed(requiredAction string, actions *allowedActionSet) bool {
	matched := false
	for _, action := range actions.actions {
		if actionMatches(action, requiredAction) {
			matched = true
			break
		}
	}

	if !matched {
		return false
	}

	// Check if any NotAction excludes this action.
	for _, notAction := range actions.notActions {
		if actionMatches(notAction, requiredAction) {
			return false
		}
	}

	return true
}

// actionMatches checks whether a pattern (which may contain '*' wildcards) matches the target action.
// Azure RBAC uses case-insensitive matching. The wildcard '*' matches any sequence of characters.
// Common patterns: "*" (matches everything), "Microsoft.Authorization/*",
// "Microsoft.Authorization/roleAssignments/*", "Microsoft.Authorization/roleAssignments/write",
// "Microsoft.Authorization/*/Write".
func actionMatches(pattern, target string) bool {
	pattern = strings.ToLower(pattern)
	target = strings.ToLower(target)

	// Simple equality.
	if pattern == target {
		return true
	}

	// Full wildcard.
	if pattern == "*" {
		return true
	}

	// Handle patterns with a single '*' wildcard by splitting into prefix and suffix.
	if strings.Count(pattern, "*") == 1 {
		parts := strings.SplitN(pattern, "*", 2)
		prefix := parts[0]
		suffix := parts[1]

		return strings.HasPrefix(target, prefix) && strings.HasSuffix(target, suffix) &&
			len(target) >= len(prefix)+len(suffix)
	}

	return false
}
