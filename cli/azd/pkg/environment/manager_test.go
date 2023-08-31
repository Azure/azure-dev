package environment

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	emptyEnvList []*contracts.EnvListEnvironment = []*contracts.EnvListEnvironment{}
	localEnvList []*contracts.EnvListEnvironment = []*contracts.EnvListEnvironment{
		{
			Name:      "env1",
			IsDefault: true,
		},
		{
			Name:      "env2",
			IsDefault: false,
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
		"key1": "value1",
	})
)

func Test_EnvManager_List(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())

	t.Run("LocalOnly", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		localDataStore.On("List", *mockContext.Context).Return(localEnvList, nil)
		remoteDataStore.On("List", *mockContext.Context).Return(emptyEnvList, nil)

		manager := NewManager(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		envList, err := manager.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)

		require.Equal(t, 2, len(envList))
		require.Equal(t, "env1", envList[0].Name)
		require.Equal(t, true, envList[1].HasLocal)
		require.Equal(t, false, envList[1].HasRemote)
	})

	t.Run("RemoteOnly", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		localDataStore.On("List", *mockContext.Context).Return(emptyEnvList, nil)
		remoteDataStore.On("List", *mockContext.Context).Return(remoteEnvList, nil)

		manager := NewManager(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		envList, err := manager.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)

		require.Equal(t, 3, len(envList))
		require.Equal(t, "env1", envList[0].Name)
		require.Equal(t, false, envList[1].HasLocal)
		require.Equal(t, true, envList[1].HasRemote)
	})

	t.Run("LocalAndRemote", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		localDataStore.On("List", *mockContext.Context).Return(localEnvList, nil)
		remoteDataStore.On("List", *mockContext.Context).Return(remoteEnvList, nil)

		manager := NewManager(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		envList, err := manager.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)

		require.Equal(t, 3, len(envList))
		require.Equal(t, "env1", envList[0].Name)
		require.Equal(t, true, envList[1].HasLocal)
		require.Equal(t, true, envList[1].HasRemote)
	})
}

func Test_EnvManager_Get(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())

	t.Run("ExistsLocally", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		localDataStore.On("Get", *mockContext.Context, "env1").Return(getEnv, nil)

		manager := NewManager(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		env, err := manager.Get(*mockContext.Context, "env1")
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, getEnv, env)
	})

	t.Run("ExistsRemotely", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		localDataStore.On("Get", *mockContext.Context, "env1").Return(nil, ErrNotFound)
		remoteDataStore.On("Get", *mockContext.Context, "env1").Return(getEnv, nil)
		localDataStore.On("Save", *mockContext.Context, getEnv).Return(nil)

		manager := NewManager(azdContext, mockContext.Console, localDataStore, remoteDataStore)
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

		manager := NewManager(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		env, err := manager.Get(*mockContext.Context, "env1")
		require.ErrorIs(t, err, ErrNotFound)
		require.Nil(t, env)
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

		localDataStore.On("Save", *mockContext.Context, env).Return(nil)
		remoteDataStore.On("Save", *mockContext.Context, env).Return(nil)

		manager := NewManager(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		err := manager.Save(*mockContext.Context, env)
		require.NoError(t, err)

		localDataStore.AssertCalled(t, "Save", *mockContext.Context, env)
		remoteDataStore.AssertCalled(t, "Save", *mockContext.Context, env)
	})

	t.Run("Error", func(t *testing.T) {
		localDataStore := &MockDataStore{}
		remoteDataStore := &MockDataStore{}

		env := NewWithValues("env1", map[string]string{
			"key1": "value1",
		})

		localDataStore.On("Save", *mockContext.Context, env).Return(errors.New("error"))

		manager := NewManager(azdContext, mockContext.Console, localDataStore, remoteDataStore)
		err := manager.Save(*mockContext.Context, env)
		require.Error(t, err)

		localDataStore.AssertCalled(t, "Save", *mockContext.Context, env)
		remoteDataStore.AssertNotCalled(t, "Save", *mockContext.Context, env)
	})
}

type MockDataStore struct {
	mock.Mock
}

func (m *MockDataStore) Path(env *Environment) string {
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

func (m *MockDataStore) Save(ctx context.Context, env *Environment) error {
	args := m.Called(ctx, env)
	return args.Error(0)
}
