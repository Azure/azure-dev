// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	EnvAzureTenantID       = "AZURE_TENANT_ID"
	EnvAzureSubscriptionID = "AZURE_SUBSCRIPTION_ID"
	EnvAzureLocation       = "AZURE_LOCATION"
	EnvAzureAccountName    = "AZURE_ACCOUNT_NAME"
)

// GetEnvironmentValues retrieves Azure environment configuration from azd client.
// Returns empty map if environment cannot be accessed.
func GetEnvironmentValues(ctx context.Context, azdClient *azdext.AzdClient) (map[string]string, error) {
	envValueMap := make(map[string]string)

	envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return envValueMap, fmt.Errorf("failed to get current environment: %w", err)
	}
	env := envResponse.Environment

	envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: env.Name,
	})
	if err != nil {
		return envValueMap, fmt.Errorf("failed to get environment values: %w", err)
	}

	for _, value := range envValues.KeyValues {
		envValueMap[value.Key] = value.Value
	}

	return envValueMap, nil
}
