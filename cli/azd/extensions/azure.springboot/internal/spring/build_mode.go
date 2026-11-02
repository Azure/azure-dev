// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func validateRawDockerSettings(service map[string]any) error {
	value, present := service["docker"]
	if !present {
		return nil
	}
	settings, ok := value.(map[string]any)
	if !ok {
		return validationError("invalid_docker_settings", "service docker configuration must be an object",
			"omit docker for native Maven packaging, or select an authored recipe with docker.path")
	}
	for key, value := range settings {
		switch key {
		case "path", "context", "platform", "registry", "tag":
			text, ok := value.(string)
			if !ok || (key == "path" && (strings.TrimSpace(text) == "" || text != strings.TrimSpace(text))) {
				return validationError("invalid_docker_settings",
					"explicit Docker settings must contain valid string values",
					"omit docker.path for native Maven packaging, or select a nonempty authored Dockerfile path")
			}
		case "remoteBuild":
			if enabled, ok := value.(bool); ok && !enabled {
				continue
			}
			fallthrough
		default:
			return validationError("custom_docker_settings_unsupported",
				"service docker configuration contains unsupported settings",
				"use only docker.path, docker.context, docker.platform, docker.registry, and docker.tag")
		}
	}
	return nil
}

func usesAuthoredDockerfile(service *azdext.ServiceConfig) bool {
	return service.GetHost() == SpringHost && service.GetDocker().GetPath() != ""
}

// validateSpringBuild validates local packaging inputs without changing authored files.
func validateSpringBuild(project *azdext.ProjectConfig, service *azdext.ServiceConfig) error {
	if err := validateContainerSettings(service); err != nil {
		return err
	}
	if usesAuthoredDockerfile(service) {
		_, err := validateAuthoredDockerfile(project, service)
		return err
	}
	_, err := servicePath(project, service)
	return err
}

func prepareSpringBuild(project *azdext.ProjectConfig, service *azdext.ServiceConfig) error {
	return validateSpringBuild(project, service)
}
