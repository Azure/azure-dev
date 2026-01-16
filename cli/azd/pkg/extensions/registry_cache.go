// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const (
	// Default cache TTL is 4 hours
	defaultCacheTTL = 4 * time.Hour
	// Cache directory name under azd config dir
	cacheSubDir = "cache"
	// Extensions cache subdirectory
	extensionsCacheSubDir = "extensions"
	// Environment variable to override cache TTL
	cacheTTLEnvVar = "AZD_EXTENSION_CACHE_TTL"
)

var (
	// ErrCacheExpired indicates the cache entry has expired
	ErrCacheExpired = errors.New("cache expired")
	// ErrCacheNotFound indicates no cache file exists
	ErrCacheNotFound = errors.New("cache not found")
	// sourceNameSanitizer replaces unsafe filename characters
	sourceNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]`)
)

// RegistryCache represents cached registry data for a single source
type RegistryCache struct {
	// ExpiresOn is the time when this cache entry expires (RFC3339 format)
	ExpiresOn string `json:"expiresOn"`
	// Extensions is the list of extension metadata from the registry
	Extensions []*ExtensionMetadata `json:"extensions"`
}

// RegistryCacheManager handles reading and writing per-source registry cache files
type RegistryCacheManager struct {
	cacheDir string
	ttl      time.Duration
}

// NewRegistryCacheManager creates a new cache manager
func NewRegistryCacheManager() (*RegistryCacheManager, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get config directory: %w", err)
	}

	cacheDir := filepath.Join(configDir, cacheSubDir, extensionsCacheSubDir)

	// Ensure cache directory exists
	if err := os.MkdirAll(cacheDir, osutil.PermissionDirectoryOwnerOnly); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	ttl := getCacheTTL()

	return &RegistryCacheManager{
		cacheDir: cacheDir,
		ttl:      ttl,
	}, nil
}

// getCacheTTL returns the cache TTL, checking for environment variable override
func getCacheTTL() time.Duration {
	if envTTL := os.Getenv(cacheTTLEnvVar); envTTL != "" {
		if duration, err := time.ParseDuration(envTTL); err == nil {
			log.Printf("using custom cache TTL from %s: %s", cacheTTLEnvVar, duration)
			return duration
		}
		log.Printf("invalid cache TTL value '%s', using default %s", envTTL, defaultCacheTTL)
	}
	return defaultCacheTTL
}

// sanitizeSourceName converts a source name to a safe filename
func sanitizeSourceName(sourceName string) string {
	// Replace unsafe characters with underscores
	safe := sourceNameSanitizer.ReplaceAllString(sourceName, "_")
	// Convert to lowercase for consistency
	safe = strings.ToLower(safe)
	// Ensure non-empty
	if safe == "" {
		safe = "default"
	}
	return safe
}

// getCacheFilePath returns the cache file path for a given source
func (m *RegistryCacheManager) getCacheFilePath(sourceName string) string {
	safeSourceName := sanitizeSourceName(sourceName)
	return filepath.Join(m.cacheDir, safeSourceName+".json")
}

// Get retrieves cached registry data for a source if valid (not expired)
func (m *RegistryCacheManager) Get(ctx context.Context, sourceName string) (*RegistryCache, error) {
	cacheFilePath := m.getCacheFilePath(sourceName)

	data, err := os.ReadFile(cacheFilePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrCacheNotFound
		}
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}

	var cache RegistryCache
	if err := json.Unmarshal(data, &cache); err != nil {
		// Corrupt cache file, treat as not found
		log.Printf("failed to unmarshal cache file %s: %v", cacheFilePath, err)
		return nil, ErrCacheNotFound
	}

	// Check expiration
	expiresOn, err := time.Parse(time.RFC3339, cache.ExpiresOn)
	if err != nil {
		log.Printf("failed to parse cache expiration time: %v", err)
		return nil, ErrCacheExpired
	}

	if time.Now().UTC().After(expiresOn) {
		return nil, ErrCacheExpired
	}

	return &cache, nil
}

// Set writes registry data to the cache for a source
func (m *RegistryCacheManager) Set(
	ctx context.Context,
	sourceName string,
	extensions []*ExtensionMetadata,
) error {
	cache := &RegistryCache{
		ExpiresOn:  time.Now().UTC().Add(m.ttl).Format(time.RFC3339),
		Extensions: extensions,
	}

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache data: %w", err)
	}

	cacheFilePath := m.getCacheFilePath(sourceName)
	if err := os.WriteFile(cacheFilePath, data, osutil.PermissionFile); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}

	log.Printf("updated cache for source %s (expires: %s)", sourceName, cache.ExpiresOn)
	return nil
}

// GetExtensionLatestVersion finds an extension in the cache and returns its latest version
func (m *RegistryCacheManager) GetExtensionLatestVersion(
	ctx context.Context,
	sourceName string,
	extensionId string,
) (string, error) {
	cache, err := m.Get(ctx, sourceName)
	if err != nil {
		return "", err
	}

	for _, ext := range cache.Extensions {
		if strings.EqualFold(ext.Id, extensionId) {
			if len(ext.Versions) == 0 {
				return "", fmt.Errorf("extension %s has no versions", extensionId)
			}
			// Latest version is the last element in the Versions slice
			return ext.Versions[len(ext.Versions)-1].Version, nil
		}
	}

	return "", fmt.Errorf("extension %s not found in cache", extensionId)
}

// IsExpiredOrMissing checks if cache for a source needs refresh
func (m *RegistryCacheManager) IsExpiredOrMissing(ctx context.Context, sourceName string) bool {
	_, err := m.Get(ctx, sourceName)
	return err != nil
}

// GetCacheDir returns the cache directory path
func (m *RegistryCacheManager) GetCacheDir() string {
	return m.cacheDir
}
