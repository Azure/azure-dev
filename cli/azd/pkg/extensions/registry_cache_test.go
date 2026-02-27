// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_sanitizeSourceName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple name", "azd", "azd"},
		{"with dots", "my.source", "my.source"},
		{"with dashes", "my-source", "my-source"},
		{"with underscores", "my_source", "my_source"},
		{"with spaces", "my source", "my_source"},
		{"with special chars", "my@source:test", "my_source_test"},
		{"uppercase", "MySource", "mysource"},
		{"url-like", "https://example.com", "https___example.com"},
		{"empty", "", "default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeSourceName(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func Test_RegistryCacheManager_GetSet(t *testing.T) {
	// Create a temp directory for cache
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	cacheManager, err := NewRegistryCacheManager()
	require.NoError(t, err)
	require.NotNil(t, cacheManager)

	ctx := context.Background()
	sourceName := "test-source"

	// Initially cache should not exist
	_, err = cacheManager.Get(ctx, sourceName)
	require.ErrorIs(t, err, ErrCacheNotFound)

	// Set cache
	extensions := []*ExtensionMetadata{
		{
			Id:          "test.extension",
			DisplayName: "Test Extension",
			Versions: []ExtensionVersion{
				{Version: "1.0.0"},
				{Version: "1.1.0"},
			},
		},
	}

	err = cacheManager.Set(ctx, sourceName, extensions)
	require.NoError(t, err)

	// Verify cache file was created
	cacheFile := filepath.Join(cacheManager.GetCacheDir(), "test-source.json")
	require.FileExists(t, cacheFile)

	// Get cache should now succeed
	cache, err := cacheManager.Get(ctx, sourceName)
	require.NoError(t, err)
	require.NotNil(t, cache)
	require.Len(t, cache.Extensions, 1)
	require.Equal(t, "test.extension", cache.Extensions[0].Id)
}

func Test_RegistryCacheManager_Expiration(t *testing.T) {
	// Create a temp directory for cache
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	// Set a very short TTL for testing
	t.Setenv(cacheTTLEnvVar, "1ms")

	cacheManager, err := NewRegistryCacheManager()
	require.NoError(t, err)

	ctx := context.Background()
	sourceName := "expiring-source"

	// Set cache
	err = cacheManager.Set(ctx, sourceName, []*ExtensionMetadata{})
	require.NoError(t, err)

	// Wait for cache to expire
	time.Sleep(10 * time.Millisecond)

	// Get should return expired error
	_, err = cacheManager.Get(ctx, sourceName)
	require.ErrorIs(t, err, ErrCacheExpired)
}

func Test_RegistryCacheManager_CustomTTL(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)
	t.Setenv(cacheTTLEnvVar, "2h")

	ttl := getCacheTTL()
	require.Equal(t, 2*time.Hour, ttl)
}

func Test_RegistryCacheManager_InvalidTTL(t *testing.T) {
	t.Setenv(cacheTTLEnvVar, "invalid")

	ttl := getCacheTTL()
	require.Equal(t, defaultCacheTTL, ttl)
}

func Test_RegistryCacheManager_IsExpiredOrMissing(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	cacheManager, err := NewRegistryCacheManager()
	require.NoError(t, err)

	ctx := context.Background()

	// Missing cache
	require.True(t, cacheManager.IsExpiredOrMissing(ctx, "missing-source"))

	// Set cache
	err = cacheManager.Set(ctx, "existing-source", []*ExtensionMetadata{})
	require.NoError(t, err)

	// Should not be expired
	require.False(t, cacheManager.IsExpiredOrMissing(ctx, "existing-source"))
}

func Test_RegistryCacheManager_CorruptCache(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	cacheManager, err := NewRegistryCacheManager()
	require.NoError(t, err)

	// Write corrupt JSON to cache file
	cacheFile := filepath.Join(cacheManager.GetCacheDir(), "corrupt-source.json")
	err = os.WriteFile(cacheFile, []byte("not valid json"), 0600)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = cacheManager.Get(ctx, "corrupt-source")
	require.ErrorIs(t, err, ErrCacheNotFound)
}

func Test_RegistryCacheManager_PerSourceIsolation(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	cacheManager, err := NewRegistryCacheManager()
	require.NoError(t, err)

	ctx := context.Background()

	// Set cache for source A
	extensionsA := []*ExtensionMetadata{
		{Id: "extension.a", DisplayName: "Extension A"},
	}
	err = cacheManager.Set(ctx, "source-a", extensionsA)
	require.NoError(t, err)

	// Set cache for source B
	extensionsB := []*ExtensionMetadata{
		{Id: "extension.b", DisplayName: "Extension B"},
	}
	err = cacheManager.Set(ctx, "source-b", extensionsB)
	require.NoError(t, err)

	// Verify isolation
	cacheA, err := cacheManager.Get(ctx, "source-a")
	require.NoError(t, err)
	require.Len(t, cacheA.Extensions, 1)
	require.Equal(t, "extension.a", cacheA.Extensions[0].Id)

	cacheB, err := cacheManager.Get(ctx, "source-b")
	require.NoError(t, err)
	require.Len(t, cacheB.Extensions, 1)
	require.Equal(t, "extension.b", cacheB.Extensions[0].Id)
}
