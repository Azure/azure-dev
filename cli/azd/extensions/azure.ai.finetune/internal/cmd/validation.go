// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azure.ai.finetune/internal/utils"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

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
