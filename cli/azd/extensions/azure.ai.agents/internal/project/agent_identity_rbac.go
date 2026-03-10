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

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/google/uuid"
)

const (
	roleAzureAIUser              = "53ca6127-db72-4b80-b1b0-d745d6d5456d"
	roleMonitoringMetricsPublish = "3913510d-42f4-4e42-8a64-420c390055eb"
	agentIdentitySuffix          = "AgentIdentity"

	// agentAPIVersion used for temp agent create/delete to trigger identity provisioning
	agentAPIVersion = "v1"

	// Polling / timeout constants
	endpointPollInterval = 10 * time.Second
	endpointPollAttempts = 18 // ~3 minutes
	identityPollInterval = 10 * time.Second
	identityPollAttempts = 6 // ~1 minute
	rbacPropagationDelay = 30 * time.Second
)

// agentIdentityInfo holds the parsed project information needed for agent identity RBAC.
type agentIdentityInfo struct {
	AccountName    string
	ProjectName    string
	AccountScope   string // AI account resource ID (scope for Azure AI User role)
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
	projectIdx := strings.Index(projectResourceID, "/projects/")
	if projectIdx == -1 {
		return nil, fmt.Errorf("could not derive AI account scope from project resource ID: %s", projectResourceID)
	}
	info.AccountScope = projectResourceID[:projectIdx]

	return info, nil
}

// agentIdentityDisplayName returns the expected display name for the agent identity SP.
func agentIdentityDisplayName(accountName, projectName string) string {
	return fmt.Sprintf("%s-%s-%s", accountName, projectName, agentIdentitySuffix)
}

// EnsureAgentIdentityRBAC discovers (or triggers creation of) the agent identity service principal
// and assigns the required RBAC roles. This is designed to be called from the predeploy handler
// when the vnext experience is enabled.
//
// It fetches azd environment values and creates its own Azure credential, so callers only need to
// pass the AzdClient.
func (p *FoundryParser) EnsureAgentIdentityRBAC(ctx context.Context) error {
	azdEnvClient := p.AzdClient.Environment()
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
	tenantResponse, err := p.AzdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
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
	displayName := agentIdentityDisplayName(info.AccountName, info.ProjectName)

	fmt.Println()
	fmt.Println("Agent Identity RBAC")
	fmt.Printf("  AI Account: %s\n", info.AccountName)
	fmt.Printf("  Project:    %s\n", info.ProjectName)

	// Step 1: Discover or trigger agent identity
	fmt.Println("[1/2] Discovering agent identity...")

	graphClient, err := graphsdk.NewGraphClient(cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create Graph client: %w", err)
	}

	agentIdentities, err := discoverAgentIdentity(ctx, graphClient, displayName)
	if err != nil {
		return fmt.Errorf("failed to discover agent identity: %w", err)
	}

	if len(agentIdentities) == 0 {
		fmt.Println("  Agent identity not found — triggering creation via temp agent...")

		if err := triggerAgentIdentityCreation(ctx, cred, info, azdEnv); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Could not trigger agent identity creation: %v\n", err)
			fmt.Fprintln(os.Stderr, "    Wait a few minutes and re-run: azd deploy")
			return nil
		}

		// Poll for agent identity to appear in Azure AD
		fmt.Println("  Waiting for agent identity to appear...")
		for i := range identityPollAttempts {
			agentIdentities, err = discoverAgentIdentity(ctx, graphClient, displayName)
			if err == nil && len(agentIdentities) > 0 {
				fmt.Println("  ✓ Agent identity detected in Azure AD")
				break
			}
			if i < identityPollAttempts-1 {
				time.Sleep(identityPollInterval)
			}
		}

		if len(agentIdentities) == 0 {
			fmt.Fprintln(os.Stderr, "  ⚠ Agent identity not found yet — the platform can take up to 15 minutes.")
			fmt.Fprintln(os.Stderr, "    Wait a few minutes and re-run: azd deploy")
			return nil
		}
	} else {
		fmt.Println("  ✓ Agent identity found in Azure AD")
	}

	// Step 2: Assign RBAC roles
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

		// Azure AI User on the AI account
		if err := assignRoleToIdentity(ctx, cred, principalID, roleAzureAIUser, "Azure AI User → AI account", info.AccountScope); err != nil {
			fmt.Fprintf(os.Stderr, "    ✗ Azure AI User — %v\n", err)
		}

		// Monitoring Metrics Publisher on App Insights
		appInsightsRID := azdEnv["APPLICATIONINSIGHTS_RESOURCE_ID"]
		if appInsightsRID != "" {
			if err := assignRoleToIdentity(ctx, cred, principalID, roleMonitoringMetricsPublish, "Monitoring Metrics Publisher → App Insights", appInsightsRID); err != nil {
				fmt.Fprintf(os.Stderr, "    ✗ Monitoring Metrics Publisher — %v\n", err)
			}
		} else {
			fmt.Println("    ⚠ APPLICATIONINSIGHTS_RESOURCE_ID not set — skipping Monitoring Metrics Publisher")
		}
	}

	fmt.Println()
	fmt.Println("✓ Agent identity RBAC complete")
	return nil
}

// discoverAgentIdentity searches Azure AD for service principals matching the given display name.
func discoverAgentIdentity(ctx context.Context, graphClient *graphsdk.GraphClient, displayName string) ([]graphsdk.ServicePrincipal, error) {
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

// triggerAgentIdentityCreation creates and immediately deletes a throwaway Foundry agent
// to force the platform to provision the agent identity service principal.
func triggerAgentIdentityCreation(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	info *agentIdentityInfo,
	azdEnv map[string]string,
) error {
	projectEndpoint := azdEnv["AZURE_AI_PROJECT_ENDPOINT"]
	if projectEndpoint == "" {
		return fmt.Errorf("AZURE_AI_PROJECT_ENDPOINT not set")
	}

	// Find the first model deployment
	modelName, err := getFirstModelDeployment(ctx, cred, info)
	if err != nil {
		return fmt.Errorf("failed to find model deployment: %w", err)
	}
	if modelName == "" {
		return fmt.Errorf("no model deployments found — deploy a model first, then re-run")
	}
	fmt.Printf("  Using model: %s\n", modelName)

	// Wait for data-plane endpoint to be reachable
	fmt.Println("  Waiting for project data-plane endpoint...")
	agentClient := agent_api.NewAgentClient(projectEndpoint, cred)

	endpointReady := false
	for range endpointPollAttempts {
		_, err := agentClient.ListAgents(ctx, &agent_api.ListAgentQueryParameters{
			Limit: to.Ptr(int32(1)),
		}, agentAPIVersion)
		if err == nil {
			endpointReady = true
			fmt.Println("  ✓ Endpoint is ready")
			break
		}
		time.Sleep(endpointPollInterval)
	}

	if !endpointReady {
		return fmt.Errorf("endpoint not reachable after %d attempts", endpointPollAttempts)
	}

	// Create a temp agent to trigger identity creation
	tempAgentName := "setup-temp-" + uuid.New().String()[:8]
	description := "Temporary agent for identity provisioning."
	createReq := &agent_api.CreateAgentRequest{
		Name: tempAgentName,
		CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{
			Description: &description,
			Definition: &agent_api.PromptAgentDefinition{
				AgentDefinition: agent_api.AgentDefinition{
					Kind: agent_api.AgentKindPrompt,
				},
				Model:        modelName,
				Instructions: &description,
			},
		},
	}

	agent, err := agentClient.CreateAgent(ctx, createReq, agentAPIVersion)
	if err != nil {
		return fmt.Errorf("could not create temp agent: %w", err)
	}
	fmt.Printf("  ✓ Created temp agent: %s\n", agent.Name)

	// Delete the temp agent
	_, deleteErr := agentClient.DeleteAgent(ctx, agent.Name, agentAPIVersion)
	if deleteErr != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ Could not delete temp agent %s (clean up manually): %v\n", agent.Name, deleteErr)
	} else {
		fmt.Println("  ✓ Deleted temp agent")
	}

	return nil
}

// getFirstModelDeployment returns the name of the first model deployment for the AI account.
func getFirstModelDeployment(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	info *agentIdentityInfo,
) (string, error) {
	deploymentsClient, err := armcognitiveservices.NewDeploymentsClient(
		info.SubscriptionID, cred, azure.NewArmClientOptions())
	if err != nil {
		return "", fmt.Errorf("failed to create deployments client: %w", err)
	}

	pager := deploymentsClient.NewListPager(info.ResourceGroup, info.AccountName, nil)
	if pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to list deployments: %w", err)
		}
		if len(page.Value) > 0 && page.Value[0].Name != nil {
			return *page.Value[0].Name, nil
		}
	}

	return "", nil
}

// assignRoleToIdentity assigns a single RBAC role to a service principal at the given scope.
// It is idempotent: existing assignments are detected and skipped.
func assignRoleToIdentity(
	ctx context.Context,
	cred *azidentity.AzureDeveloperCLICredential,
	principalID string,
	roleID string,
	roleName string,
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

	fullRoleDefinitionID := fmt.Sprintf("%s/providers/Microsoft.Authorization/roleDefinitions/%s", scope, roleID)

	// Check for existing assignment
	pager := client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: to.Ptr("atScope()"),
	})

	alreadyExists := false
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
				*assignment.Properties.RoleDefinitionID == fullRoleDefinitionID {
				alreadyExists = true
				break
			}
		}
		if alreadyExists {
			break
		}
	}

	if alreadyExists {
		fmt.Printf("    ✓ %s (already assigned)\n", roleName)
		return nil
	}

	// Create assignment
	roleAssignmentName := uuid.New().String()
	parameters := armauthorization.RoleAssignmentCreateParameters{
		Properties: &armauthorization.RoleAssignmentProperties{
			RoleDefinitionID: to.Ptr(fullRoleDefinitionID),
			PrincipalID:      to.Ptr(principalID),
		},
	}

	_, err = client.Create(ctx, scope, roleAssignmentName, parameters, nil)
	if err != nil {
		if strings.Contains(err.Error(), "RoleAssignmentExists") || strings.Contains(err.Error(), "409") {
			fmt.Printf("    ✓ %s (already assigned)\n", roleName)
			return nil
		}
		return fmt.Errorf("failed to create role assignment: %w", err)
	}

	fmt.Printf("    ✓ %s\n", roleName)

	fmt.Printf("    ⏳ Waiting %s for RBAC propagation...\n", rbacPropagationDelay)
	time.Sleep(rbacPropagationDelay)

	return nil
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
