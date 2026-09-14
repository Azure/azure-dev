// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockProvider implements the Provider interface using testify/mock
type MockProvider struct {
	mock.Mock
	name string
}

func (m *MockProvider) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	args := m.Called(ctx, serviceConfig)
	return args.Error(0)
}

// mockFactoryKeyProvider returns a factory key based on service config
func mockFactoryKeyProvider(config *ServiceConfig) string {
	if config == nil {
		return ""
	}
	return config.Language // Use language as factory key for testing
}

// createTestServiceConfig creates a ServiceConfig for testing
func createTestServiceConfig(name, language string) *ServiceConfig {
	return &ServiceConfig{
		Name:     name,
		Language: language,
	}
}

func TestNewComponentManager(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	assert.NotNil(t, manager)
	assert.NotNil(t, manager.factories)
	assert.NotNil(t, manager.instances)
	assert.Equal(t, "test", manager.managerTypeName)
	assert.NotNil(t, manager.factoryKeyFunc)
}

func TestComponentManager_RegisterFactory(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	// Test registering a factory
	factory := func() *MockProvider {
		provider := &MockProvider{name: "test-provider"}
		provider.On("Initialize", mock.Anything, mock.Anything).Return(nil)
		return provider
	}

	manager.RegisterFactory("go", factory)

	// Verify factory is registered
	assert.True(t, manager.HasFactory("go"))
	assert.False(t, manager.HasFactory("python"))
}

func TestComponentManager_CreateInstance(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")
	serviceConfig := createTestServiceConfig("web-service", "go")
	factoryCalls := 0
	factory := func() *MockProvider {
		factoryCalls++
		return &MockProvider{name: "preview-provider"}
	}
	manager.RegisterFactory("go", factory)

	first, err := manager.CreateInstance(serviceConfig)
	require.NoError(t, err)
	second, err := manager.CreateInstance(serviceConfig)
	require.NoError(t, err)
	require.NotSame(t, first, second)
	assert.Empty(t, first.Calls, "creating an instance must not initialize it")
	assert.Empty(t, second.Calls, "creating an instance must not initialize it")
	assert.Empty(t, manager.instances)
	assert.Equal(t, 2, factoryCalls)

	cachedProvider := &MockProvider{name: "deployment-provider"}
	cachedProvider.On("Initialize", t.Context(), serviceConfig).Return(nil).Once()
	manager.RegisterFactory("go", func() *MockProvider { return cachedProvider })
	cached, err := manager.GetOrCreateInstance(t.Context(), serviceConfig)
	require.NoError(t, err)
	manager.RegisterFactory("go", factory)

	fresh, err := manager.CreateInstance(serviceConfig)
	require.NoError(t, err)
	assert.NotSame(t, cached, fresh)
	assert.Empty(t, fresh.Calls)
	assert.Equal(t, 3, factoryCalls)

	stored, err := manager.GetInstance(serviceConfig.Name)
	require.NoError(t, err)
	assert.Same(t, cached, stored, "creating a fresh instance must not replace the cached deployment provider")
	cachedProvider.AssertExpectations(t)
}

func TestComponentManager_CreateInstance_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		serviceConfig *ServiceConfig
		wantError     string
	}{
		{
			name:      "NilServiceConfig",
			wantError: "service config is required",
		},
		{
			name:          "NoFactory",
			serviceConfig: createTestServiceConfig("web-service", "missing"),
			wantError:     "no factory registered for test: missing",
		},
		{
			name:          "NilFactory",
			serviceConfig: createTestServiceConfig("web-service", "nil-factory"),
			wantError:     "no factory registered for test: nil-factory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")
			manager.RegisterFactory("nil-factory", nil)

			instance, err := manager.CreateInstance(tt.serviceConfig)
			require.EqualError(t, err, tt.wantError)
			assert.Nil(t, instance)
			assert.Empty(t, manager.instances)
		})
	}
}

func TestComponentManager_CreateInstance_FactoryOutsideLock(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")
	provider := &MockProvider{}
	manager.RegisterFactory("go", func() *MockProvider {
		require.True(t, manager.mutex.TryLock(), "the factory must run without the manager's read or write lock")
		manager.mutex.Unlock()
		manager.RegisterFactory("other", func() *MockProvider { return &MockProvider{} })
		return provider
	})

	instance, err := manager.CreateInstance(createTestServiceConfig("web-service", "go"))
	require.NoError(t, err)
	assert.Same(t, provider, instance)
	assert.True(t, manager.HasFactory("other"))
}

func TestComponentManager_CreateInstance_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")
	serviceConfig := createTestServiceConfig("web-service", "go")
	factory := func() *MockProvider { return &MockProvider{} }
	manager.RegisterFactory("go", factory)

	const count = 10
	instances := make([]*MockProvider, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() {
			manager.RegisterFactory("go", factory)
			instances[i], errs[i] = manager.CreateInstance(serviceConfig)
		})
	}
	wg.Wait()

	seen := make(map[*MockProvider]bool)
	for i, instance := range instances {
		require.NoError(t, errs[i])
		require.NotNil(t, instance)
		assert.False(t, seen[instance], "each call must create a fresh provider")
		seen[instance] = true
		assert.Empty(t, instance.Calls)
	}
	assert.Empty(t, manager.instances)
}

func TestComponentManager_GetOrCreateInstance_Success(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	// Register a factory
	factory := func() *MockProvider {
		provider := &MockProvider{name: "go-provider"}
		provider.On("Initialize", mock.Anything, mock.Anything).Return(nil)
		return provider
	}
	manager.RegisterFactory("go", factory)

	ctx := t.Context()
	serviceConfig := createTestServiceConfig("web-service", "go")

	// First call should create a new instance
	instance1, err := manager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)
	assert.NotNil(t, instance1)
	assert.Equal(t, "go-provider", instance1.name)

	// Second call should return the same instance
	instance2, err := manager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)
	assert.Same(t, instance1, instance2)

	// Verify expectations
	instance1.AssertExpectations(t)
}

func TestComponentManager_GetOrCreateInstance_NilServiceConfig(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")
	ctx := t.Context()

	instance, err := manager.GetOrCreateInstance(ctx, nil)
	assert.Error(t, err)
	assert.Nil(t, instance)
	assert.Contains(t, err.Error(), "service config is required")
}

func TestComponentManager_GetOrCreateInstance_NoFactory(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")
	ctx := t.Context()
	serviceConfig := createTestServiceConfig("web-service", "unknown-language")

	instance, err := manager.GetOrCreateInstance(ctx, serviceConfig)
	assert.Error(t, err)
	assert.Nil(t, instance)
	assert.Contains(t, err.Error(), "no factory registered for test: unknown-language")
}

func TestComponentManager_GetOrCreateInstance_InitializationError(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	// Register a factory that creates a provider with initialization error
	initError := errors.New("initialization failed")
	factory := func() *MockProvider {
		provider := &MockProvider{name: "failing-provider"}
		provider.On("Initialize", mock.Anything, mock.Anything).Return(initError)
		return provider
	}
	manager.RegisterFactory("go", factory)

	ctx := t.Context()
	serviceConfig := createTestServiceConfig("web-service", "go")

	instance, err := manager.GetOrCreateInstance(ctx, serviceConfig)
	assert.Error(t, err)
	assert.Nil(t, instance)
	assert.Contains(t, err.Error(), "failed to initialize test provider")
	assert.Contains(t, err.Error(), "initialization failed")
}

func TestComponentManager_GetOrCreateInstance_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	// Register a factory
	factory := func() *MockProvider {
		provider := &MockProvider{name: "concurrent-provider"}
		provider.On("Initialize", mock.Anything, mock.Anything).Return(nil)
		return provider
	}
	manager.RegisterFactory("go", factory)

	ctx := t.Context()
	serviceConfig := createTestServiceConfig("web-service", "go")

	// Create multiple goroutines that try to get the same instance
	const numGoroutines = 10
	instances := make([]*MockProvider, numGoroutines)
	errors := make([]error, numGoroutines)
	var wg sync.WaitGroup

	for i := range numGoroutines {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			instance, err := manager.GetOrCreateInstance(ctx, serviceConfig)
			instances[index] = instance
			errors[index] = err
		}(i)
	}

	wg.Wait()

	// All should succeed and return the same instance
	var firstInstance *MockProvider
	for i := range numGoroutines {
		assert.NoError(t, errors[i])
		assert.NotNil(t, instances[i])

		if i == 0 {
			firstInstance = instances[i]
		} else {
			assert.Same(t, firstInstance, instances[i])
		}
	}

	// Verify expectations on the single instance that was created
	firstInstance.AssertExpectations(t)
}

func TestComponentManager_GetInstance_Success(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	// Register a factory and create an instance
	factory := func() *MockProvider {
		provider := &MockProvider{name: "test-provider"}
		provider.On("Initialize", mock.Anything, mock.Anything).Return(nil)
		return provider
	}
	manager.RegisterFactory("go", factory)

	ctx := t.Context()
	serviceConfig := createTestServiceConfig("web-service", "go")

	// Create the instance first
	_, err := manager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)

	// Now get the existing instance
	instance, err := manager.GetInstance("web-service")
	assert.NoError(t, err)
	assert.NotNil(t, instance)
	assert.Equal(t, "test-provider", instance.name)
}

func TestComponentManager_GetInstance_NotFound(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	instance, err := manager.GetInstance("non-existent-service")
	assert.Error(t, err)
	assert.Nil(t, instance)
	assert.Contains(t, err.Error(), "no test instance found for service: non-existent-service")
}

func TestComponentManager_GetAnyInstance_Success(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	// Register a factory and create an instance
	factory := func() *MockProvider {
		provider := &MockProvider{name: "any-provider"}
		provider.On("Initialize", mock.Anything, mock.Anything).Return(nil)
		return provider
	}
	manager.RegisterFactory("go", factory)

	ctx := t.Context()
	serviceConfig := createTestServiceConfig("web-service", "go")

	// Create the instance first
	_, err := manager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)

	// Now get any available instance
	instance, err := manager.GetAnyInstance()
	assert.NoError(t, err)
	assert.NotNil(t, instance)
	assert.Equal(t, "any-provider", instance.name)
}

func TestComponentManager_GetAnyInstance_NoInstances(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	instance, err := manager.GetAnyInstance()
	assert.Error(t, err)
	assert.Nil(t, instance)
	assert.Contains(t, err.Error(), "no test instances available")
}

func TestComponentManager_GetAnyInstance_MultipleInstances(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	// Register a factory and create multiple instances
	factory := func() *MockProvider {
		provider := &MockProvider{name: "multi-provider"}
		provider.On("Initialize", mock.Anything, mock.Anything).Return(nil)
		return provider
	}
	manager.RegisterFactory("go", factory)

	ctx := t.Context()

	// Create multiple instances
	serviceConfig1 := createTestServiceConfig("web-service-1", "go")
	serviceConfig2 := createTestServiceConfig("web-service-2", "go")

	_, err := manager.GetOrCreateInstance(ctx, serviceConfig1)
	require.NoError(t, err)
	_, err = manager.GetOrCreateInstance(ctx, serviceConfig2)
	require.NoError(t, err)

	// GetAnyInstance should return one of them
	instance, err := manager.GetAnyInstance()
	assert.NoError(t, err)
	assert.NotNil(t, instance)
	assert.Equal(t, "multi-provider", instance.name)
}

func TestComponentManager_HasFactory(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	// Initially no factories
	assert.False(t, manager.HasFactory("go"))
	assert.False(t, manager.HasFactory("python"))

	// Register a factory
	factory := func() *MockProvider {
		provider := &MockProvider{name: "test-provider"}
		provider.On("Initialize", mock.Anything, mock.Anything).Return(nil)
		return provider
	}
	manager.RegisterFactory("go", factory)

	// Check factory existence
	assert.True(t, manager.HasFactory("go"))
	assert.False(t, manager.HasFactory("python"))
}

func TestComponentManager_Close(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	// Register a factory and create instances
	factory := func() *MockProvider {
		provider := &MockProvider{name: "closable-provider"}
		provider.On("Initialize", mock.Anything, mock.Anything).Return(nil)
		return provider
	}
	manager.RegisterFactory("go", factory)

	ctx := t.Context()
	serviceConfig := createTestServiceConfig("web-service", "go")

	// Create an instance
	_, err := manager.GetOrCreateInstance(ctx, serviceConfig)
	require.NoError(t, err)

	// Verify instance exists
	instance, err := manager.GetInstance("web-service")
	assert.NoError(t, err)
	assert.NotNil(t, instance)

	// Close the manager
	err = manager.Close()
	assert.NoError(t, err)

	// Verify instance no longer exists
	instance, err = manager.GetInstance("web-service")
	assert.Error(t, err)
	assert.Nil(t, instance)

	// Verify GetAnyInstance also fails
	instance, err = manager.GetAnyInstance()
	assert.Error(t, err)
	assert.Nil(t, instance)
}

func TestComponentManager_ConcurrentFactoryRegistration(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	const numGoroutines = 10
	var wg sync.WaitGroup

	// Concurrently register different factories
	for i := range numGoroutines {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			factoryKey := fmt.Sprintf("language-%d", index)
			factory := func() *MockProvider {
				provider := &MockProvider{name: fmt.Sprintf("provider-%d", index)}
				provider.On("Initialize", mock.Anything, mock.Anything).Return(nil)
				return provider
			}
			manager.RegisterFactory(factoryKey, factory)
		}(i)
	}

	wg.Wait()

	// Verify all factories are registered
	for i := range numGoroutines {
		factoryKey := fmt.Sprintf("language-%d", i)
		assert.True(t, manager.HasFactory(factoryKey))
	}
}

func TestComponentManager_DifferentServices_SameFactory(t *testing.T) {
	t.Parallel()

	manager := NewComponentManager[*MockProvider](mockFactoryKeyProvider, "test")

	// Register a factory
	factory := func() *MockProvider {
		provider := &MockProvider{name: "shared-factory-provider"}
		provider.On("Initialize", mock.Anything, mock.Anything).Return(nil)
		return provider
	}
	manager.RegisterFactory("go", factory)

	ctx := t.Context()

	// Create instances for different services using the same factory
	serviceConfig1 := createTestServiceConfig("web-service", "go")
	serviceConfig2 := createTestServiceConfig("api-service", "go")

	instance1, err := manager.GetOrCreateInstance(ctx, serviceConfig1)
	require.NoError(t, err)
	instance2, err := manager.GetOrCreateInstance(ctx, serviceConfig2)
	require.NoError(t, err)

	// They should be different instances
	assert.NotSame(t, instance1, instance2)
	assert.Equal(t, "shared-factory-provider", instance1.name)
	assert.Equal(t, "shared-factory-provider", instance2.name)

	// Both should be retrievable by service name
	retrieved1, err := manager.GetInstance("web-service")
	assert.NoError(t, err)
	assert.Same(t, instance1, retrieved1)

	retrieved2, err := manager.GetInstance("api-service")
	assert.NoError(t, err)
	assert.Same(t, instance2, retrieved2)
}
