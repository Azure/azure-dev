package extensions

import (
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

func Initialize(serviceLocator *ioc.NestedContainer) (map[string]*Extension, error) {
	var manager *Manager
	if err := serviceLocator.Resolve(&manager); err != nil {
		return nil, err
	}

	extensions, err := manager.Initialize()
	if err != nil {
		return nil, err
	}

	return extensions, nil
}
