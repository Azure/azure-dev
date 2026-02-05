// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"context"
	"fmt"

	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
)

// QuotaService provides methods to check quota availability for AI model deployments.
type QuotaService struct {
	credential azcore.TokenCredential
}

// NewQuotaService creates a new QuotaService instance.
func NewQuotaService(credential azcore.TokenCredential) *QuotaService {
	return &QuotaService{
		credential: credential,
	}
}

// QuotaCheckResult contains the result of a quota availability check.
type QuotaCheckResult struct {
	Available     bool
	CurrentUsage  float64
	Limit         float64
	Requested     int32
	UsageName     string
	QuotaExceeded bool
}

// CheckQuota validates whether the requested capacity is available for the given usage name in a location.
// It returns a QuotaCheckResult with details about current usage, limits, and availability.
func (s *QuotaService) CheckQuota(
	ctx context.Context,
	subscriptionId string,
	location string,
	usageName string,
	requestedCapacity int32,
) (*QuotaCheckResult, error) {
	client, err := armcognitiveservices.NewUsagesClient(subscriptionId, s.credential, azure.NewArmClientOptions())
	if err != nil {
		return nil, fmt.Errorf("creating usages client: %w", err)
	}

	pager := client.NewListPager(location, nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing usages: %w", err)
		}

		for _, usage := range page.Value {
			if usage.Name != nil && usage.Name.Value != nil && *usage.Name.Value == usageName {
				currentUsage := float64(0)
				limit := float64(0)

				if usage.CurrentValue != nil {
					currentUsage = *usage.CurrentValue
				}
				if usage.Limit != nil {
					limit = *usage.Limit
				}

				available := (limit - currentUsage) >= float64(requestedCapacity)

				return &QuotaCheckResult{
					Available:     available,
					CurrentUsage:  currentUsage,
					Limit:         limit,
					Requested:     requestedCapacity,
					UsageName:     usageName,
					QuotaExceeded: !available,
				}, nil
			}
		}
	}

	// If usage name not found, assume quota is available (may be a new/unmeasured resource)
	return &QuotaCheckResult{
		Available:     true,
		CurrentUsage:  0,
		Limit:         0,
		Requested:     requestedCapacity,
		UsageName:     usageName,
		QuotaExceeded: false,
	}, nil
}

// QuotaExceededError represents an error when quota is insufficient for the requested deployment.
type QuotaExceededError struct {
	ModelName         string
	SkuName           string
	Location          string
	CurrentUsage      float64
	Limit             float64
	Requested         int32
	AlternateRegions  []string
}

func (e *QuotaExceededError) Error() string {
	available := e.Limit - e.CurrentUsage
	msg := fmt.Sprintf(`Insufficient quota for model '%s' (%s) in region '%s'.

  Current usage: %.0f
  Limit:         %.0f
  Requested:     %d
  Available:     %.0f

To fix this, you can either:

  1. Request a quota increase at: https://aka.ms/oai/quotaincrease

  2. Try a different region where the model is available:
     azd env set AZURE_LOCATION <region>`,
		e.ModelName, e.SkuName, e.Location, e.CurrentUsage, e.Limit, e.Requested, available)

	if len(e.AlternateRegions) > 0 {
		msg += fmt.Sprintf("\n\n     Available regions: %s", formatRegionList(e.AlternateRegions))
	}

	return msg
}

// ModelNotFoundError represents an error when a model is not available in the selected region.
type ModelNotFoundError struct {
	ModelName        string
	Location         string
	AvailableRegions []string
}

func (e *ModelNotFoundError) Error() string {
	msg := fmt.Sprintf(`Model '%s' is not available in region '%s'.`, e.ModelName, e.Location)

	if len(e.AvailableRegions) > 0 {
		msg += fmt.Sprintf(`

The model is available in these regions: %s

To use a different region, run:
  azd env set AZURE_LOCATION <region>`, formatRegionList(e.AvailableRegions))
	} else {
		msg += `

To fix this:
  1. Verify the model name is correct
  2. See available models: https://learn.microsoft.com/azure/ai-services/openai/concepts/models`
	}

	return msg
}

// NoDeployableSkusError represents an error when no SKUs with capacity are available.
type NoDeployableSkusError struct {
	ModelName        string
	ModelVersion     string
	Location         string
	AvailableRegions []string
}

func (e *NoDeployableSkusError) Error() string {
	msg := fmt.Sprintf(`No deployable SKUs found for model '%s' (version %s) in region '%s'.
All available SKUs have zero default capacity.`,
		e.ModelName, e.ModelVersion, e.Location)

	if len(e.AvailableRegions) > 0 {
		msg += fmt.Sprintf(`

The model has deployable SKUs in these regions: %s

To use a different region, run:
  azd env set AZURE_LOCATION <region>`, formatRegionList(e.AvailableRegions))
	} else {
		msg += `

To fix this, try a different model version or region.`
	}

	return msg
}

// formatRegionList formats a list of regions for display, limiting to first few if too many.
func formatRegionList(regions []string) string {
	const maxDisplay = 5
	if len(regions) <= maxDisplay {
		return joinRegions(regions)
	}
	return fmt.Sprintf("%s (and %d more)", joinRegions(regions[:maxDisplay]), len(regions)-maxDisplay)
}

func joinRegions(regions []string) string {
	if len(regions) == 0 {
		return ""
	}
	if len(regions) == 1 {
		return regions[0]
	}
	result := regions[0]
	for i := 1; i < len(regions); i++ {
		result += ", " + regions[i]
	}
	return result
}
