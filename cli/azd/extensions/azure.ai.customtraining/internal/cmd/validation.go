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

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
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

// validateOrInitEnvironment ensures the azd environment is populated with the
// account/project/tenant/subscription values needed by all `job` subcommands.
//
// Resolution priority (flags beat stored env values, per product direction):
//
//  1. If both --subscription (-s) and --project-endpoint (-e) are provided,
//     parse them, look up the tenant, and **overwrite** those values in the
//     current azd environment. A yellow warning is printed so the user is aware
//     their stored env was modified.
//  2. If neither flag is provided, fall back to the existing azd environment
//     (must already be configured).
//  3. If only one of the two flags is provided, error: both must come together.
//  4. If env is unconfigured AND both flags are provided AND no current env
//     exists yet, run a one-time implicit initialization (creates the azd env,
//     sets values, and `azd env new` makes it current).
//  5. If env is unconfigured AND no flags are provided, error directing the
//     user to run init or pass both flags.
//
// Subcommands continue reading values via GetEnvironmentValues from the
// current env, so no subcommand changes are required.
func validateOrInitEnvironment(ctx context.Context, subscriptionId, projectEndpoint string) error {
	ctx = azdext.WithAccessToken(ctx)

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return err
	}
	defer azdClient.Close()

	// Reject mixed flag usage early — both must be provided together.
	if (subscriptionId == "") != (projectEndpoint == "") {
		return fmt.Errorf(
			"--subscription (-s) and --project-endpoint (-e) must be provided together")
	}

	envValues, _ := utils.GetEnvironmentValues(ctx, azdClient)
	required := []string{
		utils.EnvAzureTenantID,
		utils.EnvAzureSubscriptionID,
		utils.EnvAzureAccountName,
		utils.EnvAzureProjectName,
	}

	allConfigured := true
	for _, varName := range required {
		if envValues[varName] == "" {
			allConfigured = false
			break
		}
	}

	flagsProvided := subscriptionId != "" && projectEndpoint != ""

	// Path 1: env already configured + flags provided → override stored values.
	if allConfigured && flagsProvided {
		return overrideEnvWithFlags(ctx, azdClient, subscriptionId, projectEndpoint)
	}

	// Path 2: env already configured, no flags → use as-is.
	if allConfigured {
		return nil
	}

	// Path 3: env not configured, no flags → error.
	if !flagsProvided {
		return fmt.Errorf(
			"required environment variables not set. Either run 'azd ai training init' or " +
				"provide both --subscription (-s) and --project-endpoint (-e) flags")
	}

	// Path 4: env not configured + flags provided → first-time implicit init.
	fmt.Println("Environment not configured. Running implicit initialization...")
	return implicitInit(ctx, azdClient, subscriptionId, projectEndpoint)
}

// overrideEnvWithFlags writes the values derived from --subscription /
// --project-endpoint into the current azd environment, replacing any
// previously stored values. A yellow warning is printed so the user knows
// their stored env was modified by this invocation.
//
// This mirrors the same setEnvValues payload that `azd ai training init`
// writes (tenant, subscription, resource group, location, account, project),
// but skips init's project-scaffolding and env-creation steps because both
// already exist by the time we reach this code path.
func overrideEnvWithFlags(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	subscriptionId, projectEndpoint string,
) error {
	accountName, projectName, err := parseProjectEndpoint(projectEndpoint)
	if err != nil {
		return fmt.Errorf("failed to parse --project-endpoint: %w", err)
	}

	// Re-resolve tenant from the provided subscription so cross-tenant
	// scenarios work (the previously stored AZURE_TENANT_ID may belong to a
	// different subscription).
	tenantResp, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subscriptionId,
	})
	if err != nil {
		return fmt.Errorf("failed to look up tenant for subscription %q: %w", subscriptionId, err)
	}

	// Look up the project via ARM (same call init uses) to get the authoritative
	// resource group + location. Without this, AZURE_LOCATION and
	// AZURE_RESOURCE_GROUP_NAME would remain stale from the previous project.
	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResp.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return fmt.Errorf("failed to create azure credential: %w", err)
	}
	project, err := findProjectByEndpoint(ctx, subscriptionId, accountName, projectName, credential)
	if err != nil {
		return fmt.Errorf("failed to find project for --project-endpoint: %w", err)
	}

	currentEnv, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || currentEnv.Environment == nil {
		return fmt.Errorf("failed to determine current azd environment: %w", err)
	}
	envName := currentEnv.Environment.Name

	if err := setEnvValues(ctx, azdClient, envName, map[string]string{
		utils.EnvAzureTenantID:       tenantResp.TenantId,
		utils.EnvAzureSubscriptionID: project.SubscriptionId,
		utils.EnvAzureResourceGroup:  project.ResourceGroupName,
		utils.EnvAzureLocation:       project.Location,
		utils.EnvAzureAccountName:    project.AiAccountName,
		utils.EnvAzureProjectName:    project.AiProjectName,
	}); err != nil {
		return fmt.Errorf("failed to update azd environment %q: %w", envName, err)
	}

	color.Yellow(
		"Warning: --subscription and --project-endpoint overrode azd environment %q "+
			"(subscription, project endpoint, and the derived tenant, resource group, "+
			"location, and account name). These changes persist for subsequent commands. "+
			"Run 'azd ai training init' to reconfigure interactively.\n",
		envName,
	)
	return nil
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
