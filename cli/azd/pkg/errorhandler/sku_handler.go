// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"context"
	"fmt"
	"os"
)

// SkuNotAvailableHandler provides dynamic suggestions for SKU availability
// errors by including the current AZURE_LOCATION in the guidance.
type SkuNotAvailableHandler struct{}

// NewSkuNotAvailableHandler creates a new SkuNotAvailableHandler.
func NewSkuNotAvailableHandler() ErrorHandler {
	return &SkuNotAvailableHandler{}
}

func (h *SkuNotAvailableHandler) Handle(_ context.Context, err error) *ErrorWithSuggestion {
	location := os.Getenv("AZURE_LOCATION")

	suggestion := "Try a different region with 'azd env set AZURE_LOCATION <region>'."
	if location != "" {
		suggestion = fmt.Sprintf(
			"The current region is '%s'. Try a different region with "+
				"'azd env set AZURE_LOCATION <region>'. "+
				"To see available SKUs, run 'az vm list-skus --location %s --output table'.",
			location, location,
		)
	}

	return &ErrorWithSuggestion{
		Err:        err,
		Message:    "The requested VM size or SKU is not available in this region.",
		Suggestion: suggestion,
		DocUrl:     "https://learn.microsoft.com/azure/azure-resource-manager/troubleshooting/error-sku-not-available",
	}
}
