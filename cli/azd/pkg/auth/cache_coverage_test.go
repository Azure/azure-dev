// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- memoryCache ---

func TestMemoryCache_ReadFromInner(t *testing.T) {
	inner := &memoryCache{
		cache: map[string][]byte{
			"inner-key": []byte("inner-value"),
		},
	}

	outer := &memoryCache{
		cache: make(map[string][]byte),
		inner: inner,
	}

	val, err := outer.Read("inner-key")
	require.NoError(t, err)
	assert.Equal(t, []byte("inner-value"), val)
}

func TestMemoryCache_ReadKeyNotFound(t *testing.T) {
	c := &memoryCache{
		cache: make(map[string][]byte),
	}

	_, err := c.Read("missing")
	require.ErrorIs(t, err, errCacheKeyNotFound)
}

func TestMemoryCache_ReadKeyNotFoundWithInner(t *testing.T) {
	inner := &memoryCache{
		cache: make(map[string][]byte),
	}

	c := &memoryCache{
		cache: make(map[string][]byte),
		inner: inner,
	}

	_, err := c.Read("missing")
	require.ErrorIs(t, err, errCacheKeyNotFound)
}

func TestMemoryCache_SetNoChange(t *testing.T) {
	c := &memoryCache{
		cache: map[string][]byte{
			"key": []byte("value"),
		},
	}

	// Set the same value — should be no-op
	err := c.Set("key", []byte("value"))
	require.NoError(t, err)
}

func TestMemoryCache_SetWithInner(t *testing.T) {
	inner := &memoryCache{
		cache: make(map[string][]byte),
	}

	c := &memoryCache{
		cache: make(map[string][]byte),
		inner: inner,
	}

	err := c.Set("key", []byte("value"))
	require.NoError(t, err)

	// Both outer and inner should have the value
	val, err := c.Read("key")
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), val)

	innerVal, err := inner.Read("key")
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), innerVal)
}

func TestMemoryCache_SetWithInnerError(t *testing.T) {
	c := &memoryCache{
		cache: make(map[string][]byte),
		inner: &failingCache{},
	}

	err := c.Set("key", []byte("value"))
	require.Error(t, err)
	assert.ErrorIs(t, err, assert.AnError)
}

// --- fileCache ---

func TestFileCache_ReadWrite(t *testing.T) {
	root := t.TempDir()
	c := &fileCache{
		prefix: "test-",
		root:   root,
		ext:    "json",
	}

	err := c.Set("mykey", []byte(`{"hello":"world"}`))
	require.NoError(t, err)

	val, err := c.Read("mykey")
	require.NoError(t, err)
	assert.Equal(t, `{"hello":"world"}`, string(val))
}

func TestFileCache_ReadNotFound(t *testing.T) {
	root := t.TempDir()
	c := &fileCache{
		prefix: "test-",
		root:   root,
		ext:    "json",
	}

	_, err := c.Read("nonexistent")
	require.ErrorIs(t, err, errCacheKeyNotFound)
}

func TestFileCache_Overwrite(t *testing.T) {
	root := t.TempDir()
	c := &fileCache{
		prefix: "test-",
		root:   root,
		ext:    "dat",
	}

	require.NoError(t, c.Set("k", []byte("v1")))
	require.NoError(t, c.Set("k", []byte("v2")))

	val, err := c.Read("k")
	require.NoError(t, err)
	assert.Equal(t, "v2", string(val))
}

func TestFileCache_PathForCache(t *testing.T) {
	c := &fileCache{
		prefix: "msal_",
		root:   "/tmp/auth",
		ext:    "json",
	}
	path := c.pathForCache("mykey")
	assert.Contains(t, path, "msal_mykey.json")
}

func TestFileCache_PathForLock(t *testing.T) {
	c := &fileCache{
		prefix: "msal_",
		root:   "/tmp/auth",
		ext:    "json",
	}
	path := c.pathForLock("mykey")
	assert.Contains(t, path, "msal_mykey.json.lock")
}

// --- fixedMarshaller ---

func TestFixedMarshaller_Marshal(t *testing.T) {
	fm := &fixedMarshaller{val: []byte("hello")}
	data, err := fm.Marshal()
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), data)
}

func TestFixedMarshaller_Unmarshal(t *testing.T) {
	fm := &fixedMarshaller{}
	err := fm.Unmarshal([]byte("world"))
	require.NoError(t, err)
	assert.Equal(t, []byte("world"), fm.val)
}

// --- failingCache for testing inner-error paths ---

type failingCache struct{}

func (f *failingCache) Read(_ string) ([]byte, error) {
	return nil, errCacheKeyNotFound
}

func (f *failingCache) Set(
	_ string, _ []byte,
) error {
	return assert.AnError
}
