// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.ai.builder/internal/pkg/azure"
)

type AiModel struct {
	Name      string
	Locations []*AiModelLocation
}

type AiModelDescription struct {
	Name         string
	Format       string
	Kind         string
	Capabilities []string
	Status       string
	Locations    []string
	SKUs         []string
}

type AiModelLocation struct {
	Model    *armcognitiveservices.Model
	Location *armsubscriptions.Location
}

type ModelCatalogService struct {
	credential  azcore.TokenCredential
	azureClient *azure.AzureClient
}

func NewModelCatalogService(credential azcore.TokenCredential) *ModelCatalogService {
	return &ModelCatalogService{
		credential:  credential,
		azureClient: azure.NewAzureClient(credential),
	}
}

func (c *ModelCatalogService) ListAllCapabilities(ctx context.Context, models []*AiModel) []string {
	return filterDistinctModelData(models, func(m *armcognitiveservices.Model) []string {
		capabilities := []string{}
		for key := range m.Model.Capabilities {
			capabilities = append(capabilities, key)
		}

		return capabilities
	})
}

func (c *ModelCatalogService) ListAllStatuses(ctx context.Context, models []*AiModel) []string {
	return filterDistinctModelData(models, func(m *armcognitiveservices.Model) []string {
		return []string{string(*m.Model.LifecycleStatus)}
	})
}

func (c *ModelCatalogService) ListAllFormats(ctx context.Context, models []*AiModel) []string {
	return filterDistinctModelData(models, func(m *armcognitiveservices.Model) []string {
		return []string{*m.Model.Format}
	})
}

func (c *ModelCatalogService) ListAllKinds(ctx context.Context, models []*AiModel) []string {
	return filterDistinctModelData(models, func(m *armcognitiveservices.Model) []string {
		return []string{*m.Kind}
	})
}

func (c *ModelCatalogService) ListModelVersions(ctx context.Context, model *AiModel) ([]string, error) {
	versions := make(map[string]struct{})
	for _, location := range model.Locations {
		versions[*location.Model.Model.Version] = struct{}{}
	}

	versionList := make([]string, len(versions))
	for version := range versions {
		versionList = append(versionList, version)
	}

	slices.Sort(versionList)

	return versionList, nil
}

func (c *ModelCatalogService) ListModelSkus(ctx context.Context, model *AiModel) ([]string, error) {
	skus := make(map[string]struct{})
	for _, location := range model.Locations {
		for _, sku := range location.Model.Model.SKUs {
			skus[*sku.Name] = struct{}{}
		}
	}

	skuList := make([]string, len(skus))
	for sku := range skus {
		skuList = append(skuList, sku)
	}

	slices.Sort(skuList)

	return skuList, nil
}

type FilterOptions struct {
	Capabilities []string
	Statuses     []string
	Formats      []string
	Kinds        []string
	Locations    []string
}

func (c *ModelCatalogService) ListFilteredModels(ctx context.Context, allModels []*AiModel, options *FilterOptions) []*AiModel {
	if options == nil {
		return allModels
	}

	filteredModels := []*AiModel{}

	for _, model := range allModels {
		// Initialize flags to true if the corresponding filter is not provided.
		isCapabilityMatch := len(options.Capabilities) == 0
		isLocationMatch := len(options.Locations) == 0
		isStatusMatch := len(options.Statuses) == 0
		isFormatMatch := len(options.Formats) == 0
		isKindMatch := len(options.Kinds) == 0

		for _, location := range model.Locations {
			if !isCapabilityMatch && len(options.Capabilities) > 0 {
				for modelCapability := range location.Model.Model.Capabilities {
					if slices.Contains(options.Capabilities, modelCapability) {
						isCapabilityMatch = true
						break
					}
				}
			}

			if !isLocationMatch && len(options.Locations) > 0 && slices.Contains(options.Locations, *location.Location.Name) {
				isLocationMatch = true
			}

			if !isStatusMatch && len(options.Statuses) > 0 &&
				slices.Contains(options.Statuses, string(*location.Model.Model.LifecycleStatus)) {
				isStatusMatch = true
			}

			if !isFormatMatch && len(options.Formats) > 0 && slices.Contains(options.Formats, *location.Model.Model.Format) {
				isFormatMatch = true
			}

			if !isKindMatch && len(options.Kinds) > 0 && slices.Contains(options.Kinds, *location.Model.Kind) {
				isKindMatch = true
			}
		}

		if isLocationMatch && isCapabilityMatch && isFormatMatch && isStatusMatch && isKindMatch {
			filteredModels = append(filteredModels, model)
		}
	}

	return filteredModels
}

func (c *ModelCatalogService) ListAllModels(ctx context.Context, subscriptionId string) ([]*AiModel, error) {
	locations, err := c.azureClient.ListLocations(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	modelsClient, err := createModelsClient(subscriptionId, c.credential)
	if err != nil {
		return nil, err
	}

	var locationResults sync.Map
	var wg sync.WaitGroup

	for _, location := range locations {
		wg.Add(1)
		go func(location *armsubscriptions.Location) {
			defer wg.Done()
			pager := modelsClient.NewListPager(*location.Name, nil)

			results := []*armcognitiveservices.Model{}

			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					return
				}

				results = append(results, page.Value...)
			}

			locationResults.Store(location, results)
		}(location)
	}

	wg.Wait()

	modelMap := map[string]*AiModel{}

	locationResults.Range(func(key, value any) bool {
		location := key.(*armsubscriptions.Location)
		modelsList := value.([]*armcognitiveservices.Model)

		for _, model := range modelsList {
			modelName := *model.Model.Name
			existingModel, exists := modelMap[modelName]
			if !exists {
				existingModel = &AiModel{
					Name:      modelName,
					Locations: []*AiModelLocation{},
				}
			}

			existingModel.Locations = append(existingModel.Locations, &AiModelLocation{
				Model:    model,
				Location: location,
			})

			modelMap[modelName] = existingModel
		}

		return true
	})

	allModels := []*AiModel{}
	for _, model := range modelMap {
		allModels = append(allModels, model)
	}

	slices.SortFunc(allModels, func(a, b *AiModel) int {
		return strings.Compare(a.Name, b.Name)
	})

	return allModels, nil
}

func filterDistinctModelData(
	models []*AiModel,
	filterFunc func(*armcognitiveservices.Model) []string,
) []string {
	filtered := make(map[string]struct{})
	for _, model := range models {
		for _, location := range model.Locations {
			value := filterFunc(location.Model)
			for _, v := range value {
				if v != "" {
					filtered[v] = struct{}{}
				}
			}
		}
	}

	list := make([]string, len(filtered))
	i := 0
	for value := range filtered {
		list[i] = value
		i++
	}

	slices.Sort(list)
	return list
}

func createModelsClient(
	subscriptionId string,
	credential azcore.TokenCredential,
) (*armcognitiveservices.ModelsClient, error) {
	client, err := armcognitiveservices.NewModelsClient(subscriptionId, credential, nil)
	if err != nil {
		return nil, err
	}

	return client, nil
}
