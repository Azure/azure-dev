// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"errors"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
)

// authFailureHints maps ARM authorization error codes to actionable user guidance.
var authFailureHints = map[string]string{
	"AuthorizationFailed": "You do not have sufficient permissions for this deployment. " +
		"Ensure you have the Owner or Contributor role on the target subscription or resource group.",
	"LinkedAuthorizationFailed": "The deployment requires cross-resource authorization. " +
		"Ensure you have Owner or User Access Administrator role to create role assignments.",
	"RoleAssignmentUpdateNotPermitted": "You cannot update the existing role assignment. " +
		"Ensure you have the Owner or User Access Administrator role on the subscription.",
	"PrincipalNotFound": "The service principal referenced in the template was not found. " +
		"Ensure the principal exists in your Microsoft Entra ID (formerly Azure Active Directory) tenant " +
		"and has not been recently deleted.",
	"NoRegisteredProviderFound": "A required Azure resource provider is not registered in your subscription. " +
		"Register it with 'az provider register --namespace <ProviderNamespace>' and retry.",
	"RoleAssignmentExists": "A role assignment already exists with the same principal and role. " +
		"This is usually safe to ignore. The template should handle this gracefully.",
}

// deploymentAuthHint inspects a deployment error for authorization-related root causes
// and returns actionable guidance if found.
func deploymentAuthHint(err error) string {
	var armErr *azapi.AzureDeploymentError
	if !errors.As(err, &armErr) {
		return ""
	}

	if armErr.Details == nil {
		return ""
	}

	return findAuthHint(armErr.Details)
}

func findAuthHint(line *azapi.DeploymentErrorLine) string {
	if line == nil {
		return ""
	}

	if line.Code != "" {
		if hint, ok := authFailureHints[line.Code]; ok {
			return hint
		}

		if line.Code == "Forbidden" {
			messageLower := strings.ToLower(line.Message)
			if strings.Contains(messageLower, "roleassignment") || // cspell:disable-line
				strings.Contains(messageLower, "role assignment") {
				return "You do not have permission to manage role assignments. " +
					"Ensure you have the Owner or User Access Administrator role on the subscription."
			}
		}
	}

	for _, inner := range line.Inner {
		if hint := findAuthHint(inner); hint != "" {
			return hint
		}
	}

	return ""
}
