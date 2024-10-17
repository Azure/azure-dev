package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/sdk/azdcore/azure"
	"github.com/azure/azure-dev/cli/sdk/azdcore/config"
	"github.com/azure/azure-dev/cli/sdk/azdcore/ext"
	"github.com/azure/azure-dev/cli/sdk/azdcore/ext/prompt"
	"github.com/azure/azure-dev/cli/sdk/azdcore/ux"
)

type AiConfig struct {
	Subscription  string `json:"subscription"`
	ResourceGroup string `json:"resourceGroup"`
	Service       string `json:"service"`
	Model         string `json:"model"`
}

var (
	ErrNotFound           = errors.New("service config not found")
	ErrNoModelDeployments = errors.New("no model deployments found")
	ErrNoAiServices       = errors.New("no Azure AI services found")
)

func LoadOrPrompt(ctx context.Context, azdContext *ext.Context) (*AiConfig, error) {
	config, err := Load(ctx, azdContext)
	if err != nil && errors.Is(err, ErrNotFound) {
		account, err := PromptAccount(ctx, azdContext)
		if err != nil {
			return nil, err
		}

		parsedResource, err := arm.ParseResourceID(*account.ID)
		if err != nil {
			return nil, err
		}

		config = &AiConfig{
			Subscription:  parsedResource.SubscriptionID,
			ResourceGroup: parsedResource.ResourceGroupName,
			Service:       parsedResource.Name,
		}

		if err := Save(ctx, azdContext, config); err != nil {
			return nil, err
		}
	}

	return config, nil
}

func Load(ctx context.Context, azdContext *ext.Context) (*AiConfig, error) {
	var azdConfig config.Config

	env, err := azdContext.Environment(ctx)
	if err == nil {
		azdConfig = env.Config
	} else {
		userConfig, err := azdContext.UserConfig(ctx)
		if err == nil {
			azdConfig = userConfig
		}
	}

	if azdConfig == nil {
		return nil, errors.New("azd configuration is not available")
	}

	var config AiConfig
	has, err := azdConfig.GetSection("ai.config", &config)
	if err != nil {
		return nil, err
	}

	if has {
		return &config, nil
	}

	return nil, ErrNotFound
}

func Save(ctx context.Context, azdContext *ext.Context, config *AiConfig) error {
	if azdContext == nil {
		return errors.New("azdContext is required")
	}

	if config == nil {
		return errors.New("config is required")
	}

	credential, err := azdContext.Credential()
	if err != nil {
		return err
	}

	clientFactory, err := armcognitiveservices.NewClientFactory(config.Subscription, credential, nil)
	if err != nil {
		return err
	}

	accountClient := clientFactory.NewAccountsClient()

	// Validate account name
	_, err = accountClient.Get(ctx, config.ResourceGroup, config.Service, nil)
	if err != nil {
		return fmt.Errorf("the specified service configuration is invalid: %w", err)
	}

	// Validate model deployment name
	if config.Model != "" {
		deploymentClient := clientFactory.NewDeploymentsClient()
		_, err = deploymentClient.Get(ctx, config.ResourceGroup, config.Service, config.Model, nil)
		if err != nil {
			return fmt.Errorf("the specified model deployment is invalid: %w", err)
		}
	}

	env, err := azdContext.Environment(ctx)
	if err == nil && env != nil {
		if err := env.Config.Set("ai.config", config); err != nil {
			return err
		}

		if err := azdContext.SaveEnvironment(ctx, env); err != nil {
			return err
		}

		return nil
	}

	userConfig, err := azdContext.UserConfig(ctx)
	if err == nil && userConfig != nil {
		if err := userConfig.Set("ai.config", config); err != nil {
			return err
		}

		if err := azdContext.SaveUserConfig(ctx, userConfig); err != nil {
			return err
		}

		return nil
	}

	return errors.New("unable to save service configuration")
}

func PromptAccount(ctx context.Context, azdContext *ext.Context) (*armcognitiveservices.Account, error) {
	subscription, err := prompt.PromptSubscription(ctx, nil)
	if err != nil {
		return nil, err
	}

	credential, err := azdContext.Credential()
	if err != nil {
		return nil, err
	}

	selectedAccount, err := prompt.PromptSubscriptionResource(ctx, subscription, prompt.PromptResourceOptions{
		ResourceType:            to.Ptr(azure.ResourceTypeCognitiveServiceAccount),
		ResourceTypeDisplayName: "Azure AI service",
	})
	if err != nil {
		return nil, err
	}

	parsedService, err := arm.ParseResourceID(selectedAccount.Id)
	if err != nil {
		return nil, err
	}

	accountClient, err := armcognitiveservices.NewAccountsClient(subscription.Id, credential, nil)
	if err != nil {
		return nil, err
	}

	accountResponse, err := accountClient.Get(ctx, parsedService.ResourceGroupName, parsedService.Name, nil)
	if err != nil {
		return nil, err
	}

	return &accountResponse.Account, nil
}

func PromptModelDeployment(ctx context.Context, azdContext *ext.Context) (*armcognitiveservices.Deployment, error) {
	aiConfig, err := LoadOrPrompt(ctx, azdContext)
	if err != nil {
		return nil, err
	}

	credential, err := azdContext.Credential()
	if err != nil {
		return nil, err
	}

	deploymentsClient, err := armcognitiveservices.NewDeploymentsClient(aiConfig.Subscription, credential, nil)
	if err != nil {
		return nil, err
	}

	deploymentList := []*armcognitiveservices.Deployment{}

	loadingSpinner := ux.NewSpinner(&ux.SpinnerConfig{
		Text: "Loading model deployments...",
	})

	err = loadingSpinner.Run(ctx, func(ctx context.Context) error {
		modelPager := deploymentsClient.NewListPager(aiConfig.ResourceGroup, aiConfig.Service, nil)
		for modelPager.More() {
			models, err := modelPager.NextPage(ctx)
			if err != nil {
				return err
			}

			deploymentList = append(deploymentList, models.Value...)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	choices := make([]string, len(deploymentList))
	for i, deployment := range deploymentList {
		choices[i] = fmt.Sprintf(
			"%s (Model: %s, Version: %s)",
			*deployment.Name,
			*deployment.Properties.Model.Name,
			*deployment.Properties.Model.Version,
		)
	}

	if len(choices) == 0 {
		return nil, ErrNoModelDeployments
	}

	modelSelector := ux.NewSelect(&ux.SelectConfig{
		Message:        "Select a model",
		Allowed:        choices,
		DisplayCount:   10,
		DisplayNumbers: ux.Ptr(true),
	})

	selectedModelIndex, err := modelSelector.Ask()
	if err != nil {
		return nil, err
	}

	return deploymentList[*selectedModelIndex], nil
}
