// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/google/uuid"
)

const (
	roleAzureAIUser     = "53ca6127-db72-4b80-b1b0-d745d6d5456d"
	agentIdentitySuffix = "AgentIdentity"

	rbacVerifyMaxAttempts      = 12
	rbacVerifyPollInterval     = 5 * time.Second
	identityLookupMaxAttempts  = 12
	identityLookupPollInterval = 15 * time.Second
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

// isVnextEnabled checks if the enableHostedAgentVNext flag is set in the azd environment or OS env.
func isVnextEnabled(azdEnv map[string]string) bool {
	vnextValue := azdEnv["enableHostedAgentVNext"]
	if vnextValue == "" {
		vnextValue = os.Getenv("enableHostedAgentVNext")
	}
	if enabled, err := strconv.ParseBool(vnextValue); err == nil && enabled {
		return true
	}
	return false
}

// isRoleAssignmentsSkipped checks if AZD_AGENT_SKIP_ROLE_ASSIGNMENTS is set to a truthy value
// in the azd environment or OS environment. When true, both developer RBAC pre-flight checks
// and per-agent identity RBAC assignments are skipped.
func isRoleAssignmentsSkipped(azdEnv map[string]string) bool {
	value := azdEnv["AZD_AGENT_SKIP_ROLE_ASSIGNMENTS"]
	if value == "" {
		value = os.Getenv("AZD_AGENT_SKIP_ROLE_ASSIGNMENTS")
	}
	if skip, err := strconv.ParseBool(value); err == nil && skip {
		return true
	}
	return false
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

// agentIdentityDisplayName returns the expected display name for a per-agent identity SP.
// The platform creates an identity named {account}-{project}-{agentName}-AgentIdentity
// for each deployed hosted agent.
func agentIdentityDisplayName(accountName, projectName, agentName string) string {
	return fmt.Sprintf("%s-%s-%s-%s", accountName, projectName, agentName, agentIdentitySuffix)
}

// EnsureAgentIdentityRBAC looks up the per-agent identity service principals in Entra ID
// and assigns the required RBAC roles. This is designed to be called from the postdeploy
// handler when the vnext experience is enabled.
//
// Each deployed hosted agent gets a platform-created Entra service principal named
// {account}-{project}-{agentName}-AgentIdentity. This function looks up each identity
// and assigns Azure AI User scoped to the Foundry Project.
func EnsureAgentIdentityRBAC(ctx context.Context, azdClient *azdext.AzdClient, agentNames []string) error {
	if len(agentNames) == 0 {
		return nil
	}

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

	if !isVnextEnabled(azdEnv) {
		return nil
	}

	if isRoleAssignmentsSkipped(azdEnv) {
		fmt.Println("  (-) Skipping agent identity RBAC (AZD_AGENT_SKIP_ROLE_ASSIGNMENTS is set)")
		return nil
	}

	projectResourceID := azdEnv["AZURE_AI_PROJECT_ID"]
	if projectResourceID == "" {
		return fmt.Errorf("AZURE_AI_PROJECT_ID not set, unable to ensure agent identity RBAC")
	}

	info, err := parseAgentIdentityInfo(projectResourceID)
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

	return ensureAgentIdentityRBACWithCred(ctx, cred, info, agentNames)
}

// ensureAgentIdentityRBACWithCred performs the core per-agent identity RBAC logic using
// the provided credential. For each agent name, it looks up the per-agent identity
// and assigns Azure AI User scoped to the Foundry Project.
func ensureAgentIdentityRBACWithCred(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	info *agentIdentityInfo,
	agentNames []string,
) error {
	fmt.Println()
	fmt.Println("Agent Identity RBAC")
	fmt.Printf("  AI Account: %s\n", info.AccountName)
	fmt.Printf("  Project:    %s\n", info.ProjectName)
	fmt.Printf("  Agents:     %d\n", len(agentNames))

	graphClient, err := graphsdk.NewGraphClient(cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create Graph client: %w", err)
	}

	for i, agentName := range agentNames {
		fmt.Println()
		fmt.Printf("[%d/%d] Processing agent: %s\n", i+1, len(agentNames), agentName)

		if err := ensureSingleAgentRBAC(ctx, cred, graphClient, info, agentName); err != nil {
			return err
		}
	}

	fmt.Println()
	fmt.Println("✓ Agent identity RBAC complete")
	return nil
}

// ensureSingleAgentRBAC handles identity lookup and role assignment for a single agent.
func ensureSingleAgentRBAC(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	graphClient *graphsdk.GraphClient,
	info *agentIdentityInfo,
	agentName string,
) error {
	displayName := agentIdentityDisplayName(info.AccountName, info.ProjectName, agentName)

	// Poll for the identity — the platform provisions it asynchronously during agent deployment,
	// so it may not be visible in Entra ID immediately after deploy completes.
	var agentIdentities []graphsdk.ServicePrincipal
	var err error
	for attempt := range identityLookupMaxAttempts {
		agentIdentities, err = discoverAgentIdentity(ctx, graphClient, displayName)
		if err != nil {
			return fmt.Errorf("failed to discover agent identity: %w", err)
		}
		if len(agentIdentities) > 0 {
			break
		}
		if attempt < identityLookupMaxAttempts-1 {
			fmt.Printf("  Identity not ready yet in Entra ID, retrying in %s (%d/%d)...\n",
				identityLookupPollInterval, attempt+1, identityLookupMaxAttempts)
			time.Sleep(identityLookupPollInterval)
		}
	}

	if len(agentIdentities) == 0 {
		return fmt.Errorf(
			"agent identity '%s' not found in Entra ID — "+
				"the platform may not have provisioned it yet, wait a few minutes and re-run: azd deploy",
			displayName)
	}
	fmt.Println("  ✓ Agent identity found in Entra ID")

	// Assign Azure AI User role scoped to the Foundry Project
	for _, sp := range agentIdentities {
		principalID := ""
		if sp.Id != nil {
			principalID = *sp.Id
		}
		if principalID == "" {
			continue
		}

		fmt.Printf("  Agent identity: %s (%s)\n", sp.DisplayName, principalID)

		created, err := assignRoleToIdentity(
			ctx, cred, principalID, roleAzureAIUser, "Azure AI User → Foundry Project", info.ProjectScope,
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
	}

	return nil
}

// discoverAgentIdentity searches Entra ID for service principals matching the given display name.
func discoverAgentIdentity(
	ctx context.Context,
	graphClient *graphsdk.GraphClient,
	displayName string,
) ([]graphsdk.ServicePrincipal, error) {
	filter := fmt.Sprintf("displayName eq '%s'", displayName)
	resp, err := graphClient.
		ServicePrincipals().
		Filter(filter).
		Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list service principals: %w", err)
	}
	return resp.Value, nil
}

// assignRoleToIdentity assigns a single RBAC role to a service principal at the given scope.
// It is idempotent: existing assignments are detected and skipped.
// Returns true if a new assignment was created, false if it already existed.
func assignRoleToIdentity(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	principalID string,
	roleID string,
	roleName string,
	scope string,
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

	// Create assignment with explicit ServicePrincipal type
	fullRoleDefinitionID := fmt.Sprintf(
		"%s/providers/Microsoft.Authorization/roleDefinitions/%s", scope, roleID)
	roleAssignmentName := uuid.New().String()
	principalType := armauthorization.PrincipalTypeServicePrincipal
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
