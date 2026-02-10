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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
)

// AiModelService provides operations for querying AI model availability,
// resolving deployments, and checking quota/usage from Azure Cognitive Services.
type AiModelService struct {
	azureClient    *azapi.AzureClient
	subManager     *account.SubscriptionsManager
	catalogCacheMu sync.RWMutex
	catalogCache   map[string][]*armcognitiveservices.Model // key: "subscriptionId:location"
}

// NewAiModelService creates a new AiModelService.
func NewAiModelService(
	azureClient *azapi.AzureClient,
	subManager *account.SubscriptionsManager,
) *AiModelService {
	return &AiModelService{
		azureClient:  azureClient,
		subManager:   subManager,
		catalogCache: make(map[string][]*armcognitiveservices.Model),
	}
}

// ListModels fetches AI models from the Azure Cognitive Services catalog.
// If locations is empty, fetches across all subscription locations in parallel.
func (s *AiModelService) ListModels(
	ctx context.Context,
	subscriptionId string,
	locations []string,
) ([]AiModel, error) {
	if len(locations) == 0 {
		resolvedLocations, err := s.ListLocations(ctx, subscriptionId)
		if err != nil {
			return nil, err
		}

		locations = resolvedLocations
	}

	rawModels, err := s.fetchModelsForLocations(ctx, subscriptionId, locations)
	if err != nil {
		return nil, err
	}

	return s.convertToAiModels(rawModels), nil
}

// ListLocations returns subscription location names that can be used for model queries.
func (s *AiModelService) ListLocations(
	ctx context.Context,
	subscriptionId string,
) ([]string, error) {
	subLocations, err := s.subManager.GetLocations(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("listing locations: %w", err)
	}

	locations := make([]string, 0, len(subLocations))
	for _, loc := range subLocations {
		locations = append(locations, loc.Name)
	}

	return locations, nil
}

// ListFilteredModels fetches and filters AI models based on the provided criteria.
func (s *AiModelService) ListFilteredModels(
	ctx context.Context,
	subscriptionId string,
	options *FilterOptions,
) ([]AiModel, error) {
	// Fetch canonical models and apply filters in-memory so model metadata
	// (especially Locations) remains complete.
	models, err := s.ListModels(ctx, subscriptionId, nil)
	if err != nil {
		return nil, err
	}

	return FilterModels(models, options), nil
}

// ListModelVersions returns available versions for a specific model at a location.
func (s *AiModelService) ListModelVersions(
	ctx context.Context,
	subscriptionId string,
	modelName string,
	location string,
) ([]AiModelVersion, string, error) {
	models, err := s.ListModels(ctx, subscriptionId, []string{location})
	if err != nil {
		return nil, "", err
	}

	for _, model := range models {
		if model.Name == modelName {
			defaultVersion := ""
			for _, v := range model.Versions {
				if v.IsDefault {
					defaultVersion = v.Version
					break
				}
			}
			return model.Versions, defaultVersion, nil
		}
	}

	return nil, "", fmt.Errorf("model %q not found at location %q", modelName, location)
}

// ListModelSkus returns available SKUs for a model+version at a location.
func (s *AiModelService) ListModelSkus(
	ctx context.Context,
	subscriptionId string,
	modelName string,
	location string,
	version string,
) ([]AiModelSku, error) {
	versions, _, err := s.ListModelVersions(ctx, subscriptionId, modelName, location)
	if err != nil {
		return nil, err
	}

	for _, v := range versions {
		if v.Version == version {
			return v.Skus, nil
		}
	}

	return nil, fmt.Errorf("version %q not found for model %q at %q", version, modelName, location)
}

// ResolveModelDeployments returns all valid deployment configurations for the given model.
// Returns multiple candidates when multiple version/SKU/location combos are valid.
// Capacity resolution: options.Capacity → SKU default → 0 (caller must handle).
func (s *AiModelService) ResolveModelDeployments(
	ctx context.Context,
	subscriptionId string,
	modelName string,
	options *DeploymentOptions,
) ([]AiModelDeployment, error) {
	return s.resolveDeployments(ctx, subscriptionId, modelName, options, nil)
}

// ResolveModelDeploymentsWithQuota resolves deployments and filters by quota.
// Skips SKUs where resolved capacity exceeds remaining quota.
// Populates RemainingQuota on results.
func (s *AiModelService) ResolveModelDeploymentsWithQuota(
	ctx context.Context,
	subscriptionId string,
	modelName string,
	options *DeploymentOptions,
	quotaOpts *QuotaCheckOptions,
) ([]AiModelDeployment, error) {
	return s.resolveDeployments(ctx, subscriptionId, modelName, options, quotaOpts)
}

// ListUsages returns quota/usage data for a location.
func (s *AiModelService) ListUsages(
	ctx context.Context,
	subscriptionId string,
	location string,
) ([]AiModelUsage, error) {
	rawUsages, err := s.azureClient.GetAiUsages(ctx, subscriptionId, location)
	if err != nil {
		return nil, fmt.Errorf("getting usages at %q: %w", location, err)
	}

	usages := make([]AiModelUsage, 0, len(rawUsages))
	for _, u := range rawUsages {
		if u.Name == nil || u.Name.Value == nil {
			continue
		}
		usages = append(usages, AiModelUsage{
			Name:         *u.Name.Value,
			CurrentValue: safeFloat64(u.CurrentValue),
			Limit:        safeFloat64(u.Limit),
		})
	}

	return usages, nil
}

// ListLocationsWithQuota returns locations with sufficient quota for all given requirements.
// When allowedLocations are provided, they are intersected with AI Services-supported locations
// to avoid querying locations where AI Services are not available.
func (s *AiModelService) ListLocationsWithQuota(
	ctx context.Context,
	subscriptionId string,
	allowedLocations []string,
	requirements []QuotaRequirement,
) ([]string, error) {
	skuLocations, err := s.azureClient.GetResourceSkuLocations(
		ctx, subscriptionId, "AIServices", "S0", "Standard", "accounts")
	if err != nil {
		return nil, fmt.Errorf("getting AI Services locations: %w", err)
	}

	if len(allowedLocations) == 0 {
		allowedLocations = skuLocations
	}

	var sharedResults sync.Map
	var wg sync.WaitGroup

	for _, loc := range allowedLocations {
		// Skip locations where AIServices is not available to avoid unnecessary usage API calls.
		if !slices.Contains(skuLocations, loc) {
			continue
		}
		wg.Add(1)
		go func(loc string) {
			defer wg.Done()
			usages, err := s.azureClient.GetAiUsages(ctx, subscriptionId, loc)
			if err != nil {
				return
			}
			sharedResults.Store(loc, usages)
		}(loc)
	}
	wg.Wait()

	var results []string
	sharedResults.Range(func(key, value any) bool {
		loc := key.(string)
		usages := value.([]*armcognitiveservices.Usage)

		for _, req := range requirements {
			minCap := req.MinCapacity
			if minCap <= 0 {
				minCap = 1
			}
			found := slices.ContainsFunc(usages, func(u *armcognitiveservices.Usage) bool {
				if u.Name == nil || u.Name.Value == nil || *u.Name.Value != req.UsageName {
					return false
				}
				remaining := safeFloat64(u.Limit) - safeFloat64(u.CurrentValue)
				return remaining >= minCap
			})
			if !found {
				return true // skip this location
			}
		}
		results = append(results, loc)
		return true
	})

	slices.Sort(results)
	return results, nil
}

// ListModelLocationsWithQuota returns model locations that have sufficient remaining quota.
// MaxRemainingQuota is the max remaining quota across the model's SKU usage names
// in each location where usage data exists.
func (s *AiModelService) ListModelLocationsWithQuota(
	ctx context.Context,
	subscriptionId string,
	modelName string,
	allowedLocations []string,
	minRemaining float64,
) ([]ModelLocationQuota, error) {
	if minRemaining <= 0 {
		minRemaining = 1
	}

	models, err := s.ListModels(ctx, subscriptionId, nil)
	if err != nil {
		return nil, err
	}

	var targetModel *AiModel
	for i := range models {
		if models[i].Name == modelName {
			targetModel = &models[i]
			break
		}
	}
	if targetModel == nil {
		return nil, fmt.Errorf("%w: %q", ErrModelNotFound, modelName)
	}

	modelLocations := targetModel.Locations
	if len(allowedLocations) > 0 {
		modelLocations = slices.DeleteFunc(slices.Clone(modelLocations), func(loc string) bool {
			return !slices.Contains(allowedLocations, loc)
		})
	}

	var sharedResults sync.Map
	var wg sync.WaitGroup

	for _, loc := range modelLocations {
		wg.Add(1)
		go func(loc string) {
			defer wg.Done()
			usages, err := s.ListUsages(ctx, subscriptionId, loc)
			if err != nil {
				return
			}
			sharedResults.Store(loc, usages)
		}(loc)
	}
	wg.Wait()

	results := []ModelLocationQuota{}
	sharedResults.Range(func(key, value any) bool {
		loc := key.(string)
		usages := value.([]AiModelUsage)
		usageMap := make(map[string]AiModelUsage, len(usages))
		for _, usage := range usages {
			usageMap[usage.Name] = usage
		}

		maxRemainingAtLocation, found := maxModelRemainingQuota(*targetModel, usageMap)
		if found && maxRemainingAtLocation >= minRemaining {
			results = append(results, ModelLocationQuota{
				Location:          loc,
				MaxRemainingQuota: maxRemainingAtLocation,
			})
		}

		return true
	})

	slices.SortFunc(results, func(a, b ModelLocationQuota) int {
		return strings.Compare(a.Location, b.Location)
	})

	return results, nil
}

// FilterModelsByQuota cross-references models' SKU usage names against usage data
// to filter out models without sufficient remaining capacity.
func FilterModelsByQuota(
	models []AiModel,
	usages []AiModelUsage,
	minRemaining float64,
) []AiModel {
	if minRemaining <= 0 {
		minRemaining = 1
	}

	usageMap := make(map[string]AiModelUsage, len(usages))
	for _, u := range usages {
		usageMap[u.Name] = u
	}

	var filtered []AiModel
	for _, model := range models {
		if modelHasQuota(model, usageMap, minRemaining) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

// FilterModelsByQuotaAcrossLocations filters models to those having sufficient quota in at least one location.
// When locations is empty, model-declared locations are used.
func (s *AiModelService) FilterModelsByQuotaAcrossLocations(
	ctx context.Context,
	subscriptionId string,
	models []AiModel,
	locations []string,
	minRemaining float64,
) ([]AiModel, error) {
	effectiveLocations := locations
	if len(effectiveLocations) == 0 {
		effectiveLocations = modelLocations(models)
	}

	usagesByLocation, err := s.listUsagesByLocation(ctx, subscriptionId, effectiveLocations)
	if err != nil {
		return nil, err
	}

	return filterModelsByAnyLocationQuota(models, usagesByLocation, minRemaining), nil
}

// resolveDeployments is the internal deployment resolution logic.
// Returns all valid deployment candidates instead of just the first match.
// No implicit defaults: when options fields are empty, no filtering is applied for that dimension.
// Location is only set on results when exactly one location is provided; otherwise left empty.
// Quota checking requires exactly one location; returns an error if quota is requested with != 1 location.
func (s *AiModelService) resolveDeployments(
	ctx context.Context,
	subscriptionId string,
	modelName string,
	options *DeploymentOptions,
	quotaOpts *QuotaCheckOptions,
) ([]AiModelDeployment, error) {
	if options == nil {
		options = &DeploymentOptions{}
	}

	// Fail explicitly if quota is requested without exactly one location.
	if quotaOpts != nil && len(options.Locations) != 1 {
		return nil, fmt.Errorf(
			"%w, got %d", ErrQuotaLocationRequired, len(options.Locations))
	}

	models, err := s.ListModels(ctx, subscriptionId, options.Locations)
	if err != nil {
		return nil, err
	}

	// Find the target model
	var targetModel *AiModel
	for i := range models {
		if models[i].Name == modelName {
			targetModel = &models[i]
			break
		}
	}
	if targetModel == nil {
		return nil, fmt.Errorf("%w: %q", ErrModelNotFound, modelName)
	}

	// Fetch quota data (guaranteed single location by check above)
	var usageMap map[string]AiModelUsage
	if quotaOpts != nil {
		usages, err := s.ListUsages(ctx, subscriptionId, options.Locations[0])
		if err != nil {
			return nil, fmt.Errorf("getting usages for quota check: %w", err)
		}
		usageMap = make(map[string]AiModelUsage, len(usages))
		for _, u := range usages {
			usageMap[u.Name] = u
		}
	}

	// Resolve: iterate versions → SKUs to collect all valid candidates.
	// No implicit version or SKU filtering — callers must pass explicit filters.
	var results []AiModelDeployment

	for _, version := range targetModel.Versions {
		if len(options.Versions) > 0 && !slices.Contains(options.Versions, version.Version) {
			continue
		}

		for _, sku := range version.Skus {
			if len(options.Skus) > 0 && !slices.Contains(options.Skus, sku.Name) {
				continue
			}

			capacity := ResolveCapacity(sku, options.Capacity)

			// Quota check
			if quotaOpts != nil && usageMap != nil {
				usage, ok := usageMap[sku.UsageName]
				if !ok {
					continue
				}

				remaining := usage.Limit - usage.CurrentValue
				minReq := quotaOpts.MinRemainingCapacity
				if minReq <= 0 {
					minReq = 1
				}
				if remaining < minReq || (capacity > 0 && float64(capacity) > remaining) {
					continue
				}
			}

			// Only set location when exactly one was provided — never guess.
			deployLocation := ""
			if len(options.Locations) == 1 {
				deployLocation = options.Locations[0]
			}

			deployment := AiModelDeployment{
				ModelName: modelName,
				Format:    targetModel.Format,
				Version:   version.Version,
				Location:  deployLocation,
				Sku:       sku,
				Capacity:  capacity,
			}

			// Populate remaining quota if available
			if quotaOpts != nil && usageMap != nil {
				usage, ok := usageMap[sku.UsageName]
				if ok {
					remaining := usage.Limit - usage.CurrentValue
					deployment.RemainingQuota = &remaining
				}
			}

			results = append(results, deployment)
		}
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("%w for model %q with the specified options", ErrNoDeploymentMatch, modelName)
	}

	return results, nil
}

// fetchModelsForLocations fetches models across multiple locations in parallel.
func (s *AiModelService) fetchModelsForLocations(
	ctx context.Context,
	subscriptionId string,
	locations []string,
) (map[string][]*armcognitiveservices.Model, error) {
	result := make(map[string][]*armcognitiveservices.Model)
	var mu sync.Mutex
	var errMu sync.Mutex
	var wg sync.WaitGroup
	errs := []error{}

	for _, loc := range locations {
		// Check cache first
		cacheKey := subscriptionId + ":" + loc
		s.catalogCacheMu.RLock()
		cached, ok := s.catalogCache[cacheKey]
		s.catalogCacheMu.RUnlock()
		if ok {
			mu.Lock()
			result[loc] = cached
			mu.Unlock()
			continue
		}

		wg.Add(1)
		go func(loc string) {
			defer wg.Done()
			models, err := s.azureClient.GetAiModels(ctx, subscriptionId, loc)
			if err != nil {
				errMu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", loc, err))
				errMu.Unlock()
				return
			}

			// Cache the result
			cacheKey := subscriptionId + ":" + loc
			s.catalogCacheMu.Lock()
			s.catalogCache[cacheKey] = models
			s.catalogCacheMu.Unlock()

			mu.Lock()
			result[loc] = models
			mu.Unlock()
		}(loc)
	}
	wg.Wait()

	if len(result) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("fetching model catalogs: %w", errors.Join(errs...))
	}

	return result, nil
}

// convertToAiModels converts raw ARM models grouped by location into domain AiModel types.
func (s *AiModelService) convertToAiModels(
	rawByLocation map[string][]*armcognitiveservices.Model,
) []AiModel {
	// Aggregate: model name → location → version → SKUs
	modelMap := make(map[string]*AiModel)

	for loc, models := range rawByLocation {
		for _, m := range models {
			if m.Model == nil || m.Model.Name == nil {
				continue
			}
			name := *m.Model.Name

			aiModel, exists := modelMap[name]
			if !exists {
				aiModel = &AiModel{
					Name:   name,
					Format: safeString(m.Model.Format),
				}
				if m.Model.LifecycleStatus != nil {
					aiModel.LifecycleStatus = string(*m.Model.LifecycleStatus)
				}
				if m.Model.Capabilities != nil {
					for key := range m.Model.Capabilities {
						aiModel.Capabilities = append(aiModel.Capabilities, key)
					}
					slices.Sort(aiModel.Capabilities)
				}
				modelMap[name] = aiModel
			}

			// Track locations
			if !slices.Contains(aiModel.Locations, loc) {
				aiModel.Locations = append(aiModel.Locations, loc)
			}

			// Build version entry
			ver := safeString(m.Model.Version)
			isDefault := m.Model.IsDefaultVersion != nil && *m.Model.IsDefaultVersion

			var skus []AiModelSku
			if m.Model.SKUs != nil {
				for _, sku := range m.Model.SKUs {
					skus = append(skus, convertSku(sku))
				}
			}

			// Find or create version in model
			versionFound := false
			for i := range aiModel.Versions {
				if aiModel.Versions[i].Version == ver {
					versionFound = true
					if isDefault {
						aiModel.Versions[i].IsDefault = true
					}
					// Merge SKUs (deduplicate by name + usage_name, since the same SKU name
					// can appear with different usage names representing different quota pools)
					for _, newSku := range skus {
						if !slices.ContainsFunc(aiModel.Versions[i].Skus, func(s AiModelSku) bool {
							return s.Name == newSku.Name && s.UsageName == newSku.UsageName
						}) {
							aiModel.Versions[i].Skus = append(aiModel.Versions[i].Skus, newSku)
						}
					}
					break
				}
			}
			if !versionFound {
				aiModel.Versions = append(aiModel.Versions, AiModelVersion{
					Version:   ver,
					IsDefault: isDefault,
					Skus:      skus,
				})
			}
		}
	}

	// Convert map to sorted slice
	result := make([]AiModel, 0, len(modelMap))
	for _, model := range modelMap {
		slices.Sort(model.Locations)
		result = append(result, *model)
	}
	slices.SortFunc(result, func(a, b AiModel) int {
		return strings.Compare(a.Name, b.Name)
	})

	return result
}

// FilterModels applies FilterOptions to a list of models.
func FilterModels(models []AiModel, options *FilterOptions) []AiModel {
	if options == nil {
		return models
	}

	var filtered []AiModel
	for _, model := range models {
		if len(options.ExcludeModelNames) > 0 && slices.Contains(options.ExcludeModelNames, model.Name) {
			continue
		}
		if len(options.Formats) > 0 && !slices.Contains(options.Formats, model.Format) {
			continue
		}
		if len(options.Statuses) > 0 && !slices.Contains(options.Statuses, model.LifecycleStatus) {
			continue
		}
		if len(options.Capabilities) > 0 {
			hasCapability := false
			for _, cap := range options.Capabilities {
				if slices.Contains(model.Capabilities, cap) {
					hasCapability = true
					break
				}
			}
			if !hasCapability {
				continue
			}
		}
		if len(options.Locations) > 0 {
			hasLocation := false
			for _, loc := range options.Locations {
				if slices.Contains(model.Locations, loc) {
					hasLocation = true
					break
				}
			}
			if !hasLocation {
				continue
			}
		}
		filtered = append(filtered, model)
	}

	return filtered
}

func convertSku(sku *armcognitiveservices.ModelSKU) AiModelSku {
	result := AiModelSku{
		Name:      safeString(sku.Name),
		UsageName: safeString(sku.UsageName),
	}
	if sku.Capacity != nil {
		if sku.Capacity.Default != nil {
			result.DefaultCapacity = *sku.Capacity.Default
		}
		if sku.Capacity.Minimum != nil {
			result.MinCapacity = *sku.Capacity.Minimum
		}
		if sku.Capacity.Maximum != nil {
			result.MaxCapacity = *sku.Capacity.Maximum
		}
		if sku.Capacity.Step != nil {
			result.CapacityStep = *sku.Capacity.Step
		}
	}
	return result
}

// ResolveCapacity resolves the deployment capacity for a SKU.
// If preferred is set and valid within the SKU's min/max/step constraints, it's used.
// Otherwise falls back to the SKU's default capacity.
func ResolveCapacity(sku AiModelSku, preferred *int32) int32 {
	if preferred != nil {
		cap := *preferred
		if cap > 0 &&
			(sku.MinCapacity <= 0 || cap >= sku.MinCapacity) &&
			(sku.MaxCapacity <= 0 || cap <= sku.MaxCapacity) &&
			(sku.CapacityStep <= 0 || cap%sku.CapacityStep == 0) {
			return cap
		}
	}
	return sku.DefaultCapacity
}

// ModelHasDefaultVersion returns true if any version of the model is marked as default.
func ModelHasDefaultVersion(model AiModel) bool {
	for _, v := range model.Versions {
		if v.IsDefault {
			return true
		}
	}
	return false
}

func modelHasQuota(model AiModel, usageMap map[string]AiModelUsage, minRemaining float64) bool {
	for _, version := range model.Versions {
		for _, sku := range version.Skus {
			usage, ok := usageMap[sku.UsageName]
			if ok {
				remaining := usage.Limit - usage.CurrentValue
				if remaining >= minRemaining {
					return true
				}
			}
		}
	}
	return false
}

func maxModelRemainingQuota(model AiModel, usageMap map[string]AiModelUsage) (float64, bool) {
	var maxRemaining float64
	found := false
	for _, version := range model.Versions {
		for _, sku := range version.Skus {
			usage, ok := usageMap[sku.UsageName]
			if !ok {
				continue
			}

			remaining := usage.Limit - usage.CurrentValue
			if !found || remaining > maxRemaining {
				maxRemaining = remaining
			}
			found = true
		}
	}

	return maxRemaining, found
}

func modelLocations(models []AiModel) []string {
	locationSet := map[string]struct{}{}
	for _, model := range models {
		for _, location := range model.Locations {
			locationSet[location] = struct{}{}
		}
	}

	locations := make([]string, 0, len(locationSet))
	for location := range locationSet {
		locations = append(locations, location)
	}

	slices.Sort(locations)

	return locations
}

func filterModelsByAnyLocationQuota(
	models []AiModel,
	usagesByLocation map[string][]AiModelUsage,
	minRemaining float64,
) []AiModel {
	eligible := map[string]struct{}{}

	for _, usages := range usagesByLocation {
		for _, model := range FilterModelsByQuota(models, usages, minRemaining) {
			eligible[model.Name] = struct{}{}
		}
	}

	filtered := make([]AiModel, 0, len(eligible))
	for _, model := range models {
		if _, ok := eligible[model.Name]; ok {
			filtered = append(filtered, model)
		}
	}

	return filtered
}

func (s *AiModelService) listUsagesByLocation(
	ctx context.Context,
	subscriptionId string,
	locations []string,
) (map[string][]AiModelUsage, error) {
	const maxConcurrentUsageCalls = 8

	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, maxConcurrentUsageCalls)
	usagesByLocation := make(map[string][]AiModelUsage, len(locations))
	var firstErr error

	for _, location := range locations {
		location := location
		wg.Add(1)

		go func() {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				if firstErr == nil {
					firstErr = ctx.Err()
				}
				mu.Unlock()

				return
			}
			defer func() { <-sem }()

			usages, err := s.ListUsages(ctx, subscriptionId, location)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()

				return
			}

			mu.Lock()
			usagesByLocation[location] = usages
			mu.Unlock()
		}()
	}

	wg.Wait()

	if len(usagesByLocation) == 0 && firstErr != nil {
		return nil, firstErr
	}

	return usagesByLocation, nil
}

func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func safeFloat64(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
