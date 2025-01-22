package extensions

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListExtensions(t *testing.T) {
	ctx := context.Background()

	registry := &Registry{
		Extensions: []*ExtensionMetadata{
			{Id: "ext1", DisplayName: "Extension 1"},
			{Id: "ext2", DisplayName: "Extension 2"},
		},
	}

	source, err := newRegistrySource("testSource", registry)
	require.NoError(t, err)

	extensions, err := source.ListExtensions(ctx)
	require.NoError(t, err)
	require.Len(t, extensions, 2)
	require.Equal(t, "testSource", extensions[0].Source)
	require.Equal(t, "testSource", extensions[1].Source)
}

func TestGetExtension(t *testing.T) {
	ctx := context.Background()

	registry := &Registry{
		Extensions: []*ExtensionMetadata{
			{Id: "ext1", DisplayName: "Extension 1"},
			{Id: "ext2", DisplayName: "Extension 2"},
		},
	}

	source, err := newRegistrySource("testSource", registry)
	require.NoError(t, err)

	extension, err := source.GetExtension(ctx, "ext1")
	require.NoError(t, err)
	require.Equal(t, "ext1", extension.Id)
	require.Equal(t, "Extension 1", extension.DisplayName)

	notFoundExtension, err := source.GetExtension(ctx, "nonexistent")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrRegistryExtensionNotFound)
	require.Nil(t, notFoundExtension)
}
