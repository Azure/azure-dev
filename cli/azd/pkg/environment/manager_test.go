// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/state"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	emptyEnvList []*contracts.EnvListEnvironment = []*contracts.EnvListEnvironment{}
	localEnvList []*contracts.EnvListEnvironment = []*contracts.EnvListEnvironment{
		{
			Name:       "env1",
			IsDefault:  true,
			DotEnvPath: ".azure/env1/.env",
		},
		{
			Name:       "env2",
			IsDefault:  false,
			DotEnvPath: ".azure/env1/.env",
		},
	}
	remoteEnvList []*contracts.EnvListEnvironment = []*contracts.EnvListEnvironment{
		{
			Name:      "env1",
			IsDefault: false,
		},
		{
			Name:      "env2",
			IsDefault: false,
		},
		{
			Name:      "env3",
			IsDefault: false,
		},
	}

	getEnv *Environment = NewWithValues("env1", map[string]string{
		"key1":            "value1",
		EnvNameEnvVarName: "env1",
	})
)

func newManagerForTest(
	azdContext *azdcontext.AzdContext,
	console input.Console,
	localDataStore LocalDataStore,
	remoteDataStore RemoteDataStore,
) Manager {
	return &manager{
		azdContext: azdContext,
		console:    console,
		local:      localDataStore,
		remote:     remoteDataStore,
		envCache:   make(map[string]*Environment),
	}
}

func Test_EnvManager_PromptEnvironmentName(t *testing.T) {
	t.Run("valid name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "would you like to create it?")
		}).Respond(true)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Would you like to set the new environment")
		}).Respond(true)

		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).SetError(errors.New("prompt should not be called for valid environment name"))

		expected := "hello"
		envManager := createEnvManagerForManagerTest(t, mockContext)
		env, err := envManager.LoadOrInitInteractive(*mockContext.Context, expected)
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, expected, env.Name())
	})

	t.Run("empty name gets prompted", func(t *testing.T) {
		expected := "someEnv"

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Select an environment to use")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 0, nil // Create an environment
		})

		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).Respond(expected)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Would you like to set the new environment")
		}).Respond(true)

		envManager := createEnvManagerForManagerTest(t, mockContext)
		env, err := envManager.LoadOrInitInteractive(*mockContext.Context, "")

		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, expected, env.Name())
	})
}

func createEnvManagerForManagerTest(t *testing.T, mockContext *mocks.MockContext) Manager {
	azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	localDataStore := NewLocalFileDataStore(azdCtx, config.NewFileConfigManager(config.NewManager()))

	return newManagerForTest(azdCtx, mockContext.Console, localDataStore, nil)
}

func Test_EnvManager_CreateAndInitEnvironment(t *testing.T) {
	t.Run("invalid name", func(t *testing.T) {
		invalidEnvName := "*!33"

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "would you like to create it?")
		}).Respond(true)

		envManager := createEnvManagerForManagerTest(t, mockContext)
		env, err := envManager.LoadOrInitInteractive(*mockContext.Context, invalidEnvName)
		require.Error(t, err)
		require.Nil(t, env)
		require.ErrorContains(t, err, fmt.Sprintf("environment name '%s' is invalid", invalidEnvName))
	})
}

func Test_EnvManager_List(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())

	t.Run("LocalOnly", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		localDataStore.On("List", *mockContext.Context).Return(localEnvList, nil)
		remoteDataStore.On("List", *mockContext.Context).Return(emptyEnvList, nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		envList, err := manager.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)

		require.Equal(t, 2, len(envList))
		require.Equal(t, "env1", envList[0].Name)
		require.Equal(t, true, envList[0].HasLocal)
		require.Equal(t, false, envList[0].HasRemote)
		require.Equal(t, ".azure/env1/.env", envList[0].DotEnvPath)
	})

	t.Run("RemoteOnly", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		localDataStore.On("List", *mockContext.Context).Return(emptyEnvList, nil)
		remoteDataStore.On("List", *mockContext.Context).Return(remoteEnvList, nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		envList, err := manager.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)

		require.Equal(t, 3, len(envList))
		require.Equal(t, "env1", envList[0].Name)
		require.Equal(t, false, envList[0].HasLocal)
		require.Equal(t, true, envList[0].HasRemote)
		require.Equal(t, "", envList[0].DotEnvPath)
	})

	t.Run("LocalAndRemote", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		localDataStore.On("List", *mockContext.Context).Return(localEnvList, nil)
		remoteDataStore.On("List", *mockContext.Context).Return(remoteEnvList, nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		envList, err := manager.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)

		require.Equal(t, 3, len(envList))
		require.Equal(t, "env1", envList[0].Name)
		require.Equal(t, true, envList[0].HasLocal)
		require.Equal(t, true, envList[0].HasRemote)
		require.Equal(t, ".azure/env1/.env", envList[0].DotEnvPath)
	})
}

func Test_EnvManager_Get(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())

	t.Run("ExistsLocally", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		localDataStore.On("Get", *mockContext.Context, "env1").Return(getEnv, nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		env, err := manager.Get(*mockContext.Context, "env1")
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, getEnv, env)
		require.Equal(t, "env1", env.Name())
		require.Equal(t, "env1", env.Getenv(EnvNameEnvVarName))

		localDataStore.AssertNotCalled(t, "Save")
	})

	t.Run("ExistsRemotely", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		localDataStore.On("Get", *mockContext.Context, "env1").Return(nil, ErrNotFound)
		remoteDataStore.On("Get", *mockContext.Context, "env1").Return(getEnv, nil)
		localDataStore.On("Save", *mockContext.Context, getEnv, mock.Anything).Return(nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		env, err := manager.Get(*mockContext.Context, "env1")
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, getEnv, env)
	})

	t.Run("NotFound", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		localDataStore.On("Get", *mockContext.Context, "env1").Return(nil, ErrNotFound)
		remoteDataStore.On("Get", *mockContext.Context, "env1").Return(nil, ErrNotFound)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		env, err := manager.Get(*mockContext.Context, "env1")
		require.ErrorIs(t, err, ErrNotFound)
		require.Nil(t, env)
	})

	// Validates that environments with missing AZURE_ENV_NAME environment variable
	// are syncronized with the environment name.
	t.Run("MissingEnvVarName", func(t *testing.T) {
		localDataStore := &MockDataStore{}

		foundEnv := NewWithValues("env1", map[string]string{
			"key1": "value1",
		})

		localDataStore.On("Get", *mockContext.Context, "env1").Return(foundEnv, nil)
		localDataStore.On("Save", *mockContext.Context, foundEnv, mock.Anything).Return(nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, nil)
		env, err := manager.Get(*mockContext.Context, "env1")
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, getEnv, env)
		require.Equal(t, "env1", env.Name())
		require.Equal(t, "env1", env.Getenv(EnvNameEnvVarName))

		localDataStore.AssertCalled(t, "Save", *mockContext.Context, foundEnv, mock.Anything)
	})

	t.Run("InvalidEnvironmentName", func(t *testing.T) {
		localDataStore := &MockDataStore{}

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, nil)

		// Test various invalid names
		invalidNames := []string{
			"#invalid",
			"$bad%name",
			"no spaces",
			"no*asterisk",
		}

		for _, invalidName := range invalidNames {
			env, err := manager.Get(*mockContext.Context, invalidName)
			require.Error(t, err, "Should error for invalid name: %s", invalidName)
			require.Nil(t, env)
			require.Contains(t, err.Error(), "invalid")
			require.Contains(t, err.Error(), "alphanumeric")
		}

		// localDataStore.Get should never be called for invalid names
		localDataStore.AssertNotCalled(t, "Get")
	})
}

func Test_EnvManager_Save(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())

	t.Run("Success", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		env := NewWithValues("env1", map[string]string{
			"key1": "value1",
		})

		localDataStore.On("Save", *mockContext.Context, env, mock.Anything).Return(nil)
		remoteDataStore.On("Save", *mockContext.Context, env, mock.Anything).Return(nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		err := manager.Save(*mockContext.Context, env)
		require.NoError(t, err)

		localDataStore.AssertCalled(t, "Save", *mockContext.Context, env, mock.Anything)
		remoteDataStore.AssertCalled(t, "Save", *mockContext.Context, env, mock.Anything)
	})

	t.Run("Error", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		env := NewWithValues("env1", map[string]string{
			"key1": "value1",
		})

		localDataStore.On("Save", *mockContext.Context, env, mock.Anything).Return(errors.New("error"))

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		err := manager.Save(*mockContext.Context, env)
		require.Error(t, err)

		localDataStore.AssertCalled(t, "Save", *mockContext.Context, env, mock.Anything)
		remoteDataStore.AssertNotCalled(t, "Save", *mockContext.Context, env, mock.Anything)
	})
}

func Test_EnvManager_CreateFromContainer(t *testing.T) {
	t.Run("WithRemoteConfig", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerContainerComponents(t, mockContext)

		mockContext.Container.MustRegisterSingleton(func() *state.RemoteConfig {
			return &state.RemoteConfig{
				Backend: string(RemoteKindAzureBlobStorage),
				Config:  map[string]interface{}{},
			}
		})

		var envManager Manager
		err := mockContext.Container.Resolve(&envManager)
		require.NoError(t, err)

		manager := envManager.(*manager)
		require.NotNil(t, manager.local)
		require.NotNil(t, manager.remote)
		require.IsType(t, new(StorageBlobDataStore), manager.remote)
	})

	t.Run("WithoutRemoteConfig", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerContainerComponents(t, mockContext)

		mockContext.Container.MustRegisterSingleton(func() *state.RemoteConfig {
			return nil
		})

		var envManager Manager
		err := mockContext.Container.Resolve(&envManager)
		require.NoError(t, err)

		manager := envManager.(*manager)
		require.NotNil(t, manager.local)
		require.Nil(t, manager.remote)
	})
}

func registerContainerComponents(t *testing.T, mockContext *mocks.MockContext) {
	mockContext.Container.MustRegisterSingleton(func() context.Context {
		return *mockContext.Context
	})
	mockContext.Container.MustRegisterSingleton(func() auth.MultiTenantCredentialProvider {
		return mockContext.MultiTenantCredentialProvider
	})

	mockContext.Container.MustRegisterSingleton(NewManager)
	mockContext.Container.MustRegisterSingleton(NewLocalFileDataStore)
	mockContext.Container.MustRegisterNamedSingleton(string(RemoteKindAzureBlobStorage), NewStorageBlobDataStore)

	mockContext.Container.MustRegisterSingleton(func() *azcore.ClientOptions {
		return mockContext.CoreClientOptions
	})
	mockContext.Container.MustRegisterSingleton(storage.NewBlobSdkClient)
	mockContext.Container.MustRegisterSingleton(config.NewManager)
	mockContext.Container.MustRegisterSingleton(storage.NewBlobClient)

	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
	mockContext.Container.MustRegisterSingleton(func() *azdcontext.AzdContext {
		return azdContext
	})
	mockContext.Container.MustRegisterSingleton(func() auth.HttpClient {
		return mockContext.HttpClient
	})

	storageAccountConfig := &storage.AccountConfig{
		AccountName:   "test",
		ContainerName: "test",
	}
	mockContext.Container.MustRegisterSingleton(func() *storage.AccountConfig {
		return storageAccountConfig
	})

	mockContext.Container.MustRegisterSingleton(func() *cloud.Cloud {
		return cloud.AzurePublic()
	})
}

type MockDataStore struct {
	mock.Mock
}

func (m *MockDataStore) EnvPath(env *Environment) string {
	args := m.Called(env)
	return args.String(0)
}

func (m *MockDataStore) ConfigPath(env *Environment) string {
	args := m.Called(env)
	return args.String(0)
}

func (m *MockDataStore) List(ctx context.Context) ([]*contracts.EnvListEnvironment, error) {
	args := m.Called(ctx)
	return args.Get(0).([]*contracts.EnvListEnvironment), args.Error(1)
}

func (m *MockDataStore) Get(ctx context.Context, name string) (*Environment, error) {
	args := m.Called(ctx, name)

	env, ok := args.Get(0).(*Environment)
	if ok {
		return env, args.Error(1)
	}

	return nil, args.Error(1)
}

func (m *MockDataStore) Reload(ctx context.Context, env *Environment) error {
	args := m.Called(ctx, env)
	return args.Error(0)
}

func (m *MockDataStore) Save(ctx context.Context, env *Environment, options *SaveOptions) error {
	args := m.Called(ctx, env, options)
	return args.Error(0)
}

func (m *MockDataStore) Delete(ctx context.Context, name string) error {
	args := m.Called(ctx, name)
	return args.Error(0)
}

func Test_EnvManager_InstanceCaching(t *testing.T) {
	t.Run("Get returns same instance for same name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
		localDataStore := &MockDataStore{}

		env1 := NewWithValues("test-env", map[string]string{
			"key1":            "value1",
			EnvNameEnvVarName: "test-env",
		})

		// First Get should load from data store
		localDataStore.On("Get", *mockContext.Context, "test-env").Return(env1, nil).Once()

		// Subsequent Gets should trigger Reload on the cached instance
		localDataStore.On("Reload", *mockContext.Context, env1).Return(nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, nil)

		// First call - loads from data store
		result1, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)
		require.NotNil(t, result1)

		// Second call - should return same instance from cache (after reload)
		result2, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)
		require.NotNil(t, result2)

		// Verify it's the exact same pointer
		require.Same(t, result1, result2, "Get should return same instance for same environment name")

		// Third call - verify cache still works
		result3, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)
		require.Same(t, result1, result3, "Cache should persist across multiple calls")

		// Verify Reload was called for cached retrievals
		localDataStore.AssertCalled(t, "Reload", *mockContext.Context, env1)
		localDataStore.AssertNumberOfCalls(t, "Get", 1) // Only called once for initial load
	})

	t.Run("Cached instance is reloaded on each Get", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
		localDataStore := &MockDataStore{}

		env := NewWithValues("test-env", map[string]string{
			"key1":            "initial-value",
			EnvNameEnvVarName: "test-env",
		})

		localDataStore.On("Get", *mockContext.Context, "test-env").Return(env, nil).Once()

		// Mock Reload to simulate updating the environment with new data
		callCount := 0
		localDataStore.On("Reload", *mockContext.Context, env).Run(func(args mock.Arguments) {
			callCount++
			// Simulate reload updating the environment
			e := args.Get(1).(*Environment)
			e.DotenvSet("key1", fmt.Sprintf("reloaded-value-%d", callCount))
		}).Return(nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, nil)

		// First Get
		result1, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)
		require.Equal(t, "initial-value", result1.Getenv("key1"))

		// Second Get - should reload and get updated value
		result2, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)
		require.Same(t, result1, result2, "Should be same instance")
		require.Equal(t, "reloaded-value-1", result2.Getenv("key1"), "Reload should update values")

		// Third Get - should reload again
		result3, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)
		require.Same(t, result1, result3, "Should be same instance")
		require.Equal(t, "reloaded-value-2", result3.Getenv("key1"), "Reload should update values again")

		localDataStore.AssertNumberOfCalls(t, "Reload", 2)
	})

	t.Run("Reload failure returns error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
		localDataStore := &MockDataStore{}

		env := NewWithValues("test-env", map[string]string{
			"key1":            "value1",
			EnvNameEnvVarName: "test-env",
		})

		// First Get succeeds
		localDataStore.On("Get", *mockContext.Context, "test-env").Return(env, nil).Once()

		// Second Get: Reload fails - should return error
		reloadErr := errors.New("reload failed")
		localDataStore.On("Reload", *mockContext.Context, env).Return(reloadErr).Once()

		// Third Get: Reload succeeds
		localDataStore.On("Reload", *mockContext.Context, env).Return(nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, nil)

		// First call - loads and caches env
		result1, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)
		require.Same(t, env, result1)

		// Second call - reload fails, should return the error
		result2, err := manager.Get(*mockContext.Context, "test-env")
		require.Error(t, err)
		require.ErrorIs(t, err, reloadErr)
		require.Nil(t, result2, "Should return nil on reload failure")

		// Third call - reload succeeds, should return cached instance
		result3, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)
		require.Same(t, env, result3, "Should still return cached instance after reload succeeds")

		localDataStore.AssertNumberOfCalls(t, "Get", 1) // Only called once for initial load
		localDataStore.AssertNumberOfCalls(t, "Reload", 2)
	})

	t.Run("Delete removes from cache", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
		localDataStore := &MockDataStore{}

		env1 := NewWithValues("test-env", map[string]string{
			"key1":            "value1",
			EnvNameEnvVarName: "test-env",
		})
		env2 := NewWithValues("test-env", map[string]string{
			"key1":            "value2",
			EnvNameEnvVarName: "test-env",
		})

		// First Get loads env1
		localDataStore.On("Get", *mockContext.Context, "test-env").Return(env1, nil).Once()
		localDataStore.On("Reload", *mockContext.Context, env1).Return(nil)

		// Delete succeeds
		localDataStore.On("Delete", *mockContext.Context, "test-env").Return(nil).Once()

		// After delete, Get loads fresh env2
		localDataStore.On("Get", *mockContext.Context, "test-env").Return(env2, nil).Once()
		localDataStore.On("Reload", *mockContext.Context, env2).Return(nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, nil)

		// Load and cache env1
		result1, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)
		require.Same(t, env1, result1)

		// Verify it's cached
		result2, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)
		require.Same(t, env1, result2)

		// Delete the environment
		err = manager.Delete(*mockContext.Context, "test-env")
		require.NoError(t, err)

		// Get after delete should load fresh instance
		result3, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)
		require.Same(t, env2, result3, "After delete, should load fresh instance")
		require.NotSame(t, env1, result3, "Should not return cached instance after delete")

		localDataStore.AssertNumberOfCalls(t, "Get", 2) // Initial load and after delete
	})

	t.Run("Different environment names get different instances", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
		localDataStore := &MockDataStore{}

		env1 := NewWithValues("env1", map[string]string{
			"key1":            "value1",
			EnvNameEnvVarName: "env1",
		})
		env2 := NewWithValues("env2", map[string]string{
			"key1":            "value2",
			EnvNameEnvVarName: "env2",
		})

		localDataStore.On("Get", *mockContext.Context, "env1").Return(env1, nil)
		localDataStore.On("Get", *mockContext.Context, "env2").Return(env2, nil)
		localDataStore.On("Reload", *mockContext.Context, env1).Return(nil)
		localDataStore.On("Reload", *mockContext.Context, env2).Return(nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, nil)

		// Get env1
		result1, err := manager.Get(*mockContext.Context, "env1")
		require.NoError(t, err)
		require.Same(t, env1, result1)

		// Get env2
		result2, err := manager.Get(*mockContext.Context, "env2")
		require.NoError(t, err)
		require.Same(t, env2, result2)

		// Verify they are different instances
		require.NotSame(t, result1, result2, "Different environment names should have different instances")

		// Get env1 again - should return same instance as first call
		result3, err := manager.Get(*mockContext.Context, "env1")
		require.NoError(t, err)
		require.Same(t, result1, result3, "Same environment name should return same cached instance")
	})

	t.Run("Save does not affect cache", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
		localDataStore := &MockDataStore{}

		env := NewWithValues("test-env", map[string]string{
			"key1":            "value1",
			EnvNameEnvVarName: "test-env",
		})

		localDataStore.On("Get", *mockContext.Context, "test-env").Return(env, nil).Once()
		localDataStore.On("Reload", *mockContext.Context, env).Return(nil)
		localDataStore.On("Save", *mockContext.Context, env, mock.Anything).Return(nil)

		manager := newManagerForTest(azdContext, mockContext.Console, localDataStore, nil)

		// Get and cache the environment
		result1, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)

		// Modify and save
		result1.DotenvSet("key2", "value2")
		err = manager.Save(*mockContext.Context, result1)
		require.NoError(t, err)

		// Get again - should return same instance with modifications
		result2, err := manager.Get(*mockContext.Context, "test-env")
		require.NoError(t, err)
		require.Same(t, result1, result2, "Save should not affect cached instance")
		require.Equal(t, "value2", result2.Getenv("key2"), "Modifications should be visible in cached instance")

		localDataStore.AssertNumberOfCalls(t, "Get", 1) // Only initial load, not after Save
	})
}
