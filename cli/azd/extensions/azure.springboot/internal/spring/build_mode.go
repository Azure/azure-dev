// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

func validateRawServiceSettings(service map[string]any) error {
	if _, present := service["database"]; present {
		return validationError(
			"explicit_database_config_unsupported",
			"azure.springboot does not support an authored database block",
			"remove database and let azure.springboot discover the effective Spring datasource",
		)
	}
	if config, ok := service["config"].(map[string]any); ok {
		if _, present := config["database"]; present {
			return validationError(
				"explicit_database_config_unsupported",
				"azure.springboot does not support an authored config.database block",
				"remove config.database and let azure.springboot discover the effective Spring datasource",
			)
		}
	}
	if _, present := service["docker"]; present {
		return validationError(
			"explicit_docker_config_unsupported",
			"azure.springboot does not support an authored docker block",
			"remove docker and let azure.springboot use native Maven packaging",
		)
	}
	return nil
}

func validateAutomaticServiceConfig(service *azdext.ServiceConfig) error {
	if service == nil {
		return validationError("missing_service_config", "service configuration is missing", "check azure.yaml")
	}
	if additional := service.GetAdditionalProperties(); additional != nil {
		if _, present := additional.AsMap()["database"]; present {
			return validationError(
				"explicit_database_config_unsupported",
				"azure.springboot does not support an authored database block",
				"remove database and let azure.springboot discover the effective Spring datasource",
			)
		}
	}
	if _, present := service.GetConfig().GetFields()["database"]; present {
		return validationError(
			"explicit_database_config_unsupported",
			"azure.springboot does not support an authored config.database block",
			"remove config.database and let azure.springboot discover the effective Spring datasource",
		)
	}
	options := service.GetDocker()
	if options.GetPath() == "" {
		platform := strings.TrimSpace(options.GetPlatform())
		if platform != "" && !strings.EqualFold(platform, docker.DefaultPlatform) {
			return validationError(
				"unsupported_native_container_platform",
				"azure.springboot native packaging supports only linux/amd64",
				"remove docker.platform or set it to linux/amd64",
			)
		}
	}
	return nil
}

// validateSpringBuild validates local packaging inputs without changing authored files.
func validateSpringBuild(project *azdext.ProjectConfig, service *azdext.ServiceConfig) error {
	if err := validateAutomaticServiceConfig(service); err != nil {
		return err
	}
	_, err := servicePath(project, service)
	return err
}

func prepareSpringBuild(project *azdext.ProjectConfig, service *azdext.ServiceConfig) error {
	return validateSpringBuild(project, service)
}
