// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
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

	t.Run("MixedCaseHttpsUrl", func(t *testing.T) {
		kind, ok := inferSourceKind("HTTPS://Example.com/registry.json")
		require.True(t, ok)
		require.Equal(t, extensions.SourceKindUrl, kind)
	})

	t.Run("UpperCaseHttpUrl", func(t *testing.T) {
		kind, ok := inferSourceKind("HTTP://example.com/registry.json")
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
	require.NoError(t, action.sourceManager.Add(t.Context(), "my-source", &extensions.SourceConfig{
		Name:     "my-source",
		Type:     extensions.SourceKindUrl,
		Location: "https://example.com/registry.json",
	}))

	action.flags.source = "my-source"
	require.NoError(t, action.resolveSourceLocation(t.Context()))
	require.Equal(t, "my-source", action.flags.source)
}

func TestResolveSourceLocation_PlainNameUnchanged(t *testing.T) {
	t.Parallel()

	action, _ := newBundleInstallTestAction(t)
	action.flags.source = "not-a-location"
	require.NoError(t, action.resolveSourceLocation(t.Context()))
	require.Equal(t, "not-a-location", action.flags.source)
}

func TestResolveSourceLocation_NoPromptDirectsToSourceAdd(t *testing.T) {
	t.Parallel()

	action, _ := newBundleInstallTestAction(t)
	action.flags.global.NoPrompt = true
	action.flags.source = "https://example.com/registry.json"

	err := action.resolveSourceLocation(t.Context())
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

	require.NoError(t, action.resolveSourceLocation(t.Context()))
	require.Equal(t, "local-dev", action.flags.source)

	src, err := action.sourceManager.Get(t.Context(), "local-dev")
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

	require.NoError(t, action.resolveSourceLocation(t.Context()))
	require.Equal(t, "registry", action.flags.source)

	_, err := action.sourceManager.Get(t.Context(), "registry")
	require.NoError(t, err)
}

func TestResolveSourceLocation_UrlDeclinedReturnsError(t *testing.T) {
	t.Parallel()

	console := mockinput.NewMockConsole()
	console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(false)

	action, _ := newBundleInstallTestAction(t)
	action.console = console
	action.flags.source = "https://example.com/registry.json"

	err := action.resolveSourceLocation(t.Context())
	require.Error(t, err)
	require.ErrorAs(t, err, new(*internal.ErrorWithSuggestion))

	_, getErr := action.sourceManager.Get(t.Context(), "example-com")
	require.ErrorIs(t, getErr, extensions.ErrSourceNotFound)
}

func TestResolveSourceLocation_ExistingUrlLocationReused(t *testing.T) {
	t.Parallel()

	action, _ := newBundleInstallTestAction(t)
	require.NoError(t, action.sourceManager.Add(t.Context(), "myreg", &extensions.SourceConfig{
		Name:     "myreg",
		Type:     extensions.SourceKindUrl,
		Location: "https://example.com/registry.json",
	}))

	action.flags.source = "HTTPS://example.com/registry.json"
	require.NoError(t, action.resolveSourceLocation(t.Context()))
	require.Equal(t, "myreg", action.flags.source)

	sources, err := action.sourceManager.List(t.Context())
	require.NoError(t, err)
	matches := 0
	for _, src := range sources {
		if src.Location == "https://example.com/registry.json" {
			matches++
		}
	}
	require.Equal(t, 1, matches)
}

func TestResolveSourceLocation_ExistingFileLocationReusedFromRelativePath(t *testing.T) {
	registryPath := writeRegistryFile(t)
	dir := filepath.Dir(registryPath)

	action, _ := newBundleInstallTestAction(t)
	require.NoError(t, action.sourceManager.Add(t.Context(), "filereg", &extensions.SourceConfig{
		Name:     "filereg",
		Type:     extensions.SourceKindFile,
		Location: registryPath,
	}))

	t.Chdir(dir)
	action.flags.source = "registry.json"
	require.NoError(t, action.resolveSourceLocation(t.Context()))
	require.Equal(t, "filereg", action.flags.source)
}

func TestResolveSourceLocation_FilePersistsAbsolutePath(t *testing.T) {
	registryPath := writeRegistryFile(t)
	dir := filepath.Dir(registryPath)

	console := mockinput.NewMockConsole()
	console.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("local-dev")

	action, _ := newBundleInstallTestAction(t)
	action.console = console

	t.Chdir(dir)
	action.flags.source = "registry.json"
	require.NoError(t, action.resolveSourceLocation(t.Context()))
	require.Equal(t, "local-dev", action.flags.source)

	src, err := action.sourceManager.Get(t.Context(), "local-dev")
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(src.Location), "location %q should be absolute", src.Location)
	require.Equal(t, registryPath, src.Location)
}

func TestResolveSourceLocation_UrlAcceptedRegistersSource(t *testing.T) {
	t.Parallel()

	action, mockContext := newInstallSourceTestAction(t)
	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.URL.String() == "https://example.com/registry.json"
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, extensions.Registry{
			SchemaVersion: extensions.CurrentRegistrySchemaVersion,
		})
	})

	mockContext.Console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)
	mockContext.Console.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("example-registry")

	action.flags.source = "https://example.com/registry.json"
	require.NoError(t, action.resolveSourceLocation(t.Context()))
	require.Equal(t, "example-registry", action.flags.source)

	src, err := action.sourceManager.Get(t.Context(), "example-registry")
	require.NoError(t, err)
	require.Equal(t, extensions.SourceKindUrl, src.Type)
	require.Equal(t, "https://example.com/registry.json", src.Location)
}

func TestResolveSourceLocation_NoPromptFileDirectsToSourceAdd(t *testing.T) {
	t.Parallel()

	registryPath := writeRegistryFile(t)

	action, _ := newBundleInstallTestAction(t)
	action.flags.global.NoPrompt = true
	action.flags.source = registryPath

	err := action.resolveSourceLocation(t.Context())
	require.Error(t, err)
	require.ErrorAs(t, err, new(*internal.ErrorWithSuggestion))

	sources, err := action.sourceManager.List(t.Context())
	require.NoError(t, err)
	for _, src := range sources {
		require.NotEqual(t, registryPath, src.Location, "the file source must not be registered")
	}
}

func newInstallSourceTestAction(t *testing.T) (*extensionInstallAction, *mocks.MockContext) {
	t.Helper()

	mockContext := mocks.NewMockContext(t.Context())
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := extensions.NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*extensions.Runner, error) {
		return extensions.NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := extensions.NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	action := &extensionInstallAction{
		console:          mockContext.Console,
		extensionManager: manager,
		sourceManager:    sourceManager,
		flags:            &extensionInstallFlags{global: &internal.GlobalCommandOptions{}},
	}
	return action, mockContext
}

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
