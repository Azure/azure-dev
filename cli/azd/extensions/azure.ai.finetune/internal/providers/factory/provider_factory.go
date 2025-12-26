package factory

import (
	"context"
	"fmt"

	"azure.ai.finetune/internal/providers"
	azureprovider "azure.ai.finetune/internal/providers/azure"
	openaiprovider "azure.ai.finetune/internal/providers/openai"
	"azure.ai.finetune/internal/utils"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
)

type ProviderType string

const (
	ProviderTypeOpenAI ProviderType = "openai"
	ProviderTypeAzure  ProviderType = "azure"
)

const (
	// OpenAI API version for Azure cognitive services
	apiVersion = "2025-04-01-preview"
	// Azure cognitive services endpoint URL pattern
	azureCognitiveServicesEndpoint = "https://%s.cognitiveservices.azure.com/openai"
)

func GetOpenAIClientFromAzdClient(ctx context.Context, azdClient *azdext.AzdClient) (*openai.Client, error) {
	envValueMap, err := utils.GetEnvironmentValues(ctx, azdClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get environment values: %w", err)
	}

	azureContext := &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			TenantId:       envValueMap[utils.EnvAzureTenantID],
			SubscriptionId: envValueMap[utils.EnvAzureSubscriptionID],
			Location:       envValueMap[utils.EnvAzureLocation],
		},
		Resources: []string{},
	}

	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   azureContext.Scope.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create azure credential: %w", err)
	}

	// Get Azure credentials and endpoint - TODO
	// You'll need to get these from your environment or config
	accountName := envValueMap["AZURE_ACCOUNT_NAME"]
	endpoint := fmt.Sprintf(azureCognitiveServicesEndpoint, accountName)
	// Create OpenAI client
	client := openai.NewClient(
		//azure.WithEndpoint(endpoint, apiVersion),
		option.WithBaseURL(endpoint),
		option.WithQuery("api-version", apiVersion),
		azure.WithTokenCredential(credential),
	)
	return &client, nil
}

// NewFineTuningProvider creates a FineTuningProvider based on provider type
func NewFineTuningProvider(ctx context.Context, azdClient *azdext.AzdClient) (providers.FineTuningProvider, error) {
	client, err := GetOpenAIClientFromAzdClient(ctx, azdClient)
	return openaiprovider.NewOpenAIProvider(client), err
}

// NewModelDeploymentProvider creates a ModelDeploymentProvider based on provider type
func NewModelDeploymentProvider(subscriptionId string, credential azcore.TokenCredential) (providers.ModelDeploymentProvider, error) {
	clientFactory, err := armcognitiveservices.NewClientFactory(
		subscriptionId,
		credential,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create armcognitiveservices client factory: %w", err)
	}
	return azureprovider.NewAzureProvider(clientFactory), err
}
