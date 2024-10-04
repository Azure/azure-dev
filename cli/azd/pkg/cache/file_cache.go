package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// CacheResolver is a function that resolves the cache value.
type CacheResolver[T any] func(ctx context.Context) (*T, error)

// FileCache is a cache that stores the value in a file otherwise resolves it.
type FileCache[T any] struct {
	filePath      string
	resolver      CacheResolver[T]
	cacheDuration time.Duration
	value         *T
}

// NewFileCache creates a new file cache.
func NewFileCache[T any](cacheFilePath string, cacheDuration time.Duration, resolver CacheResolver[T]) *FileCache[T] {
	return &FileCache[T]{
		filePath:      cacheFilePath,
		resolver:      resolver,
		cacheDuration: cacheDuration,
	}
}

// Resolve returns the value from the cache or resolves it.
func (c *FileCache[T]) Resolve(ctx context.Context) (*T, error) {
	if c.isValid() {
		if c.value == nil {
			if err := c.loadFromFile(); err == nil {
				return c.value, nil
			}
		}
		return c.value, nil
	}

	value, err := c.resolver(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve data: %w", err)
	}

	if err := c.Set(value); err != nil {
		return nil, fmt.Errorf("failed to set cache: %w", err)
	}

	return c.value, nil
}

// Set sets the value in the cache.
func (c *FileCache[T]) Set(value *T) error {
	c.value = value
	jsonValue, err := json.Marshal(c.value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	if err := os.WriteFile(c.filePath, jsonValue, osutil.PermissionFile); err != nil {
		return fmt.Errorf("failed to write cache: %w", err)
	}

	return nil
}

// isValid checks if the cache is valid.
func (c *FileCache[T]) isValid() bool {
	val, has := os.LookupEnv("AZD_NO_CACHE")
	if has {
		noCache, err := strconv.ParseBool(val)
		if err == nil && noCache {
			return false
		}
	}

	info, err := os.Stat(c.filePath)
	if os.IsNotExist(err) {
		return false
	}

	return time.Since(info.ModTime()) < c.cacheDuration
}

// loadFromFile loads the cache from the file.
func (c *FileCache[T]) loadFromFile() error {
	data, err := os.ReadFile(c.filePath)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &c.value)
}
