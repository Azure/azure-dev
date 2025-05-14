// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func (a *AddAction) selectSearch(
	console input.Console,
	ctx context.Context,
	_ PromptOptions) (*project.ResourceConfig, error) {
	r := &project.ResourceConfig{}
	r.Type = project.ResourceTypeAiSearch
	return r, nil
}

func (a *AddAction) selectOpenAi(
	console input.Console,
	ctx context.Context,
	_ PromptOptions) (*project.ResourceConfig, error) {
	r := &project.ResourceConfig{}
	r.Type = project.ResourceTypeOpenAiModel
	return r, nil
}

func (a *AddAction) promptOpenAi(
	console input.Console,
	ctx context.Context,
	r *project.ResourceConfig,
	_ PromptOptions) (*project.ResourceConfig, error) {
	aiOption, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Which type of Azure OpenAI service?",
		Options: []string{
			"Chat (GPT)",                   // 0 - chat
			"Embeddings (Document search)", // 1 - embeddings
		}})
	if err != nil {
		return nil, err
	}

	var allModels []ModelList
	for {
		err = provisioning.EnsureSubscriptionAndLocation(
			ctx, a.envManager, a.env, a.prompter, provisioning.EnsureSubscriptionAndLocationOptions{})
		if err != nil {
			return nil, err
		}

		console.ShowSpinner(
			ctx,
			fmt.Sprintf("Fetching available models in %s...", a.env.GetLocation()),
			input.Step)

		supportedModels, err := a.supportedModelsInLocation(ctx, a.env.GetSubscriptionId(), a.env.GetLocation())
		if err != nil {
			return nil, err
		}
		console.StopSpinner(ctx, "", input.Step)

		for _, model := range supportedModels {
			if model.Kind == "OpenAI" && slices.ContainsFunc(model.Model.Skus, func(sku ModelSku) bool {
				return sku.Name == "Standard"
			}) {
				switch aiOption {
				case 0:
					if model.Model.Name == "gpt-4o" || model.Model.Name == "gpt-4" {
						allModels = append(allModels, model)
					}
				case 1:
					if strings.HasPrefix(model.Model.Name, "text-embedding") {
						allModels = append(allModels, model)
					}
				}
			}

		}
		if len(allModels) > 0 {
			break
		}

		_, err = a.rm.FindResourceGroupForEnvironment(
			ctx, a.env.GetSubscriptionId(), a.env.Name())
		var notFoundError *azureutil.ResourceNotFoundError
		if errors.As(err, &notFoundError) { // not yet provisioned, we're safe here
			console.MessageUxItem(ctx, &ux.WarningMessage{
				Description: fmt.Sprintf("No models found in %s", a.env.GetLocation()),
			})
			confirm, err := console.Confirm(ctx, input.ConsoleOptions{
				Message: "Try a different location?",
			})
			if err != nil {
				return nil, err
			}
			if confirm {
				a.env.SetLocation("")
				continue
			}
		} else if err != nil {
			return nil, fmt.Errorf("finding resource group: %w", err)
		}

		return nil, fmt.Errorf("no models found in %s", a.env.GetLocation())
	}

	slices.SortFunc(allModels, func(a ModelList, b ModelList) int {
		return strings.Compare(b.Model.SystemData.CreatedAt, a.Model.SystemData.CreatedAt)
	})

	displayModels := make([]string, 0, len(allModels))
	models := make([]Model, 0, len(allModels))
	for _, model := range allModels {
		models = append(models, model.Model)
		displayModels = append(displayModels, fmt.Sprintf("%s\t%s", model.Model.Name, model.Model.Version))
	}

	if console.IsSpinnerInteractive() {
		displayModels, err = output.TabAlign(displayModels, 5)
		if err != nil {
			return nil, fmt.Errorf("writing models: %w", err)
		}
	}

	sel, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Select the model",
		Options: displayModels,
	})
	if err != nil {
		return nil, err
	}

	r.Props = project.AIModelProps{
		Model: project.AIModelPropsModel{
			Name:    models[sel].Name,
			Version: models[sel].Version,
		},
	}

	return r, nil
}

func (a *AddAction) supportedModelsInLocation(ctx context.Context, subId, location string) ([]ModelList, error) {
	models, err := a.azureClient.GetAiModels(ctx, subId, location)
	if err != nil {
		return nil, fmt.Errorf("getting models: %w", err)
	}
	var modelList []ModelList
	for _, model := range models {
		var skus []ModelSku
		for _, sku := range model.Model.SKUs {
			skus = append(skus, ModelSku{
				Name:      *sku.Name,
				UsageName: *sku.UsageName,
				Capacity: ModelSkuCapacity{
					Maximum: convert.ToValueWithDefault(sku.Capacity.Maximum, 0),
					Minimum: convert.ToValueWithDefault(sku.Capacity.Minimum, 0),
					Step:    convert.ToValueWithDefault(sku.Capacity.Step, 0),
					Default: convert.ToValueWithDefault(sku.Capacity.Default, 0),
				},
			})
		}
		modelList = append(modelList, ModelList{
			Kind: *model.Kind,
			Model: Model{
				Name:    *model.Model.Name,
				Skus:    skus,
				Version: *model.Model.Version,
				SystemData: ModelSystemData{
					CreatedAt: model.Model.SystemData.CreatedAt.String(),
				},
				Format:           *model.Model.Format,
				IsDefaultVersion: *model.Model.IsDefaultVersion,
			},
		})
	}
	return modelList, nil
}

type ModelResponse struct {
	Value    []ModelList `json:"value"`
	NextLink *string     `json:"nextLink"`
}

type ModelList struct {
	Kind  string `json:"kind"`
	Model Model  `json:"model"`
}

type Model struct {
	Name             string          `json:"name"`
	Skus             []ModelSku      `json:"skus"`
	Version          string          `json:"version"`
	SystemData       ModelSystemData `json:"systemData"`
	Format           string          `json:"format"`
	IsDefaultVersion bool            `json:"isDefaultVersion"`
}

type ModelSku struct {
	Name      string           `json:"name"`
	UsageName string           `json:"usageName"`
	Capacity  ModelSkuCapacity `json:"capacity"`
}

type ModelSkuCapacity struct {
	Maximum int32 `json:"maximum"`
	Minimum int32 `json:"minimum"`
	Step    int32 `json:"step"`
	Default int32 `json:"default"`
}

type ModelSystemData struct {
	CreatedAt string `json:"createdAt"`
}

func (a *AddAction) selectAiModel(
	console input.Console,
	ctx context.Context,
	p PromptOptions) (*project.ResourceConfig, error) {
	r := &project.ResourceConfig{}
	r.Type = project.ResourceTypeAiProject
	return r, nil
}

func (a *AddAction) promptAiModel(
	console input.Console,
	ctx context.Context,
	r *project.ResourceConfig,
	p PromptOptions) (*project.ResourceConfig, error) {
	// check if there are models in the project already
	aiProject := project.AiFoundryModelProps{}
	for _, resource := range p.PrjConfig.Resources {
		if resource.Type == project.ResourceTypeAiProject && resource.Name == "ai-project" {
			em, castOk := resource.Props.(project.AiFoundryModelProps)
			if !castOk {
				return nil, fmt.Errorf("invalid resource properties")
			}
			r.Name = resource.Name
			aiProject = em
			r.Props = aiProject
			break
		}
	}

	modelCatalog, err := a.aiDeploymentCatalog(ctx, a.env.GetSubscriptionId(), aiProject.Models)
	if err != nil {
		return nil, err
	}

	modelNameSelection, m, err := selectFromMap(ctx, console, "Which model do you want to use?", modelCatalog, nil)
	if err != nil {
		return nil, err
	}
	_, k, err := selectFromMap(ctx, console, "Which deployment kind do you want to use?", m.Kinds, nil)
	if err != nil {
		return nil, err
	}

	modelVersionSelection, modelDefinition, err := selectFromMap(
		ctx, console, "Which model version do you want to use?", k.Versions, nil /*defVersion*/)
	if err != nil {
		return nil, err
	}
	skuSelection, err := selectFromSkus(ctx, console, "Select model SKU", modelDefinition.Model.Skus)
	if err != nil {
		return nil, err
	}

	aiProject.Models = append(aiProject.Models, project.AiServicesModel{
		Name:    modelNameSelection,
		Version: modelVersionSelection,
		Format:  modelDefinition.Model.Format,
		Sku: project.AiServicesModelSku{
			Name:      skuSelection.Name,
			UsageName: skuSelection.UsageName,
			Capacity:  skuSelection.Capacity.Default,
		},
	})
	r.Props = aiProject
	return r, nil
}

func selectFromMap[T any](
	ctx context.Context, console input.Console, q string, m map[string]T, defaultOpt *string) (string, T, error) {
	mIterator := maps.Keys(m)
	var options []string
	var value T
	for option := range mIterator {
		options = append(options, option)
	}
	if len(options) == 1 {
		key := options[0]
		return key, m[key], nil
	}
	defOpt := options[0]
	if defaultOpt != nil {
		defOpt = *defaultOpt
	}
	slices.Sort(options)
	selectedIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message:      q,
		Options:      options,
		DefaultValue: defOpt,
	})
	if err != nil {
		return "", value, err
	}
	key := options[selectedIndex]
	return key, m[key], nil
}

func selectFromSkus(ctx context.Context, console input.Console, q string, s []ModelSku) (ModelSku, error) {
	var sku ModelSku
	if len(s) == 0 {
		return sku, fmt.Errorf("no skus found")
	}
	if len(s) == 1 {
		return s[0], nil
	}
	var options []string
	for _, option := range s {
		options = append(options, option.Name)
	}
	selectedIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message:      q,
		Options:      options,
		DefaultValue: options[0],
	})
	if err != nil {
		return sku, err
	}
	return s[selectedIndex], nil
}

func (a *AddAction) aiDeploymentCatalog(
	ctx context.Context, subId string, excludeModels []project.AiServicesModel) (map[string]ModelCatalogKind, error) {
	allLocations, err := a.accountManager.GetLocations(ctx, subId)
	if err != nil {
		return nil, fmt.Errorf("getting locations: %w", err)
	}

	var sharedResults sync.Map
	var wg sync.WaitGroup

	a.console.ShowSpinner(ctx, "Retrieving available models...", input.Step)

	for _, location := range allLocations {
		wg.Add(1)
		go func(location string) {
			defer wg.Done()
			results, err := a.supportedModelsInLocation(ctx, subId, location)
			if err != nil {
				// log the error and continue. Do not fail the entire operation when pulling location error
				log.Println("error getting models in location", location, ":", err, "skipping")
				return
			}
			var filterSkusWithZeroCapacity []ModelList
			for _, model := range results {
				if len(model.Model.Skus) == 0 {
					continue
				}
				var skus []ModelSku
				for _, sku := range model.Model.Skus {
					if sku.Capacity.Default > 0 {
						skus = append(skus, sku)
					}
				}
				if len(skus) == 0 {
					continue
				}
				model.Model.Skus = skus
				filterSkusWithZeroCapacity = append(filterSkusWithZeroCapacity, model)
			}
			sharedResults.Store(location, filterSkusWithZeroCapacity)
		}(location.Name)
	}
	wg.Wait()
	a.console.StopSpinner(ctx, "", input.StepDone)

	combinedResults := map[string]ModelCatalogKind{}
	sharedResults.Range(func(key, value any) bool {
		// cast should be safe as the call to sharedResults.Store() use a string key
		locationNameKey := key.(string)
		models := value.([]ModelList)
		for _, model := range models {
			if model.Kind == "OpenAI" {
				// OpenAI kind is part of the `Add OpenAI` where clients connect directly to the service w/o an AIProject
				continue
			}
			nameKey := model.Model.Name
			// check if model is in the exclude list
			if slices.ContainsFunc(excludeModels, func(m project.AiServicesModel) bool {
				return nameKey == m.Name &&
					model.Model.Format == m.Format &&
					model.Model.Version == m.Version &&
					slices.ContainsFunc(model.Model.Skus, func(sku ModelSku) bool { return sku.Name == m.Sku.Name })
			}) {
				// skip this model as it is in the exclude list
				// exclude list is used to remove models which might have been added to the project already
				// This validation is also blocking adding same model with different sku
				continue
			}
			kindKey := model.Kind
			versionKey := model.Model.Version
			modelKey, exists := combinedResults[nameKey]
			if !exists {
				combinedResults[nameKey] = ModelCatalogKind{
					Kinds: map[string]ModelCatalogVersions{
						kindKey: {
							Versions: map[string]ModelCatalog{
								versionKey: {
									ModelList: model,
									Locations: []string{locationNameKey},
								},
							},
						},
					},
				}
			} else {
				// nameKey exists - check if kindKey exists
				kindKeyMap, kindExists := modelKey.Kinds[kindKey]
				if !kindExists {
					// kindKey does not exist - add it
					modelKey.Kinds[kindKey] = ModelCatalogVersions{
						Versions: map[string]ModelCatalog{
							versionKey: {
								ModelList: model,
								Locations: []string{locationNameKey},
							},
						},
					}
				} else {
					// kindKey exists - check if versionKey exists
					versionList, versionExists := kindKeyMap.Versions[versionKey]
					if !versionExists {
						// versionKey does not exist - add it
						kindKeyMap.Versions[versionKey] = ModelCatalog{
							ModelList: model,
							Locations: []string{locationNameKey},
						}
					} else {
						// versionKey exists - add location to existing version
						versionList.Locations = append(versionList.Locations, locationNameKey)
						kindKeyMap.Versions[versionKey] = versionList
					}
					modelKey.Kinds[kindKey] = kindKeyMap
				}
				// update the combinedResults map
				combinedResults[nameKey] = modelKey
			}
		}
		return true
	})
	return combinedResults, nil
}

type ModelCatalog struct {
	ModelList
	Locations []string
}

type ModelCatalogKind struct {
	Kinds map[string]ModelCatalogVersions
}

type ModelCatalogVersions struct {
	Versions map[string]ModelCatalog
}
