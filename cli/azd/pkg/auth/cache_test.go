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

	d1 := fixedMarshaller{
		val: []byte("some data"),
	}

	d2 := fixedMarshaller{
		val: []byte("some different data"),
	}

	// write some data.
	require.NoError(t, c.Export(&d1, "d1"))
	require.NoError(t, c.Export(&d2, "d2"))

	var r1 fixedMarshaller
	var r2 fixedMarshaller

	// read back that data we wrote.
	require.NoError(t, c.Replace(&r1, "d1"))
	require.NoError(t, c.Replace(&r2, "d2"))

	require.NotNil(t, r1.val)
	require.NotNil(t, r2.val)
	require.Equal(t, d1.val, r1.val)
	require.Equal(t, d2.val, r2.val)

	// the data should be shared across instances.
	c = newCredentialCache(root)

	require.NoError(t, c.Replace(&r1, "d1"))
	require.NoError(t, c.Replace(&r2, "d2"))

	require.NotNil(t, r1.val)
	require.NotNil(t, r2.val)
	require.Equal(t, d1.val, r1.val)
	require.Equal(t, d2.val, r2.val)
}
