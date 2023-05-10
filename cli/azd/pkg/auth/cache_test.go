// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"testing"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
	"github.com/stretchr/testify/require"
)

func TestCache(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	c := newCache(root)

	d1 := fixedMarshaller{
		val: []byte("some data"),
	}

	d2 := fixedMarshaller{
		val: []byte("some different data"),
	}

	// write some data.
	r := c.Export(ctx, &d1, cache.ExportHints{PartitionKey: "d1"})
	require.NoError(t, r)
	r = c.Export(ctx, &d2, cache.ExportHints{PartitionKey: "d2"})
	require.NoError(t, r)

	var r1 fixedMarshaller
	var r2 fixedMarshaller

	// read back that data we wrote.
	r = c.Replace(ctx, &r1, cache.ReplaceHints{PartitionKey: "d1"})
	require.NoError(t, r)
	r = c.Replace(ctx, &r2, cache.ReplaceHints{PartitionKey: "d2"})
	require.NoError(t, r)

	require.NotNil(t, r1.val)
	require.NotNil(t, r2.val)
	require.Equal(t, d1.val, r1.val)
	require.Equal(t, d2.val, r2.val)

	// the data should be shared across instances.
	c = newCache(root)

	r = c.Replace(ctx, &r1, cache.ReplaceHints{PartitionKey: "d1"})
	require.NoError(t, r)
	r = c.Replace(ctx, &r2, cache.ReplaceHints{PartitionKey: "d2"})
	require.NoError(t, r)

	require.NotNil(t, r1.val)
	require.NotNil(t, r2.val)
	require.Equal(t, d1.val, r1.val)
	require.Equal(t, d2.val, r2.val)
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
}
