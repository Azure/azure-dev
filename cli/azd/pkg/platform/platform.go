package platform

import (
	"context"
	"fmt"

	"github.com/wbreza/container/v4"
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
func Initialize(container *container.Container, defaultPlatform PlatformKind) (Provider, error) {
	// Enable the platform provider if it is configured
	var platformConfig *Config
	platformType := defaultPlatform

	// Override platform type when specified
	if err := container.Resolve(context.TODO(), &platformConfig); err != nil {
		Error = err
	}

	if platformConfig != nil {
		platformType = platformConfig.Type
	}

	var provider Provider
	platformKey := fmt.Sprintf("%s-platform", platformType)

	// Resolve the platform provider
	if err := container.ResolveNamed(context.TODO(), platformKey, &provider); err != nil {
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
