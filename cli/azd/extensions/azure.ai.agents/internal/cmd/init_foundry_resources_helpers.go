// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/azure"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armcognitiveservices "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FoundryProjectInfo holds information about a discovered or parsed Foundry project.
// This is the unified type used by both init flows.
type FoundryProjectInfo struct {
	SubscriptionId    string
	ResourceGroupName string
	AccountName       string
	ProjectName       string
	Location          string // may be empty when parsed from resource ID alone
	ResourceId        string // full ARM resource ID
}

// FoundryDeploymentInfo holds information about an existing model deployment in a Foundry project.
type FoundryDeploymentInfo struct {
	Name        string
	ModelName   string
	ModelFormat string
	Version     string
	SkuName     string
	SkuCapacity int
}

const foundryProjectResourceType = "Microsoft.CognitiveServices/accounts/projects"

// setEnvValue sets a single environment variable in the azd environment.
func setEnvValue(ctx context.Context, azdClient *azdext.AzdClient, envName, key, value string) error {
	_, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("failed to set environment variable %s: %w", key, err)
	}

	return nil
}

// projectResourceIdRegex is the precompiled regex for parsing Foundry project ARM resource IDs.
var projectResourceIdRegex = regexp.MustCompile(
	`^/subscriptions/([^/]+)/resourceGroups/([^/]+)/providers/Microsoft\.CognitiveServices/accounts/([^/]+)/projects/([^/]+)$`,
)

// extractProjectDetails parses an ARM resource ID into a FoundryProjectInfo.
func extractProjectDetails(projectResourceId string) (*FoundryProjectInfo, error) {
	matches := projectResourceIdRegex.FindStringSubmatch(projectResourceId)
	if matches == nil || len(matches) != 5 {
		return nil, fmt.Errorf(
			"the given Microsoft Foundry project ID does not match expected format: " +
				"/subscriptions/[SUBSCRIPTION_ID]/resourceGroups/[RESOURCE_GROUP]/providers/" +
				"Microsoft.CognitiveServices/accounts/[ACCOUNT_NAME]/projects/[PROJECT_NAME]",
		)
	}

	return &FoundryProjectInfo{
		SubscriptionId:    matches[1],
		ResourceGroupName: matches[2],
		AccountName:       matches[3],
		ProjectName:       matches[4],
		ResourceId:        projectResourceId,
	}, nil
}

// extractSubscriptionId extracts the subscription ID from an Azure resource ID.
func extractSubscriptionId(resourceId string) string {
	parts := strings.Split(resourceId, "/")
	for i, part := range parts {
		if strings.EqualFold(part, "subscriptions") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// extractResourceGroup extracts the resource group name from an Azure resource ID.
func extractResourceGroup(resourceId string) string {
	parts := strings.Split(resourceId, "/")
	for i, part := range parts {
		if strings.EqualFold(part, "resourceGroups") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func foundryProjectInfoFromResource(resource *armresources.GenericResourceExpanded) (*FoundryProjectInfo, bool) {
	if resource == nil || resource.ID == nil || *resource.ID == "" {
		return nil, false
	}

	project, err := extractProjectDetails(*resource.ID)
	if err != nil {
		return nil, false
	}

	if resource.Location != nil {
		project.Location = *resource.Location
	}

	return project, true
}

func updateFoundryProjectInfo(project *FoundryProjectInfo, resource *armcognitiveservices.Project) {
	if project == nil || resource == nil {
		return
	}

	if resource.ID != nil && *resource.ID != "" {
		project.ResourceId = *resource.ID
	}

	if resource.Name != nil && *resource.Name != "" {
		if idx := strings.LastIndex(*resource.Name, "/"); idx != -1 {
			project.ProjectName = (*resource.Name)[idx+1:]
		} else {
			project.ProjectName = *resource.Name
		}
	}

	if resource.Location != nil {
		project.Location = *resource.Location
	}
}

// listFoundryProjects enumerates all Foundry projects in a subscription by listing
// subscription resources filtered to Foundry projects.
func listFoundryProjects(
	ctx context.Context,
	credential azcore.TokenCredential,
	subscriptionId string,
) ([]FoundryProjectInfo, error) {
	resourcesClient, err := armresources.NewClient(subscriptionId, credential, azure.NewArmClientOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to create resources client: %w", err)
	}

	var results []FoundryProjectInfo

	pager := resourcesClient.NewListPager(&armresources.ClientListOptions{
		Filter: new(fmt.Sprintf("resourceType eq '%s'", foundryProjectResourceType)),
	})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list Foundry projects: %w", err)
		}

		for _, resource := range page.Value {
			if project, ok := foundryProjectInfoFromResource(resource); ok {
				results = append(results, *project)
			}
		}
	}

	return results, nil
}

func getFoundryProject(
	ctx context.Context,
	credential azcore.TokenCredential,
	subscriptionId string,
	projectResourceId string,
) (*FoundryProjectInfo, error) {
	project, err := extractProjectDetails(projectResourceId)
	if err != nil {
		return nil, err
	}

	if !strings.EqualFold(project.SubscriptionId, subscriptionId) {
		return nil, fmt.Errorf("provided project resource ID does not match the selected subscription")
	}

	projectsClient, err := armcognitiveservices.NewProjectsClient(project.SubscriptionId, credential, azure.NewArmClientOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to create projects client: %w", err)
	}

	response, err := projectsClient.Get(ctx, project.ResourceGroupName, project.AccountName, project.ProjectName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get Foundry project: %w", err)
	}

	updateFoundryProjectInfo(project, &response.Project)

	return project, nil
}

// listProjectDeployments lists all model deployments in a Foundry account.
func listProjectDeployments(
	ctx context.Context,
	credential azcore.TokenCredential,
	subscriptionId, resourceGroup, accountName string,
) ([]FoundryDeploymentInfo, error) {
	deploymentsClient, err := armcognitiveservices.NewDeploymentsClient(subscriptionId, credential, azure.NewArmClientOptions())
	if err != nil {
		return nil, fmt.Errorf("failed to create deployments client: %w", err)
	}

	pager := deploymentsClient.NewListPager(resourceGroup, accountName, nil)
	var results []FoundryDeploymentInfo
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list deployments: %w", err)
		}
		for _, deployment := range page.Value {
			info := FoundryDeploymentInfo{}
			if deployment.Name != nil {
				info.Name = *deployment.Name
			}
			if deployment.Properties != nil && deployment.Properties.Model != nil {
				m := deployment.Properties.Model
				if m.Name != nil {
					info.ModelName = *m.Name
				}
				if m.Format != nil {
					info.ModelFormat = *m.Format
				}
				if m.Version != nil {
					info.Version = *m.Version
				}
			}
			if deployment.SKU != nil {
				if deployment.SKU.Name != nil {
					info.SkuName = *deployment.SKU.Name
				}
				if deployment.SKU.Capacity != nil {
					info.SkuCapacity = int(*deployment.SKU.Capacity)
				}
			}
			results = append(results, info)
		}
	}
	return results, nil
}

// lookupAcrResourceId finds the ARM resource ID for an ACR given its login server endpoint.
func lookupAcrResourceId(
	ctx context.Context,
	credential azcore.TokenCredential,
	subscriptionId string,
	loginServer string,
) (string, error) {
	parts := strings.Split(loginServer, ".")
	if len(parts) < 2 || parts[0] == "" {
		return "", fmt.Errorf("invalid login server format: %q, expected e.g. %q", loginServer, "registry.azurecr.io")
	}
	registryName := parts[0]

	client, err := armcontainerregistry.NewRegistriesClient(subscriptionId, credential, azure.NewArmClientOptions())
	if err != nil {
		return "", fmt.Errorf("failed to create container registry client: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to list registries: %w", err)
		}
		for _, registry := range page.Value {
			if registry.Name != nil && strings.EqualFold(*registry.Name, registryName) {
				if registry.ID != nil {
					return *registry.ID, nil
				}
			}
		}
	}

	return "", fmt.Errorf("container registry '%s' not found in subscription", registryName)
}

// configureFoundryProjectEnv sets all Foundry project environment variables and discovers
// ACR and AppInsights connections. This is the shared implementation used by both init flows.
func configureFoundryProjectEnv(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	credential azcore.TokenCredential,
	envName string,
	project FoundryProjectInfo,
	subscriptionId string,
) error {
	resourceId := project.ResourceId
	if resourceId == "" {
		resourceId = fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s/projects/%s",
			project.SubscriptionId, project.ResourceGroupName, project.AccountName, project.ProjectName)
	}

	if err := setEnvValue(ctx, azdClient, envName, "AZURE_AI_PROJECT_ID", resourceId); err != nil {
		return err
	}

	if err := setEnvValue(ctx, azdClient, envName, "AZURE_RESOURCE_GROUP", project.ResourceGroupName); err != nil {
		return err
	}

	if err := setEnvValue(ctx, azdClient, envName, "AZURE_AI_ACCOUNT_NAME", project.AccountName); err != nil {
		return err
	}

	if err := setEnvValue(ctx, azdClient, envName, "AZURE_AI_PROJECT_NAME", project.ProjectName); err != nil {
		return err
	}

	aiFoundryEndpoint := fmt.Sprintf("https://%s.services.ai.azure.com/api/projects/%s", project.AccountName, project.ProjectName)
	if err := setEnvValue(ctx, azdClient, envName, "AZURE_AI_PROJECT_ENDPOINT", aiFoundryEndpoint); err != nil {
		return err
	}

	aoaiEndpoint := fmt.Sprintf("https://%s.openai.azure.com/", project.AccountName)
	if err := setEnvValue(ctx, azdClient, envName, "AZURE_OPENAI_ENDPOINT", aoaiEndpoint); err != nil {
		return err
	}

	// Discover and configure connections (ACR, AppInsights)
	foundryClient, err := azure.NewFoundryProjectsClient(project.AccountName, project.ProjectName, credential)
	if err != nil {
		return fmt.Errorf("creating Foundry client: %w", err)
	}
	connections, err := foundryClient.GetAllConnections(ctx)
	if err != nil {
		fmt.Printf("Could not get Microsoft Foundry project connections: %v. Please set connection environment variables manually.\n", err)
		return nil
	}

	var acrConnections []azure.Connection
	var appInsightsConnections []azure.Connection
	for _, conn := range connections {
		switch conn.Type {
		case azure.ConnectionTypeContainerRegistry:
			acrConnections = append(acrConnections, conn)
		case azure.ConnectionTypeAppInsights:
			connWithCreds, err := foundryClient.GetConnectionWithCredentials(ctx, conn.Name)
			if err != nil {
				fmt.Printf("Could not get full details for Application Insights connection '%s': %v\n", conn.Name, err)
				continue
			}
			if connWithCreds != nil {
				conn = *connWithCreds
			}
			appInsightsConnections = append(appInsightsConnections, conn)
		}
	}

	if err := configureAcrConnection(ctx, azdClient, credential, envName, subscriptionId, acrConnections); err != nil {
		return err
	}

	if err := configureAppInsightsConnection(ctx, azdClient, envName, appInsightsConnections); err != nil {
		return err
	}

	return nil
}

// configureAcrConnection handles ACR connection selection and env var setting.
func configureAcrConnection(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	credential azcore.TokenCredential,
	envName string,
	subscriptionId string,
	acrConnections []azure.Connection,
) error {
	if len(acrConnections) == 0 {
		fmt.Println("\n" +
			"An Azure Container Registry (ACR) is required\n\n" +
			"Foundry Hosted Agents need an Azure Container Registry to store container images before deployment.\n\n" +
			"You can:\n" +
			"  • Use an existing ACR\n" +
			"  • Or create a new one from the template during 'azd up'\n\n" +
			"Learn more: aka.ms/azdaiagent/docs")

		resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Enter your ACR login server (e.g., myregistry.azurecr.io), or leave blank to create a new one",
				IgnoreHintKeys: true,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for ACR endpoint: %w", err)
		}

		if resp.Value != "" {
			resourceId, err := lookupAcrResourceId(ctx, credential, subscriptionId, resp.Value)
			if err != nil {
				return fmt.Errorf("failed to lookup ACR resource ID: %w", err)
			}

			if err := setEnvValue(ctx, azdClient, envName, "AZURE_CONTAINER_REGISTRY_ENDPOINT", resp.Value); err != nil {
				return err
			}
			if err := setEnvValue(ctx, azdClient, envName, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID", resourceId); err != nil {
				return err
			}
		}
		return nil
	}

	var selectedConnection *azure.Connection

	if len(acrConnections) == 1 {
		selectedConnection = &acrConnections[0]
		fmt.Printf("Using container registry connection: %s (%s)\n", selectedConnection.Name, selectedConnection.Target)
	} else {
		fmt.Printf("Found %d container registry connections:\n", len(acrConnections))

		choices := make([]*azdext.SelectChoice, len(acrConnections))
		for i, conn := range acrConnections {
			choices[i] = &azdext.SelectChoice{
				Label: conn.Name,
				Value: fmt.Sprintf("%d", i),
			}
		}

		defaultIndex := int32(0)
		selectResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "Select a container registry connection to use for this agent",
				Choices:       choices,
				SelectedIndex: &defaultIndex,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to prompt for connection selection: %w", err)
		}
		selectedConnection = &acrConnections[int(*selectResp.Value)]
	}

	if err := setEnvValue(ctx, azdClient, envName, "AZURE_AI_PROJECT_ACR_CONNECTION_NAME", selectedConnection.Name); err != nil {
		return err
	}
	if err := setEnvValue(ctx, azdClient, envName, "AZURE_CONTAINER_REGISTRY_ENDPOINT", selectedConnection.Target); err != nil {
		return err
	}

	return nil
}

// configureAppInsightsConnection handles AppInsights connection selection and env var setting.
func configureAppInsightsConnection(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	appInsightsConnections []azure.Connection,
) error {
	if len(appInsightsConnections) == 0 {
		fmt.Println("\n" +
			"Application Insights (optional)\n\n" +
			"Enable telemetry to collect logs, traces, and diagnostics for this agent.\n\n" +
			"You can:\n" +
			"  • Use an existing Application Insights resource\n" +
			"  • Or create a new one during 'azd up'\n\n" +
			"Docs: aka.ms/azdaiagent/docs")

		resourceIdResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Enter your Application Insights resource ID, or leave blank to create a new one",
				IgnoreHintKeys: true,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for Application Insights resource ID: %w", err)
		}

		if resourceIdResp.Value != "" {
			if err := setEnvValue(ctx, azdClient, envName, "APPLICATIONINSIGHTS_RESOURCE_ID", resourceIdResp.Value); err != nil {
				return err
			}

			connStrResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
				Options: &azdext.PromptOptions{
					Message:        "Enter your Application Insights connection string",
					IgnoreHintKeys: true,
				},
			})
			if err != nil {
				return fmt.Errorf("prompting for Application Insights connection string: %w", err)
			}

			if connStrResp.Value != "" {
				if err := setEnvValue(ctx, azdClient, envName, "APPLICATIONINSIGHTS_CONNECTION_STRING", connStrResp.Value); err != nil {
					return err
				}
			}
		}
		return nil
	}

	var selectedConnection *azure.Connection

	if len(appInsightsConnections) == 1 {
		selectedConnection = &appInsightsConnections[0]
		fmt.Printf("Using Application Insights connection: %s (%s)\n", selectedConnection.Name, selectedConnection.Target)
	} else {
		fmt.Printf("Found %d Application Insights connections:\n", len(appInsightsConnections))

		choices := make([]*azdext.SelectChoice, len(appInsightsConnections))
		for i, conn := range appInsightsConnections {
			choices[i] = &azdext.SelectChoice{
				Label: conn.Name,
				Value: fmt.Sprintf("%d", i),
			}
		}

		defaultIndex := int32(0)
		selectResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       "Select an Application Insights connection to use for this agent",
				Choices:       choices,
				SelectedIndex: &defaultIndex,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to prompt for connection selection: %w", err)
		}
		selectedConnection = &appInsightsConnections[int(*selectResp.Value)]
	}

	if selectedConnection != nil {
		if err := setEnvValue(ctx, azdClient, envName, "APPLICATIONINSIGHTS_CONNECTION_NAME", selectedConnection.Name); err != nil {
			return err
		}
		if err := setEnvValue(
			ctx, azdClient, envName, "APPLICATIONINSIGHTS_CONNECTION_STRING", selectedConnection.Credentials.Key,
		); err != nil {
			return err
		}
	}

	return nil
}

// --- Shared project/environment/context setup helpers ---

// createNewEnvironment creates a new azd environment with the given name via the azd workflow,
// then fetches and returns it. Both init flows use this same mechanism.
func createNewEnvironment(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
) (*azdext.Environment, error) {
	if envName != "" {
		if existingEnv := getExistingEnvironment(ctx, envName, azdClient); existingEnv != nil {
			return existingEnv, nil
		}
	}

	envArgs := []string{"env", "new"}
	if envName != "" {
		envArgs = append(envArgs, envName)
	}

	workflow := &azdext.Workflow{
		Name: "env new",
		Steps: []*azdext.WorkflowStep{
			{Command: &azdext.WorkflowCommand{Args: envArgs}},
		},
	}

	_, err := azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
		Workflow: workflow,
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("environment creation was cancelled")
		}
		// The workflow may have created the environment on disk before returning
		// an "already exists" error (e.g. a concurrent process raced, or a previous
		// partial run left env files behind). Re-fetch so we can reuse it.
		if envName != "" && status.Code(err) == codes.AlreadyExists {
			if existingEnv := getExistingEnvironment(ctx, envName, azdClient); existingEnv != nil {
				return existingEnv, nil
			}
		}
		return nil, exterrors.Dependency(
			exterrors.CodeEnvironmentCreationFailed,
			fmt.Sprintf("failed to create new azd environment: %s", err),
			"run 'azd env new' manually to create an environment",
		)
	}

	// Re-fetch the environment after creation
	env := getExistingEnvironment(ctx, envName, azdClient)
	if env == nil {
		return nil, exterrors.Dependency(
			exterrors.CodeEnvironmentNotFound,
			"azd environment not found after creation",
			"run 'azd env new' to create an environment and try again",
		)
	}

	return env, nil
}

// loadAzureContext reads the current Azure context values (tenant, subscription, location)
// from the azd environment and returns a populated AzureContext. Missing values are left empty.
func loadAzureContext(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
) (*azdext.AzureContext, error) {
	envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: envName,
	})
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf("failed to get environment values: %s", err),
			"run 'azd env get-values' to verify environment state",
		)
	}

	envValueMap := make(map[string]string)
	for _, value := range envValues.KeyValues {
		envValueMap[value.Key] = value.Value
	}

	return &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			TenantId:       envValueMap["AZURE_TENANT_ID"],
			SubscriptionId: envValueMap["AZURE_SUBSCRIPTION_ID"],
			Location:       envValueMap["AZURE_LOCATION"],
		},
		Resources: []string{},
	}, nil
}

// --- Shared subscription/location helpers ---

// ensureSubscription prompts for a subscription if not already set in the AzureContext.
// If a subscription is already set, looks up the tenant for it. Returns the (possibly refreshed)
// credential scoped to the resolved tenant. Both init flows use this.
func ensureSubscription(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	envName string,
	promptMessage string,
) (azcore.TokenCredential, error) {
	if azureContext.Scope.SubscriptionId == "" {
		fmt.Println(promptMessage)

		subscriptionResponse, err := azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("subscription selection was cancelled")
			}
			return nil, exterrors.FromPrompt(err, "failed to prompt for subscription")
		}

		azureContext.Scope.SubscriptionId = subscriptionResponse.Subscription.Id
		azureContext.Scope.TenantId = subscriptionResponse.Subscription.UserTenantId
	} else {
		tenantResponse, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
			SubscriptionId: azureContext.Scope.SubscriptionId,
		})
		if err != nil {
			return nil, exterrors.Auth(
				exterrors.CodeTenantLookupFailed,
				fmt.Sprintf("failed to lookup tenant for subscription %s: %s", azureContext.Scope.SubscriptionId, err),
				"verify your Azure login with 'azd auth login'",
			)
		}
		azureContext.Scope.TenantId = tenantResponse.TenantId
	}

	// Persist to environment
	if err := setEnvValue(ctx, azdClient, envName, "AZURE_SUBSCRIPTION_ID", azureContext.Scope.SubscriptionId); err != nil {
		return nil, err
	}
	if err := setEnvValue(ctx, azdClient, envName, "AZURE_TENANT_ID", azureContext.Scope.TenantId); err != nil {
		return nil, err
	}

	// Refresh credential with the resolved tenant
	newCredential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   azureContext.Scope.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil, exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("failed to create Azure credential: %s", err),
			"run 'azd auth login' to authenticate",
		)
	}

	return newCredential, nil
}

// ensureLocation prompts for an Azure location if not already set in the AzureContext.
// Both init flows use this.
func ensureLocation(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	envName string,
) error {
	allowedLocations := supportedRegionsForInit()

	if azureContext.Scope.Location != "" && locationAllowed(azureContext.Scope.Location, allowedLocations) {
		return nil
	}
	if azureContext.Scope.Location != "" {
		fmt.Printf("%s", output.WithWarningFormat(
			"The current AZURE_LOCATION '%s' is not supported for this agent setup. Please choose a different location.\n",
			azureContext.Scope.Location,
		))
		azureContext.Scope.Location = ""
	}

	fmt.Println("Select an Azure location. This determines which models are available and where your Foundry project resources will be deployed.")

	locationName, err := promptLocationForInit(ctx, azdClient, azureContext, allowedLocations)
	if err != nil {
		return err
	}

	azureContext.Scope.Location = locationName

	return setEnvValue(ctx, azdClient, envName, "AZURE_LOCATION", azureContext.Scope.Location)
}

// ensureSubscriptionAndLocation ensures both subscription and location are set.
// Returns the (possibly refreshed) credential.
func ensureSubscriptionAndLocation(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	envName string,
	subscriptionMessage string,
) (azcore.TokenCredential, error) {
	newCredential, err := ensureSubscription(ctx, azdClient, azureContext, envName, subscriptionMessage)
	if err != nil {
		return nil, err
	}

	if err := ensureLocation(ctx, azdClient, azureContext, envName); err != nil {
		return nil, err
	}

	return newCredential, nil
}

func normalizeLocationName(location string) string {
	return strings.TrimSpace(strings.ToLower(location))
}

func locationAllowed(location string, allowedLocations []string) bool {
	if len(allowedLocations) == 0 {
		return true
	}

	normalized := normalizeLocationName(location)
	return slices.ContainsFunc(allowedLocations, func(allowed string) bool {
		return normalized == normalizeLocationName(allowed)
	})
}

func promptLocationForInit(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	allowedLocations []string,
) (string, error) {
	locationResponse, err := azdClient.Prompt().PromptLocation(ctx, &azdext.PromptLocationRequest{
		AzureContext:     azureContext,
		AllowedLocations: allowedLocations,
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", exterrors.Cancelled("location selection was cancelled")
		}
		return "", exterrors.FromPrompt(err, "failed to prompt for location")
	}

	return locationResponse.Location.Name, nil
}

func agentModelFilter(locations []string, excludeModelNames []string) *azdext.AiModelFilterOptions {
	filter := &azdext.AiModelFilterOptions{
		Capabilities: []string{agentsV2ModelCapability},
	}

	if len(locations) > 0 {
		filter.Locations = locations
	}

	if len(excludeModelNames) > 0 {
		filter.ExcludeModelNames = excludeModelNames
	}

	return filter
}

// --- Shared model helpers ---

// selectNewModel prompts the user to select a model from the AI catalog, filtered by location.
// Both init flows use this for the "deploy new model" path.
func selectNewModel(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	modelFlag string,
) (*azdext.AiModel, error) {
	defaultModel := "gpt-4.1-mini"
	if modelFlag != "" {
		defaultModel = modelFlag
	}

	promptReq := &azdext.PromptAiModelRequest{
		AzureContext: azureContext,
		Filter:       agentModelFilter([]string{azureContext.Scope.Location}, nil),
		SelectOptions: &azdext.SelectOptions{
			Message: "Select a model",
		},
		Quota: &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 1,
		},
		DefaultValue: defaultModel,
	}

	modelResp, err := azdClient.Prompt().PromptAiModel(ctx, promptReq)
	if err != nil {
		return nil, exterrors.FromPrompt(err, "failed to prompt for model selection")
	}

	return modelResp.Model, nil
}

// resolveModelDeployments resolves model deployments without prompting, returning all candidates
// filtered by location and quota. Both init flows use this for deployment resolution.
func resolveModelDeployments(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	model *azdext.AiModel,
	location string,
) ([]*azdext.AiModelDeployment, error) {
	resolveResp, err := azdClient.Ai().ResolveModelDeployments(ctx, &azdext.ResolveModelDeploymentsRequest{
		AzureContext: azureContext,
		ModelName:    model.Name,
		Options: &azdext.AiModelDeploymentOptions{
			Locations: []string{location},
		},
		Quota: &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 1,
		},
	})
	if err != nil {
		return nil, err
	}

	return resolveResp.Deployments, nil
}

func selectBestModelDeploymentCandidate(
	model *azdext.AiModel,
	deployments []*azdext.AiModelDeployment,
) *azdext.AiModelDeployment {
	if len(deployments) == 0 {
		return nil
	}

	orderedCandidates := make([]*azdext.AiModelDeployment, len(deployments))
	copy(orderedCandidates, deployments)

	defaultVersions := make(map[string]struct{}, len(model.Versions))
	for _, version := range model.Versions {
		if version.IsDefault {
			defaultVersions[version.Version] = struct{}{}
		}
	}

	sortModelDeploymentCandidates(orderedCandidates, defaultVersions)

	for _, candidate := range orderedCandidates {
		capacity, ok := resolveNoPromptCapacity(candidate)
		if !ok {
			continue
		}

		return cloneDeploymentWithCapacity(candidate, capacity)
	}

	return nil
}

func resolveModelDeployment(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	model *azdext.AiModel,
	location string,
) (*azdext.AiModelDeployment, error) {
	deployments, err := resolveModelDeployments(ctx, azdClient, azureContext, model, location)
	if err != nil {
		return nil, exterrors.FromAiService(err, exterrors.CodeModelResolutionFailed)
	}

	if len(deployments) == 0 {
		return nil, exterrors.Dependency(
			exterrors.CodeModelResolutionFailed,
			fmt.Sprintf("no deployment candidates found for model '%s' in location '%s'", model.Name, location),
			"",
		)
	}

	if candidate := selectBestModelDeploymentCandidate(model, deployments); candidate != nil {
		return candidate, nil
	}

	return nil, fmt.Errorf("no deployment candidates found for model '%s' with a valid non-interactive capacity", model.Name)
}

// sortModelDeploymentCandidates sorts deployment candidates by preference:
// default versions first, then by SKU priority, version, SKU name, usage name.
func sortModelDeploymentCandidates(candidates []*azdext.AiModelDeployment, defaultVersions map[string]struct{}) {
	for i := range len(candidates) {
		for j := i + 1; j < len(candidates); j++ {
			a, b := candidates[i], candidates[j]
			_, aDefault := defaultVersions[a.Version]
			_, bDefault := defaultVersions[b.Version]

			swap := false
			if aDefault != bDefault {
				swap = !aDefault
			} else if skuPriority(a.Sku.Name) != skuPriority(b.Sku.Name) {
				swap = skuPriority(a.Sku.Name) > skuPriority(b.Sku.Name)
			} else if a.Version != b.Version {
				swap = a.Version > b.Version
			} else if a.Sku.Name != b.Sku.Name {
				swap = a.Sku.Name > b.Sku.Name
			} else {
				swap = a.Sku.UsageName > b.Sku.UsageName
			}

			if swap {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
}

// --- Shared "select Foundry project" flow ---

// selectFoundryProject lists Foundry projects in the subscription and prompts
// the user to select one. If projectResourceId is provided (from --project-id flag),
// finds the matching project without prompting. Returns nil if user chose
// "Create a new Foundry project" or no projects exist.
// When a project is selected, configures all project-related environment variables.
func selectFoundryProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	credential azcore.TokenCredential,
	azureContext *azdext.AzureContext,
	envName string,
	subscriptionId string,
	projectResourceId string,
) (*FoundryProjectInfo, error) {
	spinnerText := "Searching for Foundry projects in your subscription..."
	if projectResourceId != "" {
		spinnerText = "Getting details on the provided Foundry project..."
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:        spinnerText,
		ClearOnStop: true,
	})
	if err := spinner.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start spinner: %w", err)
	}

	var (
		projects []FoundryProjectInfo
		err      error
	)
	if projectResourceId != "" {
		var project *FoundryProjectInfo
		project, err = getFoundryProject(ctx, credential, subscriptionId, projectResourceId)
		if err == nil {
			projects = append(projects, *project)
		}
	} else {
		projects, err = listFoundryProjects(ctx, credential, subscriptionId)
	}
	if stopErr := spinner.Stop(ctx); stopErr != nil {
		return nil, stopErr
	}
	if err != nil {
		if projectResourceId != "" {
			return nil, err
		}
		return nil, fmt.Errorf("failed to list Foundry projects: %w", err)
	}

	if len(projects) == 0 {
		return nil, nil
	}

	var selectedIdx int32 = -1

	if projectResourceId != "" {
		selectedIdx = 0
	} else {
		// Sort projects alphabetically by account/project name for display
		slices.SortFunc(projects, func(a, b FoundryProjectInfo) int {
			labelA := fmt.Sprintf("%s / %s", a.AccountName, a.ProjectName)
			labelB := fmt.Sprintf("%s / %s", b.AccountName, b.ProjectName)
			return strings.Compare(labelA, labelB)
		})

		// Interactive prompt
		projectChoices := make([]*azdext.SelectChoice, 0, len(projects)+1)
		for i, p := range projects {
			projectChoices = append(projectChoices, &azdext.SelectChoice{
				Label: fmt.Sprintf("%s / %s (%s)", p.AccountName, p.ProjectName, p.Location),
				Value: fmt.Sprintf("%d", i),
			})
		}
		projectChoices = append(projectChoices, &azdext.SelectChoice{
			Label: "Create a new Foundry project",
			Value: "__create_new__",
		})

		projectResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: "Select a Foundry project",
				Choices: projectChoices,
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("project selection was cancelled")
			}
			return nil, fmt.Errorf("failed to prompt for project selection: %w", err)
		}

		selectedIdx = *projectResp.Value
	}

	if selectedIdx < 0 || int(selectedIdx) >= len(projects) {
		// User chose "Create a new Foundry project"
		return nil, nil
	}

	selectedProject := projects[selectedIdx]

	// Set location from the selected project
	azureContext.Scope.Location = selectedProject.Location
	if err := setEnvValue(ctx, azdClient, envName, "AZURE_LOCATION", selectedProject.Location); err != nil {
		return nil, fmt.Errorf("failed to set AZURE_LOCATION: %w", err)
	}

	// Configure all Foundry project environment variables
	if err := configureFoundryProjectEnv(ctx, azdClient, credential, envName, selectedProject, subscriptionId); err != nil {
		return nil, fmt.Errorf("failed to configure Foundry project environment: %w", err)
	}

	return &selectedProject, nil
}

// --- Shared "select model deployment" flow ---

// selectModelDeployment lists model deployments in a Foundry project and prompts
// the user to select one. If modelDeploymentFlag is provided, finds the matching
// deployment by name without prompting. modelFilter optionally restricts to deployments
// matching a specific model ID (from manifest). Returns nil if user chose
// "Create a new model deployment" or no deployments exist.
func selectModelDeployment(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	credential azcore.TokenCredential,
	project FoundryProjectInfo,
	modelDeploymentFlag string,
	modelFilter string,
) (*FoundryDeploymentInfo, error) {
	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:        "Searching for model deployments in your Foundry Project...",
		ClearOnStop: true,
	})
	if err := spinner.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start spinner: %w", err)
	}

	deployments, err := listProjectDeployments(ctx, credential, project.SubscriptionId, project.ResourceGroupName, project.AccountName)
	if stopErr := spinner.Stop(ctx); stopErr != nil {
		return nil, stopErr
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list deployments: %w", err)
	}

	// Filter by model if specified
	if modelFilter != "" {
		filtered := make([]FoundryDeploymentInfo, 0)
		for _, d := range deployments {
			if strings.EqualFold(d.ModelName, modelFilter) {
				filtered = append(filtered, d)
			}
		}
		deployments = filtered
	}

	if len(deployments) == 0 {
		if modelFilter != "" {
			fmt.Printf("No existing deployments found matching model '%s'.\n", modelFilter)
		} else {
			fmt.Println("No existing deployments found. You can create a new model deployment.")
		}
		return nil, nil
	}

	if modelDeploymentFlag != "" {
		// Flag provided: find matching deployment by name
		for _, d := range deployments {
			if strings.EqualFold(d.Name, modelDeploymentFlag) {
				return &d, nil
			}
		}
		return nil, exterrors.Validation(
			exterrors.CodeModelDeploymentNotFound,
			fmt.Sprintf("model deployment %q not found in Foundry project", modelDeploymentFlag),
			"verify the deployment name or omit --model-deployment to select interactively",
		)
	}

	// Interactive prompt
	deployChoices := make([]*azdext.SelectChoice, 0, len(deployments)+1)
	for _, d := range deployments {
		label := fmt.Sprintf("%s (%s v%s, %s)", d.Name, d.ModelName, d.Version, d.SkuName)
		deployChoices = append(deployChoices, &azdext.SelectChoice{
			Label: label,
			Value: d.Name,
		})
	}
	deployChoices = append(deployChoices, &azdext.SelectChoice{
		Label: "Create a new model deployment",
		Value: "__create_new__",
	})

	deployResp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select a model deployment",
			Choices: deployChoices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("model deployment selection was cancelled")
		}
		return nil, exterrors.Dependency(
			exterrors.CodePromptFailed,
			fmt.Sprintf("failed to prompt for deployment selection: %v", err),
			"use --model-deployment to specify a deployment name in non-interactive mode",
		)
	}

	deploymentIdx := *deployResp.Value
	if deploymentIdx >= 0 && int(deploymentIdx) < len(deployments) {
		d := deployments[deploymentIdx]
		fmt.Printf("Model deployment name: %s\n", d.Name)
		return &d, nil
	}

	// User chose "Create a new model deployment"
	return nil, nil
}
