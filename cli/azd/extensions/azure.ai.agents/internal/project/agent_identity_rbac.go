// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/google/uuid"
)

const (
	roleAzureAIUser                 = "53ca6127-db72-4b80-b1b0-d745d6d5456d"
	roleCognitiveServicesOpenAIUser = "5e0bd9bd-7b93-4f28-af87-19fc36ad61bd"
	roleMonitoringMetricsPublisher  = "3913510d-42f4-4e42-8a64-420c390055eb"
	agentIdentitySuffix             = "AgentIdentity"

	rbacVerifyMaxAttempts      = 12
	rbacVerifyPollInterval     = 5 * time.Second
	identityLookupMaxAttempts  = 12
	identityLookupPollInterval = 15 * time.Second
)

// agentIdentityInfo holds the parsed project information needed for agent identity RBAC.
type agentIdentityInfo struct {
	AccountName    string
	ProjectName    string
	AccountScope   string // AI account resource ID (scope for role assignments)
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

	// AI account scope is the project resource ID up to (but not including) "/projects/{project}"
	before, _, ok := strings.Cut(projectResourceID, "/projects/")
	if !ok {
		return nil, fmt.Errorf("could not derive AI account scope from project resource ID: %s", projectResourceID)
	}
	info.AccountScope = before

	return info, nil
}

// agentIdentityDisplayName returns the expected display name for the agent identity SP.
func agentIdentityDisplayName(accountName, projectName string) string {
	return fmt.Sprintf("%s-%s-%s", accountName, projectName, agentIdentitySuffix)
}

// EnsureAgentIdentityRBAC looks up the agent identity service principal in Azure AD
// and assigns the required RBAC roles. This is designed to be called from the postdeploy
// handler when the vnext experience is enabled.
//
// The platform provisions the agent identity automatically when an agent is deployed.
// This function assumes the identity already exists and assigns permissions to it.
func EnsureAgentIdentityRBAC(ctx context.Context, azdClient *azdext.AzdClient) error {
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

	return ensureAgentIdentityRBACWithCred(ctx, cred, azdEnv, info)
}

// ensureAgentIdentityRBACWithCred performs the core agent identity RBAC logic using
// the provided credential and pre-loaded environment values.
func ensureAgentIdentityRBACWithCred(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	azdEnv map[string]string,
	info *agentIdentityInfo,
) error {
	fmt.Println()
	fmt.Println("Agent Identity RBAC")
	fmt.Printf("  AI Account: %s\n", info.AccountName)
	fmt.Printf("  Project:    %s\n", info.ProjectName)

	// Step 1: Look up agent identity in Azure AD
	fmt.Println("[1/2] Looking up agent identity...")

	displayName := agentIdentityDisplayName(info.AccountName, info.ProjectName)
	graphClient, err := graphsdk.NewGraphClient(cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create Graph client: %w", err)
	}

	// Poll for the identity — the platform provisions it asynchronously during agent deployment,
	// so it may not be visible in Azure AD immediately after deploy completes.
	var agentIdentities []graphsdk.ServicePrincipal
	for attempt := range identityLookupMaxAttempts {
		agentIdentities, err = discoverAgentIdentity(ctx, graphClient, displayName)
		if err != nil {
			return fmt.Errorf("failed to discover agent identity: %w", err)
		}
		if len(agentIdentities) > 0 {
			break
		}
		if attempt < identityLookupMaxAttempts-1 {
			fmt.Printf("  Identity not yet visible in Azure AD, retrying in %s (%d/%d)...\n",
				identityLookupPollInterval, attempt+1, identityLookupMaxAttempts)
			time.Sleep(identityLookupPollInterval)
		}
	}

	if len(agentIdentities) == 0 {
		return fmt.Errorf(
			"agent identity '%s' not found in Azure AD — "+
				"the platform may not have provisioned it yet, wait a few minutes and re-run: azd deploy",
			displayName)
	}
	fmt.Println("  ✓ Agent identity found in Azure AD")

	// Step 2: Assign RBAC roles in parallel
	fmt.Println()
	fmt.Println("[2/2] Assigning RBAC to agent identity...")

	for _, sp := range agentIdentities {
		principalID := ""
		if sp.Id != nil {
			principalID = *sp.Id
		}
		if principalID == "" {
			continue
		}

		fmt.Printf("  Agent identity: %s (%s)\n", sp.DisplayName, principalID)

		// Build the list of role assignments to perform
		type roleAssignment struct {
			roleID   string
			roleName string
			scope    string
		}

		assignments := []roleAssignment{
			{roleAzureAIUser, "Azure AI User → AI account", info.AccountScope},
			{roleCognitiveServicesOpenAIUser, "Cognitive Services OpenAI User → AI account", info.AccountScope},
		}

		appInsightsRID := azdEnv["APPLICATIONINSIGHTS_RESOURCE_ID"]
		if appInsightsRID != "" {
			assignments = append(assignments,
				roleAssignment{roleMonitoringMetricsPublisher, "Monitoring Metrics Publisher → App Insights", appInsightsRID})
		} else {
			fmt.Println("    ⚠ APPLICATIONINSIGHTS_RESOURCE_ID not set — skipping Monitoring Metrics Publisher")
		}

		// Run all assignments in parallel
		type assignResult struct {
			created bool
			err     error
		}

		results := make([]assignResult, len(assignments))
		var wg sync.WaitGroup

		for i, a := range assignments {
			wg.Add(1)
			go func(i int, a roleAssignment) {
				defer wg.Done()
				created, err := assignRoleToIdentity(ctx, cred, principalID, a.roleID, a.roleName, a.scope)
				results[i] = assignResult{created: created, err: err}
			}(i, a)
		}

		wg.Wait()

		// Report results in order and check for errors
		for i, r := range results {
			if r.err != nil {
				return fmt.Errorf("failed to assign %s role: %w", assignments[i].roleName, r.err)
			}
			if r.created {
				fmt.Printf("    ✓ %s (created)\n", assignments[i].roleName)
			} else {
				fmt.Printf("    ✓ %s (already assigned)\n", assignments[i].roleName)
			}
		}

		// Verify newly created assignments have propagated
		for i, r := range results {
			if !r.created {
				continue
			}
			a := assignments[i]
			fmt.Printf("    ⏳ Verifying %s...\n", a.roleName)
			if err := verifyRoleAssignment(
				ctx, cred, principalID, a.roleID, a.scope,
			); err != nil {
				return fmt.Errorf("failed to verify %s role assignment: %w", a.roleName, err)
			}
			fmt.Printf("    ✓ %s (verified)\n", a.roleName)
		}
	}

	fmt.Println()
	fmt.Println("✓ Agent identity RBAC complete")
	return nil
}

// discoverAgentIdentity searches Azure AD for service principals matching the given display name.
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
