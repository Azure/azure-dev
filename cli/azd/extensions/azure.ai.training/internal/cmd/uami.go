// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"azure.ai.training/internal/utils"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ensureUAMIBeforeSubmit gates 'job submit' on the project having a UAMI.
//
// Behavior:
//   - If AZURE_AI_TRAINING_HAS_UAMI == "true" → allow.
//   - If AZURE_AI_TRAINING_HAS_UAMI == "false" → block with error.
//   - If unset → make one ARM call to resolve. On success, persist the
//     value to the azd env and act on it. On any failure (auth, network,
//     404 — e.g. project deleted), allow the submit to proceed and do
//     not write anything to the env, so the next submit retries.
func ensureUAMIBeforeSubmit(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envValues map[string]string,
	credential azcore.TokenCredential,
) error {
	projectName := envValues[utils.EnvAzureProjectName]

	switch envValues[utils.EnvAzureHasUAMI] {
	case "true":
		return nil
	case "false":
		return fmt.Errorf("%s", utils.NoUAMIMessage(projectName))
	}

	// Unset → resolve lazily via ARM (single call).
	subscriptionId := envValues[utils.EnvAzureSubscriptionID]
	accountName := envValues[utils.EnvAzureAccountName]
	if subscriptionId == "" || accountName == "" || projectName == "" {
		// Insufficient context to make the ARM call; allow submit (the
		// downstream call will fail with its own clearer error).
		return nil
	}

	project, err := findProjectByEndpoint(ctx, subscriptionId, accountName, projectName, credential)
	if err != nil {
		// Soft-fail: don't block, don't persist. Next submit will retry.
		return nil
	}

	// Persist the resolved value so subsequent submits use the cached value.
	currentEnv, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err == nil && currentEnv.Environment != nil {
		_ = setEnvValues(ctx, azdClient, currentEnv.Environment.Name, map[string]string{
			utils.EnvAzureHasUAMI: utils.BoolEnv(project.HasUAMI),
		})
	}

	if !project.HasUAMI {
		return fmt.Errorf("%s", utils.NoUAMIMessage(projectName))
	}
	return nil
}
