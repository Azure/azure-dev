// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// resourceTypePattern matches ARM resource types like "Microsoft.Web/staticSites"
var resourceTypePattern = regexp.MustCompile(`Microsoft\.\w+/\w+`)

// ResourceTypeLocationResolver queries Azure for the locations
// where a given resource type is available.
type ResourceTypeLocationResolver interface {
	GetLocations(
		ctx context.Context,
		subscriptionID string,
		resourceType string,
	) ([]string, error)
}

// ResourceNotAvailableHandler provides dynamic suggestions for resource availability
// errors by querying the ARM Providers API for supported regions.
type ResourceNotAvailableHandler struct {
	locationResolver ResourceTypeLocationResolver
}

// NewResourceNotAvailableHandler creates a new ResourceNotAvailableHandler.
func NewResourceNotAvailableHandler(
	locationResolver ResourceTypeLocationResolver,
) ErrorHandler {
	return &ResourceNotAvailableHandler{
		locationResolver: locationResolver,
	}
}

func (h *ResourceNotAvailableHandler) Handle(ctx context.Context, err error) *ErrorWithSuggestion {
	location := os.Getenv("AZURE_LOCATION")
	subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID")

	// Try to extract the resource type from the error message
	resourceType := extractResourceType(err.Error())

	// Try to look up available locations for the resource type
	var availableLocations []string
	if resourceType != "" && subscriptionID != "" && h.locationResolver != nil {
		locations, resolveErr := h.locationResolver.GetLocations(ctx, subscriptionID, resourceType)
		if resolveErr == nil {
			availableLocations = locations
		}
	}

	return h.buildSuggestion(err, location, resourceType, availableLocations)
}

func (h *ResourceNotAvailableHandler) buildSuggestion(
	err error,
	location string,
	resourceType string,
	availableLocations []string,
) *ErrorWithSuggestion {
	var msg, suggestion string

	if resourceType != "" {
		msg = fmt.Sprintf(
			"The resource type '%s' is not available in the current region.",
			resourceType,
		)
	} else {
		msg = "A resource type is not available in the current region."
	}

	switch {
	case len(availableLocations) > 0 && location != "":
		suggestion = fmt.Sprintf(
			"The current region is '%s'. '%s' is available in: %s. "+
				"Change region with 'azd env set AZURE_LOCATION <region>'.",
			location,
			resourceType,
			strings.Join(availableLocations, ", "),
		)
	case len(availableLocations) > 0:
		suggestion = fmt.Sprintf(
			"'%s' is available in: %s. "+
				"Set a supported region with 'azd env set AZURE_LOCATION <region>'.",
			resourceType,
			strings.Join(availableLocations, ", "),
		)
	case location != "":
		suggestion = fmt.Sprintf(
			"The current region is '%s'. "+
				"Try a different region with 'azd env set AZURE_LOCATION <region>'.",
			location,
		)
	default:
		suggestion = "Try a different region with 'azd env set AZURE_LOCATION <region>'."
	}

	return &ErrorWithSuggestion{
		Err:        err,
		Message:    msg,
		Suggestion: suggestion,
		DocUrl:     "https://learn.microsoft.com/azure/azure-resource-manager/troubleshooting/error-sku-not-available",
	}
}

// extractResourceType attempts to extract an ARM resource type
// (e.g., "Microsoft.Web/staticSites") from an error message.
func extractResourceType(errMessage string) string {
	match := resourceTypePattern.FindString(errMessage)
	return match
}
