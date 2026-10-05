// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/require"
)

func TestSourceRejectsInvalidVersionMigrations(t *testing.T) {
	tests := []struct {
		name       string
		migrations []ExtensionVersionMigration
		wantError  string
	}{
		{
			name:       "unpublished successor",
			migrations: []ExtensionVersionMigration{{From: "1.0.47-beta", To: "1.0.0-beta.0"}},
			wantError:  `successor version "1.0.0-beta.0" is not published`,
		},
		{
			name: "duplicate historical version",
			migrations: []ExtensionVersionMigration{
				{From: "1.0.47-beta", To: "1.0.0-beta.1"},
				{From: "1.0.47-beta", To: "1.0.0-beta.1"},
			},
			wantError: `version "1.0.47-beta" is already migrated`,
		},
		{
			name: "chained migration",
			migrations: []ExtensionVersionMigration{
				{From: "1.0.47-beta", To: "1.0.0-beta.2"},
				{From: "1.0.0-beta.2", To: "1.0.0-beta.1"},
			},
			wantError: `successor version "1.0.0-beta.2" cannot also be migrated`,
		},
		{
			name:       "malformed historical version",
			migrations: []ExtensionVersionMigration{{From: "nightly", To: "1.0.0-beta.1"}},
			wantError:  "invalid semver format",
		},
		{
			name:       "malformed successor",
			migrations: []ExtensionVersionMigration{{From: "1.0.47-beta", To: "nightly"}},
			wantError:  "invalid semver format",
		},
		{
			name:       "self migration",
			migrations: []ExtensionVersionMigration{{From: "1.0.0-beta.1", To: "1.0.0-beta.1"}},
			wantError:  "'from' and 'to' must differ",
		},
		{
			name:       "non decreasing raw precedence",
			migrations: []ExtensionVersionMigration{{From: "1.0.0-beta.1", To: "1.0.0-beta.2"}},
			wantError:  "'from' must have higher raw semantic-version precedence",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := &Registry{
				SchemaVersion: CurrentRegistrySchemaVersion,
				Extensions: []*ExtensionMetadata{{
					Id: "test.extension",
					Versions: []ExtensionVersion{
						{Version: "1.0.47-beta"}, {Version: "1.0.0-beta.1"}, {Version: "1.0.0-beta.2"},
					},
					VersionMigrations: tt.migrations,
				}},
			}
			before, err := json.Marshal(registry)
			require.NoError(t, err)

			t.Run("json source", func(t *testing.T) {
				source, err := newJsonSource("migration-source", string(before))
				require.ErrorContains(t, err, tt.wantError)
				require.ErrorContains(t, err, "migration-source")
				require.ErrorContains(t, err, "test.extension")
				require.Nil(t, source)
			})
			t.Run("registry source", func(t *testing.T) {
				source, err := newRegistrySource("migration-source", registry)
				require.ErrorContains(t, err, tt.wantError)
				require.Nil(t, source)
				after, err := json.Marshal(registry)
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
			t.Run("existing cache", func(t *testing.T) {
				cache := &RegistryCacheManager{cacheDir: t.TempDir(), ttl: time.Hour}
				require.NoError(t, cache.Set(t.Context(), "migration-source", registry.Extensions))
				path := cache.getCacheFilePath("migration-source")
				before, err := os.ReadFile(path)
				require.NoError(t, err)
				_, err = cache.GetExtensionLatestVersion(t.Context(), "migration-source", "test.extension")
				require.ErrorContains(t, err, tt.wantError)
				require.ErrorContains(t, err, "migration-source")
				require.ErrorIs(t, err, errInvalidVersionMigrations)
				result, err := NewUpdateChecker(cache).CheckForUpdate(t.Context(), &Extension{
					Id: "test.extension", Source: "migration-source", Version: "1.0.47-beta",
				})
				require.ErrorIs(t, err, errInvalidVersionMigrations)
				require.Nil(t, result)
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
		})
	}
}

func TestManagerRejectsInvalidMigrationSourceBeforeResolution(t *testing.T) {
	registry := &Registry{Extensions: []*ExtensionMetadata{{
		Id: "test.extension",
		Versions: []ExtensionVersion{
			{Version: "1.0.47-beta"}, {Version: "1.0.0-beta.1"},
		},
		VersionMigrations: []ExtensionVersionMigration{{From: "1.0.47-beta", To: "1.0.0-beta.0"}},
	}}}
	data, err := json.Marshal(registry)
	require.NoError(t, err)
	badPath := filepath.Join(t.TempDir(), "invalid-registry.json")
	require.NoError(t, os.WriteFile(badPath, data, 0o600))
	goodPath := filepath.Join(t.TempDir(), "valid-registry.json")
	require.NoError(t, os.WriteFile(goodPath, []byte(`{"extensions":[]}`), 0o600))
	manager := &Manager{sourceManager: NewSourceManager(nil, nil, nil)}
	sources, err := manager.createSourcesFromConfig(t.Context(), []*SourceConfig{
		{Name: "valid-source", Type: SourceKindFile, Location: goodPath},
		{Name: "invalid-source", Type: SourceKindFile, Location: badPath},
	}, nil)
	require.ErrorIs(t, err, errInvalidVersionMigrations)
	require.ErrorContains(t, err, "invalid-source")
	require.ErrorContains(t, err, "test.extension")
	require.Nil(t, sources)
	after, err := os.ReadFile(badPath)
	require.NoError(t, err)
	require.Equal(t, data, after)
}

func TestSourceValidMigrationPreservesRawResolution(t *testing.T) {
	registry := &Registry{
		SchemaVersion: CurrentRegistrySchemaVersion,
		Extensions: []*ExtensionMetadata{{
			Id: "test.extension",
			Versions: []ExtensionVersion{
				{Version: "1.0.47-beta"}, {Version: "1.0.0-beta.1"},
			},
			VersionMigrations: []ExtensionVersionMigration{{From: "1.0.47-beta", To: "1.0.0-beta.1"}},
		}},
	}
	data, err := json.Marshal(registry)
	require.NoError(t, err)
	source, err := newJsonSource("migration-source", string(data))
	require.NoError(t, err)
	extension, err := source.GetExtension(t.Context(), "test.extension")
	require.NoError(t, err)
	for _, tt := range []struct {
		query   string
		version string
	}{
		{"", "1.0.0-beta.1"},
		{"1.0.47-beta", "1.0.47-beta"},
		{">=1.0.1-0", "1.0.47-beta"},
		{"1.0.46-beta", ""},
	} {
		t.Run(tt.query, func(t *testing.T) {
			result := ClassifyInstallResolution(
				[]*ExtensionMetadata{extension},
				&InstallResolutionOptions{FilterOptions: FilterOptions{Id: extension.Id, Version: tt.query}},
				semver.MustParse("1.36.0"),
			)
			if tt.version == "" {
				require.Error(t, result.Error())
				require.Nil(t, result.Candidate(extension))
			} else {
				require.NoError(t, result.Error())
				require.Equal(t, tt.version, result.Candidate(extension).Version.Version)
			}
		})
	}
	extension.Source = ""
	after, err := json.Marshal(registry.Extensions[0])
	require.NoError(t, err)
	loaded, err := json.Marshal(extension)
	require.NoError(t, err)
	require.Equal(t, after, loaded)
}

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
