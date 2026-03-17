// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
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

// PermissionCheckResult describes the outcome of a permission check.
type PermissionCheckResult struct {
	// HasPermission is true when the required actions are granted by at least one role.
	HasPermission bool
	// Conditional is true when every role that grants the required action has an
	// ABAC condition attached. Conditions restrict the scope of the action (e.g.,
	// limiting which role definitions can be assigned) and the deployment may still
	// fail at the server-side validation stage.
	Conditional bool
}

// HasRequiredPermissions checks whether the given principal has all the specified
// permissions at the subscription scope. Each required permission should be an Azure
// resource provider action string such as
// "Microsoft.Authorization/roleAssignments/write".
//
// The check is performed by:
//  1. Listing all role assignments for the principal on the subscription.
//  2. Retrieving the role definition for each assignment.
//  3. Checking that the required actions are included in at least one role's
//     effective permissions (Actions minus NotActions). NotActions are evaluated
//     per role definition, not globally, matching Azure RBAC semantics.
//  4. Detecting whether all granting role assignments have ABAC conditions.
func (s *PermissionsService) HasRequiredPermissions(
	ctx context.Context,
	subscriptionId string,
	principalId string,
	requiredActions []string,
) (*PermissionCheckResult, error) {
	credential, err := s.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("getting credential for subscription %s: %w", subscriptionId, err)
	}

	// Create a role assignments client to list the principal's role assignments at subscription scope.
	roleAssignmentsClient, err := armauthorization.NewRoleAssignmentsClient(
		subscriptionId, credential, s.armClientOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("creating role assignments client: %w", err)
	}

	// Create a role definitions client to retrieve the definition for each assignment.
	roleDefinitionsClient, err := armauthorization.NewRoleDefinitionsClient(credential, s.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating role definitions client: %w", err)
	}

	// Collect role assignments with metadata about conditions.
	subscriptionScope := fmt.Sprintf("/subscriptions/%s", subscriptionId)
	filter := fmt.Sprintf("assignedTo('%s')", principalId)
	pager := roleAssignmentsClient.NewListForScopePager(
		subscriptionScope,
		&armauthorization.RoleAssignmentsClientListForScopeOptions{
			Filter: new(filter),
		},
	)

	var assignments []roleAssignmentInfo
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf(
				"listing role assignments for principal %s: %w", principalId, err)
		}
		for _, ra := range page.Value {
			if ra.Properties != nil && ra.Properties.RoleDefinitionID != nil {
				hasCondition := ra.Properties.Condition != nil &&
					*ra.Properties.Condition != ""
				assignments = append(assignments, roleAssignmentInfo{
					roleDefinitionID: *ra.Properties.RoleDefinitionID,
					hasCondition:     hasCondition,
				})
			}
		}
	}

	if len(assignments) == 0 {
		return &PermissionCheckResult{HasPermission: false}, nil
	}

	// Check each role definition and track whether granting roles have conditions.
	return s.checkActionsFromRoles(
		ctx, roleDefinitionsClient, assignments, requiredActions)
}

// roleAssignmentInfo pairs a role definition ID with whether the assignment has a condition.
type roleAssignmentInfo struct {
	roleDefinitionID string
	hasCondition     bool
}

// checkActionsFromRoles checks whether every required action is granted by at least
// one role definition. Each role is evaluated independently: an action is granted by a
// role if it matches an Action entry and is NOT excluded by a NotAction entry of that
// same role. It also tracks whether all granting assignments are conditional (ABAC).
func (s *PermissionsService) checkActionsFromRoles(
	ctx context.Context,
	client *armauthorization.RoleDefinitionsClient,
	assignments []roleAssignmentInfo,
	requiredActions []string,
) (*PermissionCheckResult, error) {
	// Track which required actions are still unresolved, and whether any granting
	// role is unconditional (no ABAC condition).
	remaining := make(map[string]bool, len(requiredActions))
	for _, a := range requiredActions {
		remaining[a] = true
	}

	// hasUnconditionalGrant tracks whether at least one granting role has no condition.
	hasUnconditionalGrant := false

	for _, assignment := range assignments {
		if len(remaining) == 0 && hasUnconditionalGrant {
			break
		}

		resp, err := client.GetByID(ctx, assignment.roleDefinitionID, nil)
		if err != nil {
			return nil, fmt.Errorf(
				"getting role definition %s: %w", assignment.roleDefinitionID, err)
		}

		if resp.Properties == nil || resp.Properties.Permissions == nil {
			continue
		}

		// Collect this role's actions and notActions.
		var actions, notActions []string
		for _, perm := range resp.Properties.Permissions {
			for _, a := range perm.Actions {
				if a != nil {
					actions = append(actions, *a)
				}
			}
			for _, na := range perm.NotActions {
				if na != nil {
					notActions = append(notActions, *na)
				}
			}
		}

		// Check each remaining required action against this role.
		for action := range remaining {
			if isActionAllowedByRole(action, actions, notActions) {
				delete(remaining, action)
				if !assignment.hasCondition {
					hasUnconditionalGrant = true
				}
			}
		}
	}

	if len(remaining) > 0 {
		return &PermissionCheckResult{HasPermission: false}, nil
	}

	return &PermissionCheckResult{
		HasPermission: true,
		Conditional:   !hasUnconditionalGrant,
	}, nil
}

// isActionAllowedByRole checks whether a single role's Actions (minus NotActions)
// grant the required action.
func isActionAllowedByRole(requiredAction string, actions []string, notActions []string) bool {
	matched := false
	for _, action := range actions {
		if actionMatches(action, requiredAction) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}

	for _, notAction := range notActions {
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

	// Handle patterns with '*' wildcards by splitting into segments and matching each in order.
	if strings.Contains(pattern, "*") {
		return wildcardMatch(pattern, target)
	}

	return false
}

// wildcardMatch matches target against a pattern that may contain one or more '*' wildcards,
// where each '*' matches zero or more characters.
//
// The algorithm:
//   - The first segment (before the first '*') must be a strict prefix of target.
//   - The last segment (after the last '*') must be a strict suffix of target.
//   - All middle segments must appear in order in the remaining portion of target.
func wildcardMatch(pattern, target string) bool {
	segments := strings.Split(pattern, "*")

	// First segment must match as a prefix (unless empty, meaning pattern starts with '*').
	lo := 0
	if segments[0] != "" {
		if !strings.HasPrefix(target, segments[0]) {
			return false
		}
		lo = len(segments[0])
	}

	// Last segment must match as a suffix (unless empty, meaning pattern ends with '*').
	hi := len(target)
	if lastSeg := segments[len(segments)-1]; lastSeg != "" {
		if !strings.HasSuffix(target, lastSeg) {
			return false
		}
		hi = len(target) - len(lastSeg)
		if hi < lo {
			return false
		}
	}

	// Middle segments must appear in order within target[lo:hi].
	pos := lo
	for _, seg := range segments[1 : len(segments)-1] {
		if seg == "" {
			continue
		}
		idx := strings.Index(target[pos:hi], seg)
		if idx == -1 {
			return false
		}
		pos += idx + len(seg)
	}

	return true
}
