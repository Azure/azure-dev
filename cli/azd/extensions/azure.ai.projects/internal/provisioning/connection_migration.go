// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"fmt"
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
			// The provider still owns the system registry connection. Its literal
			// category and managed-identity auth distinguish it from the generic
			// connection loop (whose properties are an ARM expression).
			if properties["category"] != "ContainerRegistry" || properties["authType"] != "ManagedIdentity" {
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
