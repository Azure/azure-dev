// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/fatih/color"
)

// ProjectHasUAMI reports whether the given project identity has a populated
// User-Assigned Managed Identity. We require both:
//   - identity.type contains "UserAssigned" (covers "UserAssigned" and
//     combined values like "SystemAssigned, UserAssigned")
//   - identity.userAssignedIdentities is non-empty
//
// System-assigned alone does not satisfy this check by design — Foundry
// training submit currently requires a UAMI specifically.
func ProjectHasUAMI(identity *armcognitiveservices.Identity) bool {
	if identity == nil || identity.Type == nil {
		return false
	}
	if !strings.Contains(strings.ToLower(string(*identity.Type)), "userassigned") {
		return false
	}
	return len(identity.UserAssignedIdentities) > 0
}

// BoolEnv returns the canonical env-var representation for a bool.
func BoolEnv(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// NoUAMIMessage returns the wording shown to the user when a project has no
// User-Assigned Managed Identity. The same text is used as a warning during
// init / -e / -s flows and as an error during 'job submit'.
func NoUAMIMessage(projectName string) string {
	return fmt.Sprintf(
		"project %q has no User-Assigned Managed Identity. "+
			"Without it, 'azd ai training job submit' operation will fail.",
		projectName,
	)
}

// WarnIfNoUAMI prints a yellow warning when the project is missing a UAMI.
// No-op when hasUAMI is true.
func WarnIfNoUAMI(projectName string, hasUAMI bool) {
	if hasUAMI {
		return
	}
	color.Yellow("Warning: " + NoUAMIMessage(projectName))
}
