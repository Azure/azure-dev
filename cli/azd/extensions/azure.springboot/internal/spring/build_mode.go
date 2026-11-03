// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func validateRawServiceConfiguration(serviceName string, service map[string]any) error {
	var unsupported []string
	for key := range service {
		switch key {
		case "name", "project", "language", "host", "module", "resourceGroup",
			"resourceName", "apiVersion", "dist", "output", "image", "k8s", "infra",
			"uses", "env", "hooks", "condition", "remoteBuild":
			continue
		default:
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	slices.Sort(unsupported)
	return unsupportedServiceConfigurationError(serviceName, unsupported)
}

func validateServiceConfiguration(service *azdext.ServiceConfig) error {
	if service == nil {
		return validationError("missing_service_config", "service configuration is missing", "check azure.yaml")
	}
	var unsupported []string
	if service.Docker != nil {
		unsupported = append(unsupported, "docker")
	}
	if service.Config != nil {
		unsupported = append(unsupported, "config")
	}
	if service.AdditionalProperties != nil {
		for key := range service.AdditionalProperties.GetFields() {
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	slices.Sort(unsupported)
	return unsupportedServiceConfigurationError(service.GetName(), slices.Compact(unsupported))
}

func unsupportedServiceConfigurationError(serviceName string, keys []string) error {
	return validationError(
		"unsupported_spring_service_configuration",
		fmt.Sprintf(
			"service %q contains unsupported springbootapp configuration: %s",
			serviceName,
			strings.Join(keys, ", "),
		),
		"check the service configuration and retry",
	)
}

// validateSpringBuild validates local packaging inputs without changing authored files.
func validateSpringBuild(project *azdext.ProjectConfig, service *azdext.ServiceConfig) error {
	if err := validateServiceConfiguration(service); err != nil {
		return err
	}
	_, err := servicePath(project, service)
	return err
}

func prepareSpringBuild(project *azdext.ProjectConfig, service *azdext.ServiceConfig) error {
	return validateSpringBuild(project, service)
}
