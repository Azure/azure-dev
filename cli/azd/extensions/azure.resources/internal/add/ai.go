// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"azure.resources/internal/environment"
	"azure.resources/internal/resources"
	"azure.resources/internal/resources/scaffold"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func (w *Wizard) environmentValues(ctx context.Context) (map[string]string, error) {
	name, err := environment.ResolveName(ctx, w.Client.Environment(), w.Environment)
	if err != nil {
		return nil, err
	}
	response, err := w.Client.Environment().GetValues(ctx,
		&azdext.GetEnvironmentRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("reading environment values: %w", err)
	}
	values := map[string]string{}
	for _, entry := range response.GetKeyValues() {
		values[entry.GetKey()] = entry.GetValue()
	}
	return values, nil
}

func (w *Wizard) azureScope(
	ctx context.Context, pending map[string]string, requireLocation bool,
) (*azdext.AzureContext, map[string]string, error) {
	values, err := w.environmentValues(ctx)
	if err != nil {
		return nil, nil, err
	}
	scope := &azdext.AzureScope{
		SubscriptionId: values["AZURE_SUBSCRIPTION_ID"],
		Location:       values["AZURE_LOCATION"],
	}
	if scope.SubscriptionId == "" {
		response, err := w.Client.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{
			Message: "Select an Azure subscription",
		})
		if err != nil {
			return nil, nil, fmt.Errorf("selecting subscription: %w", err)
		}
		subscription := response.GetSubscription()
		if subscription.GetId() == "" {
			return nil, nil, fmt.Errorf("no subscription was selected")
		}
		scope.SubscriptionId = subscription.Id
		// Guest users authenticate through their access tenant, not the resource tenant.
		scope.TenantId = subscription.UserTenantId
		pending["AZURE_SUBSCRIPTION_ID"] = scope.SubscriptionId
	}
	if scope.TenantId == "" {
		response, err := w.Client.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
			SubscriptionId: scope.SubscriptionId,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("resolving subscription access tenant: %w", err)
		}
		scope.TenantId = response.GetTenantId()
		if scope.TenantId == "" {
			return nil, nil, fmt.Errorf("the subscription access tenant could not be resolved")
		}
	}
	if values["AZURE_TENANT_ID"] != scope.TenantId {
		pending["AZURE_TENANT_ID"] = scope.TenantId
	}
	azureContext := &azdext.AzureContext{Scope: scope}
	if requireLocation && scope.Location == "" {
		response, err := w.Client.Prompt().PromptLocation(ctx,
			&azdext.PromptLocationRequest{AzureContext: azureContext})
		if err != nil {
			return nil, nil, fmt.Errorf("selecting location: %w", err)
		}
		if response.GetLocation().GetName() == "" {
			return nil, nil, fmt.Errorf("no location was selected")
		}
		scope.Location = response.Location.Name
		pending["AZURE_LOCATION"] = scope.Location
	}
	return azureContext, values, nil
}

func (w *Wizard) configureAI(
	ctx context.Context, resourceType string, pending map[string]string,
) (string, resources.Definition, error) {
	definition := resources.Definition{Type: resourceType}
	name := ""
	if resourceType == resources.AIProject {
		for _, other := range slices.Sorted(maps.Keys(w.Resources)) {
			existing := w.Resources[other]
			if existing.Type == resources.AIProject && !existing.Existing {
				name, definition = other, existing
				break
			}
		}
	}
	capabilities := []string(nil)
	if resourceType == resources.OpenAIModel {
		kind, err := w.choose(ctx, "Which type of Azure OpenAI service?",
			[]string{"Chat (GPT)", "Embeddings (Document search)"})
		if err != nil {
			return "", definition, err
		}
		capabilities = []string{"chat"}
		if kind == "Embeddings (Document search)" {
			capabilities = []string{"embeddings"}
		}
	}
	azureContext, _, err := w.azureScope(ctx, pending, resourceType == resources.OpenAIModel)
	if err != nil {
		return "", definition, err
	}
	// Foundry deployment locations are resolved during provisioning, independently of the project location.
	filter := &azdext.AiModelFilterOptions{Capabilities: capabilities}
	deploymentOptions := &azdext.AiModelDeploymentOptions{}
	if resourceType == resources.OpenAIModel {
		filter.Locations = []string{azureContext.Scope.Location}
		filter.Formats = []string{"OpenAI"}
		deploymentOptions.Locations = filter.Locations
		// The standalone OpenAI generator deploys Standard with a fixed capacity.
		deploymentOptions.Skus = []string{"Standard"}
		deploymentOptions.Capacity = new(int32(20))
	}
	modelResponse, err := w.Client.Prompt().PromptAiModel(ctx, &azdext.PromptAiModelRequest{
		AzureContext: azureContext, Filter: filter,
		SelectOptions: &azdext.SelectOptions{Message: "Which model do you want to use?"},
	})
	if err != nil {
		return "", definition, fmt.Errorf("selecting AI model: %w", err)
	}
	model := modelResponse.GetModel()
	if model.GetName() == "" {
		return "", definition, fmt.Errorf("no AI model was selected")
	}
	deploymentResponse, err := w.Client.Prompt().PromptAiDeployment(ctx, &azdext.PromptAiDeploymentRequest{
		AzureContext: azureContext, ModelName: model.Name, Options: deploymentOptions,
		UseDefaultCapacity: resourceType == resources.OpenAIModel,
	})
	if err != nil {
		return "", definition, fmt.Errorf("selecting AI deployment: %w", err)
	}
	deployment := deploymentResponse.GetDeployment()
	if deployment.GetModelName() != model.Name || deployment.GetVersion() == "" ||
		deployment.GetFormat() == "" || deployment.GetSku().GetName() == "" || deployment.GetCapacity() <= 0 {
		return "", definition, fmt.Errorf("the selected AI deployment is incomplete or does not match the model")
	}
	if resourceType == resources.OpenAIModel &&
		deployment.Location != "" && deployment.Location != azureContext.Scope.Location {
		return "", definition, fmt.Errorf("the selected AI deployment does not match the project location")
	}
	if model.Format != "" && !strings.EqualFold(model.Format, deployment.Format) {
		return "", definition, fmt.Errorf("the selected AI deployment does not match the model format")
	}
	if resourceType == resources.OpenAIModel {
		if !strings.EqualFold(deployment.Format, "OpenAI") || deployment.Sku.Name != "Standard" {
			return "", definition, fmt.Errorf("standalone OpenAI models require the OpenAI format and Standard SKU")
		}
		if deployment.Capacity != *deploymentOptions.Capacity {
			return "", definition, fmt.Errorf("standalone OpenAI models require a capacity of 20")
		}
		definition.Model = resources.AIModelPropsModel{Name: deployment.ModelName, Version: deployment.Version}
		name, err = w.resourceName(ctx, w.uniqueName(model.Name))
		return name, definition, err
	}
	if strings.TrimSpace(deployment.Sku.UsageName) == "" {
		return "", definition, fmt.Errorf("the selected Foundry deployment requires a SKU usage name")
	}
	added := resources.AiServicesModel{
		Name: deployment.ModelName, Version: deployment.Version, Format: deployment.Format,
		Sku: resources.AiServicesModelSku{
			Name: deployment.Sku.Name, UsageName: deployment.Sku.UsageName, Capacity: deployment.Capacity,
		},
	}
	if slices.ContainsFunc(definition.Models, func(other resources.AiServicesModel) bool {
		return strings.EqualFold(scaffold.BicepName(other.Name), scaffold.BicepName(added.Name))
	}) {
		return "", definition, fmt.Errorf("the selected model deployment is already configured in %s", name)
	}
	definition.Models = append(definition.Models, added)
	if name == "" {
		name = w.uniqueName("ai-project")
	}
	return name, definition, nil
}
