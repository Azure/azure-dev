// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/google/uuid"
)

const roleAzureAIUser = "53ca6127-db72-4b80-b1b0-d745d6d5456d"

// agentIdentityInfo holds the parsed project information needed for agent identity RBAC.
type agentIdentityInfo struct {
	AccountName    string
	ProjectName    string
	ProjectScope   string // Full project resource ID (scope for role assignments)
	AccountScope   string // AI account resource ID
	SubscriptionID string
	ResourceGroup  string
}

// parseAgentIdentityInfo extracts account name, project name, subscription, resource group,
// and the AI account scope from the full project resource ID.
//
// Expected format:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.CognitiveServices/accounts/{account}/projects/{project}
func parseAgentIdentityInfo(projectResourceID string) (*agentIdentityInfo, error) {
	parts := strings.Split(projectResourceID, "/")
	if len(parts) < 11 {
		return nil, fmt.Errorf("invalid project resource ID format: %s", projectResourceID)
	}

	info := &agentIdentityInfo{}

	for i, part := range parts {
		switch {
		case part == "subscriptions" && i+1 < len(parts):
			info.SubscriptionID = parts[i+1]
		case part == "resourceGroups" && i+1 < len(parts):
			info.ResourceGroup = parts[i+1]
		case part == "accounts" && i+1 < len(parts):
			info.AccountName = parts[i+1]
		case part == "projects" && i+1 < len(parts):
			info.ProjectName = parts[i+1]
		}
	}

	if info.SubscriptionID == "" || info.ResourceGroup == "" || info.AccountName == "" || info.ProjectName == "" {
		return nil, fmt.Errorf(
			"could not extract all required fields from project resource ID: %s", projectResourceID)
	}

	// Project scope is the full resource ID
	info.ProjectScope = projectResourceID

	// AI account scope is the project resource ID up to (but not including) "/projects/{project}"
	before, _, ok := strings.Cut(projectResourceID, "/projects/")
	if !ok {
		return nil, fmt.Errorf("could not derive AI account scope from project resource ID: %s", projectResourceID)
	}
	info.AccountScope = before

	return info, nil
}

// assignRoleToIdentity assigns a single RBAC role to a principal at the given scope.
// It is idempotent: existing assignments are detected and skipped.
// Returns true if a new assignment was created, false if it already existed.
func assignRoleToIdentity(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	principalID string,
	roleID string,
	roleName string,
	scope string,
	principalType armauthorization.PrincipalType,
) (bool, error) {
	subscriptionID := extractSubscriptionID(scope)
	if subscriptionID == "" {
		return false, fmt.Errorf("could not extract subscription ID from scope %s for role %s", scope, roleName)
	}

	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create role assignments client for role %s: %w", roleName, err)
	}

	roleDefinitionSuffix := fmt.Sprintf("/roleDefinitions/%s", roleID)

	// Check for existing assignment
	pager := client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: new("atScope()"),
	})

	alreadyExists := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return false, fmt.Errorf("failed to list role assignments for role %s: %w", roleName, err)
		}
		for _, assignment := range page.Value {
			if assignment.Properties != nil &&
				assignment.Properties.PrincipalID != nil &&
				assignment.Properties.RoleDefinitionID != nil &&
				*assignment.Properties.PrincipalID == principalID &&
				strings.HasSuffix(*assignment.Properties.RoleDefinitionID, roleDefinitionSuffix) {
				alreadyExists = true
				break
			}
		}
		if alreadyExists {
			break
		}
	}

	if alreadyExists {
		return false, nil
	}

	// Create assignment
	fullRoleDefinitionID := fmt.Sprintf(
		"%s/providers/Microsoft.Authorization/roleDefinitions/%s", scope, roleID)
	roleAssignmentName := uuid.New().String()
	parameters := armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			RoleDefinitionID: new(fullRoleDefinitionID),
			PrincipalID:      new(principalID),
			PrincipalType:    &principalType,
		},
	}

	_, err = client.Create(ctx, scope, roleAssignmentName, parameters, nil)
	if err != nil {
		if strings.Contains(err.Error(), "RoleAssignmentExists") ||
			strings.Contains(err.Error(), "409") {
			return false, nil
		}
		return false, fmt.Errorf("failed to create role assignment for role %s: %w", roleName, err)
	}

	return true, nil
}

// extractSubscriptionID pulls the subscription ID from an Azure resource ID.
func extractSubscriptionID(resourceID string) string {
	parts := strings.Split(resourceID, "/")
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
