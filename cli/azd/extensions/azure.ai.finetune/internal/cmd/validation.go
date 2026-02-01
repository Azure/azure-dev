// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"

	"azure.ai.finetune/internal/utils"
)

// sanitizeEnvironmentName converts a project name to a valid azd environment name.
// azd environment names must contain only lowercase letters, numbers, and hyphens,
// and must start and end with a letter or number.
func sanitizeEnvironmentName(name string) string {
	// Convert to lowercase
	result := strings.ToLower(name)

	// Replace spaces, underscores, and other common separators with hyphens
	result = strings.ReplaceAll(result, " ", "-")
	result = strings.ReplaceAll(result, "_", "-")

	// Remove any characters that aren't lowercase letters, numbers, or hyphens
	re := regexp.MustCompile(`[^a-z0-9-]`)
	result = re.ReplaceAllString(result, "")

	// Replace multiple consecutive hyphens with a single hyphen
	re = regexp.MustCompile(`-+`)
	result = re.ReplaceAllString(result, "-")

	// Trim leading and trailing hyphens (must start/end with letter or number)
	result = strings.Trim(result, "-")

	// If empty after sanitization, use a default name
	if result == "" {
		result = "finetuning-env"
	}

	return result
}

// Common hints for required flags
const (
	HintFindJobID = "To find job IDs, run: azd ai finetuning jobs list"

	HintDeploymentName = "Deployment name can be any unique identifier for your endpoint"

	HintSubmitJobUsage = `Usage options:
  1. Provide a config file:    azd ai finetuning jobs submit --file config.yaml
  2. Provide model and data:   azd ai finetuning jobs submit --model <model> --training-file <file>`
)

// validateRequiredFlags checks if any of the provided flag values are empty and returns
// a user-friendly error with hints for known flags.
func validateRequiredFlags(flags map[string]string) error {
	var missingFlags []string
	for name, value := range flags {
		if value == "" {
			missingFlags = append(missingFlags, name)
		}
	}

	if len(missingFlags) == 0 {
		return nil
	}

	// Sort for consistent output
	sort.Strings(missingFlags)

	// Format flags: --flag1, --flag2
	formatted := make([]string, len(missingFlags))
	for i, name := range missingFlags {
		formatted[i] = "--" + name
	}

	var errorMsg string
	if len(missingFlags) == 1 {
		errorMsg = fmt.Sprintf("%s is required", formatted[0])
	} else {
		errorMsg = fmt.Sprintf("%s are required", strings.Join(formatted, ", "))
	}

	// Collect hints for known flags (in priority order)
	var hints []string
	for _, name := range missingFlags {
		switch name {
		case "job-id", "id":
			hints = append(hints, fmt.Sprintf("  • %s: %s", name, HintFindJobID))
		case "deployment-name":
			hints = append(hints, fmt.Sprintf("  • %s: %s", name, HintDeploymentName))
		}
	}

	if len(hints) > 0 {
		hintBlock := color.YellowString("\n\nUsage:\n%s", strings.Join(hints, "\n"))
		return fmt.Errorf("%s%s", errorMsg, hintBlock)
	}

	return fmt.Errorf("%s", errorMsg)
}

// validateSubmitFlags validates the submit command flag combinations.
// Either --file must be provided, or both --model and --training-file must be provided.
func validateSubmitFlags(file, model, trainingFile string) error {
	if file != "" {
		// Config file provided - valid
		return nil
	}

	if model == "" && trainingFile == "" {
		// Neither option provided
		errorMsg := "either --file or --model with --training-file is required"
		hint := color.YellowString("\n\n%s\n", HintSubmitJobUsage)
		return fmt.Errorf("%s%s", errorMsg, hint)
	}

	if model == "" {
		errorMsg := "--model is required when --training-file is provided"
		hint := color.YellowString("\n\n%s\n", HintSubmitJobUsage)
		return fmt.Errorf("%s%s", errorMsg, hint)
	}

	if trainingFile == "" {
		errorMsg := "--training-file is required when --model is provided"
		hint := color.YellowString("\n\n%s\n", HintSubmitJobUsage)
		return fmt.Errorf("%s%s", errorMsg, hint)
	}

	return nil
}

// validateOrInitEnvironment checks if environment is configured, and if not, attempts implicit initialization
// using the provided subscription ID and project endpoint flags.
func validateOrInitEnvironment(ctx context.Context, subscriptionId, projectEndpoint string) error {
	ctx = azdext.WithAccessToken(ctx)

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return err
	}
	defer azdClient.Close()

	envValues, _ := utils.GetEnvironmentValues(ctx, azdClient)
	required := []string{utils.EnvAzureTenantID, utils.EnvAzureSubscriptionID, utils.EnvAzureLocation, utils.EnvAzureAccountName}

	// Check if environment is already configured
	allConfigured := true
	for _, varName := range required {
		if envValues[varName] == "" {
			allConfigured = false
			break
		}
	}

	if allConfigured {
		// Warn user if they provided flags that will be ignored
		if subscriptionId != "" || projectEndpoint != "" {
			color.Yellow("Warning: Environment is already configured. The --subscription and --project-endpoint flags are being ignored.")
			color.Yellow("To reconfigure, run 'azd ai finetuning init' with the new values.\n")
		}
		return nil
	}

	// Environment not configured - check if we have flags for implicit init
	if projectEndpoint == "" || subscriptionId == "" {
		return fmt.Errorf("required environment variables not set. Either run 'azd ai finetuning init' or provide both --subscription (-s) and --project-endpoint (-e) flags")
	}

	// Perform implicit initialization
	fmt.Println("Environment not configured. Running implicit initialization...")

	// Extract project name from endpoint to use as default environment name
	_, projectName, err := parseProjectEndpoint(projectEndpoint)
	if err != nil {
		return fmt.Errorf("failed to parse project endpoint: %w", err)
	}

	// Sanitize project name for use as azd environment name
	// (must be lowercase letters, numbers, hyphens, and start/end with letter or number)
	envName := sanitizeEnvironmentName(projectName)

	initFlags := &initFlags{
		subscriptionId:  subscriptionId,
		projectEndpoint: projectEndpoint,
		env:             envName,
	}
	initFlags.NoPrompt = true // Run in non-interactive mode

	// Ensure project exists first (required before creating environment)
	_, err = ensureProject(ctx, initFlags, azdClient)
	if err != nil {
		return fmt.Errorf("implicit initialization failed: %w", err)
	}

	_, err = ensureEnvironment(ctx, initFlags, azdClient)
	if err != nil {
		return fmt.Errorf("implicit initialization failed: %w", err)
	}

	fmt.Println("Environment configured successfully.")
	return nil
}
