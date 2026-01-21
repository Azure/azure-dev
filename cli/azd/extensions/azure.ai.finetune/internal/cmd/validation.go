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
)

// validateRequiredFlag returns a user-friendly error for missing required flags
// The error message is in red, the hint is in yellow
func validateRequiredFlag(flagName string) error {
	switch flagName {
	case "id":
		errorMsg := fmt.Sprintf("--%s is required", flagName)
		hint := color.YellowString("\n\n%s\n", HintFindJobID)
		return fmt.Errorf("%s%s", errorMsg, hint)
	}
	return fmt.Errorf("--%s is required", flagName)
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
