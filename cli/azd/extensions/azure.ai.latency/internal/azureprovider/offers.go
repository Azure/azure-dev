// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureprovider

import (
	"context"
	"fmt"
	"math"
	"strings"

	"azure.ai.latency/internal/model"
)

// GetOptions resolves live Priority Processing and PTU-M eligibility.
func (p *Provider) GetOptions(
	ctx context.Context,
	deployment model.DeploymentContext,
) ([]model.OfferOption, error) {
	if p == nil || p.cognitive == nil {
		return nil, fmt.Errorf("Azure provider is not initialized")
	}
	if strings.TrimSpace(deployment.SubscriptionID) != "" &&
		!strings.EqualFold(strings.TrimSpace(deployment.SubscriptionID), p.subscriptionID) {
		return nil, fmt.Errorf("deployment subscription does not match the provider subscription")
	}
	if strings.TrimSpace(deployment.Model) == "" ||
		strings.TrimSpace(deployment.ModelVersion) == "" ||
		strings.TrimSpace(deployment.Region) == "" {
		return nil, fmt.Errorf("model, version, and region are required for live offer eligibility")
	}

	models, err := p.cognitive.ListModels(ctx, deployment.Region)
	if err != nil {
		return nil, safeServiceError("query regional Cognitive Services models", err)
	}
	definition := findModel(models, deployment.Model, deployment.ModelVersion)
	if definition == nil {
		return []model.OfferOption{
			unknownOffer(model.OfferPriorityProcessing, false),
			unknownOffer(model.OfferPTUM, true),
		}, nil
	}

	priority := priorityOption(*definition, deployment)
	ptum, err := p.ptumOption(ctx, *definition, deployment)
	if err != nil {
		return nil, err
	}
	return []model.OfferOption{priority, ptum}, nil
}

func findModel(models []regionalModel, name, version string) *regionalModel {
	for index := range models {
		item := &models[index]
		if strings.EqualFold(strings.TrimSpace(item.Name), strings.TrimSpace(name)) &&
			strings.EqualFold(strings.TrimSpace(item.Version), strings.TrimSpace(version)) {
			return item
		}
	}
	return nil
}

func priorityOption(definition regionalModel, deployment model.DeploymentContext) model.OfferOption {
	supported := map[string]struct{}{}
	for key, value := range definition.Capabilities {
		if !strings.EqualFold(key, "priorityTierSkus") {
			continue
		}
		for item := range strings.SplitSeq(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				supported[strings.ToLower(item)] = struct{}{}
			}
		}
	}
	_, available := supported[strings.ToLower(strings.TrimSpace(deployment.SKUName))]
	note := fmt.Sprintf(
		"The regional Models API does not report Priority Processing for %s.",
		displaySKU(deployment.SKUName),
	)
	if available {
		note = fmt.Sprintf(
			"The regional Models API reports Priority Processing for %s.",
			displaySKU(deployment.SKUName),
		)
	}
	return model.OfferOption{
		Offer:                     model.OfferPriorityProcessing,
		Available:                 model.Bool(available),
		Availability:              availabilityLabel(available),
		Note:                      note,
		RequiresLiveCapacityCheck: false,
	}
}

func (p *Provider) ptumOption(
	ctx context.Context,
	definition regionalModel,
	deployment model.DeploymentContext,
) (model.OfferOption, error) {
	targetSKU := provisionedSKU(deployment.DeploymentType)
	var sku *regionalSKU
	for index := range definition.SKUs {
		if strings.EqualFold(definition.SKUs[index].Name, targetSKU) {
			sku = &definition.SKUs[index]
			break
		}
	}
	if sku == nil {
		return model.OfferOption{
			Offer:        model.OfferPTUM,
			Available:    model.Bool(false),
			Availability: "Unavailable",
			Note: fmt.Sprintf(
				"The regional Models API does not list %s for this model and version.",
				targetSKU,
			),
			RequiresLiveCapacityCheck: true,
		}, nil
	}

	minimum := sku.Minimum
	if minimum != nil && *minimum <= 0 {
		minimum = nil
	}
	var availableCapacity *float64
	var quotaHeadroom *float64
	capacityKnown := false
	quotaKnown := false
	if minimum != nil && *minimum > 0 {
		capacities, err := p.cognitive.ListModelCapacities(
			ctx,
			modelFormat(definition),
			deployment.Model,
			deployment.ModelVersion,
		)
		if ctxErr := contextError(ctx, err); ctxErr != nil {
			return model.OfferOption{}, ctxErr
		}
		if err == nil {
			availableCapacity = selectAvailableCapacity(capacities, deployment.Region, targetSKU)
			capacityKnown = availableCapacity != nil
		}

		usages, err := p.cognitive.ListUsages(ctx, deployment.Region)
		if ctxErr := contextError(ctx, err); ctxErr != nil {
			return model.OfferOption{}, ctxErr
		}
		if err == nil {
			usageName := strings.TrimSpace(sku.UsageName)
			if usageName == "" {
				usageName = "OpenAI." + targetSKU
			}
			quotaHeadroom = selectQuotaHeadroom(usages, usageName)
			quotaKnown = quotaHeadroom != nil
		}
	}

	option := model.OfferOption{
		Offer:                     model.OfferPTUM,
		Available:                 nil,
		Availability:              "Conditional",
		RequiresLiveCapacityCheck: true,
		MinimumPTUs:               minimum,
	}
	details := []string{"The regional Models API lists " + targetSKU}
	if minimum == nil || *minimum <= 0 {
		details = append(details, "the minimum PTU requirement was not reported")
	}
	if availableCapacity != nil {
		details = append(details, fmt.Sprintf("current capacity %.15g PTUs", *availableCapacity))
	} else {
		details = append(details, "current regional capacity could not be established")
	}
	if quotaHeadroom != nil {
		details = append(details, fmt.Sprintf("quota headroom %.15g PTUs", *quotaHeadroom))
	} else {
		details = append(details, "subscription quota headroom could not be established")
	}

	if minimum != nil && *minimum > 0 && capacityKnown && quotaKnown {
		available := *availableCapacity >= float64(*minimum) && *quotaHeadroom >= float64(*minimum)
		option.Available = model.Bool(available)
		option.Availability = availabilityLabel(available)
	}
	option.Note = strings.Join(details, "; ") + ". Capacity can change."
	return option, nil
}

func provisionedSKU(deploymentType string) string {
	switch strings.ToLower(strings.TrimSpace(deploymentType)) {
	case "data_zone_standard":
		return "DataZoneProvisionedManaged"
	case "regional_standard":
		return "ProvisionedManaged"
	default:
		return "GlobalProvisionedManaged"
	}
}

func modelFormat(definition regionalModel) string {
	if value := strings.TrimSpace(definition.Format); value != "" {
		return value
	}
	return "OpenAI"
}

func selectAvailableCapacity(capacities []modelCapacity, region, skuName string) *float64 {
	var selected *float64
	for _, capacity := range capacities {
		if normalizeRegion(capacity.Region) != normalizeRegion(region) ||
			!strings.EqualFold(strings.TrimSpace(capacity.SKUName), strings.TrimSpace(skuName)) ||
			capacity.AvailableCapacity == nil {
			continue
		}
		value := *capacity.AvailableCapacity
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		if selected == nil || value > *selected {
			selected = model.Float64(value)
		}
	}
	return selected
}

func selectQuotaHeadroom(usages []quotaUsage, usageName string) *float64 {
	for _, usage := range usages {
		if !strings.EqualFold(strings.TrimSpace(usage.Name), strings.TrimSpace(usageName)) ||
			usage.Current == nil || usage.Limit == nil {
			continue
		}
		headroom := max(*usage.Limit-*usage.Current, 0)
		if math.IsNaN(headroom) || math.IsInf(headroom, 0) {
			return nil
		}
		return model.Float64(headroom)
	}
	return nil
}

func normalizeRegion(region string) string {
	var normalized strings.Builder
	for _, character := range strings.ToLower(region) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			normalized.WriteRune(character)
		}
	}
	return normalized.String()
}

func unknownOffer(offer string, requiresCapacity bool) model.OfferOption {
	return model.OfferOption{
		Offer:                     offer,
		Available:                 nil,
		Availability:              "Unknown",
		Note:                      "The model and version were not returned by the regional Models API.",
		RequiresLiveCapacityCheck: requiresCapacity,
	}
}

func availabilityLabel(available bool) string {
	if available {
		return "Available"
	}
	return "Unavailable"
}

func displaySKU(sku string) string {
	if value := strings.TrimSpace(sku); value != "" {
		return value
	}
	return "this deployment type"
}
