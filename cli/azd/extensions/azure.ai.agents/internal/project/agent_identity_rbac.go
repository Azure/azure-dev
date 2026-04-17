// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/google/uuid"
)

const (
	roleAzureAIUser     = "53ca6127-db72-4b80-b1b0-d745d6d5456d"
	agentIdentitySuffix = "AgentIdentity"

	rbacVerifyMaxAttempts  = 12
	rbacVerifyPollInterval = 5 * time.Second
)

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

// EnsureAgentIdentityRBAC assigns the required RBAC roles to per-agent identity service principals.
// This is designed to be called from the postdeploy handler after agent deployment.
//
// EnsureAgentIdentityRBAC assigns Cognitive Services OpenAI Contributor roles to
// each deployed agent's instance identity. The principal ID is expected to be
// provided in the agentIdentities map from the deploy response. If a principal ID
// is empty, the agent is skipped with a warning.
func EnsureAgentIdentityRBAC(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentIdentities map[string]string,
) error {
	if len(agentIdentities) == 0 {
		return nil
	}

	envClient := azdClient.Environment()
	envResp, err := envClient.GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}
	envName := envResp.Environment.Name

	skipValue := ""
	skipResp, err := envClient.GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     "AZD_AGENT_SKIP_ROLE_ASSIGNMENTS",
	})
	if err == nil {
		skipValue = skipResp.Value
	}
	if skipValue == "" {
		skipValue = os.Getenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS")
	}
	if skip, parseErr := strconv.ParseBool(skipValue); parseErr == nil && skip {
		fmt.Println("  (-) Skipping agent identity RBAC (AZD_AGENT_SKIP_ROLE_ASSIGNMENTS is set)")
		return nil
	}

	projectResp, err := envClient.GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     "AZURE_AI_PROJECT_ID",
	})
	if err != nil || projectResp.Value == "" {
		return fmt.Errorf("AZURE_AI_PROJECT_ID not set, unable to ensure agent identity RBAC")
	}

	info, err := parseAgentIdentityInfo(projectResp.Value)
	if err != nil {
		return fmt.Errorf("failed to parse project resource ID: %w", err)
	}

	// Get tenant ID and create credential
	tenantResponse, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: info.SubscriptionID,
	})
	if err != nil {
		return fmt.Errorf("failed to get tenant ID: %w", err)
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResponse.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return fmt.Errorf("failed to create Azure credential: %w", err)
	}

	return ensureAgentIdentityRBACWithCred(ctx, cred, info, agentIdentities)
}

// ensureAgentIdentityRBACWithCred performs the core per-agent identity RBAC logic using
// the provided credential. For each agent, it assigns Azure AI User scoped to the Foundry Project.
// The principal ID must be provided from the deploy response. Agents are processed in parallel.
func ensureAgentIdentityRBACWithCred(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	info *agentIdentityInfo,
	agentIdentities map[string]string,
) error {
	fmt.Println()
	fmt.Println("Agent Identity RBAC")
	fmt.Printf("  AI Account: %s\n", info.AccountName)
	fmt.Printf("  Project:    %s\n", info.ProjectName)
	fmt.Printf("  Agents:     %d\n", len(agentIdentities))

	// Process agents in parallel — each has an independent identity.
	type agentResult struct {
		name string
		err  error
	}

	sortedNames := slices.Sorted(maps.Keys(agentIdentities))
	results := make([]agentResult, len(sortedNames))
	var wg sync.WaitGroup

	for i, agentName := range sortedNames {
		principalID := agentIdentities[agentName]
		wg.Add(1)
		go func(i int, name, pid string) {
			defer wg.Done()
			results[i] = agentResult{
				name: name,
				err:  ensureSingleAgentRBAC(ctx, cred, info, name, pid),
			}
		}(i, agentName, principalID)
	}

	wg.Wait()

	// Report results in order and return first error.
	for _, r := range results {
		if r.err != nil {
			return fmt.Errorf("agent identity RBAC failed for %q: %w", r.name, r.err)
		}
	}

	fmt.Println()
	fmt.Println("✓ Agent identity RBAC complete")
	return nil
}

// ensureSingleAgentRBAC handles role assignment for a single agent.
// The principalID must be provided from the deploy response's instance identity.
func ensureSingleAgentRBAC(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	info *agentIdentityInfo,
	agentName string,
	principalID string,
) error {
	if principalID == "" {
		fmt.Printf("  %s\n", output.WithErrorFormat(
			"ERROR: agent %q has no instance identity principal ID — skipping RBAC assignment. ", agentName,
		))
		return nil
	}

	fmt.Printf("  Agent identity: %s (principal: %s)\n", agentName, principalID)

	created, err := assignRoleToIdentity(
		ctx, cred, principalID, roleAzureAIUser, "Azure AI User → Foundry Project", info.ProjectScope,
		armauthorization.PrincipalTypeServicePrincipal,
	)
	if err != nil {
		return fmt.Errorf("failed to assign Azure AI User role: %w", err)
	}

	if created {
		fmt.Println("    ✓ Azure AI User → Foundry Project (created)")
		fmt.Println("    ⏳ Verifying Azure AI User...")
		if err := verifyRoleAssignment(ctx, cred, principalID, roleAzureAIUser, info.ProjectScope); err != nil {
			return fmt.Errorf("failed to verify Azure AI User role assignment: %w", err)
		}
		fmt.Println("    ✓ Azure AI User → Foundry Project (verified)")
	} else {
		fmt.Println("    ✓ Azure AI User → Foundry Project (already assigned)")
	}

	return nil
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

// verifyRoleAssignment polls until the given role assignment is visible in the scope's
// role assignment list, confirming that RBAC propagation has completed.
func verifyRoleAssignment(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	principalID string,
	roleID string,
	scope string,
) error {
	subscriptionID := extractSubscriptionID(scope)
	if subscriptionID == "" {
		return fmt.Errorf("could not extract subscription ID from scope: %s", scope)
	}

	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignments client: %w", err)
	}

	roleDefinitionSuffix := fmt.Sprintf("/roleDefinitions/%s", roleID)

	for attempt := range rbacVerifyMaxAttempts {
		pager := client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
			Filter: new("atScope()"),
		})

		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return fmt.Errorf("failed to list role assignments: %w", err)
			}
			for _, assignment := range page.Value {
				if assignment.Properties != nil &&
					assignment.Properties.PrincipalID != nil &&
					assignment.Properties.RoleDefinitionID != nil &&
					*assignment.Properties.PrincipalID == principalID &&
					strings.HasSuffix(*assignment.Properties.RoleDefinitionID, roleDefinitionSuffix) {
					return nil
				}
			}
		}

		if attempt < rbacVerifyMaxAttempts-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(rbacVerifyPollInterval):
				// wait for the next polling attempt
			}
		}
	}

	return fmt.Errorf(
		"role assignment not visible after %d attempts (principal: %s, role: %s, scope: %s)",
		rbacVerifyMaxAttempts, principalID, roleID, scope)
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
