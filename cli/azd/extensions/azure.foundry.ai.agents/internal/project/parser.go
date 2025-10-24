// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/google/uuid"
)

type FoundryParser struct {
	AzdClient *azdext.AzdClient
}

// Check if there is a service using containerapp host and contains agent.yaml file in the service path
func shouldRun(ctx context.Context, project *azdext.ProjectConfig) (bool, error) {
	projectPath := project.Path
	for _, service := range project.Services {
		if service.Host == "containerapp" {
			servicePath := filepath.Join(projectPath, service.RelativePath)

			agentYamlPath := filepath.Join(servicePath, "agent.yaml")
			agentYmlPath := filepath.Join(servicePath, "agent.yml")
			agentPath := ""

			if _, err := os.Stat(agentYamlPath); err == nil {
				agentPath = agentYamlPath
			}

			if _, err := os.Stat(agentYmlPath); err == nil {
				agentPath = agentYmlPath
			}
			if agentPath != "" {
				// read the file content into bytes and close the file
				content, err := os.ReadFile(agentPath)
				if err != nil {
					return false, fmt.Errorf("failed to read agent yaml file: %w", err)
				}

				agent, err := agent_yaml.LoadAndValidateAgentManifest(content)
				if err != nil {
					return false, fmt.Errorf("failed to validate agent yaml file: %w", err)
				}

				return agent.Agent.Kind == agent_yaml.AgentKindYamlContainerApp, nil
			}
		}
	}

	return false, nil
}

func (p *FoundryParser) SetIdentity(ctx context.Context, args *azdext.ProjectEventArgs) error {
	shouldRun, err := shouldRun(ctx, args.Project)
	if err != nil {
		return fmt.Errorf("failed to determine if extension should attach: %w", err)
	}
	if !shouldRun {
		return nil
	}

	// Get aiFoundryProjectResourceId from environment config
	azdEnvClient := p.AzdClient.Environment()
	response, err := azdEnvClient.GetConfigString(ctx, &azdext.GetConfigStringRequest{
		Path: "infra.parameters.aiFoundryProjectResourceId",
	})
	if err != nil {
		return fmt.Errorf("failed to get environment config: %w", err)
	}
	aiFoundryProjectResourceID := response.Value
	fmt.Println("✓ Retrieved aiFoundryProjectResourceId")

	// Extract subscription ID from resource ID
	parts := strings.Split(aiFoundryProjectResourceID, "/")
	if len(parts) < 3 {
		return fmt.Errorf("invalid resource ID format: %s", aiFoundryProjectResourceID)
	}

	// Find subscription ID
	var subscriptionID string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
			break
		}
	}

	if subscriptionID == "" {
		return fmt.Errorf("subscription ID not found in resource ID: %s", aiFoundryProjectResourceID)
	}

	// Get the tenant ID
	tenantResponse, err := p.AzdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subscriptionID,
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

	// Get AI Foundry Project's managed identity
	fmt.Println("Retrieving AI Foundry Project identity...")
	projectPrincipalID, err := getProjectPrincipalID(ctx, cred, aiFoundryProjectResourceID, subscriptionID)
	if err != nil {
		return fmt.Errorf("failed to get Project principal ID: %w", err)
	}
	fmt.Printf("Principal ID: %s\n", projectPrincipalID)

	// Get Application ID from Principal ID
	fmt.Println("Retrieving Application ID...")
	projectClientID, err := getApplicationID(context.Background(), cred, projectPrincipalID)
	if err != nil {
		return fmt.Errorf("failed to get Application ID: %w", err)
	}

	fmt.Printf("Application ID: %s\n", projectClientID)

	// Save to environment
	cResponse, err := azdEnvClient.GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}

	_, err = azdEnvClient.SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: cResponse.Environment.Name,
		Key:     "AI_FOUNDRY_PROJECT_APP_ID",
		Value:   projectClientID,
	})
	if err != nil {
		return fmt.Errorf("failed to set AI_FOUNDRY_PROJECT_APP_ID in environment: %w", err)
	}

	fmt.Println("✓ Application ID saved to environment")

	return nil
}

// getProjectPrincipalID retrieves the principal ID from the AI Foundry Project using Azure SDK
func getProjectPrincipalID(ctx context.Context, cred *azidentity.AzureDeveloperCLICredential, resourceID, subscriptionID string) (string, error) {
	// Create resources client
	client, err := armresources.NewClient(subscriptionID, cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create resources client: %w", err)
	}

	// Get the resource
	// API version for AI Foundry projects (Machine Learning workspaces)
	apiVersion := "2025-06-01"
	resp, err := client.GetByID(ctx, resourceID, apiVersion, nil)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve resource: %w", err)
	}

	// Extract principal ID from identity
	if resp.Identity == nil {
		return "", fmt.Errorf("resource does not have an identity")
	}

	if resp.Identity.PrincipalID == nil {
		return "", fmt.Errorf("resource identity does not have a principal ID")
	}

	principalID := *resp.Identity.PrincipalID
	if principalID == "" {
		return "", fmt.Errorf("principal ID is empty")
	}

	return principalID, nil
}

// getApplicationID retrieves the application ID from the principal ID using Microsoft Graph API
func getApplicationID(ctx context.Context, cred *azidentity.AzureDeveloperCLICredential, principalID string) (string, error) {
	// Create Graph client
	graphClient, err := graphsdk.NewGraphClient(cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create Graph client: %w", err)
	}

	// Get service principal directly by object ID (principal ID)
	servicePrincipal, err := graphClient.
		ServicePrincipalById(principalID).
		Get(ctx)

	if err != nil {
		return "", fmt.Errorf("failed to retrieve service principal with principal ID '%s': %w", principalID, err)
	}

	appID := servicePrincipal.AppId
	if appID == "" {
		return "", fmt.Errorf("application ID is empty")
	}

	return appID, nil
}

// getCognitiveServicesAccountLocation retrieves the location of a Cognitive Services account using Azure SDK
func getCognitiveServicesAccountLocation(ctx context.Context, cred *azidentity.AzureDeveloperCLICredential, subscriptionID, resourceGroupName, accountName string) (string, error) {
	// Create cognitive services accounts client
	client, err := armcognitiveservices.NewAccountsClient(subscriptionID, cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create cognitive services client: %w", err)
	}

	// Get the account
	resp, err := client.Get(ctx, resourceGroupName, accountName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get cognitive services account: %w", err)
	}

	// Extract location
	if resp.Location == nil {
		return "", fmt.Errorf("cognitive services account does not have a location")
	}

	location := *resp.Location
	if location == "" {
		return "", fmt.Errorf("location is empty")
	}

	return location, nil
}

/////////////////////////////////////////////////////////////////////////////

// Config structures for JSON parsing
type AgentRegistrationPayload struct {
	Description string          `json:"description"`
	Definition  AgentDefinition `json:"definition"`
}

type AgentDefinition struct {
	Kind                      string                     `json:"kind"`
	ContainerProtocolVersions []ContainerProtocolVersion `json:"container_protocol_versions"`
	ContainerAppResourceID    string                     `json:"container_app_resource_id"`
	IngressSubdomainSuffix    string                     `json:"ingress_subdomain_suffix"`
}

type ContainerProtocolVersion struct {
	Protocol string `json:"protocol"`
	Version  string `json:"version"`
}

type AgentResponse struct {
	Version string `json:"version"`
}

type DataPlanePayload struct {
	Agent  AgentReference `json:"agent"`
	Input  string         `json:"input"`
	Stream bool           `json:"stream"`
}

type AgentReference struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type DataPlaneResponse struct {
	Output string `json:"output"`
}

func (p *FoundryParser) CoboPostDeploy(ctx context.Context, args *azdext.ProjectEventArgs) error {
	shouldRun, err := shouldRun(ctx, args.Project)
	if err != nil {
		return fmt.Errorf("failed to determine if extension should attach: %w", err)
	}
	if !shouldRun {
		return nil
	}

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
	values := envResponse.KeyValues
	azdEnv := make(map[string]string, len(values))
	for _, kv := range values {
		azdEnv[kv.Key] = kv.Value
	}

	// Get required values from azd environment
	containerAppPrincipalID := azdEnv["SERVICE_API_IDENTITY_PRINCIPAL_ID"]
	aiFoundryResourceID := azdEnv["AI_FOUNDRY_RESOURCE_ID"]
	aiFoundryProjectResourceID := azdEnv["AI_FOUNDRY_PROJECT_RESOURCE_ID"]
	deploymentName := azdEnv["DEPLOYMENT_NAME"]
	resourceID := azdEnv["SERVICE_API_RESOURCE_ID"]
	agentName := azdEnv["AGENT_NAME"]
	//aiProjectEndpoint := azdEnv["AI_PROJECT_ENDPOINT"]

	// Validate required variables
	if err := validateRequired("AI_FOUNDRY_RESOURCE_ID", aiFoundryResourceID); err != nil {
		return err
	}
	if err := validateRequired("AI_FOUNDRY_PROJECT_RESOURCE_ID", aiFoundryProjectResourceID); err != nil {
		return err
	}
	if err := validateRequired("SERVICE_API_IDENTITY_PRINCIPAL_ID", containerAppPrincipalID); err != nil {
		return err
	}
	if err := validateRequired("DEPLOYMENT_NAME", deploymentName); err != nil {
		return err
	}
	if err := validateRequired("AGENT_NAME", agentName); err != nil {
		return err
	}

	// Extract project information from resource IDs
	parts := strings.Split(aiFoundryProjectResourceID, "/")
	if len(parts) < 11 {
		fmt.Fprintln(os.Stderr, "Error: Invalid AI Foundry Project Resource ID format")
		os.Exit(1)
	}

	projectSubscriptionID := parts[2]
	projectResourceGroup := parts[4]
	projectAIFoundryName := parts[8]
	projectName := parts[10]

	// Get the tenant ID
	tenantResponse, err := p.AzdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: projectSubscriptionID,
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

	// Get AI Foundry region using SDK
	aiFoundryRegion, err := getCognitiveServicesAccountLocation(ctx, cred, projectSubscriptionID, projectResourceGroup, projectAIFoundryName)
	if err != nil {
		return fmt.Errorf("failed to get AI Foundry region: %w", err)
	}

	fmt.Printf("AI Foundry region: %s\n", aiFoundryRegion)
	fmt.Printf("Project: %s\n", projectName)
	fmt.Printf("Deployment: %s\n", deploymentName)
	fmt.Printf("Agent: %s\n", agentName)

	// Assign Azure AI User role
	if err := assignAzureAIRole(ctx, cred, containerAppPrincipalID, aiFoundryResourceID); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to assign 'Azure AI User' role: %v\n", err)
		fmt.Fprintln(os.Stderr, "This requires Owner or User Access Administrator role on the AI Foundry Account.")
		fmt.Fprintln(os.Stderr, "Manual command:")
		fmt.Fprintf(os.Stderr, "az role assignment create \\\n")
		fmt.Fprintf(os.Stderr, "  --assignee %s \\\n", containerAppPrincipalID)
		fmt.Fprintf(os.Stderr, "  --role \"53ca6127-db72-4b80-b1b0-d745d6d5456d\" \\\n")
		fmt.Fprintf(os.Stderr, "  --scope \"%s\"\n", aiFoundryResourceID)
		return err
	}

	if err := validateRequired("SERVICE_API_RESOURCE_ID", resourceID); err != nil {
		return err
	}

	// Deactivate hello-world revision
	if err := deactivateHelloWorldRevision(ctx, cred, resourceID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to deactivate hello-world revision: %v\n", err)
		// Don't return error, just warn - this is not critical for the deployment
	}

	// Verify authentication configuration
	if err := verifyAuthConfiguration(ctx, cred, resourceID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to verify authentication configuration: %v\n", err)
		// Don't return error, just warn - this is not critical for the deployment
	}

	// Get the Container App endpoint (FQDN) for testing using SDK
	fmt.Println("Retrieving Container App endpoint...")
	acaEndpoint, err := getContainerAppEndpoint(ctx, cred, resourceID, projectSubscriptionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to retrieve Container App endpoint: %v\n", err)
	} else {
		fmt.Printf("Container App endpoint: %s\n", acaEndpoint)
	}

	// Get AI Foundry Project endpoint using SDK
	fmt.Println("Retrieving AI Foundry Project API endpoint...")
	aiFoundryProjectEndpoint, err := getAIFoundryProjectEndpoint(ctx, cred, aiFoundryProjectResourceID, projectSubscriptionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to retrieve AI Foundry Project API endpoint: %v\n", err)
	} else {
		fmt.Printf("AI Foundry Project API endpoint: %s\n", aiFoundryProjectEndpoint)
	}

	// Acquire AAD token using SDK
	token, err := getAccessToken(ctx, cred, "https://ai.azure.com")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to acquire access token: %v\n", err)
		return fmt.Errorf("failed to acquire access token: %w", err)
	}

	// Get latest revision and build ingress suffix using SDK
	latestRevision, err := getLatestRevisionName(ctx, cred, resourceID, projectSubscriptionID)
	if err != nil {
		return fmt.Errorf("failed to get latest revision: %w", err)
	}

	ingressSuffix := "--" + latestRevision[strings.LastIndex(latestRevision, "--")+2:]
	if ingressSuffix == "--"+latestRevision {
		ingressSuffix = "--" + latestRevision
	}

	// Construct agent registration URI
	workspaceName := fmt.Sprintf("%s@%s@AML", projectAIFoundryName, projectName)
	apiPath := fmt.Sprintf("/agents/v2.0/subscriptions/%s/resourceGroups/%s/providers/Microsoft.MachineLearningServices/workspaces/%s/agents/%s/versions?api-version=2025-05-15-preview",
		projectSubscriptionID, projectResourceGroup, workspaceName, agentName)

	uri := ""
	if aiFoundryProjectEndpoint != "" {
		uri = aiFoundryProjectEndpoint + apiPath
	} else {
		uri = fmt.Sprintf("https://%s.api.azureml.ms%s", aiFoundryRegion, apiPath)
	}

	// Register agent with retry logic
	agentVersion := registerAgent(uri, token, resourceID, ingressSuffix)

	// Test authentication and agent
	if agentVersion != "" {
		testUnauthenticatedAccess(acaEndpoint)
		testDataPlane(aiFoundryProjectEndpoint, token, agentName, agentVersion)
	}

	// Print Azure Portal link
	fmt.Println()
	fmt.Println("======================================")
	fmt.Println("Azure Portal")
	fmt.Println("======================================")
	fmt.Printf("https://portal.azure.com/#@/resource%s\n", resourceID)

	fmt.Println()
	fmt.Println("✓ Post-deployment completed successfully")

	return nil
}

// validateRequired checks if a required variable is set
func validateRequired(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s not set", name)
	}
	return nil
}

// assignAzureAIRole assigns the Azure AI User role to the container app identity using Azure SDK
func assignAzureAIRole(ctx context.Context, cred *azidentity.AzureDeveloperCLICredential, principalID, scope string) error {
	fmt.Println()
	fmt.Println("======================================")
	fmt.Println("Assigning Azure AI Access Permissions")
	fmt.Println("======================================")

	roleDefinitionID := "53ca6127-db72-4b80-b1b0-d745d6d5456d" // Azure AI User

	fmt.Println("Assigning 'Azure AI User' role to Container App identity...")
	fmt.Printf("Principal ID: %s\n", principalID)
	fmt.Printf("Scope: %s\n", scope)
	fmt.Println()

	// Extract subscription ID from scope
	// Scope format: /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/...
	parts := strings.Split(scope, "/")
	var subscriptionID string
	for i, part := range parts {
		if part == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
			break
		}
	}
	if subscriptionID == "" {
		return fmt.Errorf("could not extract subscription ID from scope: %s", scope)
	}

	// Create role assignments client
	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create role assignments client: %w", err)
	}

	// Construct full role definition ID
	fullRoleDefinitionID := fmt.Sprintf("%s/providers/Microsoft.Authorization/roleDefinitions/%s", scope, roleDefinitionID)

	// Check if the role assignment already exists
	// Use atScope() to list all role assignments at this scope, then filter in code
	pager := client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: to.Ptr("atScope()"),
	})

	assignmentExists := false
	var existingAssignmentId string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list role assignments: %w", err)
		}
		for _, assignment := range page.Value {
			if assignment.Properties != nil && assignment.Properties.PrincipalID != nil && assignment.Properties.RoleDefinitionID != nil {
				// Filter by both principal ID and role definition ID
				if *assignment.Properties.PrincipalID == principalID && *assignment.Properties.RoleDefinitionID == fullRoleDefinitionID {
					assignmentExists = true
					if assignment.Name != nil {
						existingAssignmentId = *assignment.Name
					}
					break
				}
			}
		}
		if assignmentExists {
			break
		}
	}

	if assignmentExists {
		fmt.Println("✓ Role assignment already exists")
		if existingAssignmentId != "" {
			fmt.Printf("  Assignment ID: %s\n", existingAssignmentId)
		}
	} else {
		// Generate a unique name for the role assignment
		roleAssignmentName := uuid.New().String()
		// Create role assignment
		parameters := armauthorization.RoleAssignmentCreateParameters{
			Properties: &armauthorization.RoleAssignmentProperties{
				RoleDefinitionID: to.Ptr(fullRoleDefinitionID),
				PrincipalID:      to.Ptr(principalID),
			},
		}

		resp, err := client.Create(ctx, scope, roleAssignmentName, parameters, nil)
		if err != nil {
			// Check if the error is due to role assignment already existing (409 Conflict)
			if strings.Contains(err.Error(), "RoleAssignmentExists") || strings.Contains(err.Error(), "409") {
				fmt.Println("✓ Role assignment already exists (detected during creation)")
				assignmentExists = true // Mark as existing so we skip waiting
			} else {
				return fmt.Errorf("failed to create role assignment: %w", err)
			}
		} else {
			fmt.Println("✓ Successfully assigned 'Azure AI User' role")

			if resp.Name != nil {
				fmt.Printf("  Assignment ID: %s\n", *resp.Name)
			}
		}
	}

	// Only wait for propagation if we just created a new assignment
	if !assignmentExists {
		fmt.Println()
		fmt.Println("⏳ Waiting 30 seconds for RBAC propagation...")
		time.Sleep(30 * time.Second)

		// Validate the assignment
		fmt.Println("Validating role assignment...")
		pager = client.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
			Filter: to.Ptr("atScope()"),
		})

		validated := false
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Warning: Could not validate role assignment. It may still be propagating.")
				break
			}

			for _, assignment := range page.Value {
				if assignment.Properties != nil && assignment.Properties.RoleDefinitionID != nil {
					if strings.Contains(*assignment.Properties.RoleDefinitionID, roleDefinitionID) {
						fmt.Println("✓ Role assignment validated successfully")
						fmt.Printf("  Role: Azure AI User\n")
						validated = true
						break
					}
				}
			}
			if validated {
				break
			}
		}

		if !validated {
			fmt.Fprintln(os.Stderr, "Warning: Could not validate role assignment. It may still be propagating.")
		}
	}

	return nil
}

// deactivateHelloWorldRevision deactivates the hello-world placeholder revision using Azure SDK
func deactivateHelloWorldRevision(ctx context.Context, cred *azidentity.AzureDeveloperCLICredential, resourceID string) error {
	fmt.Println()
	fmt.Println("======================================")
	fmt.Println("Deactivating Hello-World Revision")
	fmt.Println("======================================")
	fmt.Println("ℹ️  Azure Container Apps requires an image during provision, but with remote Docker")
	fmt.Println("   build, the app image doesn't exist yet. A hello-world placeholder image is used")
	fmt.Println("   during 'azd provision', then replaced with your app image during 'azd deploy'.")
	fmt.Println("   Now that your app is deployed, we'll deactivate the placeholder revision.")
	fmt.Println()

	// Parse resource ID
	parsedResource, err := arm.ParseResourceID(resourceID)
	if err != nil {
		return fmt.Errorf("failed to parse resource ID: %w", err)
	}

	subscription := parsedResource.SubscriptionID
	resourceGroup := parsedResource.ResourceGroupName
	appName := parsedResource.Name

	if subscription == "" || resourceGroup == "" || appName == "" {
		return fmt.Errorf("could not parse subscription, resource group or app name from resource ID: %s", resourceID)
	}

	// Create container apps revisions client
	revisionsClient, err := armappcontainers.NewContainerAppsRevisionsClient(subscription, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create revisions client: %w", err)
	}

	// List all revisions
	pager := revisionsClient.NewListRevisionsPager(resourceGroup, appName, nil)

	var helloWorldRevision *armappcontainers.Revision
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list revisions: %w", err)
		}

		for _, revision := range page.Value {
			// Check if this is a hello-world revision
			if revision.Properties != nil &&
				revision.Properties.Template != nil &&
				revision.Properties.Template.Containers != nil &&
				len(revision.Properties.Template.Containers) > 0 {

				container := revision.Properties.Template.Containers[0]
				if container.Image != nil &&
					strings.Contains(*container.Image, "containerapps-helloworld") &&
					revision.Name != nil &&
					!strings.Contains(*revision.Name, "--azd-") {
					helloWorldRevision = revision
					break
				}
			}
		}
		if helloWorldRevision != nil {
			break
		}
	}

	if helloWorldRevision == nil {
		fmt.Println("No hello-world revision found (already removed or using custom image)")
		return nil
	}

	if helloWorldRevision.Name == nil {
		return fmt.Errorf("revision name is nil")
	}

	revisionName := *helloWorldRevision.Name
	fmt.Printf("Found hello-world revision: %s\n", revisionName)

	if helloWorldRevision.Properties != nil &&
		helloWorldRevision.Properties.Template != nil &&
		helloWorldRevision.Properties.Template.Containers != nil &&
		len(helloWorldRevision.Properties.Template.Containers) > 0 {
		if img := helloWorldRevision.Properties.Template.Containers[0].Image; img != nil {
			fmt.Printf("Image: %s\n", *img)
		}
	}

	// Double-check before deactivating
	if helloWorldRevision.Properties != nil &&
		helloWorldRevision.Properties.Template != nil &&
		helloWorldRevision.Properties.Template.Containers != nil &&
		len(helloWorldRevision.Properties.Template.Containers) > 0 {

		container := helloWorldRevision.Properties.Template.Containers[0]
		if container.Image == nil || !strings.Contains(*container.Image, "containerapps-helloworld") {
			fmt.Fprintln(os.Stderr, "Warning: Revision does not have hello-world image, skipping for safety")
			return nil
		}
	}

	if strings.Contains(revisionName, "--azd-") {
		fmt.Fprintln(os.Stderr, "Warning: Revision name contains '--azd-' pattern, skipping for safety")
		return nil
	}

	// Check if already inactive
	if helloWorldRevision.Properties != nil &&
		helloWorldRevision.Properties.Active != nil &&
		!*helloWorldRevision.Properties.Active {
		fmt.Println("Revision is already inactive")
		return nil
	}

	// Deactivate the revision
	fmt.Println("Deactivating revision...")
	_, err = revisionsClient.DeactivateRevision(ctx, resourceGroup, appName, revisionName, nil)
	if err != nil {
		return fmt.Errorf("failed to deactivate revision: %w", err)
	}

	fmt.Println("✓ Hello-world revision deactivated successfully")
	return nil
}

// verifyAuthConfiguration verifies the authentication configuration using Azure SDK
func verifyAuthConfiguration(ctx context.Context, cred *azidentity.AzureDeveloperCLICredential, resourceID string) error {
	fmt.Println()
	fmt.Println("======================================")
	fmt.Println("Verifying Authentication Configuration")
	fmt.Println("======================================")

	// Parse resource ID
	parsedResource, err := arm.ParseResourceID(resourceID)
	if err != nil {
		return fmt.Errorf("failed to parse resource ID: %w", err)
	}

	subscription := parsedResource.SubscriptionID
	resourceGroup := parsedResource.ResourceGroupName
	appName := parsedResource.Name

	if subscription == "" || resourceGroup == "" || appName == "" {
		return fmt.Errorf("could not parse subscription, resource group or app name from resource ID: %s", resourceID)
	}

	// Create container apps auth configs client
	authClient, err := armappcontainers.NewContainerAppsAuthConfigsClient(subscription, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create auth configs client: %w", err)
	}

	// Get the auth config (named "current" by convention for the active config)
	authConfig, err := authClient.Get(ctx, resourceGroup, appName, "current", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Warning: No authentication configuration found")
		return nil
	}

	// Check if Azure AD authentication is configured
	if authConfig.Properties == nil ||
		authConfig.Properties.Platform == nil ||
		authConfig.Properties.Platform.Enabled == nil {
		fmt.Fprintln(os.Stderr, "Warning: No authentication configuration found")
		return nil
	}

	if !*authConfig.Properties.Platform.Enabled {
		fmt.Fprintln(os.Stderr, "Warning: Authentication is not enabled")
		return nil
	}

	// Check for Azure AD identity provider
	if authConfig.Properties.IdentityProviders != nil &&
		authConfig.Properties.IdentityProviders.AzureActiveDirectory != nil &&
		authConfig.Properties.IdentityProviders.AzureActiveDirectory.Enabled != nil &&
		*authConfig.Properties.IdentityProviders.AzureActiveDirectory.Enabled {

		fmt.Println("✓ Azure AD authentication enabled")

		aadConfig := authConfig.Properties.IdentityProviders.AzureActiveDirectory
		if aadConfig.Registration != nil && aadConfig.Registration.ClientID != nil {
			fmt.Printf("  Client ID: %s\n", *aadConfig.Registration.ClientID)
		}

		if authConfig.Properties.GlobalValidation != nil &&
			authConfig.Properties.GlobalValidation.UnauthenticatedClientAction != nil {
			fmt.Printf("  Unauthenticated Action: %s\n", string(*authConfig.Properties.GlobalValidation.UnauthenticatedClientAction))
		}
	} else {
		fmt.Fprintln(os.Stderr, "Warning: Azure AD authentication is not configured")
	}

	return nil
}

// getContainerAppEndpoint retrieves the Container App FQDN using Azure SDK
func getContainerAppEndpoint(ctx context.Context, cred *azidentity.AzureDeveloperCLICredential, resourceID, subscriptionID string) (string, error) {
	// Parse resource ID
	parsedResource, err := arm.ParseResourceID(resourceID)
	if err != nil {
		return "", fmt.Errorf("failed to parse resource ID: %w", err)
	}

	resourceGroup := parsedResource.ResourceGroupName
	appName := parsedResource.Name

	if resourceGroup == "" || appName == "" {
		return "", fmt.Errorf("could not parse resource group or app name from resource ID: %s", resourceID)
	}

	// Create container apps client
	client, err := armappcontainers.NewContainerAppsClient(subscriptionID, cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create container apps client: %w", err)
	}

	// Get the container app
	containerApp, err := client.Get(ctx, resourceGroup, appName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get container app: %w", err)
	}

	// Extract FQDN
	if containerApp.Properties == nil ||
		containerApp.Properties.Configuration == nil ||
		containerApp.Properties.Configuration.Ingress == nil ||
		containerApp.Properties.Configuration.Ingress.Fqdn == nil {
		return "", fmt.Errorf("container app does not have an ingress FQDN")
	}

	fqdn := *containerApp.Properties.Configuration.Ingress.Fqdn
	if fqdn == "" {
		return "", fmt.Errorf("FQDN is empty")
	}

	return "https://" + fqdn, nil
}

// getAIFoundryProjectEndpoint retrieves the AI Foundry Project API endpoint using Azure SDK
func getAIFoundryProjectEndpoint(ctx context.Context, cred *azidentity.AzureDeveloperCLICredential, resourceID, subscriptionID string) (string, error) {
	// Create resources client
	client, err := armresources.NewClient(subscriptionID, cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create resources client: %w", err)
	}

	// Get the resource
	// API version for AI Foundry projects (Machine Learning workspaces)
	apiVersion := "2025-06-01"
	resp, err := client.GetByID(ctx, resourceID, apiVersion, nil)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve resource: %w", err)
	}

	// Extract AI Foundry API endpoint
	if resp.Properties == nil {
		return "", fmt.Errorf("resource does not have properties")
	}

	// Parse properties as a map to access nested endpoints
	propsMap, ok := resp.Properties.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("failed to parse resource properties")
	}

	endpoints, ok := propsMap["endpoints"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("resource does not have endpoints")
	}

	aiFoundryAPI, ok := endpoints["AI Foundry API"].(string)
	if !ok || aiFoundryAPI == "" {
		return "", fmt.Errorf("AI Foundry API endpoint not found")
	}

	return aiFoundryAPI, nil
}

// getAccessToken retrieves an access token for the specified resource using Azure SDK
func getAccessToken(ctx context.Context, cred *azidentity.AzureDeveloperCLICredential, resource string) (string, error) {
	// Get access token for the specified resource
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{resource + "/.default"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get access token: %w", err)
	}

	return token.Token, nil
}

// getLatestRevisionName retrieves the latest revision name for a Container App using Azure SDK
func getLatestRevisionName(ctx context.Context, cred *azidentity.AzureDeveloperCLICredential, resourceID, subscriptionID string) (string, error) {
	// Parse resource ID
	parsedResource, err := arm.ParseResourceID(resourceID)
	if err != nil {
		return "", fmt.Errorf("failed to parse resource ID: %w", err)
	}

	resourceGroup := parsedResource.ResourceGroupName
	appName := parsedResource.Name

	if resourceGroup == "" || appName == "" {
		return "", fmt.Errorf("could not parse resource group or app name from resource ID: %s", resourceID)
	}

	// Create container apps client
	client, err := armappcontainers.NewContainerAppsClient(subscriptionID, cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create container apps client: %w", err)
	}

	// Get the container app
	containerApp, err := client.Get(ctx, resourceGroup, appName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get container app: %w", err)
	}

	// Extract latest revision name
	if containerApp.Properties == nil || containerApp.Properties.LatestRevisionName == nil {
		return "", fmt.Errorf("container app does not have a latest revision name")
	}

	latestRevision := *containerApp.Properties.LatestRevisionName
	if latestRevision == "" {
		return "", fmt.Errorf("latest revision name is empty")
	}

	return latestRevision, nil
}

// registerAgent registers the agent with AI Foundry
func registerAgent(uri, token, resourceID, ingressSuffix string) string {
	fmt.Println()
	fmt.Println("======================================")
	fmt.Println("Registering Agent Version")
	fmt.Println("======================================")
	fmt.Printf("POST URL: %s\n", uri)

	payload := AgentRegistrationPayload{
		Description: "Test agent version description",
		Definition: AgentDefinition{
			Kind: "container_app",
			ContainerProtocolVersions: []ContainerProtocolVersion{
				{Protocol: "responses", Version: "v1"},
			},
			ContainerAppResourceID: resourceID,
			IngressSubdomainSuffix: ingressSuffix,
		},
	}

	payloadBytes, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Println("Request Body:")
	fmt.Println(string(payloadBytes))

	maxRetries := 10
	retryDelay := 60 * time.Second
	agentVersion := ""

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			fmt.Printf("Retry attempt %d of %d...\n", attempt, maxRetries-1)
		}

		client := &http.Client{}
		req, err := http.NewRequest("POST", uri, bytes.NewBuffer(payloadBytes))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
			continue
		}

		req.Header.Set("accept", "application/json")
		req.Header.Set("authorization", "Bearer "+token)
		req.Header.Set("content-type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error making request: %v\n", err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		fmt.Printf("Response Status: %d\n", resp.StatusCode)
		fmt.Println("Response Body:")
		fmt.Println(string(body))
		fmt.Println()

		if resp.StatusCode == 200 || resp.StatusCode == 201 {
			fmt.Println("✓ Agent registered successfully")

			var agentResp AgentResponse
			if err := json.Unmarshal(body, &agentResp); err == nil {
				agentVersion = agentResp.Version
				fmt.Printf("Agent version: %s\n", agentVersion)
			}
			break
		} else if resp.StatusCode == 500 && attempt < maxRetries-1 {
			fmt.Println("Warning: Registration failed with 500 error (permission propagation delay)")
			fmt.Printf("Waiting %v before retry...\n", retryDelay)
			time.Sleep(retryDelay)
		} else {
			fmt.Fprintln(os.Stderr, "Error: Registration failed")
			if resp.StatusCode != 500 {
				break
			}
		}
	}

	return agentVersion
}

// testUnauthenticatedAccess tests unauthenticated access (should return 401)
func testUnauthenticatedAccess(acaEndpoint string) {
	fmt.Println()
	fmt.Println("======================================")
	fmt.Println("Testing Unauthenticated Access")
	fmt.Println("======================================")

	uri := acaEndpoint + "/responses"
	payload := []byte(`{"input": "test"}`)

	fmt.Printf("POST URL: %s\n", uri)
	fmt.Printf("Request Body: %s\n", string(payload))

	client := &http.Client{}
	req, err := http.NewRequest("POST", uri, bytes.NewBuffer(payload))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		return
	}

	req.Header.Set("content-type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error making request: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	fmt.Printf("Response Status: %d\n", resp.StatusCode)
	fmt.Printf("Response Body: %s\n", string(body))
	fmt.Println()

	if resp.StatusCode == 401 {
		fmt.Println("✓ Authentication enforced (got 401)")
	} else {
		fmt.Fprintf(os.Stderr, "Warning: Expected 401, got %d\n", resp.StatusCode)
	}
}

// testDataPlane tests the agent data plane with authenticated request
func testDataPlane(endpoint, token, agentName, agentVersion string) {
	fmt.Println()
	fmt.Println("======================================")
	fmt.Println("Testing Agent Data Plane")
	fmt.Println("======================================")

	payload := DataPlanePayload{
		Agent: AgentReference{
			Type:    "agent_reference",
			Name:    agentName,
			Version: agentVersion,
		},
		Input:  "Tell me a joke.",
		Stream: false,
	}

	payloadBytes, _ := json.MarshalIndent(payload, "", "  ")
	uri := endpoint + "/openai/responses?api-version=2025-05-15-preview"

	fmt.Printf("POST URL: %s\n", uri)
	fmt.Println("Request Body:")
	fmt.Println(string(payloadBytes))

	client := &http.Client{}
	req, err := http.NewRequest("POST", uri, bytes.NewBuffer(payloadBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		return
	}

	req.Header.Set("accept", "application/json")
	req.Header.Set("authorization", "Bearer "+token)
	req.Header.Set("content-type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error making request: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	fmt.Printf("Response Status: %d\n", resp.StatusCode)
	fmt.Println("Response Body:")
	fmt.Println(string(body))
	fmt.Println()

	if resp.StatusCode == 200 || resp.StatusCode == 201 {
		fmt.Println("✓ Agent responded successfully")
		fmt.Println("Agent Output:")

		var dpResp DataPlaneResponse
		if err := json.Unmarshal(body, &dpResp); err == nil {
			fmt.Println(dpResp.Output)
		}
	} else {
		fmt.Fprintln(os.Stderr, "Warning: Data plane call failed")
	}
}
