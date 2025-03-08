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

type AiModelLocation struct {
	Model    *armcognitiveservices.Model
	Location *armsubscriptions.Location
}

type ModelCatalog struct {
	credential  azcore.TokenCredential
	azureClient *azure.AzureClient
}

func NewModelCatalog(credential azcore.TokenCredential) *ModelCatalog {
	return &ModelCatalog{
		credential:  credential,
		azureClient: azure.NewAzureClient(credential),
	}
}

func (c *ModelCatalog) ListAllCapabilities(ctx context.Context, models []*AiModel) []string {
	return filterDistinctModelData(models, func(m *armcognitiveservices.Model) []string {
		capabilities := []string{}
		for key := range m.Model.Capabilities {
			capabilities = append(capabilities, key)
		}

		return capabilities
	})
}

func (c *ModelCatalog) ListAllStatuses(ctx context.Context, models []*AiModel) []string {
	return filterDistinctModelData(models, func(m *armcognitiveservices.Model) []string {
		return []string{string(*m.Model.LifecycleStatus)}
	})
}

func (c *ModelCatalog) ListAllFormats(ctx context.Context, models []*AiModel) []string {
	return filterDistinctModelData(models, func(m *armcognitiveservices.Model) []string {
		return []string{*m.Model.Format}
	})
}

func (c *ModelCatalog) ListAllKinds(ctx context.Context, models []*AiModel) []string {
	return filterDistinctModelData(models, func(m *armcognitiveservices.Model) []string {
		return []string{*m.Kind}
	})
}

func (c *ModelCatalog) ListModelVersions(ctx context.Context, model *AiModel) ([]string, error) {
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

func (c *ModelCatalog) ListModelSkus(ctx context.Context, model *AiModel) ([]string, error) {
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

func (c *ModelCatalog) ListFilteredModels(ctx context.Context, allModels []*AiModel, options *FilterOptions) []*AiModel {
	if options == nil {
		return allModels
	}

	filteredModels := []*AiModel{}

	for _, model := range allModels {
		for _, location := range model.Locations {
			if len(options.Capabilities) > 0 {
				for _, capability := range options.Capabilities {
					if _, exists := location.Model.Model.Capabilities[capability]; !exists {
						continue
					}
				}
			}

			if len(options.Locations) > 0 && !slices.Contains(options.Locations, *location.Location.Name) {
				continue
			}

			if len(options.Statuses) > 0 && slices.Contains(options.Statuses, string(*location.Model.Model.LifecycleStatus)) {
				continue
			}

			if len(options.Formats) > 0 && slices.Contains(options.Formats, *location.Model.Model.Format) {
				continue
			}

			if len(options.Kinds) > 0 && slices.Contains(options.Kinds, *location.Model.Kind) {
				continue
			}
		}

		filteredModels = append(filteredModels, model)
	}

	return filteredModels
}

func (c *ModelCatalog) ListAllModels(ctx context.Context, subscriptionId string) ([]*AiModel, error) {
	locations, err := c.azureClient.ListLocation(ctx, subscriptionId)
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
