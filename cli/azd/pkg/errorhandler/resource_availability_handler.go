// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// quotedResourceTypePattern matches resource types in ARM error messages like:
// "resource type 'Microsoft.Web/staticSites'"
var quotedResourceTypePattern = regexp.MustCompile(
	`(?i)resource type '(Microsoft\.\w+/\w+)'`,
)

// bareResourceTypePattern matches any ARM resource type reference
var bareResourceTypePattern = regexp.MustCompile(`Microsoft\.\w+/\w+`)

// ResourceTypeLocationResolver queries Azure for the locations
// where a given resource type is available.
type ResourceTypeLocationResolver interface {
	GetLocations(
		ctx context.Context,
		subscriptionID string,
		resourceType string,
	) ([]string, error)
}

// EnvironmentResolver provides access to azd environment values
// like AZURE_LOCATION and AZURE_SUBSCRIPTION_ID.
type EnvironmentResolver interface {
	Getenv(key string) string
}

// ResourceNotAvailableHandler provides dynamic suggestions for resource
// availability errors by querying the ARM Providers API for supported regions.
type ResourceNotAvailableHandler struct {
	locationResolver ResourceTypeLocationResolver
	env              EnvironmentResolver
}

// NewResourceNotAvailableHandler creates a new ResourceNotAvailableHandler.
func NewResourceNotAvailableHandler(
	locationResolver ResourceTypeLocationResolver,
	env EnvironmentResolver,
) ErrorHandler {
	return &ResourceNotAvailableHandler{
		locationResolver: locationResolver,
		env:              env,
	}
}

func (h *ResourceNotAvailableHandler) Handle(
	ctx context.Context, err error,
) *ErrorWithSuggestion {
	errMsg := err.Error()
	location := h.env.Getenv("AZURE_LOCATION")
	subscriptionID := h.env.Getenv("AZURE_SUBSCRIPTION_ID")
	resourceType := extractResourceType(errMsg)

	var availableLocations []string
	if resourceType != "" && subscriptionID != "" &&
		h.locationResolver != nil {
		if locs, resolveErr := h.locationResolver.GetLocations(
			ctx, subscriptionID, resourceType,
		); resolveErr == nil {
			availableLocations = locs
		}
	}

	return h.buildSuggestion(
		err, location, resourceType, availableLocations,
	)
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
			"The resource type '%s' is not available "+
				"in the current region.",
			resourceType,
		)
	} else {
		msg = "A resource type is not available in the current region."
	}

	switch {
	case len(availableLocations) > 0 && location != "":
		suggestion = fmt.Sprintf(
			"The current region is '%s'. '%s' is available in: %s. "+
				"Change region with "+
				"'azd env set AZURE_LOCATION <region>'.",
			location,
			resourceType,
			strings.Join(availableLocations, ", "),
		)
	case len(availableLocations) > 0:
		suggestion = fmt.Sprintf(
			"'%s' is available in: %s. "+
				"Set a supported region with "+
				"'azd env set AZURE_LOCATION <region>'.",
			resourceType,
			strings.Join(availableLocations, ", "),
		)
	case location != "":
		suggestion = fmt.Sprintf(
			"The current region is '%s'. "+
				"Try a different region with "+
				"'azd env set AZURE_LOCATION <region>'.",
			location,
		)
	default:
		suggestion = "Try a different region with " +
			"'azd env set AZURE_LOCATION <region>'."
	}

	return &ErrorWithSuggestion{
		Err:        err,
		Message:    msg,
		Suggestion: suggestion,
		Links: []ErrorLink{
			{
				URL:   "https://learn.microsoft.com/azure/azure-resource-manager/troubleshooting/error-sku-not-available",
				Title: "Resolve errors for resource type not available in region",
			},
		},
	}
}

// extractResourceType attempts to extract the target ARM resource type
// (e.g., "Microsoft.Web/staticSites") from an error message. It first
// looks for the quoted form "resource type 'X'" to avoid matching
// resource types in URLs (e.g., Microsoft.Resources/deployments).
func extractResourceType(errMessage string) string {
	// Prefer the explicit "resource type '...'" form from ARM messages
	if match := quotedResourceTypePattern.FindStringSubmatch(errMessage); len(match) >= 2 {
		return match[1]
	}
	// Fall back to first bare match
	return bareResourceTypePattern.FindString(errMessage)
}
