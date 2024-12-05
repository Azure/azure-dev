package cache

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/require"
)

type testStruct struct {
	Field1 string
	Field2 int
}

func Test_FileCache_Resolve(t *testing.T) {
	ctx := context.Background()
	expected := &testStruct{Field1: "value1", Field2: 42}

	t.Run("FromResolver", func(t *testing.T) {
		loadedFromResolver := false
		cachePath := filepath.Join(t.TempDir(), "test.cache")

		// Create a FileCache instance
		fileCache := NewFileCache(cachePath, 1*time.Hour, func(ctx context.Context) (*testStruct, error) {
			loadedFromResolver = true
			return expected, nil
		})

		// Resolve the cache
		value, err := fileCache.Resolve(ctx)
		require.NoError(t, err)
		require.Equal(t, expected, value)
		require.True(t, loadedFromResolver)
	})

	t.Run("FromResolverError", func(t *testing.T) {
		loadedFromResolver := false
		cachePath := filepath.Join(t.TempDir(), "test.cache")

		// Create a FileCache instance
		fileCache := NewFileCache(cachePath, 1*time.Hour, func(ctx context.Context) (*testStruct, error) {
			loadedFromResolver = true
			return nil, errors.New("resolver error")
		})

		// Resolve the cache
		value, err := fileCache.Resolve(ctx)
		require.Error(t, err)
		require.Nil(t, value)
		require.True(t, loadedFromResolver)
	})

	t.Run("FromValidCacheFile", func(t *testing.T) {
		loadedFromResolver := false
		cachePath := filepath.Join(t.TempDir(), "test.cache")

		// Create valid cache file
		jsonBytes, err := json.Marshal(expected)
		require.NoError(t, err)

		err = os.WriteFile(cachePath, jsonBytes, osutil.PermissionFile)
		require.NoError(t, err)

		// Create a FileCache instance
		fileCache := NewFileCache(cachePath, 1*time.Hour, func(ctx context.Context) (*testStruct, error) {
			loadedFromResolver = true
			return expected, nil
		})

		// Resolve the cache
		value, err := fileCache.Resolve(ctx)
		require.NoError(t, err)
		require.Equal(t, expected, value)
		require.False(t, loadedFromResolver)
	})

	t.Run("FromValidCacheFileBusted", func(t *testing.T) {
		err := os.Setenv("AZD_NO_CACHE", "true")
		require.NoError(t, err)

		defer os.Setenv("AZD_NO_CACHE", "")

		loadedFromResolver := false
		cachePath := filepath.Join(t.TempDir(), "test.cache")

		// Create valid cache file
		jsonBytes, err := json.Marshal(expected)
		require.NoError(t, err)

		err = os.WriteFile(cachePath, jsonBytes, osutil.PermissionFile)
		require.NoError(t, err)

		// Create a FileCache instance
		fileCache := NewFileCache(cachePath, 1*time.Hour, func(ctx context.Context) (*testStruct, error) {
			loadedFromResolver = true
			return expected, nil
		})

		// Resolve the cache
		value, err := fileCache.Resolve(ctx)
		require.NoError(t, err)
		require.Equal(t, expected, value)
		require.True(t, loadedFromResolver)
	})

	t.Run("FromStaleCacheFile", func(t *testing.T) {
		loadedFromResolver := false
		cachePath := filepath.Join(t.TempDir(), "test.cache")

		// Create valid cache file
		jsonBytes, err := json.Marshal(expected)
		require.NoError(t, err)

		err = os.WriteFile(cachePath, jsonBytes, osutil.PermissionFile)
		require.NoError(t, err)

		// Create a FileCache instance
		fileCache := NewFileCache(cachePath, 1*time.Millisecond, func(ctx context.Context) (*testStruct, error) {
			loadedFromResolver = true
			return expected, nil
		})

		time.Sleep(10 * time.Millisecond)

		// Resolve the cache
		value, err := fileCache.Resolve(ctx)
		require.NoError(t, err)
		require.Equal(t, expected, value)
		require.True(t, loadedFromResolver)
	})
}

func Test_FileCache_Set(t *testing.T) {
	// Create a temporary file
	loadedFromResolver := false
	cachePath := filepath.Join(t.TempDir(), "test.cache")
	expected := &testStruct{Field1: "value1", Field2: 42}

	// Create a FileCache instance
	fileCache := NewFileCache(cachePath, 1*time.Hour, func(ctx context.Context) (*testStruct, error) {
		loadedFromResolver = true
		return expected, nil
	})

	// Set the cache
	err := fileCache.Set(expected)
	require.NoError(t, err)

	// Read the file and check the content
	data, err := os.ReadFile(cachePath)
	require.NoError(t, err)

	var loadedValue *testStruct
	err = json.Unmarshal(data, &loadedValue)
	require.NoError(t, err)
	require.Equal(t, *expected, *loadedValue)
	require.False(t, loadedFromResolver)
}

func Test_FileCache_Remove(t *testing.T) {
	// Create a temporary file
	cachePath := filepath.Join(t.TempDir(), "test.cache")
	expected := &testStruct{Field1: "value1", Field2: 42}

	jsonBytes, err := json.Marshal(expected)
	require.NoError(t, err)

	err = os.WriteFile(cachePath, jsonBytes, osutil.PermissionFile)
	require.NoError(t, err)

	// Create a FileCache instance
	fileCache := NewFileCache(cachePath, 1*time.Hour, func(ctx context.Context) (*testStruct, error) {
		return expected, nil
	})

	// Remove the cache
	err = fileCache.Remove()
	require.NoError(t, err)

	// Check that the file does not exist
	_, err = os.Stat(cachePath)
	require.ErrorIs(t, err, fs.ErrNotExist)
}
