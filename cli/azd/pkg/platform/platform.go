package platform

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// Initialize configures the IoC container with the platform specific components
func Initialize(container *ioc.NestedContainer) error {
	// Enable the platform provider if it is configured
	var platformConfig *project.PlatformConfig
	var platformType = PlatformKindDefault

	// Override platform type when specified
	if err := container.Resolve(&platformConfig); err == nil && platformConfig != nil {
		platformType = platformConfig.Type
	}

	var platformProvider project.PlatformProvider
	platformKey := fmt.Sprintf("%s-platform", platformType)

	// Resolve the platform provider
	if err := container.ResolveNamed(platformKey, &platformProvider); err != nil {
		return fmt.Errorf("failed to resolve platform provider '%s': %w", platformType, err)
	}

	if platformProvider.IsEnabled() {
		// Configure the container for the platform provider
		if err := platformProvider.ConfigureContainer(container); err != nil {
			return fmt.Errorf("failed to configure platform provider '%s': %w", platformType, err)
		}
	}

	return nil
}
