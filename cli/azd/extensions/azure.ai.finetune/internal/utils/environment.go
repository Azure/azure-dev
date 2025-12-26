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

func GetEnvironmentValues(ctx context.Context, azdClient *azdext.AzdClient) (map[string]string, error) {
	envValueMap := make(map[string]string)

	if envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
		env := envResponse.Environment
		envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
			Name: env.Name,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get environment values: %w", err)
		}

		for _, value := range envValues.KeyValues {
			envValueMap[value.Key] = value.Value
		}
	}

	return envValueMap, nil
}