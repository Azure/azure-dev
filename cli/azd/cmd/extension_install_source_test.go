// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestInferSourceKind(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	existing := filepath.Join(dir, "registry.json")
	require.NoError(t, os.WriteFile(existing, []byte("{}"), 0600))

	t.Run("HttpUrl", func(t *testing.T) {
		kind, ok := inferSourceKind("http://example.com/registry.json")
		require.True(t, ok)
		require.Equal(t, extensions.SourceKindUrl, kind)
	})

	t.Run("HttpsUrl", func(t *testing.T) {
		kind, ok := inferSourceKind("https://example.com/registry.json")
		require.True(t, ok)
		require.Equal(t, extensions.SourceKindUrl, kind)
	})

	t.Run("ExistingFile", func(t *testing.T) {
		kind, ok := inferSourceKind(existing)
		require.True(t, ok)
		require.Equal(t, extensions.SourceKindFile, kind)
	})

	t.Run("JsonExtension", func(t *testing.T) {
		kind, ok := inferSourceKind("missing-registry.json")
		require.True(t, ok)
		require.Equal(t, extensions.SourceKindFile, kind)
	})

	t.Run("PathSeparator", func(t *testing.T) {
		kind, ok := inferSourceKind("./some/path")
		require.True(t, ok)
		require.Equal(t, extensions.SourceKindFile, kind)
	})

	t.Run("PlainNameIsNotLocation", func(t *testing.T) {
		_, ok := inferSourceKind("my-source")
		require.False(t, ok)
	})
}

func TestDefaultSourceName(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"https://example.com/registry.json": "example-com",
		"https://link/to/registry.json":     "link",
		"/path/to/registry.json":            "registry",
		"./custom.json":                     "custom",
	}

	for location, expected := range cases {
		require.Equal(t, expected, defaultSourceName(location), "location %q", location)
	}
}

func TestResolveSourceLocation_ExistingSourceUnchanged(t *testing.T) {
	t.Parallel()

	action, _ := newBundleInstallTestAction(t)
	require.NoError(t, action.sourceManager.Add(context.Background(), "my-source", &extensions.SourceConfig{
		Name:     "my-source",
		Type:     extensions.SourceKindUrl,
		Location: "https://example.com/registry.json",
	}))

	action.flags.source = "my-source"
	require.NoError(t, action.resolveSourceLocation(context.Background()))
	require.Equal(t, "my-source", action.flags.source)
}

func TestResolveSourceLocation_PlainNameUnchanged(t *testing.T) {
	t.Parallel()

	action, _ := newBundleInstallTestAction(t)
	action.flags.source = "not-a-location"
	require.NoError(t, action.resolveSourceLocation(context.Background()))
	require.Equal(t, "not-a-location", action.flags.source)
}

func TestResolveSourceLocation_NoPromptDirectsToSourceAdd(t *testing.T) {
	t.Parallel()

	action, _ := newBundleInstallTestAction(t)
	action.flags.global.NoPrompt = true
	action.flags.source = "https://example.com/registry.json"

	err := action.resolveSourceLocation(context.Background())
	require.Error(t, err)
	require.ErrorAs(t, err, new(*internal.ErrorWithSuggestion))
}

func TestResolveSourceLocation_FileRegistersSource(t *testing.T) {
	t.Parallel()

	registryPath := writeRegistryFile(t)

	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("local-dev")

	action, _ := newBundleInstallTestAction(t)
	action.console = console
	action.flags.source = registryPath

	require.NoError(t, action.resolveSourceLocation(context.Background()))
	require.Equal(t, "local-dev", action.flags.source)

	src, err := action.sourceManager.Get(context.Background(), "local-dev")
	require.NoError(t, err)
	require.Equal(t, extensions.SourceKindFile, src.Type)
	require.Equal(t, registryPath, src.Location)
}

func TestResolveSourceLocation_FileUsesDefaultNameWhenBlank(t *testing.T) {
	t.Parallel()

	registryPath := writeRegistryFile(t)

	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("")

	action, _ := newBundleInstallTestAction(t)
	action.console = console
	action.flags.source = registryPath

	require.NoError(t, action.resolveSourceLocation(context.Background()))
	require.Equal(t, "registry", action.flags.source)

	_, err := action.sourceManager.Get(context.Background(), "registry")
	require.NoError(t, err)
}

func TestResolveSourceLocation_UrlDeclinedReturnsError(t *testing.T) {
	t.Parallel()

	console := mockinput.NewMockConsole()
	console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(false)

	action, _ := newBundleInstallTestAction(t)
	action.console = console
	action.flags.source = "https://example.com/registry.json"

	err := action.resolveSourceLocation(context.Background())
	require.Error(t, err)
	require.ErrorAs(t, err, new(*internal.ErrorWithSuggestion))

	// No source should have been registered.
	_, getErr := action.sourceManager.Get(context.Background(), "example-com")
	require.ErrorIs(t, getErr, extensions.ErrSourceNotFound)
}

// writeRegistryFile writes a minimal valid registry.json to a temp dir and
// returns its absolute path.
func writeRegistryFile(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	registry := &extensions.Registry{
		SchemaVersion: extensions.CurrentRegistrySchemaVersion,
		Extensions: []*extensions.ExtensionMetadata{
			{
				Id:          "test.ext",
				DisplayName: "Test Extension",
				Versions: []extensions.ExtensionVersion{
					{Version: "1.0.0", Artifacts: map[string]extensions.ExtensionArtifact{
						"linux/amd64": {URL: "artifacts/ext.tar.gz"},
					}},
				},
			},
		},
	}
	data, err := json.Marshal(registry)
	require.NoError(t, err)

	registryPath := filepath.Join(dir, "registry.json")
	require.NoError(t, os.WriteFile(registryPath, data, 0600))
	return registryPath
}
