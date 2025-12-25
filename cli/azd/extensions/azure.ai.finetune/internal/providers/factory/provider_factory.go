package factory

import (
    "context"
    "fmt"

    "github.com/Azure/azure-sdk-for-go/sdk/azidentity"
    "azure.ai.finetune/internal/providers"
    openaiprovider "azure.ai.finetune/internal/providers/openai"
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
	azureCognitiveServicesEndpoint = "https://%s.cognitiveservices.azure.com"
)

func GetOpenAIClientFromAzdClient(ctx context.Context, azdClient *azdext.AzdClient) (*openai.Client, error) {
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

	azureContext := &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			TenantId:       envValueMap["AZURE_TENANT_ID"],
			SubscriptionId: envValueMap["AZURE_SUBSCRIPTION_ID"],
			Location:       envValueMap["AZURE_LOCATION"],
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

	if endpoint == "" {
		return nil, fmt.Errorf("AZURE_OPENAI_ENDPOINT environment variable not set")
	}

	// Create OpenAI client
	client := openai.NewClient(
		//azure.WithEndpoint(endpoint, apiVersion),
		option.WithBaseURL(fmt.Sprintf("%s/openai", endpoint)),
		option.WithQuery("api-version", apiVersion),
		azure.WithTokenCredential(credential),
	)
	return &client, nil
}

// NewFineTuningProvider creates a FineTuningProvider based on provider type
func NewFineTuningProvider(ctx context.Context, azdClient *azdext.AzdClient) (providers.FineTuningProvider, error) {
    client, err := GetOpenAIClientFromAzdClient(ctx, azdClient);    
    return openaiprovider.NewOpenAIProvider(client), err;
}

// NewModelDeploymentProvider creates a ModelDeploymentProvider based on provider type
func NewModelDeploymentProvider(ctx context.Context, azdClient *azdext.AzdClient) (providers.ModelDeploymentProvider, error) {
    client, err := GetOpenAIClientFromAzdClient(ctx, azdClient);    
    return openaiprovider.NewOpenAIProvider(client), err;
}