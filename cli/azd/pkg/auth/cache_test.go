// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCache(t *testing.T) {
	root := t.TempDir()

	c := newCache(root)

	d1 := fixedMarshaller{
		val: []byte("some data"),
	}

	d2 := fixedMarshaller{
		val: []byte("some different data"),
	}

	// write some data.
	c.Export(&d1, "d1")
	c.Export(&d2, "d2")

	var r1 fixedMarshaller
	var r2 fixedMarshaller

	// read back that data we wrote.
	c.Replace(&r1, "d1")
	c.Replace(&r2, "d2")

	require.NotNil(t, r1.val)
	require.NotNil(t, r2.val)
	require.Equal(t, d1.val, r1.val)
	require.Equal(t, d2.val, r2.val)

	// the data should be shared across instances.
	c = newCache(root)

	c.Replace(&r1, "d1")
	c.Replace(&r2, "d2")

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
