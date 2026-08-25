// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestIsBundleArg(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	zipPath := filepath.Join(dir, "bundle.zip")
	require.NoError(t, os.WriteFile(zipPath, []byte("zip"), 0600))

	require.True(t, isBundleArg([]string{zipPath}))
	require.False(t, isBundleArg([]string{filepath.Join(dir, "missing.zip")})) // does not exist
	require.False(t, isBundleArg([]string{"some.extension.id"}))
	require.False(t, isBundleArg([]string{zipPath, "other"}))
	require.False(t, isBundleArg([]string{dir})) // directory, not a file
	require.False(t, isBundleArg(nil))

	// Remote bundle URLs are recognized without touching the local file system.
	// HTTP is detected here so the install path can return actionable HTTPS guidance.
	require.True(t, isBundleArg([]string{"https://example.com/my-ext_1.0.0.zip"}))
	require.True(t, isBundleArg([]string{"http://example.com/path/my-ext.ZIP"}))
	require.True(t, isBundleArg([]string{"https://example.com/my-ext.zip?token=abc"}))
	require.True(t, isBundleArg([]string{"https://example.com/%ZZ/my-ext.zip?token=abc"}))
	require.False(t, isBundleArg([]string{"https://example.com/registry.json"}))
	require.True(t, isBundleArg([]string{"https:///my-ext.zip"}))
	require.False(t, isBundleArg([]string{"ftp://example.com/my-ext.zip"}))
	require.False(t, isBundleArg([]string{"https://example.com/my-ext_1.0.0.zip", "other"}))
}

func TestNormalizeBundleSourceName(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"My Bundle":            "my-bundle",
		"ext_1.0.0":            "ext_1-0-0",
		"weird@@name!!":        "weird-name",
		"--leading-trailing--": "leading-trailing",
		"_leading":             "leading",
		"trailing_":            "trailing",
		"___":                  "",
		"UPPER":                "upper",
	}

	for input, expected := range cases {
		require.Equal(t, expected, normalizeBundleSourceName(input), "input %q", input)
	}
}

func TestBundleSourceName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "my-ext_1-0-0", bundleSourceName("/tmp/My Ext_1.0.0.zip"))
	require.Equal(t, "bundle", bundleSourceName("bundle.zip"))
}

func TestPrepareBundleInstall_LongNameUsesValidTransientSource(t *testing.T) {
	t.Parallel()

	action, _ := newBundleInstallTestAction(t)
	zipPath := makeBundleZip(t, []*extensions.ExtensionMetadata{
		{
			Id:          "test.ext",
			DisplayName: "Test Extension",
			Versions: []extensions.ExtensionVersion{
				{Version: "1.0.0", Artifacts: map[string]extensions.ExtensionArtifact{
					"linux/amd64": {URL: "artifacts/ext.tar.gz"},
				}},
			},
		},
	})
	longPath := filepath.Join(filepath.Dir(zipPath), strings.Repeat("a", 100)+".zip")
	require.NoError(t, os.Rename(zipPath, longPath))

	require.NoError(t, action.prepareBundleInstall(t.Context(), longPath))
	require.NoError(t, extensions.ValidateSourceName(action.bundleSourceName))
	require.LessOrEqual(t, len(action.bundleSourceName), extensions.SourceNameMaxLength)
	action.cleanupBundleInstall(t.Context())
}

func TestCleanupBundleInstall_RemovesTempDir(t *testing.T) {
	t.Parallel()

	bundleDir := t.TempDir()
	marker := filepath.Join(bundleDir, "registry.json")
	require.NoError(t, os.WriteFile(marker, []byte("{}"), 0600))

	// With no transient source recorded, cleanup only removes the temp dir and
	// does not touch the (nil) managers.
	action := &extensionInstallAction{bundleTempDir: bundleDir}
	action.cleanupBundleInstall(context.Background())

	require.NoDirExists(t, bundleDir)
	require.Empty(t, action.bundleTempDir)

	// Idempotent: a second call is a no-op and does not panic.
	require.NotPanics(t, func() {
		action.cleanupBundleInstall(context.Background())
	})
}

func TestRandomHexToken(t *testing.T) {
	t.Parallel()

	a, err := randomHexToken()
	require.NoError(t, err)
	require.Len(t, a, 8)

	b, err := randomHexToken()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestSourceDisplayLabel(t *testing.T) {
	t.Parallel()

	// During a bundle install the transient source name is presented as "bundle".
	withBundle := &extensionInstallAction{bundleSourceName: "demo-abc123"}
	require.Equal(t, "bundle", withBundle.sourceDisplayLabel("demo-abc123"))
	require.Equal(t, "bundle", withBundle.sourceDisplayLabel("DEMO-ABC123"))

	// The reserved bundle source is also presented as "bundle".
	require.Equal(t, "bundle", withBundle.sourceDisplayLabel(extensions.BundleSourceName))

	// Regular sources are quoted by name.
	require.Equal(t, `source "azd"`, withBundle.sourceDisplayLabel("azd"))

	noBundle := &extensionInstallAction{}
	require.Equal(t, "bundle", noBundle.sourceDisplayLabel(extensions.BundleSourceName))
	require.Equal(t, `source "local"`, noBundle.sourceDisplayLabel("local"))

	// The installed-from variant phrases the bundle case with an article.
	require.Equal(t, "a bundle", noBundle.sourceDisplayLabelForInstalled(extensions.BundleSourceName))
	require.Equal(t, "a bundle", withBundle.sourceDisplayLabelForInstalled("demo-abc123"))
	require.Equal(t, `source "azd"`, noBundle.sourceDisplayLabelForInstalled("azd"))
}

func TestWrapErrorWithSuggestion(t *testing.T) {
	t.Parallel()

	plain := fmt.Errorf("some other failure")
	require.Equal(t, plain, internal.WrapErrorWithSuggestion(plain))

	depErr := fmt.Errorf("install failed: %w", &extensions.DependencyNotFoundError{
		DependencyId: "azure.ai.inspector",
		ParentId:     "azure.ai.agents",
	})
	wrapped := internal.WrapErrorWithSuggestion(depErr)

	suggestErr, ok := errors.AsType[*internal.ErrorWithSuggestion](wrapped)
	require.True(t, ok, "expected ErrorWithSuggestion, got %T", wrapped)
	require.Contains(t, suggestErr.Suggestion, "azd extension install azure.ai.inspector")
	require.ErrorAs(t, suggestErr.Err, new(*extensions.DependencyNotFoundError))

	versionErr := fmt.Errorf("install failed: %w", &extensions.DependencyVersionNotFoundError{
		DependencyId: "azure.ai.inspector",
		ParentId:     "azure.ai.agents",
		Constraint:   ">=2.0.0",
	})
	wrapped = internal.WrapErrorWithSuggestion(versionErr)

	suggestErr, ok = errors.AsType[*internal.ErrorWithSuggestion](wrapped)
	require.True(t, ok, "expected ErrorWithSuggestion, got %T", wrapped)
	require.Contains(t, suggestErr.Suggestion, "azure.ai.inspector")
	require.Contains(t, suggestErr.Suggestion, ">=2.0.0")
	require.Contains(t, suggestErr.Suggestion, "azure.ai.agents")
	require.ErrorAs(t, suggestErr.Err, new(*extensions.DependencyVersionNotFoundError))
}

func newConfirmTestAction(console input.Console, noPrompt bool) *extensionInstallAction {
	return &extensionInstallAction{
		console: console,
		flags: &extensionInstallFlags{
			global: &internal.GlobalCommandOptions{NoPrompt: noPrompt},
		},
	}
}

func TestConfirmSourceChange(t *testing.T) {
	t.Parallel()

	installed := &extensions.Extension{
		Id:      "azure.ai.agents",
		Version: "1.0.0",
		Source:  "azd",
	}

	t.Run("ReinstallSameVersion", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)
		action := newConfirmTestAction(console, false)

		proceed, err := action.confirmSourceChange(
			context.Background(), "Installing", installed.Id, installed, extensions.BundleSourceName, "1.0.0",
		)
		require.NoError(t, err)
		require.True(t, proceed)
		require.Contains(t, lastConfirmMessage(console),
			`azure.ai.agents 1.0.0 is already installed from source "azd". Reinstall from bundle?`)
	})

	t.Run("UpdateShowsTargetVersion", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)
		action := newConfirmTestAction(console, false)

		proceed, err := action.confirmSourceChange(
			context.Background(), "Installing", installed.Id, installed, extensions.BundleSourceName, "2.0.0",
		)
		require.NoError(t, err)
		require.True(t, proceed)
		require.Contains(t, lastConfirmMessage(console),
			`azure.ai.agents 1.0.0 is already installed from source "azd". Update to 2.0.0 from bundle?`)
	})

	t.Run("DowngradeDeclined", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(false)
		action := newConfirmTestAction(console, false)

		proceed, err := action.confirmSourceChange(
			context.Background(), "Installing", installed.Id, installed, extensions.BundleSourceName, "0.9.0",
		)
		require.NoError(t, err)
		require.False(t, proceed)
		require.Contains(t, lastConfirmMessage(console),
			`azure.ai.agents 1.0.0 is already installed from source "azd". Downgrade to 0.9.0 from bundle?`)
	})

	t.Run("InstalledFromBundleReadsWithArticle", func(t *testing.T) {
		// Reverse direction: a bundle-installed extension being replaced from a
		// registry source. The installed-from clause reads "a bundle".
		bundleInstalled := &extensions.Extension{
			Id:      "azure.ai.agents",
			Version: "1.0.0",
			Source:  extensions.BundleSourceName,
		}
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)
		action := newConfirmTestAction(console, false)

		proceed, err := action.confirmSourceChange(
			context.Background(), "Installing", bundleInstalled.Id, bundleInstalled, "azd", "1.0.0",
		)
		require.NoError(t, err)
		require.True(t, proceed)
		require.Contains(t, lastConfirmMessage(console),
			`azure.ai.agents 1.0.0 is already installed from a bundle. Reinstall from source "azd"?`)
	})

	t.Run("NoPromptSkipsWithoutAsking", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		// No WhenConfirm registered: if Confirm were called it would panic/fail.
		action := newConfirmTestAction(console, true)

		proceed, err := action.confirmSourceChange(
			context.Background(), "Installing", installed.Id, installed, extensions.BundleSourceName, "1.0.0",
		)
		require.NoError(t, err)
		require.False(t, proceed)
	})
}

// lastConfirmMessage returns the most recent non-empty message the mock console
// captured, which for a confirm prompt is the prompt text (ignoring blank-line
// spacing messages).
func lastConfirmMessage(console *mockinput.MockConsole) string {
	out := console.Output()
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] != "" {
			return out[i]
		}
	}
	return ""
}

func TestConfirmReplace(t *testing.T) {
	t.Parallel()

	const question = "azure.ai.agents version 1.0.0 is installed. Downgrade to 0.9.0?"
	const skipSuffix = " (would downgrade from 1.0.0 to 0.9.0, use --force to override)"

	t.Run("Confirmed", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)
		action := newConfirmTestAction(console, false)

		proceed, err := action.confirmReplace(context.Background(), "Installing", question, skipSuffix)
		require.NoError(t, err)
		require.True(t, proceed)
		require.Contains(t, lastConfirmMessage(console), question)
	})

	t.Run("Declined", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(false)
		action := newConfirmTestAction(console, false)

		proceed, err := action.confirmReplace(context.Background(), "Installing", question, skipSuffix)
		require.NoError(t, err)
		require.False(t, proceed)
	})

	t.Run("NoPromptSkipsWithoutAsking", func(t *testing.T) {
		console := mockinput.NewMockConsole()
		// No WhenConfirm registered: if Confirm were called it would fail.
		action := newConfirmTestAction(console, true)

		proceed, err := action.confirmReplace(context.Background(), "Installing", question, skipSuffix)
		require.NoError(t, err)
		require.False(t, proceed)
	})
}

func TestVersionTransitionVerb(t *testing.T) {
	t.Parallel()

	cases := []struct {
		installed string
		target    string
		expected  string
	}{
		{"1.0.0", "1.0.0", "Reinstall"},
		{"1.0.0", "2.0.0", "Update to 2.0.0"},
		{"1.0.0", "0.9.0", "Downgrade to 0.9.0"},
		{"1.0.0-preview", "1.0.0", "Update to 1.0.0"},
		// Non-semver tags have no defined ordering -> neutral verb.
		{"nightly", "1.0.0", "Replace with 1.0.0"},
		{"1.0.0", "nightly", "Replace with nightly"},
	}

	for _, tc := range cases {
		require.Equal(t, tc.expected, versionTransitionVerb(tc.installed, tc.target),
			"installed=%s target=%s", tc.installed, tc.target)
	}
}

// makeBundleZip builds a minimal self-contained bundle .zip on disk containing a
// registry.json (with the given extensions) plus a dummy artifact, and returns
// its path. Artifact URLs in the registry should be relative (e.g.
// "artifacts/ext.tar.gz") so the bundle source can anchor them.
func makeBundleZip(t *testing.T, exts []*extensions.ExtensionMetadata) string {
	t.Helper()

	stagingDir := t.TempDir()
	registry := &extensions.Registry{
		SchemaVersion: extensions.CurrentRegistrySchemaVersion,
		Extensions:    exts,
	}
	data, err := json.Marshal(registry)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(stagingDir, "registry.json"), data, 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(stagingDir, "artifacts"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stagingDir, "artifacts", "ext.tar.gz"), []byte("x"), 0600))

	zipPath := filepath.Join(t.TempDir(), "bundle.zip")
	zipFile, err := os.Create(zipPath)
	require.NoError(t, err)
	defer zipFile.Close()

	zw := zip.NewWriter(zipFile)
	require.NoError(t, filepath.Walk(stagingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(stagingDir, path)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	}))
	require.NoError(t, zw.Close())

	return zipPath
}

// newBundleInstallTestAction wires up an install action backed by real extension
// and source managers over a mock config, for exercising the bundle install
// lifecycle. It returns the action and the user config manager so tests can seed
// installed-extension state.
func newBundleInstallTestAction(t *testing.T) (*extensionInstallAction, config.UserConfigManager) {
	t.Helper()

	action, userConfigManager, _ := newBundleInstallTestActionWithMocks(t)
	return action, userConfigManager
}

// newBundleInstallTestActionWithMocks is like newBundleInstallTestAction but also
// returns the mock context so tests can stub HTTP responses (e.g. remote bundle
// downloads).
func newBundleInstallTestActionWithMocks(
	t *testing.T,
) (*extensionInstallAction, config.UserConfigManager, *mocks.MockContext) {
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
		console:          mockinput.NewMockConsole(),
		extensionManager: manager,
		sourceManager:    sourceManager,
		transport:        mockContext.HttpClient,
		flags:            &extensionInstallFlags{global: &internal.GlobalCommandOptions{}},
	}
	return action, userConfigManager, mockContext
}

func TestPrepareBundleInstall_Success(t *testing.T) {
	t.Parallel()

	action, _ := newBundleInstallTestAction(t)
	zipPath := makeBundleZip(t, []*extensions.ExtensionMetadata{
		{
			Id:          "test.ext",
			DisplayName: "Test Extension",
			Versions: []extensions.ExtensionVersion{
				{Version: "1.0.0", Artifacts: map[string]extensions.ExtensionArtifact{
					"linux/amd64": {URL: "artifacts/ext.tar.gz"},
				}},
			},
		},
	})

	err := action.prepareBundleInstall(context.Background(), zipPath)
	require.NoError(t, err)

	// The bundled extension is queued for install against the transient source.
	require.Equal(t, []string{"test.ext"}, action.args)
	require.NotEmpty(t, action.bundleSourceName)
	require.Equal(t, action.bundleSourceName, action.flags.source)
	require.NotEmpty(t, action.bundleTempDir)
	require.DirExists(t, action.bundleTempDir)

	// The transient source is registered and resolvable.
	src, err := action.sourceManager.Get(context.Background(), action.bundleSourceName)
	require.NoError(t, err)
	require.Equal(t, extensions.SourceKindBundle, src.Type)

	// Cleanup removes the transient source and temp dir.
	action.cleanupBundleInstall(context.Background())
	require.NoDirExists(t, action.bundleTempDir)
	require.Empty(t, action.bundleSourceName)
	_, err = action.sourceManager.Get(context.Background(), src.Name)
	require.ErrorIs(t, err, extensions.ErrSourceNotFound)
}

// respondWithBundleZip stubs the mock HTTP client so any request for bundleURL
// returns the bytes of the bundle .zip at zipPath.
func respondWithBundleZip(t *testing.T, mockContext *mocks.MockContext, bundleURL string, zipPath string) {
	t.Helper()

	zipBytes, err := os.ReadFile(zipPath)
	require.NoError(t, err)

	mockContext.HttpClient.
		When(func(request *http.Request) bool {
			return request.URL.String() == bundleURL
		}).
		RespondFn(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Request:    request,
				Body:       io.NopCloser(bytes.NewReader(zipBytes)),
			}, nil
		})
}

func TestPrepareBundleInstall_RemoteURL(t *testing.T) {
	t.Parallel()

	const (
		bundleURL   = "https://example.com/builds/path-secret.zip?sig=query-secret"
		pathSecret  = "path-secret"
		querySecret = "query-secret"
	)

	action, _, mockContext := newBundleInstallTestActionWithMocks(t)
	zipPath := makeBundleZip(t, []*extensions.ExtensionMetadata{
		{
			Id:          "test.ext",
			DisplayName: "Test Extension",
			Versions: []extensions.ExtensionVersion{
				{Version: "1.0.0", Artifacts: map[string]extensions.ExtensionArtifact{
					"linux/amd64": {URL: "artifacts/ext.tar.gz"},
				}},
			},
		},
	})
	respondWithBundleZip(t, mockContext, bundleURL, zipPath)

	err := action.prepareBundleInstall(t.Context(), bundleURL)
	require.NoError(t, err)

	// The downloaded bundle goes through the same extraction and validation path.
	require.Equal(t, []string{"test.ext"}, action.args)
	require.Equal(t, action.bundleSourceName, action.flags.source)
	require.Regexp(t, `^bundle-[0-9a-f]{8}$`, action.bundleSourceName)
	require.NotContains(t, action.bundleSourceName, pathSecret)
	require.DirExists(t, action.bundleTempDir)
	require.FileExists(t, action.bundleTempZip)

	console, ok := action.console.(*mockinput.MockConsole)
	require.True(t, ok)
	for _, op := range console.SpinnerOps() {
		require.NotContains(t, op.Message, bundleURL)
		require.NotContains(t, op.Message, pathSecret)
		require.NotContains(t, op.Message, querySecret)
	}

	// Cleanup removes both the extracted directory and the downloaded .zip.
	downloadedZip := action.bundleTempZip
	action.cleanupBundleInstall(t.Context())
	require.NoFileExists(t, downloadedZip)
	require.Empty(t, action.bundleTempZip)
	require.Empty(t, action.bundleTempDir)
}

func TestPrepareBundleInstall_RejectsHTTPURL(t *testing.T) {
	t.Parallel()

	const bundleURL = "http://example.com/path-secret.zip?sig=query-secret"

	action, _, _ := newBundleInstallTestActionWithMocks(t)
	err := action.prepareBundleInstall(t.Context(), bundleURL)
	require.Error(t, err)
	require.ErrorContains(t, err, "must use HTTPS")
	require.ErrorAs(t, err, new(*internal.ErrorWithSuggestion))
	require.NotContains(t, err.Error(), "path-secret")
	require.NotContains(t, err.Error(), "query-secret")
	require.Empty(t, action.bundleTempZip)
	require.Empty(t, action.bundleTempDir)
}

func TestExtensionInstall_InvalidRemoteURLDoesNotExposeURL(t *testing.T) {
	t.Parallel()

	const (
		bundleURL   = "https://example.com/%ZZ-path-secret/bundle.zip?sig=query-secret"
		pathSecret  = "path-secret"
		querySecret = "query-secret"
	)

	action, _, _ := newBundleInstallTestActionWithMocks(t)
	action.args = []string{bundleURL}

	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid remote extension bundle URL")
	require.ErrorAs(t, err, new(*internal.ErrorWithSuggestion))
	require.NotContains(t, err.Error(), bundleURL)
	require.NotContains(t, err.Error(), pathSecret)
	require.NotContains(t, err.Error(), querySecret)

	console, ok := action.console.(*mockinput.MockConsole)
	require.True(t, ok)
	for _, message := range console.Output() {
		require.NotContains(t, message, bundleURL)
		require.NotContains(t, message, pathSecret)
		require.NotContains(t, message, querySecret)
	}
	for _, op := range console.SpinnerOps() {
		require.NotContains(t, op.Message, bundleURL)
		require.NotContains(t, op.Message, pathSecret)
		require.NotContains(t, op.Message, querySecret)
	}
}

func TestDownloadBundle_RejectsInsecureRedirectHop(t *testing.T) {
	t.Parallel()

	var insecureRequested atomic.Bool
	var finalRequested atomic.Bool
	var insecureServer *httptest.Server

	secureServer := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/bundle.zip":
			http.Redirect(writer, request, insecureServer.URL+"/redirect", http.StatusFound)
		case "/final.zip":
			finalRequested.Store(true)
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(secureServer.Close)

	insecureServer = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		insecureRequested.Store(true)
		http.Redirect(writer, request, secureServer.URL+"/final.zip", http.StatusFound)
	}))
	t.Cleanup(insecureServer.Close)

	action := &extensionInstallAction{
		console:   mockinput.NewMockConsole(),
		transport: secureServer.Client(),
	}
	t.Cleanup(func() {
		if action.bundleTempZip != "" {
			_ = os.Remove(action.bundleTempZip)
		}
	})

	_, err := action.downloadBundle(t.Context(), secureServer.URL+"/bundle.zip")
	require.Error(t, err)
	require.ErrorIs(t, err, errInsecureBundleRedirect)
	require.ErrorAs(t, err, new(*internal.ErrorWithSuggestion))
	require.False(t, insecureRequested.Load(), "HTTP redirect target should not be requested")
	require.False(t, finalRequested.Load(), "redirect chain should stop before the final HTTPS target")
	require.Empty(t, action.bundleTempZip)
}

func TestPrepareBundleInstall_RemoteDownloadFailure(t *testing.T) {
	t.Parallel()

	const (
		bundleURL = "https://example.com/missing.zip?sig=secret-value"
		secret    = "secret-value"
	)

	action, _, mockContext := newBundleInstallTestActionWithMocks(t)
	mockContext.HttpClient.
		When(func(request *http.Request) bool { return request.URL.String() == bundleURL }).
		RespondFn(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{},
				Request:    request,
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		})

	err := action.prepareBundleInstall(t.Context(), bundleURL)
	require.Error(t, err)
	// Download failures are distinct from invalid bundle contents.
	require.ErrorContains(t, err, "failed to download extension bundle")
	require.ErrorAs(t, err, new(*internal.ErrorWithSuggestion))
	require.NotContains(t, err.Error(), bundleURL)
	require.NotContains(t, err.Error(), secret)
	require.Empty(t, action.bundleTempDir)

	console, ok := action.console.(*mockinput.MockConsole)
	require.True(t, ok)
	for _, op := range console.SpinnerOps() {
		require.NotContains(t, op.Message, bundleURL)
		require.NotContains(t, op.Message, secret)
	}

	// Nothing was extracted or downloaded, so cleanup is a no-op.
	action.cleanupBundleInstall(t.Context())
	require.Empty(t, action.bundleTempZip)
}

func TestPrepareBundleInstall_RemoteTransportFailureDoesNotExposeURL(t *testing.T) {
	t.Parallel()

	const (
		bundleURL = "https://example.com/bundle.zip?sig=secret-value"
		secret    = "secret-value"
	)

	action, _, mockContext := newBundleInstallTestActionWithMocks(t)
	transportErr := fmt.Errorf("request to %s failed", bundleURL)
	mockContext.HttpClient.
		When(func(request *http.Request) bool { return request.URL.String() == bundleURL }).
		SetNonRetriableError(transportErr)

	err := action.prepareBundleInstall(t.Context(), bundleURL)
	require.EqualError(t, err, "failed to download extension bundle")
	require.ErrorAs(t, err, new(*internal.ErrorWithSuggestion))
	require.NotContains(t, err.Error(), bundleURL)
	require.NotContains(t, err.Error(), secret)

	downloadErr, ok := errors.AsType[*bundleDownloadError](err)
	require.True(t, ok)
	require.Contains(t, downloadErr.err.Error(), secret)

	console, ok := action.console.(*mockinput.MockConsole)
	require.True(t, ok)
	for _, op := range console.SpinnerOps() {
		require.NotContains(t, op.Message, bundleURL)
		require.NotContains(t, op.Message, secret)
	}
}

func TestPrepareBundleInstall_RemoteInvalidBundle(t *testing.T) {
	t.Parallel()

	const bundleURL = "https://example.com/not-a-bundle.zip"

	action, _, mockContext := newBundleInstallTestActionWithMocks(t)
	respondWithBundleZip(t, mockContext, bundleURL, makeZipWithoutRegistry(t))

	err := action.prepareBundleInstall(t.Context(), bundleURL)
	require.Error(t, err)
	// Invalid bundle contents surface as a bundle error, not a download error.
	require.ErrorContains(t, err, "bundle does not contain a registry.json")
	require.ErrorAs(t, err, new(*internal.ErrorWithSuggestion))

	// Both the downloaded .zip and the extraction directory are reclaimed.
	downloadedZip := action.bundleTempZip
	require.FileExists(t, downloadedZip)
	action.cleanupBundleInstall(t.Context())
	require.NoFileExists(t, downloadedZip)
	require.Empty(t, action.bundleTempDir)
}

func TestPrepareBundleInstall_MissingRegistry(t *testing.T) {
	t.Parallel()

	// A .zip without a registry.json at its root is rejected with guidance.
	zipPath := makeZipWithoutRegistry(t)

	action, _ := newBundleInstallTestAction(t)
	err := action.prepareBundleInstall(t.Context(), zipPath)
	require.Error(t, err)
	require.ErrorAs(t, err, new(*internal.ErrorWithSuggestion))

	// Temp dir is recorded so the deferred cleanup can still reclaim it.
	action.cleanupBundleInstall(t.Context())
	require.Empty(t, action.bundleTempDir)
}

// makeZipWithoutRegistry builds a .zip that is not a self-contained bundle (it
// has no registry.json at its root) and returns its path.
func makeZipWithoutRegistry(t *testing.T) string {
	t.Helper()

	zipPath := filepath.Join(t.TempDir(), "bad.zip")
	zipFile, err := os.Create(zipPath)
	require.NoError(t, err)
	zw := zip.NewWriter(zipFile)
	w, err := zw.Create("not-registry.txt")
	require.NoError(t, err)
	_, err = w.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, zipFile.Close())

	return zipPath
}

func TestCleanupBundleInstall_RepointsInstalledToBundle(t *testing.T) {
	t.Parallel()

	action, userConfigManager := newBundleInstallTestAction(t)
	zipPath := makeBundleZip(t, []*extensions.ExtensionMetadata{
		{
			Id:          "test.ext",
			DisplayName: "Test Extension",
			Versions: []extensions.ExtensionVersion{
				{Version: "1.0.0", Artifacts: map[string]extensions.ExtensionArtifact{
					"linux/amd64": {URL: "artifacts/ext.tar.gz"},
				}},
			},
		},
	})

	require.NoError(t, action.prepareBundleInstall(context.Background(), zipPath))

	// Simulate an extension installed from the transient bundle source.
	cfg, err := userConfigManager.Load()
	require.NoError(t, err)
	require.NoError(t, cfg.Set("extension.installed", map[string]*extensions.Extension{
		"test.ext": {Id: "test.ext", Version: "1.0.0", Source: action.bundleSourceName},
	}))
	require.NoError(t, userConfigManager.Save(cfg))
	require.NoError(t, action.extensionManager.ReloadUserConfig())

	action.cleanupBundleInstall(context.Background())

	// The installed record is re-pointed to the reserved bundle source.
	installed, err := action.extensionManager.GetInstalled(extensions.FilterOptions{Id: "test.ext"})
	require.NoError(t, err)
	require.Equal(t, extensions.BundleSourceName, installed.Source)
	require.Equal(t, extensions.SourceCategoryBundle, installed.SourceCategory)
}

func TestExtensionList_SurfacesBundleInstalledExtension(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())

	// The default registry source is fetched over HTTP; return an empty registry
	// so the installed (untracked) extension is surfaced solely by the local pass.
	mockContext.HttpClient.When(func(*http.Request) bool { return true }).
		RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(
				request, http.StatusOK,
				extensions.Registry{SchemaVersion: extensions.CurrentRegistrySchemaVersion})
		})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := extensions.NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*extensions.Runner, error) {
		return extensions.NewRunner(mockContext.CommandRunner), nil
	})

	// Seed an installed extension whose source is not backed by any configured
	// registry (as a bundle-installed extension would be).
	cfg, err := userConfigManager.Load()
	require.NoError(t, err)
	require.NoError(t, cfg.Set("extension.installed", map[string]*extensions.Extension{
		"test.bundle.ext": {
			Id:          "test.bundle.ext",
			DisplayName: "Bundled Extension",
			Version:     "1.0.0",
			Source:      extensions.BundleSourceName,
		},
	}))
	require.NoError(t, userConfigManager.Save(cfg))

	manager, err := extensions.NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)

	var buf bytes.Buffer
	action := &extensionListAction{
		flags:            &extensionListFlags{},
		formatter:        &output.JsonFormatter{},
		console:          mockinput.NewMockConsole(),
		writer:           &buf,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	_, err = action.Run(context.Background())
	require.NoError(t, err)

	var rows []extensionListItem
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rows))

	var found *extensionListItem
	for i := range rows {
		if rows[i].Id == "test.bundle.ext" {
			found = &rows[i]
			break
		}
	}
	require.NotNil(t, found, "bundle-installed extension should be surfaced in list output")
	require.Equal(t, extensions.BundleSourceName, found.Source)
	require.Equal(t, "1.0.0", found.InstalledVersion)
}
