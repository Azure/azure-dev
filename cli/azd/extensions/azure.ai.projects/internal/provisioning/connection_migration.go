// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"azure.ai.projects/internal/exterrors"
)

// Skip comments and quoted strings so examples and unrelated literal data do
// not look like declarations. Compiled ARM validation remains authoritative for
// resources and nested modules; this early check avoids evaluating old secure
// bicepparam assignments just to discover the removed parameter contract.
var (
	bicepCommentsAndStrings = regexp.MustCompile(`(?s)'''(.*?)'''|'(?:\\.|[^'\\])*'|//[^\r\n]*|/\*.*?\*/`)
	legacyConnectionParam   = regexp.MustCompile(`(?im)^\s*param\s+(connections|connectionCredentials)\b`)
)

func rejectLegacyConnectionSource(sourcePath string) error {
	//nolint:gosec // The source path is selected from the configured project infrastructure directory.
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read on-disk Bicep source %q: %w", sourcePath, err)
	}
	source := bicepCommentsAndStrings.ReplaceAllString(string(raw), " ")
	if legacyConnectionParam.MatchString(source) {
		return legacyConnectionTemplateError(sourcePath)
	}
	return nil
}

// legacyConnectionTemplateError intentionally reports no parameter or resource
// values: either can contain credentials in a user-authored template.
func legacyConnectionTemplateError(sourcePath string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidServiceConfig,
		fmt.Sprintf("on-disk Foundry template %q uses the removed generic Connection provisioning contract", sourcePath),
		"remove the legacy connections/connectionCredentials parameters and generic Connection modules/resources "+
			"from your on-disk Bicep and parameter files; keep the system ACR connection. "+
			"Declare connections as services with host: azure.ai.connection, then run `azd deploy` "+
			"after provisioning the Project. No templates or Azure resources have been changed",
	)
}

func rejectLegacyConnectionParameters(parameters map[string]any, sourcePath string) error {
	for name := range parameters {
		if strings.EqualFold(name, "connections") || strings.EqualFold(name, "connectionCredentials") {
			return legacyConnectionTemplateError(sourcePath)
		}
	}
	return nil
}

// validateProjectTemplate checks compiled ARM, not Bicep text, so renamed local
// modules, resource loops and nested deployments cannot hide generic Connections.
// Both Bicep entry paths run this before any template is sent to Azure. Only
// deployment templates/parameters and resource declarations are inspected; an
// unrelated resource's payload may legitimately contain a "connections" field.
func validateProjectTemplate(template map[string]any, sourcePath string) error {
	parameters, _ := template["parameters"].(map[string]any)
	if err := rejectLegacyConnectionParameters(parameters, sourcePath); err != nil {
		return err
	}
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
			parameters, _ := properties["parameters"].(map[string]any)
			if err := rejectLegacyConnectionParameters(parameters, sourcePath); err != nil {
				return err
			}
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
