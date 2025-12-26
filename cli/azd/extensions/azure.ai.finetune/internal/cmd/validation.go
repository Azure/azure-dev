package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func validateEnvironment(ctx context.Context) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return err
	}
	defer azdClient.Close()

	envValues := getEnvironmentValues(ctx, azdClient)
	required := []string{"AZURE_TENANT_ID", "AZURE_SUBSCRIPTION_ID", "AZURE_LOCATION", "AZURE_ACCOUNT_NAME"}

	for _, varName := range required {
		if envValues[varName] == "" {
			return fmt.Errorf("required environment variables not set. Please run 'azd ai finetune init' command to configure your environment")
		}
	}
	return nil
}

func getEnvironmentValues(ctx context.Context, azdClient *azdext.AzdClient) map[string]string {
	envValueMap := make(map[string]string)

	if envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil {
		env := envResponse.Environment
		envValues, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
			Name: env.Name,
		})
		if err != nil {
			return envValueMap
		}

		for _, value := range envValues.KeyValues {
			envValueMap[value.Key] = value.Value
		}
	}

	return envValueMap
}
