// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"azure.ai.customtraining/internal/utils"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// sanitizeEnvironmentName converts a project name to a valid azd environment name.
func sanitizeEnvironmentName(name string) string {
	result := strings.ToLower(name)
	result = strings.ReplaceAll(result, " ", "-")
	result = strings.ReplaceAll(result, "_", "-")

	re := regexp.MustCompile(`[^a-z0-9-]`)
	result = re.ReplaceAllString(result, "")

	re = regexp.MustCompile(`-+`)
	result = re.ReplaceAllString(result, "-")

	result = strings.Trim(result, "-")

	if result == "" {
		result = "training-env"
	}

	return result
}

// parseProjectEndpoint extracts account name and project name from an endpoint URL.
// Example: https://account-name.services.ai.azure.com/api/projects/project-name
// Also supports: https://account-name.cognitiveservices.azure.com/api/projects/project-name
func parseProjectEndpoint(endpoint string) (accountName string, projectName string, err error) {
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse endpoint URL: %w", err)
	}

	hostname := parsedURL.Hostname()
	hostParts := strings.Split(hostname, ".")
	if len(hostParts) < 1 || hostParts[0] == "" {
		return "", "", fmt.Errorf("invalid endpoint URL: cannot extract account name from hostname")
	}
	accountName = hostParts[0]

	pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
	if len(pathParts) != 3 || pathParts[0] != "api" || pathParts[1] != "projects" || pathParts[2] == "" {
		return "", "", fmt.Errorf("invalid endpoint URL: expected format /api/projects/{project-name}")
	}
	projectName = pathParts[2]

	return accountName, projectName, nil
}

// validateOrInitEnvironment checks if environment is configured, and if not, attempts implicit initialization.
func validateOrInitEnvironment(ctx context.Context, subscriptionId, projectEndpoint string) error {
	ctx = azdext.WithAccessToken(ctx)

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return err
	}
	defer azdClient.Close()

	// If user explicitly provided -e and -s flags, always use them (re-initialize environment)
	if projectEndpoint != "" && subscriptionId != "" {
		return implicitInit(ctx, azdClient, subscriptionId, projectEndpoint)
	}

	// No flags provided — check if environment is already configured
	envValues, _ := utils.GetEnvironmentValues(ctx, azdClient)
	required := []string{utils.EnvAzureTenantID, utils.EnvAzureSubscriptionID, utils.EnvAzureAccountName, utils.EnvAzureProjectName}

	allConfigured := true
	for _, varName := range required {
		if envValues[varName] == "" {
			allConfigured = false
			break
		}
	}

	if allConfigured {
		return nil
	}

	return fmt.Errorf("required environment variables not set. Either run 'azd ai training init' or provide both --subscription (-s) and --project-endpoint (-e) flags")
}

// implicitInit performs a lightweight initialization using the provided subscription and project endpoint flags.
func implicitInit(ctx context.Context, azdClient *azdext.AzdClient, subscriptionId, projectEndpoint string) error {
	accountName, projectName, err := parseProjectEndpoint(projectEndpoint)
	if err != nil {
		return fmt.Errorf("failed to parse project endpoint: %w", err)
	}

	envName := sanitizeEnvironmentName(projectName)

	initFlags := &initFlags{
		subscriptionId:  subscriptionId,
		projectEndpoint: projectEndpoint,
		env:             envName,
	}
	initFlags.NoPrompt = true

	azureContext, err := ensureProject(ctx, initFlags, azdClient)
	if err != nil {
		return fmt.Errorf("implicit initialization failed: %w", err)
	}

	if err := ensureAzdProject(ctx, initFlags, azdClient); err != nil {
		return fmt.Errorf("implicit initialization failed: %w", err)
	}

	env, err := ensureEnvironment(ctx, initFlags, azdClient)
	if err != nil {
		return fmt.Errorf("implicit initialization failed: %w", err)
	}

	if err := setEnvValues(ctx, azdClient, env.Name, map[string]string{
		utils.EnvAzureTenantID:       azureContext.Scope.TenantId,
		utils.EnvAzureSubscriptionID: subscriptionId,
		utils.EnvAzureAccountName:    accountName,
		utils.EnvAzureProjectName:    projectName,
	}); err != nil {
		return fmt.Errorf("implicit initialization failed: failed to set environment values: %w", err)
	}

	return nil
}
