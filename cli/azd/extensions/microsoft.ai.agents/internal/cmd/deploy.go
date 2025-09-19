// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newDeployCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "deploy",
		Short: "Deploy AI agents to Azure.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create a new AZD client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			if err := displayProjectContext(ctx, azdClient); err != nil {
				return fmt.Errorf("failed to display project context: %w", err)
			}

			color.Green("Deployed AI agent!")
			return nil
		},
	}
}

// displayProjectContext shows the current project, environment, and deployment context
func displayProjectContext(ctx context.Context, azdClient *azdext.AzdClient) error {
	if err := displayUserConfig(ctx, azdClient); err != nil {
		// Log warning but continue
		color.Yellow("WARNING: Failed to retrieve user config: %v", err)
	}

	project, err := getProject(ctx, azdClient)
	if err != nil {
		color.Yellow("WARNING: No azd project found in current working directory")
		fmt.Printf("Run %s to create a new project.\n", color.CyanString("azd init"))
		return fmt.Errorf("project not found: %w", err)
	}
	displayProject(project)

	currentEnv, err := getCurrentEnvironment(ctx, azdClient)
	if err != nil {
		color.Yellow("WARNING: No azd environment(s) found.")
		fmt.Printf("Run %s to create a new environment.\n", color.CyanString("azd env new"))
		return fmt.Errorf("environment not found: %w", err)
	}

	if err := displayEnvironmentInfo(ctx, azdClient, currentEnv); err != nil {
		return fmt.Errorf("failed to display environment info: %w", err)
	}

	if err := displayDeploymentContext(ctx, azdClient); err != nil {
		// Log warning but continue
		color.Yellow("WARNING: Failed to retrieve deployment context: %v", err)
	}

	return nil
}

// displayUserConfig shows user configuration if available
// User config is located at $HOME/.azd/config.json (`azd config show`)
func displayUserConfig(ctx context.Context, azdClient *azdext.AzdClient) error {
	defaultLocation, err := getUserConfigValue(ctx, azdClient, "defaults.location")
	if err != nil {
		return fmt.Errorf("failed to get default location: %w", err)
	}

	defaultSubscription, err := getUserConfigValue(ctx, azdClient, "defaults.subscription")
	if err != nil {
		return fmt.Errorf("failed to get default subscription: %w", err)
	}

	if defaultLocation == "" && defaultSubscription == "" {
		return nil
	}

	color.Cyan("User Config:")

	if defaultLocation != "" {
		fmt.Printf("%s: %s\n", color.HiWhiteString("Default Location"), defaultLocation)
	}
	if defaultSubscription != "" {
		fmt.Printf("%s: %s\n", color.HiWhiteString("Default Subscription"), defaultSubscription)
	}
	fmt.Println()
	return nil
}

// getUserConfigValue retrieves a specific configuration value by path
func getUserConfigValue(ctx context.Context, azdClient *azdext.AzdClient, configPath string) (string, error) {
	getConfigResponse, err := azdClient.UserConfig().Get(ctx, &azdext.GetUserConfigRequest{
		Path: configPath,
	})
	if err != nil {
		return "", fmt.Errorf("failed getting user config '%s': %w", configPath, err)
	}

	if !getConfigResponse.Found {
		return "", nil
	}

	var value string
	if err := json.Unmarshal(getConfigResponse.Value, &value); err != nil {
		return "", fmt.Errorf("failed to unmarshal config value for %s: %w", configPath, err)
	}

	return value, nil
}

// getProject retrieves the current project information
func getProject(ctx context.Context, azdClient *azdext.AzdClient) (*azdext.ProjectConfig, error) {
	getProjectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, err
	}
	return getProjectResponse.Project, nil
}

// displayProject shows project information
func displayProject(project *azdext.ProjectConfig) {
	color.Cyan("Project:")

	projectValues := map[string]string{
		"Name": project.Name,
		"Path": project.Path,
	}

	for key, value := range projectValues {
		fmt.Printf("%s: %s\n", color.HiWhiteString(key), value)
	}
	fmt.Println()
}

// getCurrentEnvironment retrieves the current environment name
func getCurrentEnvironment(ctx context.Context, azdClient *azdext.AzdClient) (string, error) {
	getEnvResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", err
	}
	return getEnvResponse.Environment.Name, nil
}

// displayEnvironmentInfo shows environment list, current environment, and values
func displayEnvironmentInfo(ctx context.Context, azdClient *azdext.AzdClient, currentEnvName string) error {
	environments, err := getEnvironments(ctx, azdClient)
	if err != nil {
		return fmt.Errorf("failed to get environments: %w", err)
	}

	if len(environments) == 0 {
		fmt.Println("No environments found")
		return nil
	}

	displayEnvironments(environments, currentEnvName)

	if currentEnvName == "" {
		return nil
	}

	if err := displayEnvironmentValues(ctx, azdClient, currentEnvName); err != nil {
		// Log warning but continue - environment values might not be available
		color.Yellow("WARNING: Failed to retrieve environment values: %v", err)
	}

	return nil
}

// getEnvironments retrieves the list of all environments
func getEnvironments(ctx context.Context, azdClient *azdext.AzdClient) ([]string, error) {
	envListResponse, err := azdClient.Environment().List(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, err
	}

	environments := make([]string, len(envListResponse.Environments))
	for i, env := range envListResponse.Environments {
		environments[i] = env.Name
	}
	return environments, nil
}

// displayEnvironments shows the list of environments with the current one highlighted
func displayEnvironments(environments []string, currentEnvName string) {
	color.Cyan("Environments:")
	for _, env := range environments {
		envLine := env
		if env == currentEnvName {
			envLine += color.HiWhiteString(" (selected)")
		}
		fmt.Printf("- %s\n", envLine)
	}
	fmt.Println()
}

// displayEnvironmentValues shows the key-value pairs for the current environment
func displayEnvironmentValues(ctx context.Context, azdClient *azdext.AzdClient, envName string) error {
	getValuesResponse, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: envName,
	})
	if err != nil {
		return err
	}

	color.Cyan("Environment values:")
	for _, pair := range getValuesResponse.KeyValues {
		fmt.Printf("%s: %s\n", color.HiWhiteString(pair.Key), color.HiBlackString(pair.Value))
	}
	fmt.Println()
	return nil
}

// displayDeploymentContext shows Azure deployment context and provisioned resources
func displayDeploymentContext(ctx context.Context, azdClient *azdext.AzdClient) error {
	deploymentContextResponse, err := azdClient.Deployment().GetDeploymentContext(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return err
	}

	displayAzureScope(deploymentContextResponse.AzureContext.Scope)
	displayProvisionedResources(deploymentContextResponse.AzureContext.Resources)
	return nil
}

// displayAzureScope shows the Azure deployment scope information
func displayAzureScope(scope *azdext.AzureScope) {
	scopeMap := map[string]string{
		"Tenant ID":       scope.TenantId,
		"Subscription ID": scope.SubscriptionId,
		"Location":        scope.Location,
		"Resource Group":  scope.ResourceGroup,
	}

	color.Cyan("Deployment Context:")
	for key, value := range scopeMap {
		if value == "" {
			value = "N/A"
		}
		fmt.Printf("%s: %s\n", color.HiWhiteString(key), value)
	}
	fmt.Println()
}

// displayProvisionedResources shows the list of provisioned Azure resources
func displayProvisionedResources(resourceIds []string) {
	color.Cyan("Provisioned Azure Resources:")
	for _, resourceId := range resourceIds {
		resource, err := arm.ParseResourceID(resourceId)
		if err != nil {
			// Log error but continue with other resources
			fmt.Printf("- %s (failed to parse resource ID)\n", resourceId)
			continue
		}

		fmt.Printf(
			"- %s (%s)\n",
			resource.Name,
			color.HiBlackString(resource.ResourceType.String()),
		)
	}
	fmt.Println()
}
