// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestStorageBlobReloadFailurePreservesState(t *testing.T) {
	for _, tt := range []struct {
		name   string
		dotenv string
		config string
		want   string
	}{
		{name: "malformed dotenv", dotenv: "invalid='", want: "loading .env"},
		{name: "invalid config", dotenv: "VALUE=on-disk", config: "{invalid", want: "loading config"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockBlobClient{}
			store := NewStorageBlobDataStore(config.NewManager(), client)
			env := New("test")
			env.DotenvSet("VALUE", "in-memory")
			env.DotenvDelete("PENDING")
			client.On("Download", t.Context(), "test/.env").
				Return(io.NopCloser(strings.NewReader(tt.dotenv)), nil).Once()
			client.On("Download", t.Context(), "test/config.json").
				Return(io.NopCloser(strings.NewReader(tt.config)), nil).Maybe()

			require.ErrorContains(t, store.Reload(t.Context(), env), tt.want)
			require.Equal(t, "in-memory", env.Getenv("VALUE"))
			require.Contains(t, env.deletedKeys, "PENDING")
			client.AssertExpectations(t)
		})
	}
}

var validBlobItems []*storage.Blob = []*storage.Blob{
	{
		Name: ".env",
		Path: "env1/.env",
	},
	{
		Name: "config.json",
		Path: "env1/config.env",
	},
	{
		Name: ".env",
		Path: "env2/.env",
	},
	{
		Name: "config.json",
		Path: "env2/config.env",
	},
}

func Test_StorageBlobDataStore_List(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	configManager := config.NewManager()

	t.Run("List", func(t *testing.T) {
		blobClient := &MockBlobClient{}
		blobClient.On("Items", *mockContext.Context).Return(validBlobItems, nil)
		dataStore := NewStorageBlobDataStore(configManager, blobClient)

		envList, err := dataStore.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
		require.Equal(t, 2, len(envList))
		require.Equal(t, "env1", envList[0].Name)
		require.Equal(t, "env2", envList[1].Name)
	})

	t.Run("Empty", func(t *testing.T) {
		blobClient := &MockBlobClient{}
		blobClient.On("Items", *mockContext.Context).Return(nil, storage.ErrContainerNotFound)
		dataStore := NewStorageBlobDataStore(configManager, blobClient)

		envList, err := dataStore.List(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, envList)
		require.Len(t, envList, 0)
	})
}

func Test_StorageBlobDataStore_SaveAndGet(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	configManager := config.NewManager()
	blobClient := &MockBlobClient{}
	dataStore := NewStorageBlobDataStore(configManager, blobClient)

	t.Run("Success", func(t *testing.T) {
		envReader := io.NopCloser(bytes.NewReader([]byte("key1=value1")))
		configReader := io.NopCloser(bytes.NewReader([]byte("{}")))
		blobClient.On("Items", *mockContext.Context).Return(validBlobItems, nil)
		blobClient.On("Download", *mockContext.Context, "env1/.env").Return(envReader, nil)
		blobClient.On("Download", *mockContext.Context, "env1/config.json").Return(configReader, nil)
		blobClient.On("Upload", *mockContext.Context, mock.AnythingOfType("string"), mock.Anything).Return(nil)

		env1 := New("env1")
		env1.DotenvSet("key1", "value1")
		err := dataStore.Save(*mockContext.Context, env1, nil)
		require.NoError(t, err)

		env, err := dataStore.Get(*mockContext.Context, "env1")
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, "env1", env.name)
		actual := env1.Getenv("key1")
		require.Equal(t, "value1", actual)
	})
}

func Test_StorageBlobDataStore_Path(t *testing.T) {
	configManager := config.NewManager()
	blobClient := &MockBlobClient{}
	dataStore := NewStorageBlobDataStore(configManager, blobClient)

	env := New("env1")
	expected := fmt.Sprintf("%s/%s", env.name, DotEnvFileName)
	actual := dataStore.EnvPath(env)

	require.Equal(t, expected, actual)
}

func Test_StorageBlobDataStore_ConfigPath(t *testing.T) {
	configManager := config.NewManager()
	blobClient := &MockBlobClient{}
	dataStore := NewStorageBlobDataStore(configManager, blobClient)

	env := New("env1")
	expected := fmt.Sprintf("%s/%s", env.name, ConfigFileName)
	actual := dataStore.ConfigPath(env)

	require.Equal(t, expected, actual)
}

type MockBlobClient struct {
	mock.Mock
}

func (m *MockBlobClient) Download(ctx context.Context, blobPath string) (io.ReadCloser, error) {
	args := m.Called(ctx, blobPath)
	return args.Get(0).(io.ReadCloser), args.Error(1)
}

func (m *MockBlobClient) Upload(ctx context.Context, blobPath string, reader io.Reader) error {
	args := m.Called(ctx, blobPath, reader)
	return args.Error(0)
}

func (m *MockBlobClient) Delete(ctx context.Context, blobPath string) error {
	args := m.Called(ctx, blobPath)
	return args.Error(0)
}

func (m *MockBlobClient) Items(ctx context.Context) ([]*storage.Blob, error) {
	args := m.Called(ctx)

	value, ok := args.Get(0).([]*storage.Blob)
	if !ok {
		return nil, args.Error(1)
	}

	return value, args.Error(1)
}
