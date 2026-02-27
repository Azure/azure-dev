// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package factory

import (
	"context"
	"fmt"
	"net/http"

	"azure.ai.finetune/internal/providers"
	azureprovider "azure.ai.finetune/internal/providers/azure"
	openaiprovider "azure.ai.finetune/internal/providers/openai"
	"azure.ai.finetune/internal/utils"
	"azure.ai.finetune/internal/version"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const (
	// OpenAI API version for Azure cognitive services
	DefaultApiVersion = "2025-11-15-preview"
	// Azure cognitive services endpoint URL pattern
	DefaultCognitiveServicesEndpoint = "https://%s.services.ai.azure.com/api/projects/%s"
	DefaultAzureFinetuningScope      = "https://ai.azure.com/.default"
)

// UserAgent is the user agent string included in all HTTP calls
var UserAgent = fmt.Sprintf("azd-ext-azure-ai-finetune/%s", version.Version)

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
	accountName := envValueMap[utils.EnvAzureAccountName]
	projectName := envValueMap[utils.EnvAzureOpenAIProjectName]
	endpoint := envValueMap[utils.EnvFinetuningRoute]
	if endpoint == "" {
		endpoint = fmt.Sprintf(DefaultCognitiveServicesEndpoint, accountName, projectName)
	}

	apiVersion := envValueMap[utils.EnvAPIVersion]
	if apiVersion == "" {
		apiVersion = DefaultApiVersion
	}

	scope := envValueMap[utils.EnvFinetuningTokenScope]
	if scope == "" {
		scope = DefaultAzureFinetuningScope
	}
	// Create OpenAI client
	fmt.Printf("User-Agent set to: %s\n", UserAgent)
	client := openai.NewClient(
		//azure.WithEndpoint(endpoint, apiVersion),
		option.WithBaseURL(endpoint),
		option.WithQuery("api-version", apiVersion),
		WithTokenCredential(credential, scope),
	)
	return &client, nil
}

// WithTokenCredential configures this client to authenticate using an [Azure Identity] TokenCredential.
// This function should be paired with a call to [WithEndpoint] to point to your Azure OpenAI instance.
//
// [Azure Identity]: https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azidentity
func WithTokenCredential(tokenCredential azcore.TokenCredential, scope string) option.RequestOption {
	bearerTokenPolicy := runtime.NewBearerTokenPolicy(tokenCredential, []string{scope}, nil)
	// add in a middleware that uses the bearer token generated from the token credential
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		pipeline := runtime.NewPipeline("finetune-extensions", pipelineVersion, runtime.PipelineOptions{}, &policy.ClientOptions{
			InsecureAllowCredentialWithHTTP: true, // allow for plain HTTP proxies, etc..
			PerCallPolicies: []policy.Policy{
				azsdk.NewUserAgentPolicy(UserAgent),
			},
			PerRetryPolicies: []policy.Policy{
				bearerTokenPolicy,
				policyAdapter(next),
			},
		})

		req2, err := runtime.NewRequestFromRequest(req)

		if err != nil {
			return nil, err
		}

		return pipeline.Do(req2)
	})
}

// NewFineTuningProvider creates a FineTuningProvider based on provider type
func NewFineTuningProvider(ctx context.Context, azdClient *azdext.AzdClient) (providers.FineTuningProvider, error) {
	client, err := GetOpenAIClientFromAzdClient(ctx, azdClient)
	return openaiprovider.NewOpenAIProvider(client), err
}

// NewModelDeploymentProvider creates a ModelDeploymentProvider based on provider type
func NewModelDeploymentProvider(subscriptionId string, credential azcore.TokenCredential) (providers.ModelDeploymentProvider, error) {
	fmt.Printf("User-Agent set to: %s for ARM client\n", UserAgent)
	clientFactory, err := armcognitiveservices.NewClientFactory(
		subscriptionId,
		credential,
		&arm.ClientOptions{
			ClientOptions: policy.ClientOptions{
				PerCallPolicies: []policy.Policy{
					azsdk.NewUserAgentPolicy(UserAgent),
				},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create armcognitiveservices client factory: %w", err)
	}
	return azureprovider.NewAzureProvider(clientFactory), err
}

type policyAdapter option.MiddlewareNext

func (mp policyAdapter) Do(req *policy.Request) (*http.Response, error) {
	return (option.MiddlewareNext)(mp)(req.Raw())
}

const pipelineVersion = "v.0.1.0"
