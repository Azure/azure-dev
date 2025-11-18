// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package platform

import "github.com/azure/azure-dev/pkg/ioc"

// Provider is an interface for a platform provider
type Provider interface {
	// Name returns the name of the platform
	Name() string

	// IsEnabled returns true if the platform is enabled
	IsEnabled() bool

	// ConfigureContainer configures the IoC container for the platform
	ConfigureContainer(container *ioc.NestedContainer) error
}
