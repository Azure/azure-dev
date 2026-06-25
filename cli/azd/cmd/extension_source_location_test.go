// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

const sourceLocationRegistryURL = "https://example.com/registry.json"

func stubRegistryHTTP(mockContext *mocks.MockContext) {
	mockContext.HttpClient.When(func(req *http.Request) bool {
		return req.URL.String() == sourceLocationRegistryURL
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, extensions.Registry{
			SchemaVersion: extensions.CurrentRegistrySchemaVersion,
			Extensions: []*extensions.ExtensionMetadata{
				{
					Id:          "test.ext",
					DisplayName: "Test Extension",
					Versions: []extensions.ExtensionVersion{
						{Version: "1.0.0"},
					},
				},
			},
		})
	})
}

func newSourceLocationTestManager(
	t *testing.T,
) (*mocks.MockContext, *extensions.Manager, *extensions.SourceManager) {
	t.Helper()

	mockContext := mocks.NewMockContext(t.Context())
	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := extensions.NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*extensions.Runner, error) {
		return extensions.NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := extensions.NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	return mockContext, manager, sourceManager
}

func TestExtensionList_DirectUrlSource(t *testing.T) {
	t.Parallel()

	mockContext, manager, sourceManager := newSourceLocationTestManager(t)
	stubRegistryHTTP(mockContext)

	var buf bytes.Buffer
	action := &extensionListAction{
		flags:            &extensionListFlags{source: sourceLocationRegistryURL},
		formatter:        &output.JsonFormatter{},
		console:          mockContext.Console,
		writer:           &buf,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	var rows []extensionListItem
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.Equal(t, "test.ext", rows[0].Id)
	require.Equal(t, sourceLocationRegistryURL, rows[0].Source)

	requireLocationNotRegistered(t, sourceManager, sourceLocationRegistryURL)
}

func TestExtensionList_DirectUrlSourceDoesNotPrompt(t *testing.T) {
	t.Parallel()

	mockContext, manager, sourceManager := newSourceLocationTestManager(t)
	stubRegistryHTTP(mockContext)

	var buf bytes.Buffer
	action := &extensionListAction{
		flags: &extensionListFlags{
			source: sourceLocationRegistryURL,
		},
		formatter:        &output.JsonFormatter{},
		console:          mockContext.Console,
		writer:           &buf,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	var rows []extensionListItem
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rows))
	require.Len(t, rows, 1)
}

func TestExtensionList_UnknownRegisteredNameErrors(t *testing.T) {
	t.Parallel()

	_, manager, sourceManager := newSourceLocationTestManager(t)

	var buf bytes.Buffer
	action := &extensionListAction{
		flags:            &extensionListFlags{source: "not-a-registered-source"},
		formatter:        &output.JsonFormatter{},
		console:          mocks.NewMockContext(t.Context()).Console,
		writer:           &buf,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func TestExtensionList_DirectRelativeFileSource(t *testing.T) {
	registryPath := writeRegistryFile(t)
	t.Chdir(filepath.Dir(registryPath))

	_, manager, sourceManager := newSourceLocationTestManager(t)

	var buf bytes.Buffer
	action := &extensionListAction{
		flags:            &extensionListFlags{source: "registry.json"},
		formatter:        &output.JsonFormatter{},
		console:          mocks.NewMockContext(t.Context()).Console,
		writer:           &buf,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	var rows []extensionListItem
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.Equal(t, registryPath, rows[0].Source)
	requireLocationNotRegistered(t, sourceManager, registryPath)
}

func TestExtensionList_DirectMissingFileSourceReturnsError(t *testing.T) {
	t.Parallel()

	_, manager, sourceManager := newSourceLocationTestManager(t)

	var buf bytes.Buffer
	action := &extensionListAction{
		flags:            &extensionListFlags{source: "./missing-registry.json"},
		formatter:        &output.JsonFormatter{},
		console:          mocks.NewMockContext(t.Context()).Console,
		writer:           &buf,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.ErrorContains(t, err, "failed listing extensions from registry")
	require.ErrorContains(t, err, "failed initializing extension source")
}

func TestExtensionShow_DirectUrlSource(t *testing.T) {
	t.Parallel()

	mockContext, manager, sourceManager := newSourceLocationTestManager(t)
	stubRegistryHTTP(mockContext)

	action := &extensionShowAction{
		args: []string{"test.ext"},
		flags: &extensionShowFlags{
			source: sourceLocationRegistryURL,
			global: &internal.GlobalCommandOptions{},
		},
		console:          mockContext.Console,
		formatter:        &output.NoneFormatter{},
		writer:           &bytes.Buffer{},
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	requireLocationNotRegistered(t, sourceManager, sourceLocationRegistryURL)
}

func TestExtensionUpgrade_UrlSourceRegistersSource(t *testing.T) {
	t.Parallel()

	mockContext, manager, sourceManager := newSourceLocationTestManager(t)
	stubRegistryHTTP(mockContext)

	mockContext.Console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)
	mockContext.Console.WhenPrompt(func(input.ConsoleOptions) bool { return true }).Respond("example-registry")

	var buf bytes.Buffer
	action := &extensionUpgradeAction{
		args: []string{"test.ext"},
		flags: &extensionUpgradeFlags{
			source: sourceLocationRegistryURL,
			global: &internal.GlobalCommandOptions{},
		},
		formatter:        &output.JsonFormatter{},
		writer:           &buf,
		console:          mockContext.Console,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)

	src, err := sourceManager.Get(t.Context(), "example-registry")
	require.NoError(t, err)
	require.Equal(t, extensions.SourceKindUrl, src.Type)
	require.Equal(t, sourceLocationRegistryURL, src.Location)

	require.Equal(t, "example-registry", action.flags.source)
}

func TestExtensionUpgrade_UrlSourceBlockedUnderNoPrompt(t *testing.T) {
	t.Parallel()

	mockContext, manager, sourceManager := newSourceLocationTestManager(t)

	var buf bytes.Buffer
	action := &extensionUpgradeAction{
		args: []string{"test.ext"},
		flags: &extensionUpgradeFlags{
			source: sourceLocationRegistryURL,
			global: &internal.GlobalCommandOptions{NoPrompt: true},
		},
		formatter:        &output.JsonFormatter{},
		writer:           &buf,
		console:          mockContext.Console,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.ErrorAs(t, err, new(*internal.ErrorWithSuggestion))

	requireLocationNotRegistered(t, sourceManager, sourceLocationRegistryURL)
}

func requireLocationNotRegistered(
	t *testing.T,
	sourceManager *extensions.SourceManager,
	location string,
) {
	t.Helper()

	sources, err := sourceManager.List(t.Context())
	require.NoError(t, err)
	for _, src := range sources {
		require.NotEqual(t, location, src.Location, "location %q must not be registered", location)
	}
}
