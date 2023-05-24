// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"math/rand"
	"testing"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
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

func TestCacheFixed(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	fixedKey := cCurrentUserCacheKey
	c := newCache(root, &fixedKey)
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
	c = newCache(root, &fixedKey)
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

func TestCache(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	c := newCache(root, nil)

	d1 := fixedMarshaller{
		val: []byte("some data"),
	}

	d2 := fixedMarshaller{
		val: []byte("some different data"),
	}

	// write some data.
	err := c.Export(ctx, &d1, cache.ExportHints{PartitionKey: "d1"})
	require.NoError(t, err)
	err = c.Export(ctx, &d2, cache.ExportHints{PartitionKey: "d2"})
	require.NoError(t, err)

	var r1 fixedMarshaller
	var r2 fixedMarshaller

	// read back that data we wrote.
	err = c.Replace(ctx, &r1, cache.ReplaceHints{PartitionKey: "d1"})
	require.NoError(t, err)
	err = c.Replace(ctx, &r2, cache.ReplaceHints{PartitionKey: "d2"})
	require.NoError(t, err)

	require.NotNil(t, r1.val)
	require.NotNil(t, r2.val)
	require.Equal(t, d1.val, r1.val)
	require.Equal(t, d2.val, r2.val)

	// the data should be shared across instances.
	c = newCache(root, nil)

	r1 = fixedMarshaller{}
	r2 = fixedMarshaller{}
	err = c.Replace(ctx, &r1, cache.ReplaceHints{PartitionKey: "d1"})
	require.NoError(t, err)
	err = c.Replace(ctx, &r2, cache.ReplaceHints{PartitionKey: "d2"})
	require.NoError(t, err)

	require.NotNil(t, r1.val)
	require.NotNil(t, r2.val)
	require.Equal(t, d1.val, r1.val)
	require.Equal(t, d2.val, r2.val)

	// update existing data
	d1Update := fixedMarshaller{
		val: []byte("some data (updated)"),
	}
	d2Update := fixedMarshaller{
		val: []byte("some different data (updated)"),
	}
	err = c.Export(ctx, &d1Update, cache.ExportHints{PartitionKey: "d1"})
	require.NoError(t, err)
	err = c.Export(ctx, &d2Update, cache.ExportHints{PartitionKey: "d2"})
	require.NoError(t, err)

	// read back that data we wrote.
	err = c.Replace(ctx, &r1, cache.ReplaceHints{PartitionKey: "d1"})
	require.NoError(t, err)
	err = c.Replace(ctx, &r2, cache.ReplaceHints{PartitionKey: "d2"})
	require.NoError(t, err)

	require.NotNil(t, r1.val)
	require.NotNil(t, r2.val)
	require.Equal(t, d1Update.val, r1.val)
	require.Equal(t, d2Update.val, r2.val)

	// read some non-existing data
	nonExist := fixedMarshaller{
		val: []byte("some data"),
	}
	err = c.Replace(ctx, &nonExist, cache.ReplaceHints{PartitionKey: "nonExist"})
	require.NoError(t, err)
	// data should not be overwritten when key is not found.
	require.Equal(t, []byte("some data"), nonExist.val)
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
