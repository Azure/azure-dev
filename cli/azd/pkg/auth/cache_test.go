// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"encoding/json"
	"math/rand"
	"testing"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randSeq(n int, rng rand.Rand) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rng.Intn(len(letters))]
	}
	return string(b)
}

func TestCache(t *testing.T) {
	root := t.TempDir()
	ctx := t.Context()
	c := newCache(root)
	// weak rng is fine for testing
	//nolint:gosec
	rng := rand.New(rand.NewSource(0))

	key := func() string {
		return randSeq(10, *rng)
	}

	data := fixedMarshaller{
		val: []byte("some data"),
	}

	// write some data.
	err := c.Export(ctx, &data, cache.ExportHints{PartitionKey: key()})
	require.NoError(t, err)

	// read back that data we wrote.
	var reader fixedMarshaller
	err = c.Replace(ctx, &reader, cache.ReplaceHints{PartitionKey: key()})
	require.NoError(t, err)
	require.NotNil(t, reader.val)
	require.Equal(t, data.val, reader.val)

	// the data should be shared across instances.
	c = newCache(root)
	reader = fixedMarshaller{}
	err = c.Replace(ctx, &reader, cache.ReplaceHints{PartitionKey: key()})
	require.NoError(t, err)
	require.Equal(t, data.val, reader.val)

	// update existing data
	otherData := fixedMarshaller{
		val: []byte("other data"),
	}
	err = c.Export(ctx, &otherData, cache.ExportHints{PartitionKey: key()})
	require.NoError(t, err)

	// read back data
	err = c.Replace(ctx, &reader, cache.ReplaceHints{PartitionKey: key()})
	require.NoError(t, err)
	require.NotNil(t, reader.val)
	require.Equal(t, otherData.val, reader.val)
}

func TestCredentialCache(t *testing.T) {
	root := t.TempDir()

	c := newCredentialCache(root)

	d1 := []byte("some data")

	d2 := []byte("some different data")

	// write some data.
	require.NoError(t, c.Set("d1", d1))
	require.NoError(t, c.Set("d2", d2))

	// read back that data we wrote.
	r1, err := c.Read("d1")
	require.NoError(t, err)

	r2, err := c.Read("d2")
	require.NoError(t, err)

	require.NotNil(t, r1)
	require.NotNil(t, r2)
	require.Equal(t, d1, r1)
	require.Equal(t, d2, r2)

	// the data should be shared across instances.
	c = newCredentialCache(root)

	r1, err = c.Read("d1")
	require.NoError(t, err)

	r2, err = c.Read("d2")
	require.NoError(t, err)

	require.NotNil(t, r1)
	require.NotNil(t, r2)
	require.Equal(t, d1, r1)
	require.Equal(t, d2, r2)

	// read some non-existing data, ensure errCacheKeyNotFound is returned.
	_, err = c.Read("nonExist")
	require.ErrorIs(t, err, errCacheKeyNotFound)
}

type mockContractHolder struct {
	contract *mockContract
}

// Marshal implements cache.Marshaler in msal/apps/cache.
func (c *mockContractHolder) Marshal() ([]byte, error) {
	return json.Marshal(c.contract)
}

// Unmarshal implements cache.Unmarshaler in msal/apps/cache.
func (c *mockContractHolder) Unmarshal(b []byte) error {
	contract := &mockContract{}

	err := json.Unmarshal(b, contract)
	if err != nil {
		return err
	}

	c.contract = contract
	return nil
}

type val struct {
	Value string `json:"value"`
}

// mockContract that simulates the MSAL cache contract.
type mockContract struct {
	AccessTokens  map[string]val `json:"AccessToken,omitempty"`
	RefreshTokens map[string]val `json:"RefreshToken,omitempty"`
	IDTokens      map[string]val `json:"IdToken,omitempty"`
	Accounts      map[string]val `json:"Account,omitempty"`
	AppMetaData   map[string]val `json:"AppMetadata,omitempty"`

	// mock remainder fields
	Remainder map[string]val `json:"Remainder,omitempty"`
}

func TestKeyNormalization(t *testing.T) {
	entries := map[string]val{
		"Upper":           {"Upper"},
		"lower":           {"lower"},
		"Upper-And-Lower": {"Upper-And-Lower"},
		"upper-and-lower": {"upper-and-lower"},
	}
	orig := mockContract{
		AccessTokens:  entries,
		RefreshTokens: entries,
		IDTokens:      entries,
		Accounts:      entries,
		AppMetaData:   entries,
		Remainder: map[string]val{
			"remainder": {"remainder"},
		},
	}

	normalizedEntries := map[string]val{
		"upper":           {"Upper"},
		"lower":           {"lower"},
		"upper-and-lower": {"upper-and-lower"},
	}
	normalized := mockContract{
		AccessTokens:  normalizedEntries,
		RefreshTokens: normalizedEntries,
		IDTokens:      normalizedEntries,
		Accounts:      normalizedEntries,
		AppMetaData:   normalizedEntries,
		Remainder: map[string]val{
			"remainder": {"remainder"},
		},
	}

	ctx := t.Context()
	c := msalCacheAdapter{&memoryCache{
		cache: map[string][]byte{},
		inner: nil,
	}}

	// Replace (retrieve) when cache is empty, expect nil
	h := mockContractHolder{}
	err := c.Replace(ctx, &h, cache.ReplaceHints{})
	require.NoError(t, err)
	require.Nil(t, h.contract)

	// Export (save) with original entry
	h.contract = &orig
	err = c.Export(ctx, &h, cache.ExportHints{})
	require.NoError(t, err)
	require.JSONEq(t, mustJson(orig), mustJson(h.contract))

	// Replace (retrieve) that will normalize the keys
	err = c.Replace(ctx, &h, cache.ReplaceHints{})
	require.NoError(t, err)
	require.JSONEq(t, mustJson(normalized), mustJson(h.contract))
}

func mustJson(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}

	return string(b)
}

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

func (c *errCache) Read(key string) ([]byte, error) {
	return nil, errCacheKeyNotFound
}

func TestMsalCacheAdapter_ReplaceInnerError(t *testing.T) {
	adapter := &msalCacheAdapter{cache: &readErrorCache{}}

	err := adapter.Replace(
		t.Context(), &fakeUnmarshaler{},
		cache.ReplaceHints{})
	require.ErrorIs(t, err, errReadFail)
}

func TestMsalCacheAdapter_ReplaceKeyNotFound(t *testing.T) {
	adapter := &msalCacheAdapter{cache: &errCache{}}

	// errCache.Read returns errCacheKeyNotFound → swallowed.
	err := adapter.Replace(
		t.Context(), &fakeUnmarshaler{},
		cache.ReplaceHints{})
	require.NoError(t, err)
}

func TestMsalCacheAdapter_ExportMarshalError(t *testing.T) {
	adapter := &msalCacheAdapter{
		cache: &memoryCache{cache: map[string][]byte{}},
	}

	err := adapter.Export(
		t.Context(), &failingMarshaler{},
		cache.ExportHints{})
	require.Error(t, err)
}
