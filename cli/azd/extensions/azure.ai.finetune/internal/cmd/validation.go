// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"

	"azure.ai.finetune/internal/utils"
)

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

func validateEnvironment(ctx context.Context) error {
	ctx = azdext.WithAccessToken(ctx)

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return err
	}
	defer azdClient.Close()

	envValues, _ := utils.GetEnvironmentValues(ctx, azdClient)
	required := []string{utils.EnvAzureTenantID, utils.EnvAzureSubscriptionID, utils.EnvAzureLocation, utils.EnvAzureAccountName}

	for _, varName := range required {
		if envValues[varName] == "" {
			return fmt.Errorf("required environment variables not set. Please run 'azd ai finetuning init' command to configure your environment")
		}
	}
	return nil
}
