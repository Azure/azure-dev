// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListExtensions(t *testing.T) {
	ctx := t.Context()

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
	ctx := t.Context()

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

func TestCategorizedSourcePropagatesCategoryAndRegistry(t *testing.T) {
	t.Parallel()

	registry := &Registry{
		SchemaVersion: CurrentRegistrySchemaVersion,
		Extensions: []*ExtensionMetadata{
			{Id: "ext1", DisplayName: "Extension 1"},
		},
	}
	source, err := newRegistrySource("team-registry", registry)
	require.NoError(t, err)

	categorized := newCategorizedSource(source, SourceCategoryDev)
	provider, ok := categorized.(RegistryProvider)
	require.True(t, ok)
	require.Same(t, registry, provider.GetRegistry())

	listed, err := categorized.ListExtensions(t.Context())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "team-registry", listed[0].Source)
	require.Equal(t, SourceCategoryDev, listed[0].SourceCategory)

	selected, err := categorized.GetExtension(t.Context(), "ext1")
	require.NoError(t, err)
	require.Equal(t, SourceCategoryDev, selected.SourceCategory)

	_, err = categorized.GetExtension(t.Context(), "missing")
	require.ErrorIs(t, err, ErrRegistryExtensionNotFound)
}

type failingSource struct {
	listErr error
	getErr  error
}

func (s *failingSource) Name() string {
	return "failing"
}

func (s *failingSource) ListExtensions(context.Context) ([]*ExtensionMetadata, error) {
	return nil, s.listErr
}

func (s *failingSource) GetExtension(context.Context, string) (*ExtensionMetadata, error) {
	return nil, s.getErr
}

func TestCategorizedSourcePreservesSourceErrors(t *testing.T) {
	t.Parallel()

	listErr := errors.New("list failed")
	getErr := errors.New("get failed")
	source := newCategorizedSource(&failingSource{listErr: listErr, getErr: getErr}, SourceCategoryOther)
	_, isRegistryProvider := source.(RegistryProvider)
	require.False(t, isRegistryProvider)

	_, err := source.ListExtensions(t.Context())
	require.ErrorIs(t, err, listErr)

	_, err = source.GetExtension(t.Context(), "ext1")
	require.ErrorIs(t, err, getErr)
}
