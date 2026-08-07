// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestSourceManager_Add(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	ctx := t.Context()

	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, configManager, mockContext.HttpClient)

	sourceConfig := &SourceConfig{
		Name:     "test-source",
		Type:     SourceKindUrl,
		Location: "http://example.com",
	}

	t.Run("InitialAdd", func(t *testing.T) {
		err := sourceManager.Add(ctx, "test-source", sourceConfig)
		require.NoError(t, err)

		newSource, err := sourceManager.Get(ctx, "test-source")
		require.NoError(t, err)
		require.Equal(t, sourceConfig.Name, newSource.Name)
	})

	t.Run("DuplicateAdd", func(t *testing.T) {
		err := sourceManager.Add(ctx, "test-source", sourceConfig)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSourceExists)
	})

	t.Run("ReservedBundleName", func(t *testing.T) {
		reserved := &SourceConfig{
			Name:     BundleSourceName,
			Type:     SourceKindFile,
			Location: "/tmp/registry.json",
		}
		err := sourceManager.Add(ctx, BundleSourceName, reserved)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSourceReserved)

		// Case-insensitive: the normalized name is also reserved.
		err = sourceManager.Add(ctx, "Bundle", reserved)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSourceReserved)
	})
}

func TestSourceManager_ProtectsMainRegistry(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	ctx := t.Context()

	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, configManager, mockContext.HttpClient)
	unofficial := &SourceConfig{
		Name:     MainRegistryName,
		Type:     SourceKindUrl,
		Location: "https://example.com/registry",
	}

	err := sourceManager.Add(ctx, MainRegistryName, unofficial)
	require.ErrorIs(t, err, ErrSourceReserved)

	err = sourceManager.Remove(ctx, MainRegistryName)
	require.ErrorIs(t, err, ErrSourceReserved)

	_, err = sourceManager.CreateSource(ctx, unofficial)
	require.ErrorIs(t, err, ErrSourceReserved)
}

func TestIsOfficialMainRegistrySource(t *testing.T) {
	tests := []struct {
		name   string
		source *SourceConfig
		want   bool
	}{
		{
			name: "official",
			source: &SourceConfig{
				Name:     MainRegistryName,
				Type:     SourceKindUrl,
				Location: "https://aka.ms/azd/extensions/registry",
			},
			want: true,
		},
		{
			name: "trailing slash",
			source: &SourceConfig{
				Name:     MainRegistryName,
				Type:     SourceKindUrl,
				Location: "https://AKA.MS/azd/extensions/registry/",
			},
			want: true,
		},
		{
			name: "wrong location",
			source: &SourceConfig{
				Name:     MainRegistryName,
				Type:     SourceKindUrl,
				Location: "https://example.com/registry",
			},
		},
		{
			name: "extra trailing slash",
			source: &SourceConfig{
				Name:     MainRegistryName,
				Type:     SourceKindUrl,
				Location: extensionRegistryUrl + "//",
			},
		},
		{
			name: "query string",
			source: &SourceConfig{
				Name:     MainRegistryName,
				Type:     SourceKindUrl,
				Location: extensionRegistryUrl + "?source=official",
			},
		},
		{
			name: "wrong type",
			source: &SourceConfig{
				Name:     MainRegistryName,
				Type:     SourceKindFile,
				Location: extensionRegistryUrl,
			},
		},
		{
			name: "wrong name",
			source: &SourceConfig{
				Name:     "custom",
				Type:     SourceKindUrl,
				Location: extensionRegistryUrl,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, IsOfficialMainRegistrySource(test.source))
		})
	}
}

func TestSourceManager_Get(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	ctx := t.Context()

	mockConfig := config.NewEmptyConfig()
	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	mockContext.ConfigManager.WithConfig(mockConfig)
	sourceManager := NewSourceManager(mockContext.Container, configManager, mockContext.HttpClient)

	expected := SourceConfig{
		Name:     "test-source",
		Type:     SourceKindUrl,
		Location: "http://example.com",
	}

	err := mockConfig.Set("extension.sources.test-source", expected)
	require.NoError(t, err)

	t.Run("GetExisting", func(t *testing.T) {
		actual, err := sourceManager.Get(ctx, "test-source")
		require.NoError(t, err)
		require.Equal(t, expected, *actual)
	})

	t.Run("NotFound", func(t *testing.T) {
		actual, err := sourceManager.Get(ctx, "not-found")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSourceNotFound)
		require.Nil(t, actual)
	})
}

func TestSourceManager_Remove(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	ctx := t.Context()

	mockConfig := config.NewEmptyConfig()
	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	mockContext.ConfigManager.WithConfig(mockConfig)
	sourceManager := NewSourceManager(mockContext.Container, configManager, mockContext.HttpClient)

	expected := SourceConfig{
		Name:     "test-source",
		Type:     SourceKindUrl,
		Location: "http://example.com",
	}

	err := mockConfig.Set("extension.sources.test-source", expected)
	require.NoError(t, err)

	t.Run("RemoveExisting", func(t *testing.T) {
		err := sourceManager.Remove(ctx, "test-source")
		require.NoError(t, err)

		deletedSource, err := sourceManager.Get(ctx, "test-source")
		require.Error(t, err)
		require.Nil(t, deletedSource)
	})

	t.Run("RemoveNotFound", func(t *testing.T) {
		err := sourceManager.Remove(ctx, "not-found")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSourceNotFound)
	})
}

func TestSourceManager_List(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	ctx := t.Context()

	mockConfig := config.NewEmptyConfig()
	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	mockContext.ConfigManager.WithConfig(mockConfig)
	sourceManager := NewSourceManager(mockContext.Container, configManager, mockContext.HttpClient)

	expected := SourceConfig{
		Name:     "test-source",
		Type:     SourceKindUrl,
		Location: "http://example.com",
	}

	err := mockConfig.Set("extension.sources.test-source", expected)
	require.NoError(t, err)

	sources, err := sourceManager.List(ctx)
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, expected, *sources[0])
}

func TestNormalizeSourceKey(t *testing.T) {
	t.Parallel()

	require.Equal(t, "my-source", NormalizeSourceKey("My Source"))
	require.Equal(t, "my.source", NormalizeSourceKey("My.Source"))
}

func TestSourceManager_CreateSource_Bundle(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	ctx := t.Context()

	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, configManager, mockContext.HttpClient)

	bundleDir := t.TempDir()
	registry := &Registry{
		SchemaVersion: CurrentRegistrySchemaVersion,
		Extensions: []*ExtensionMetadata{
			{
				Id:          "test.ext",
				DisplayName: "Test",
				Versions: []ExtensionVersion{
					{
						Version:   "1.0.0",
						Artifacts: map[string]ExtensionArtifact{"linux/amd64": {URL: "artifacts/ext.tar.gz"}},
					},
				},
			},
		},
	}
	writeBundleRegistry(t, bundleDir, registry)

	source, err := sourceManager.CreateSource(ctx, &SourceConfig{
		Name:     "bundle",
		Type:     SourceKindBundle,
		Location: bundleDir,
	})
	require.NoError(t, err)
	require.Equal(t, "bundle", source.Name())
}
