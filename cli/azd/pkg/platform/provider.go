package platform

import "github.com/wbreza/container/v4"

// Provider is an interface for a platform provider
type Provider interface {
	// Name returns the name of the platform
	Name() string

	// IsEnabled returns true if the platform is enabled
	IsEnabled() bool

	// ConfigureContainer configures the IoC container for the platform
	ConfigureContainer(container *container.Container) error
}
