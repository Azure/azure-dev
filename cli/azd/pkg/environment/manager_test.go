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

func Test_EnvManager_CreateWithType(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		envManager := createEnvManagerForManagerTest(t, mockContext)

		spec := Spec{
			Name:         "test-env",
			Subscription: "test-subscription",
			Location:     "eastus",
			Type:         "development",
		}

		env, err := envManager.Create(*mockContext.Context, spec)
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, "test-env", env.Name())
		require.Equal(t, "test-subscription", env.GetSubscriptionId())
		require.Equal(t, "eastus", env.GetLocation())
		require.Equal(t, "development", env.GetEnvironmentType())

		// Verify it's in the dotenv
		dotenv := env.Dotenv()
		require.Equal(t, "development", dotenv["AZURE_ENV_TYPE"])
	})
}

func Test_EnvManager_LoadOrInitInteractiveWithType(t *testing.T) {
	t.Run("creates new environment with type when not exists", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "would you like to create it?")
		}).Respond(true)

		envManager := createEnvManagerForManagerTest(t, mockContext)

		// Test creating new environment with type
		envName := "new-test-env"
		envType := "production"

		env, err := envManager.LoadOrInitInteractiveWithType(*mockContext.Context, envName, envType)
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, envName, env.Name())
		require.Equal(t, envType, env.GetEnvironmentType())

		// Verify it's in the dotenv
		dotenv := env.Dotenv()
		require.Equal(t, envType, dotenv["AZURE_ENV_TYPE"])
	})

	t.Run("loads existing environment ignoring type parameter", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		envManager := createEnvManagerForManagerTest(t, mockContext)

		// First create an environment with one type
		originalType := "development"
		envName := "existing-env"

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "would you like to create it?")
		}).Respond(true)

		env1, err := envManager.LoadOrInitInteractiveWithType(*mockContext.Context, envName, originalType)
		require.NoError(t, err)
		require.Equal(t, originalType, env1.GetEnvironmentType())

		// Now try to load the same environment with a different type - should ignore the new type
		differentType := "production"
		env2, err := envManager.LoadOrInitInteractiveWithType(*mockContext.Context, envName, differentType)
		require.NoError(t, err)
		require.Equal(t, envName, env2.Name())
		// Should still have the original type, not the new one
		require.Equal(t, originalType, env2.GetEnvironmentType())
	})

	t.Run("creates new environment without type when empty type provided", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "would you like to create it?")
		}).Respond(true)

		envManager := createEnvManagerForManagerTest(t, mockContext)

		envName := "no-type-env"

		env, err := envManager.LoadOrInitInteractiveWithType(*mockContext.Context, envName, "")
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, envName, env.Name())
		require.Equal(t, "", env.GetEnvironmentType())

		// Verify AZURE_ENV_TYPE is not set in dotenv when no type specified
		dotenv := env.Dotenv()
		require.NotContains(t, dotenv, "AZURE_ENV_TYPE")
	})

	t.Run("delegates to LoadOrInitInteractive when no type specified", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "would you like to create it?")
		}).Respond(true)

		envManager := createEnvManagerForManagerTest(t, mockContext)

		envName := "delegate-test-env"

		// Both methods should produce the same result when no type is specified
		env1, err1 := envManager.LoadOrInitInteractive(*mockContext.Context, envName)
		require.NoError(t, err1)

		// Create another environment with the new method but empty type
		envName2 := "delegate-test-env-2"
		env2, err2 := envManager.LoadOrInitInteractiveWithType(*mockContext.Context, envName2, "")
		require.NoError(t, err2)

		// Both should have empty environment type
		require.Equal(t, "", env1.GetEnvironmentType())
		require.Equal(t, "", env2.GetEnvironmentType())
	})

	t.Run("validates environment type and returns error for invalid type", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "would you like to create it?")
		}).Respond(true)

		envManager := createEnvManagerForManagerTest(t, mockContext)

		envName := "test-env"
		invalidEnvType := "invalid-type-with-hyphens" // hyphens not allowed in environment type

		_, err := envManager.LoadOrInitInteractiveWithType(*mockContext.Context, envName, invalidEnvType)
		require.Error(t, err)
		require.Contains(t, err.Error(), "environment type 'invalid-type-with-hyphens' is invalid")
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
