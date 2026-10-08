// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"sync"
)

// Provider is a generic interface that both ServiceTargetProvider and FrameworkServiceProvider implement
type Provider interface {
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
}

// FactoryKeyProvider extracts the factory key from a ServiceConfig
type FactoryKeyProvider func(*ServiceConfig) string

// ComponentManager provides common instance management functionality for both service targets and framework services
type ComponentManager[T Provider] struct {
	factories       map[string]ProviderFactory[T] // factoryKey -> factory
	instances       map[string]T                  // serviceName -> instance
	mutex           sync.RWMutex
	factoryKeyFunc  FactoryKeyProvider
	managerTypeName string // for error messages
}

// NewComponentManager creates a new ComponentManager with the specified factory key function
func NewComponentManager[T Provider](factoryKeyFunc FactoryKeyProvider, managerTypeName string) *ComponentManager[T] {
	return &ComponentManager[T]{
		factories:       make(map[string]ProviderFactory[T]),
		instances:       make(map[string]T),
		factoryKeyFunc:  factoryKeyFunc,
		managerTypeName: managerTypeName,
	}
}

// RegisterFactory registers a factory function for the given key
func (m *ComponentManager[T]) RegisterFactory(factoryKey string, factory func() T) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.factories[factoryKey] = factory
}

// GetOrCreateInstance gets an existing instance or creates a new one using the factory
func (m *ComponentManager[T]) GetOrCreateInstance(ctx context.Context, serviceConfig *ServiceConfig) (T, error) {
	var zero T

	if serviceConfig == nil {
		return zero, fmt.Errorf("service config is required")
	}

	factoryKey := m.factoryKeyFunc(serviceConfig)
	serviceName := serviceConfig.Name

	// First, check if we already have an instance for this service
	m.mutex.RLock()
	if instance, exists := m.instances[serviceName]; exists {
		m.mutex.RUnlock()
		return instance, nil
	}
	m.mutex.RUnlock()

	// We need to create a new instance
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Double-check after acquiring write lock
	if instance, exists := m.instances[serviceName]; exists {
		return instance, nil
	}

	// Get the factory for this key
	factory, exists := m.factories[factoryKey]
	if !exists {
		return zero, fmt.Errorf("no factory registered for %s: %s", m.managerTypeName, factoryKey)
	}

	// Create new instance and initialize it
	provider := factory()
	if err := provider.Initialize(ctx, serviceConfig); err != nil {
		return zero, fmt.Errorf("failed to initialize %s provider: %w", m.managerTypeName, err)
	}

	// Store the instance
	m.instances[serviceName] = provider
	return provider, nil
}

// GetInstance gets an existing instance for the service, returns error if not found
func (m *ComponentManager[T]) GetInstance(serviceName string) (T, error) {
	var zero T

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	instance, exists := m.instances[serviceName]
	if !exists {
		return zero, fmt.Errorf("no %s instance found for service: %s", m.managerTypeName, serviceName)
	}

	return instance, nil
}

// GetAnyInstance gets any available instance (used for Requirements call)
func (m *ComponentManager[T]) GetAnyInstance() (T, error) {
	var zero T

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	for _, instance := range m.instances {
		return instance, nil
	}

	return zero, fmt.Errorf("no %s instances available", m.managerTypeName)
}

// HasFactory checks if a factory is registered for the given key
func (m *ComponentManager[T]) HasFactory(factoryKey string) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	_, exists := m.factories[factoryKey]
	return exists
}

// Close cleans up all instances
func (m *ComponentManager[T]) Close() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Clear all instances
	m.instances = make(map[string]T)
	return nil
}
