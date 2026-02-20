// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"errors"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
)

// softDeleteHints maps ARM error codes for soft-deleted resource conflicts
// to actionable user guidance.
var softDeleteHints = map[string]string{
	"FlagMustBeSetForRestore": "A soft-deleted resource with " +
		"this name exists and is blocking deployment. " +
		"Run 'azd down --purge' to permanently remove " +
		"soft-deleted resources, or purge them manually " +
		"in the Azure portal, then retry with 'azd up'.",
	"ConflictError": "A resource conflict occurred that may " +
		"be caused by a soft-deleted resource. " +
		"Run 'azd down --purge' to purge soft-deleted " +
		"resources, then retry with 'azd up'.",
}

// softDeleteKeywords are patterns checked in Conflict error messages
// to identify soft-delete related failures across Azure services.
var softDeleteKeywords = []string{
	"soft delete",
	"soft-delete",
	"soft deleted",
	"purge",
	"deleted vault",
	"deleted resource",
	"recover or purge",
}

// softDeleteHint inspects a deployment error for soft-delete related
// conflicts and returns actionable guidance if found.
func softDeleteHint(err error) string {
	var armErr *azapi.AzureDeploymentError
	if !errors.As(err, &armErr) {
		return ""
	}

	if armErr.Details == nil {
		return ""
	}

	return findSoftDeleteHint(armErr.Details)
}

func findSoftDeleteHint(line *azapi.DeploymentErrorLine) string {
	if line == nil {
		return ""
	}

	if line.Code != "" {
		if hint, ok := softDeleteHints[line.Code]; ok {
			return hint
		}

		// Conflict errors need message inspection for soft-delete context
		if line.Code == "Conflict" || line.Code == "RequestConflict" {
			messageLower := strings.ToLower(line.Message)
			for _, kw := range softDeleteKeywords {
				if strings.Contains(messageLower, kw) {
					return "A soft-deleted resource is causing " +
						"this deployment conflict. " +
						"Run 'azd down --purge' to permanently " +
						"remove soft-deleted resources, or " +
						"purge them in the Azure portal, " +
						"then retry with 'azd up'."
				}
			}
		}
	}

	for _, inner := range line.Inner {
		if hint := findSoftDeleteHint(inner); hint != "" {
			return hint
		}
	}

	return ""
}
