package cmd

import (
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/devcenter"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func IsDevCenterEnabled(config config.Config, projectConfig *project.ProjectConfig) bool {
	if projectConfig != nil && projectConfig.DevCenter != nil {
		return true
	}

	devCenterModeNode, ok := config.Get(devcenter.ModeConfigPath)
	if !ok {
		return false
	}

	devCenterValue, ok := devCenterModeNode.(string)
	if !ok {
		return false
	}

	return strings.EqualFold(devCenterValue, "on")
}
