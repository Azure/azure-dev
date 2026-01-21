// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azure.ai.finetune/internal/utils"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
)

// Common hints for required flags
const (
	HintFindJobID = "To find job IDs, run: azd ai finetuning jobs list"

	HintSubmitJobUsage = `Usage options:
  1. Provide a config file:    azd ai finetune jobs submit --file config.yaml
  2. Provide model and trailing file:   azd ai finetune jobs submit --model <model> --training-file <file>`
)

// validateRequiredFlag returns a user-friendly error for missing required flags.
// For known flags (like "id"), it includes a helpful hint in yellow.
func validateRequiredFlag(flagName string) error {
	errorMsg := fmt.Sprintf("--%s is required", flagName)

	switch flagName {
	case "id":
		hint := color.YellowString("\n\n%s\n", HintFindJobID)
		return fmt.Errorf("%s%s", errorMsg, hint)
	default:
		return fmt.Errorf("%s", errorMsg)
	}
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
			return fmt.Errorf("required environment variables not set. Please run 'azd ai finetune init' command to configure your environment")
		}
	}
	return nil
}
