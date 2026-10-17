// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk/storage"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
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
				Return(blobDownload(tt.dotenv, "etag-env"), nil).Once()
			client.On("Download", t.Context(), "test/config.json").
				Return(blobDownload(tt.config, "etag-cfg"), nil).Maybe()

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
		Path: "env1/config.json",
	},
	{
		Name: ".env",
		Path: "env2/.env",
	},
	{
		Name: "config.json",
		Path: "env2/config.json",
	},
}

func TestStorageBlobDataStore_ListSkipsInvalidLayouts(t *testing.T) {
	client := &MockBlobClient{}
	items := append([]*storage.Blob{}, validBlobItems...)
	for _, key := range []string{
		"archive/prod/.env", "other/prod/config.json", "/prod/.env", "prod//.env",
		"prod/.env/extra", "prod/config.env", "prod/unrelated", ".env", "../.env",
		`prod\.env`, "invalid name/.env", "bad\nname/.env",
	} {
		items = append(items, &storage.Blob{Name: DotEnvFileName, Path: key})
	}
	client.On("Items", t.Context()).Return(items, nil).Twice()
	store := NewStorageBlobDataStore(config.NewManager(), client)

	envs, err := store.List(t.Context())

	require.NoError(t, err)
	require.Equal(t, []*contracts.EnvListEnvironment{
		{Name: "env1", DotEnvPath: "env1/.env", ConfigPath: "env1/config.json"},
		{Name: "env2", DotEnvPath: "env2/.env", ConfigPath: "env2/config.json"},
	}, envs)
	_, err = store.Get(t.Context(), "prod")
	require.ErrorIs(t, err, ErrNotFound)
	client.AssertNotCalled(t, "Download", mock.Anything, mock.Anything)
	client.AssertExpectations(t)
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
	blobClient := newMemoryBlobClient()
	dataStore := NewStorageBlobDataStore(config.NewManager(), blobClient)

	t.Run("Success", func(t *testing.T) {
		env1 := New("env1")
		env1.DotenvSet("key1", "value1")
		err := dataStore.Save(t.Context(), env1, nil)
		require.NoError(t, err)

		env, err := dataStore.Get(t.Context(), "env1")
		require.NoError(t, err)
		require.NotNil(t, env)
		require.Equal(t, "env1", env.name)
		require.Equal(t, "value1", env.Getenv("key1"))
	})
}

func Test_StorageBlobDataStore_Path(t *testing.T) {
	configManager := config.NewManager()
	blobClient := &MockBlobClient{}
	dataStore := NewStorageBlobDataStore(configManager, blobClient)

	env := New("env1")
	expected := fmt.Sprintf("%s/%s", env.name, DotEnvFileName)
	actual, err := dataStore.EnvPath(env)
	require.NoError(t, err)

	require.Equal(t, expected, actual)
}

func Test_StorageBlobDataStore_ConfigPath(t *testing.T) {
	configManager := config.NewManager()
	blobClient := &MockBlobClient{}
	dataStore := NewStorageBlobDataStore(configManager, blobClient)

	env := New("env1")
	expected := fmt.Sprintf("%s/%s", env.name, ConfigFileName)
	actual, err := dataStore.ConfigPath(env)
	require.NoError(t, err)

	require.Equal(t, expected, actual)
}

// TestStorageBlobDataStore_InterleavedSavesConflict verifies optimistic concurrency:
// two stores load the same remote snapshot, the first save wins, and the second
// save fails with a conflict without overwriting the winner's blob.
func TestStorageBlobDataStore_InterleavedSavesConflict(t *testing.T) {
	client := newMemoryBlobClient()
	_, err := client.Upload(t.Context(), "env1/config.json", strings.NewReader("{}"), "")
	require.NoError(t, err)
	_, err = client.Upload(t.Context(), "env1/.env", strings.NewReader("KEY=original\n"), "")
	require.NoError(t, err)

	storeA := NewStorageBlobDataStore(config.NewManager(), client)
	storeB := NewStorageBlobDataStore(config.NewManager(), client)

	envA := New("env1")
	envB := New("env1")
	require.NoError(t, storeA.Reload(t.Context(), envA))
	require.NoError(t, storeB.Reload(t.Context(), envB))

	envA.DotenvSet("KEY", "from-a")
	envB.DotenvSet("KEY", "from-b")

	require.NoError(t, storeA.Save(t.Context(), envA, nil))

	err = storeB.Save(t.Context(), envB, nil)
	require.Error(t, err, "second save must fail with a conflict instead of overwriting")
	require.ErrorIs(t, err, ErrRemoteStateConflict)
	require.ErrorIs(t, err, storage.ErrConcurrencyConflict)

	download, err := client.Download(t.Context(), "env1/.env")
	require.NoError(t, err)
	data, err := io.ReadAll(download.Body)
	require.NoError(t, err)
	require.NoError(t, download.Body.Close())
	require.Contains(t, string(data), "from-a")
	require.NotContains(t, string(data), "from-b")
}

func TestStorageBlobDataStore_SaveUsesLoadedETag(t *testing.T) {
	client := newMemoryBlobClient()
	_, err := client.Upload(t.Context(), "env1/config.json", strings.NewReader("{}"), "")
	require.NoError(t, err)
	_, err = client.Upload(t.Context(), "env1/.env", strings.NewReader("KEY=original\n"), "")
	require.NoError(t, err)

	loadedEnv, err := client.Download(t.Context(), "env1/.env")
	require.NoError(t, err)
	require.NoError(t, loadedEnv.Body.Close())
	loadedCfg, err := client.Download(t.Context(), "env1/config.json")
	require.NoError(t, err)
	require.NoError(t, loadedCfg.Body.Close())

	store := NewStorageBlobDataStore(config.NewManager(), client)
	env := New("env1")
	require.NoError(t, store.Reload(t.Context(), env))
	env.DotenvSet("KEY", "updated")
	require.NoError(t, store.Save(t.Context(), env, nil))

	require.Equal(t, loadedEnv.ETag, client.lastMatch["env1/.env"])
	require.Equal(t, loadedCfg.ETag, client.lastMatch["env1/config.json"])
	require.False(t, client.lastNoneMatchStar["env1/.env"])
	require.False(t, client.lastNoneMatchStar["env1/config.json"])
}

func TestStorageBlobDataStore_NewBlobUsesIfNoneMatchStar(t *testing.T) {
	client := newMemoryBlobClient()
	store := NewStorageBlobDataStore(config.NewManager(), client)
	env := New("env1")
	env.DotenvSet("KEY", "created")

	require.NoError(t, store.Save(t.Context(), env, nil))

	require.Equal(t, azcore.ETag(""), client.lastMatch["env1/.env"])
	require.Equal(t, azcore.ETag(""), client.lastMatch["env1/config.json"])
	require.True(t, client.lastNoneMatchStar["env1/.env"])
	require.True(t, client.lastNoneMatchStar["env1/config.json"])
}

func blobDownload(body string, etag azcore.ETag) *storage.BlobDownload {
	return &storage.BlobDownload{
		Body: io.NopCloser(strings.NewReader(body)),
		ETag: etag,
	}
}

type storedBlob struct {
	data []byte
	etag azcore.ETag
}

// memoryBlobClient is an in-memory BlobClient that enforces ETag preconditions.
type memoryBlobClient struct {
	mu                sync.Mutex
	blobs             map[string]storedBlob
	gen               int
	lastMatch         map[string]azcore.ETag
	lastNoneMatchStar map[string]bool
}

func newMemoryBlobClient() *memoryBlobClient {
	return &memoryBlobClient{
		blobs:             map[string]storedBlob{},
		lastMatch:         map[string]azcore.ETag{},
		lastNoneMatchStar: map[string]bool{},
	}
}

func (m *memoryBlobClient) Download(ctx context.Context, blobPath string) (*storage.BlobDownload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	blob, ok := m.blobs[blobPath]
	if !ok {
		return nil, &azcore.ResponseError{StatusCode: 404, ErrorCode: "BlobNotFound"}
	}
	return &storage.BlobDownload{
		Body: io.NopCloser(bytes.NewReader(blob.data)),
		ETag: blob.etag,
	}, nil
}

func (m *memoryBlobClient) Upload(
	ctx context.Context, blobPath string, reader io.Reader, match azcore.ETag,
) (azcore.ETag, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastMatch[blobPath] = match
	m.lastNoneMatchStar[blobPath] = match == ""

	existing, ok := m.blobs[blobPath]
	if match == "" {
		if ok {
			return "", &storage.ConcurrencyConflictError{BlobPath: blobPath}
		}
	} else if !ok || existing.etag != match {
		return "", &storage.ConcurrencyConflictError{BlobPath: blobPath}
	}

	m.gen++
	etag := azcore.ETag(fmt.Sprintf("etag-%d", m.gen))
	m.blobs[blobPath] = storedBlob{data: data, etag: etag}
	return etag, nil
}

func (m *memoryBlobClient) Delete(ctx context.Context, blobPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.blobs, blobPath)
	return nil
}

func (m *memoryBlobClient) Items(ctx context.Context) ([]*storage.Blob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]*storage.Blob, 0, len(m.blobs))
	for path := range m.blobs {
		name := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			name = path[i+1:]
		}
		items = append(items, &storage.Blob{Name: name, Path: path})
	}
	return items, nil
}

type MockBlobClient struct {
	mock.Mock
}

func (m *MockBlobClient) Download(ctx context.Context, blobPath string) (*storage.BlobDownload, error) {
	args := m.Called(ctx, blobPath)
	value, ok := args.Get(0).(*storage.BlobDownload)
	if !ok {
		return nil, args.Error(1)
	}
	return value, args.Error(1)
}

func (m *MockBlobClient) Upload(
	ctx context.Context, blobPath string, reader io.Reader, match azcore.ETag,
) (azcore.ETag, error) {
	args := m.Called(ctx, blobPath, reader, match)
	etag, _ := args.Get(0).(azcore.ETag)
	return etag, args.Error(1)
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
