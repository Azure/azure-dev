package platform

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

var (
	ErrPlatformNotSupported   = fmt.Errorf("unsupported platform")
	ErrPlatformConfigNotFound = fmt.Errorf("platform config not found")

	Error error = nil
)

type PlatformKind string

type Config struct {
	Type   PlatformKind   `yaml:"type"`
	Config map[string]any `yaml:"config"`
}

// Initialize configures the IoC container with the platform specific components
func Initialize(container *ioc.NestedContainer, defaultPlatform PlatformKind) (Provider, error) {
	// Enable the platform provider if it is configured
	var platformConfig *Config
	platformType := defaultPlatform

	// Override platform type when specified
	if err := container.Resolve(&platformConfig); err != nil {
		Error = err
	}

	if platformConfig != nil {
		platformType = platformConfig.Type
	}

	var provider Provider
	platformKey := fmt.Sprintf("%s-platform", platformType)

	// Resolve the platform provider
	if err := container.ResolveNamed(platformKey, &provider); err != nil {
		return nil, fmt.Errorf("failed to resolve platform provider '%s': %w", platformType, err)
	}

	if provider.IsEnabled() {
		// Configure the container for the platform provider
		if err := provider.ConfigureContainer(container); err != nil {
			return nil, fmt.Errorf("failed to configure platform provider '%s': %w", platformType, err)
		}
	}

	return provider, nil
}
