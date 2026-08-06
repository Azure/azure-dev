// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"path/filepath"
	"strings"
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

	t.Run("RemoveLegacyInvalidName", func(t *testing.T) {
		legacyConfig := config.NewEmptyConfig()
		err := legacyConfig.Set(baseConfigKey, map[string]any{
			"legacy.source": SourceConfig{
				Name:     "legacy.source",
				Type:     SourceKindUrl,
				Location: "http://example.com",
			},
		})
		require.NoError(t, err)
		mockContext.ConfigManager.WithConfig(legacyConfig)

		err = sourceManager.Remove(ctx, "legacy.source")
		require.NoError(t, err)

		sources, ok := legacyConfig.GetMap(baseConfigKey)
		require.True(t, ok)
		require.NotContains(t, sources, "legacy.source")
	})

	t.Run("RemoveNestedLegacySourceOnly", func(t *testing.T) {
		legacyConfig := config.NewEmptyConfig()
		require.NoError(t, legacyConfig.Set("extension.sources.foo.bar", SourceConfig{
			Name:     "foo.bar",
			Type:     SourceKindUrl,
			Location: "http://example.com/bar",
		}))
		require.NoError(t, legacyConfig.Set("extension.sources.foo.baz", SourceConfig{
			Name:     "foo.baz",
			Type:     SourceKindUrl,
			Location: "http://example.com/baz",
		}))
		mockContext.ConfigManager.WithConfig(legacyConfig)

		_, err := sourceManager.List(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), `"foo.bar"`)
		require.NotContains(t, err.Error(), `source "foo"`)

		require.NoError(t, sourceManager.Remove(ctx, "foo.bar"))

		_, exists := legacyConfig.Get("extension.sources.foo.bar")
		require.False(t, exists)
		remaining, exists := legacyConfig.Get("extension.sources.foo.baz")
		require.True(t, exists)
		require.NotNil(t, remaining)
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

func TestValidateSourceName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		source    string
		targetErr error
	}{
		{name: "single letter", source: "a"},
		{name: "letters and digits", source: "registry2"},
		{name: "hyphen", source: "local-dev"},
		{name: "underscore", source: "team_registry"},
		{name: "maximum length", source: strings.Repeat("a", SourceNameMaxLength)},
		{name: "empty", source: "", targetErr: ErrSourceNameInvalid},
		{name: "too long", source: strings.Repeat("a", SourceNameMaxLength+1), targetErr: ErrSourceNameInvalid},
		{name: "uppercase", source: "Dev", targetErr: ErrSourceNameInvalid},
		{name: "space", source: "my source", targetErr: ErrSourceNameInvalid},
		{name: "dot", source: "my.source", targetErr: ErrSourceNameInvalid},
		{name: "leading hyphen", source: "-dev", targetErr: ErrSourceNameInvalid},
		{name: "trailing hyphen", source: "dev-", targetErr: ErrSourceNameInvalid},
		{name: "leading underscore", source: "_dev", targetErr: ErrSourceNameInvalid},
		{name: "trailing underscore", source: "dev_", targetErr: ErrSourceNameInvalid},
		{name: "path separator", source: "foo/bar", targetErr: ErrSourceNameInvalid},
		{name: "shell operator", source: "foo;rm", targetErr: ErrSourceNameInvalid},
		{name: "unicode", source: "dév", targetErr: ErrSourceNameInvalid},
		{name: "reserved bundle", source: BundleSourceName, targetErr: ErrSourceReserved},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSourceName(tt.source)
			if tt.targetErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.targetErr)
		})
	}
}

func TestSourceManager_ListRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		source  SourceConfig
		errText string
	}{
		{
			name: "invalid key",
			key:  "legacy.source",
			source: SourceConfig{
				Name:     "legacy.source",
				Type:     SourceKindUrl,
				Location: "http://example.com",
			},
			errText: "configured extension source \"legacy.source\" has an invalid name",
		},
		{
			name: "invalid stored name",
			key:  "legacy-source",
			source: SourceConfig{
				Name:     "Legacy Source",
				Type:     SourceKindUrl,
				Location: "http://example.com",
			},
			errText: "has an invalid stored name",
		},
		{
			name: "mismatched name",
			key:  "source-one",
			source: SourceConfig{
				Name:     "source-two",
				Type:     SourceKindUrl,
				Location: "http://example.com",
			},
			errText: "does not match its stored name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())
			mockConfig := config.NewEmptyConfig()
			require.NoError(t, mockConfig.Set(baseConfigKey, map[string]any{tt.key: tt.source}))
			mockContext.ConfigManager.WithConfig(mockConfig)

			sourceManager := NewSourceManager(
				mockContext.Container,
				config.NewUserConfigManager(mockContext.ConfigManager),
				mockContext.HttpClient,
			)
			_, err := sourceManager.List(t.Context())
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.errText)
			require.NotContains(t, err.Error(), "remove "+tt.key)
		})
	}
}

func TestSourceManager_AddRejectsInvalidNameWithoutMutation(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	mockConfig := config.NewEmptyConfig()
	mockContext.ConfigManager.WithConfig(mockConfig)
	sourceManager := NewSourceManager(
		mockContext.Container,
		config.NewUserConfigManager(mockContext.ConfigManager),
		mockContext.HttpClient,
	)
	source := &SourceConfig{
		Name:     "original",
		Type:     SourceKindUrl,
		Location: "http://example.com",
	}

	err := sourceManager.Add(t.Context(), "Invalid Name", source)
	require.ErrorIs(t, err, ErrSourceNameInvalid)
	require.Equal(t, "original", source.Name)
	_, ok := mockConfig.Get(baseConfigKey)
	require.False(t, ok)
}

func TestValidSourceNamesHaveDistinctCachePaths(t *testing.T) {
	t.Parallel()

	manager := &RegistryCacheManager{cacheDir: t.TempDir()}
	names := []string{"source-one", "source_one", "source1"}
	paths := map[string]struct{}{}
	for _, name := range names {
		require.NoError(t, ValidateSourceName(name))
		path := manager.getCacheFilePath(name)
		require.Equal(t, name+".json", filepath.Base(path))
		_, exists := paths[path]
		require.False(t, exists)
		paths[path] = struct{}{}
	}
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
