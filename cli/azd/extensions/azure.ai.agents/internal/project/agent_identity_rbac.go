// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/azure/azure-dev/cli/shared/foundry/access"
)

const roleAzureAIUser = access.FoundryUserRoleID

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
	return access.AssignRole(ctx, cred, principalID, roleID, roleName, scope, principalType, nil)
}

func assignRoleToIdentityWithClient(
	ctx context.Context,
	client *armauthorization.RoleAssignmentsClient,
	principalID, roleID, roleName, scope string,
	principalType armauthorization.PrincipalType,
) (bool, error) {
	return access.AssignRoleWithClient(ctx, client, principalID, roleID, roleName, scope, principalType)
}

// extractSubscriptionID pulls the subscription ID from an Azure resource ID.
func extractSubscriptionID(resourceID string) string {
	return access.ExtractSubscriptionID(resourceID)
}
