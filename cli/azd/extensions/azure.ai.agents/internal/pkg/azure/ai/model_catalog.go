// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

type AiModel struct {
	Name                   string
	ModelDetailsByLocation map[string][]*armcognitiveservices.Model
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

func (c *ModelCatalogService) ListAllCapabilities(ctx context.Context, allModels map[string]*AiModel) []string {
	return filterDistinctModelData(allModels, func(m *armcognitiveservices.Model) []string {
		capabilities := []string{}
		for key := range m.Model.Capabilities {
			capabilities = append(capabilities, key)
		}

		return capabilities
	})
}

func (c *ModelCatalogService) ListAllStatuses(ctx context.Context, allModels map[string]*AiModel) []string {
	return filterDistinctModelData(allModels, func(m *armcognitiveservices.Model) []string {
		return []string{string(*m.Model.LifecycleStatus)}
	})
}

func (c *ModelCatalogService) ListAllFormats(ctx context.Context, allModels map[string]*AiModel) []string {
	return filterDistinctModelData(allModels, func(m *armcognitiveservices.Model) []string {
		return []string{*m.Model.Format}
	})
}

func (c *ModelCatalogService) ListAllKinds(ctx context.Context, allModels map[string]*AiModel) []string {
	return filterDistinctModelData(allModels, func(m *armcognitiveservices.Model) []string {
		return []string{*m.Kind}
	})
}

func (c *ModelCatalogService) ListModelVersions(ctx context.Context, model *AiModel, location string) ([]string, string, error) {
	versions := make(map[string]struct{})
	defaultVersion := ""

	models, exists := model.ModelDetailsByLocation[location]
	if !exists {
		return nil, "", fmt.Errorf("no model details found for location '%s'", location)
	}

	for _, m := range models {
		versions[*m.Model.Version] = struct{}{}
		if m.Model.IsDefaultVersion != nil && *m.Model.IsDefaultVersion {
			defaultVersion = *m.Model.Version
		}
	}

	versionList := make([]string, 0, len(versions))
	for version := range versions {
		versionList = append(versionList, version)
	}

	slices.Sort(versionList)

	return versionList, defaultVersion, nil
}

func (c *ModelCatalogService) ListModelSkus(ctx context.Context, model *AiModel, location string, modelVersion string) ([]string, error) {
	skus := make(map[string]struct{})

	models, exists := model.ModelDetailsByLocation[location]
	if !exists {
		return nil, fmt.Errorf("no model details found for location '%s'", location)
	}

	for _, m := range models {
		if *m.Model.Version == modelVersion {
			for _, sku := range m.Model.SKUs {
				skus[*sku.Name] = struct{}{}
			}
		}
	}

	skuList := make([]string, 0, len(skus)) // Create with capacity, not length
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

func (c *ModelCatalogService) ListFilteredModels(
	ctx context.Context,
	allModels map[string]*AiModel,
	options *FilterOptions,
) []*AiModel {
	if options == nil {
		options = &FilterOptions{}
	}

	filteredModels := []*AiModel{}

	for _, model := range allModels {
		// Initialize flags to true if the corresponding filter is not provided.
		isCapabilityMatch := len(options.Capabilities) == 0
		isLocationMatch := len(options.Locations) == 0
		isStatusMatch := len(options.Statuses) == 0
		isFormatMatch := len(options.Formats) == 0
		isKindMatch := len(options.Kinds) == 0

		for locationName, models := range model.ModelDetailsByLocation {
			for _, m := range models {
				if !isCapabilityMatch && len(options.Capabilities) > 0 {
					for modelCapability := range m.Model.Capabilities {
						if slices.Contains(options.Capabilities, modelCapability) {
							isCapabilityMatch = true
							break
						}
					}
				}

				if !isLocationMatch && len(options.Locations) > 0 &&
					slices.Contains(options.Locations, locationName) {
					isLocationMatch = true
				}

				if !isStatusMatch && len(options.Statuses) > 0 &&
					slices.Contains(options.Statuses, string(*m.Model.LifecycleStatus)) {
					isStatusMatch = true
				}

				if !isFormatMatch && len(options.Formats) > 0 &&
					slices.Contains(options.Formats, *m.Model.Format) {
					isFormatMatch = true
				}

				if !isKindMatch && len(options.Kinds) > 0 &&
					slices.Contains(options.Kinds, *m.Kind) {
					isKindMatch = true
				}
			}
		}

		if isLocationMatch && isCapabilityMatch && isFormatMatch && isStatusMatch && isKindMatch {
			filteredModels = append(filteredModels, model)
		}
	}

	// Sort the filtered models by name
	slices.SortFunc(filteredModels, func(a, b *AiModel) int {
		return strings.Compare(a.Name, b.Name)
	})

	return filteredModels
}

func (c *ModelCatalogService) ListAllModels(ctx context.Context, subscriptionId string, location string) (map[string]*AiModel, error) {
	var locations []*armsubscriptions.Location
	var err error

	if location == "" {
		// If no specific location provided, get all locations
		locations, err = c.azureClient.ListLocations(ctx, subscriptionId)
		if err != nil {
			return nil, err
		}
	} else {
		// If specific location provided, create a single-item slice with that location
		locations = []*armsubscriptions.Location{
			{
				Name: &location,
			},
		}
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
					Name:                   modelName,
					ModelDetailsByLocation: make(map[string][]*armcognitiveservices.Model),
				}
			}

			locationName := *location.Name
			existingModel.ModelDetailsByLocation[locationName] = append(existingModel.ModelDetailsByLocation[locationName], model)

			modelMap[modelName] = existingModel
		}

		return true
	})

	return modelMap, nil
	// allModels := []*AiModel{}
	// for _, model := range modelMap {
	// 	allModels = append(allModels, model)
	// }

	// slices.SortFunc(allModels, func(a, b *AiModel) int {
	// 	return strings.Compare(a.Name, b.Name)
	// })

	// return allModels, nil
}

type AiModelDeployment struct {
	Name    string
	Format  string
	Version string
	Sku     AiModelDeploymentSku
}

type AiModelDeploymentSku struct {
	Name      string
	UsageName string
	Capacity  int32
}

type AiModelDeploymentOptions struct {
	Locations []string
	Versions  []string
	Skus      []string
}

func (c *ModelCatalogService) GetModelDeployment(
	ctx context.Context,
	model *AiModel,
	options *AiModelDeploymentOptions,
) (*AiModelDeployment, error) {
	if options == nil {
		options = &AiModelDeploymentOptions{
			Skus: []string{
				"GlobalStandard",
				"Standard",
			},
		}
	}

	var modelDeployment *AiModelDeployment
	hasDefaultVersion := c.hasDefaultVersion(model)

	for locationName, models := range model.ModelDetailsByLocation {
		if modelDeployment != nil {
			break
		}

		// Check for location match if specified
		if len(options.Locations) > 0 && !slices.Contains(options.Locations, locationName) {
			continue
		}

		for _, m := range models {
			if modelDeployment != nil {
				break
			}

			// Check for version match if specified
			if len(options.Versions) > 0 && !slices.Contains(options.Versions, *m.Model.Version) {
				continue
			}

			// Check for default version if no version is specified
			if len(options.Versions) > 0 {
				if !slices.Contains(options.Versions, *m.Model.Version) {
					continue
				}
			} else if hasDefaultVersion {
				// Not all models have a default version
				if m.Model.IsDefaultVersion != nil && !*m.Model.IsDefaultVersion {
					continue
				}
			}

			// Check for SKU match if specified
			for _, sku := range m.Model.SKUs {

				if !slices.Contains(options.Skus, *sku.Name) {
					continue
				}

				modelDeployment = &AiModelDeployment{
					Name:    *m.Model.Name,
					Format:  *m.Model.Format,
					Version: *m.Model.Version,
					Sku: AiModelDeploymentSku{
						Name:      *sku.Name,
						UsageName: *sku.UsageName,
					},
				}

				if sku.Capacity.Default != nil {
					modelDeployment.Sku.Capacity = *sku.Capacity.Default
				} else {
					modelDeployment.Sku.Capacity = -1
				}

				break
			}
		}
	}

	if modelDeployment == nil {
		return nil, errors.New("No model deployment found for the specified options")
	}

	return modelDeployment, nil
}

func (c *ModelCatalogService) hasDefaultVersion(model *AiModel) bool {
	for _, models := range model.ModelDetailsByLocation {
		for _, m := range models {
			if m.Model.IsDefaultVersion != nil && *m.Model.IsDefaultVersion {
				return true
			}
		}
	}
	return false
}

func filterDistinctModelData(
	models map[string]*AiModel,
	filterFunc func(*armcognitiveservices.Model) []string,
) []string {
	filtered := make(map[string]struct{})
	for _, model := range models {
		for _, locationModels := range model.ModelDetailsByLocation {
			for _, m := range locationModels {
				value := filterFunc(m)
				for _, v := range value {
					if v != "" {
						filtered[v] = struct{}{}
					}
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
	client, err := armcognitiveservices.NewModelsClient(subscriptionId, credential, azure.NewArmClientOptions())
	if err != nil {
		return nil, err
	}

	return client, nil
}
