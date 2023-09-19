package internal

import "github.com/azure/azure-dev/cli/azd/pkg/config"

func IsDevCenterEnabled(config config.Config) bool {
	devCenterNode, ok := config.Get("devcenter")
	if !ok {
		return false
	}

	devCenterValue, ok := devCenterNode.(string)
	if !ok {
		return false
	}

	return devCenterValue == "on"
}
