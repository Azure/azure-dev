// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"fmt"
	"reflect"
	"strings"

	"azure.ai.projects/internal/exterrors"
)

// legacyConnectionTemplateError intentionally reports no parameter or resource
// values: either can contain credentials in a user-authored template.
func legacyConnectionTemplateError(sourcePath string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidServiceConfig,
		fmt.Sprintf("on-disk Foundry template %q uses the removed generic Connection provisioning contract", sourcePath),
		"remove the generic Foundry Connection modules/resources and their associated "+
			"connections/connectionCredentials inputs from your on-disk Bicep and parameter files; "+
			"keep unrelated parameters and keep the system ACR connection. "+
			"Declare connections as services with host: azure.ai.connection, then run `azd deploy` "+
			"after provisioning the Project. No templates or Azure resources have been changed",
	)
}

// validateProjectTemplate checks compiled ARM, not Bicep text, so renamed local
// modules, resource loops and inline nested deployments are inspected for actual
// generic Foundry Connection resources. Parameter names alone are not evidence:
// unrelated user IaC can legitimately declare or forward "connections" inputs.
// Both Bicep entry paths run this before any template is sent to Azure. Linked
// templates are not fetched; their parameter names do not identify their resources.
func validateProjectTemplate(template map[string]any, sourcePath string) error {
	return validateProjectResources(template["resources"], "", sourcePath)
}

func validateProjectResources(resources any, parentType, sourcePath string) error {
	validate := func(value any) error {
		resource, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		resourceType, _ := resource["type"].(string)
		resourceType = strings.ToLower(resourceType)
		if parentType != "" && !strings.Contains(resourceType, ".") {
			resourceType = parentType + "/" + resourceType
		}
		properties, _ := resource["properties"].(map[string]any)
		switch resourceType {
		case "microsoft.cognitiveservices/accounts/connections":
			return legacyConnectionTemplateError(sourcePath)
		case "microsoft.cognitiveservices/accounts/projects/connections":
			if !isSystemAcrConnection(resource) {
				return legacyConnectionTemplateError(sourcePath)
			}
		case "microsoft.resources/deployments":
			if nested, ok := properties["template"].(map[string]any); ok {
				if err := validateProjectTemplate(nested, sourcePath); err != nil {
					return err
				}
			}
		}
		return validateProjectResources(resource["resources"], resourceType, sourcePath)
	}

	// ARM languageVersion 2.0 uses symbolic-name objects rather than arrays.
	switch resources := resources.(type) {
	case []any:
		for _, resource := range resources {
			if err := validate(resource); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, resource := range resources {
			if err := validate(resource); err != nil {
				return err
			}
		}
	}
	return nil
}

// isSystemAcrConnection recognizes the registry connection emitted by acr.bicep
// or foundry-project.bicep. Category/auth and even matching resource IDs alone
// also describe user-declared connections, so preserve only the generated name,
// target, project identity and registry ID wiring. Compare ARM expressions, not
// their evaluated values; this is a migration guard, not an ARM evaluator.
func isSystemAcrConnection(resource map[string]any) bool {
	// The system connection is a single resource in its containing deployment's
	// scope. A copy loop or scope override is not part of that generated shape.
	for _, field := range []string{"copy", "scope"} {
		if _, exists := resource[field]; exists {
			return false
		}
	}

	var target, clientID, resourceID string
	var condition any
	switch resource["name"] {
	case "[format('{0}/{1}/{2}', parameters('foundryAccountName'), parameters('foundryProjectName'), " +
		"format('{0}-conn', parameters('name')))]":
		target = "[reference(resourceId('Microsoft.ContainerRegistry/registries', parameters('name')), " +
			"'2023-07-01').loginServer]"
		clientID = "[parameters('foundryProjectPrincipalId')]"
		resourceID = "[resourceId('Microsoft.ContainerRegistry/registries', parameters('name'))]"
	case "[format('{0}/{1}/{2}', parameters('accountName'), parameters('projectName'), " +
		"format('{0}-conn', parameters('acrName')))]":
		target = "[parameters('acrEndpoint')]"
		clientID = "[reference('foundryAccountPreview::project', '2025-04-01-preview', 'full').identity.principalId]"
		resourceID = "[parameters('acrResourceId')]"
		condition = "[parameters('createAcrConnection')]"
	default:
		return false
	}

	return resource["condition"] == condition && reflect.DeepEqual(resource["properties"], map[string]any{
		"category": "ContainerRegistry",
		"authType": "ManagedIdentity",
		"target":   target,
		"credentials": map[string]any{
			"clientId":   clientID,
			"resourceId": resourceID,
		},
		"isSharedToAll": true,
		"metadata":      map[string]any{"ResourceId": resourceID},
	})
}
