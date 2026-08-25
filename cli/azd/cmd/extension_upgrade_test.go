// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// testRegistry builds a minimal Registry containing the given extensions.
func testRegistry(exts ...*extensions.ExtensionMetadata) extensions.Registry {
	return extensions.Registry{Extensions: exts}
}

// testExtMeta creates a minimal ExtensionMetadata with one version.
func testExtMeta(id, version, source string) *extensions.ExtensionMetadata {
	return &extensions.ExtensionMetadata{
		Id:     id,
		Source: source,
		Versions: []extensions.ExtensionVersion{
			{Version: version},
		},
	}
}

func TestUpgradeRetryCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		version string
		want    string
	}{
		{
			name: "extension only",
			want: "azd extension update ext-a",
		},
		{
			name:   "source",
			source: "test",
			want:   "azd extension update ext-a --source test",
		},
		{
			name:    "version",
			version: "3.0.0",
			want:    "azd extension update ext-a --version 3.0.0",
		},
		{
			name:    "source and version",
			source:  "test",
			version: "3.0.0",
			want:    "azd extension update ext-a --source test --version 3.0.0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, upgradeRetryCommand("ext-a", test.source, test.version))
		})
	}
}

func TestUpgradeResolutionErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "explicit source",
			err:  upgradeSourceResolutionError("ext-a", "test", "azd"),
			want: "extension 'ext-a' not found in source 'test'",
		},
		{
			name: "installed source",
			err:  upgradeSourceResolutionError("ext-a", "", "test"),
			want: "extension 'ext-a' not available in source 'test' or the main registry",
		},
		{
			name: "main installed source",
			err:  upgradeSourceResolutionError("ext-a", "", "AZD"),
			want: "extension 'ext-a' not available in the main registry",
		},
		{
			name: "missing installed source defaults to main",
			err:  upgradeSourceResolutionError("ext-a", "", ""),
			want: "extension 'ext-a' not available in the main registry",
		},
		{
			name: "version in named source",
			err:  upgradeVersionResolutionError("ext-a", "3.0.0", "test"),
			want: "extension 'ext-a' version '3.0.0' not available in source 'test'",
		},
		{
			name: "version in main source",
			err:  upgradeVersionResolutionError("ext-a", "3.0.0", "AZD"),
			want: "extension 'ext-a' version '3.0.0' not available in the main registry",
		},
		{
			name: "version with missing source defaults to main",
			err:  upgradeVersionResolutionError("ext-a", "3.0.0", ""),
			want: "extension 'ext-a' version '3.0.0' not available in the main registry",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.EqualError(t, test.err, test.want)
		})
	}
}

func TestUpgradeFailureDetails(t *testing.T) {
	t.Parallel()

	dependencyErr := fmt.Errorf("failed to upgrade extension: %w", &extensions.DependencyVersionNotFoundError{
		DependencyId: "azure.ai.inspector",
		ParentId:     "azure.ai.agents",
		Constraint:   ">=2.0.0",
	})

	suggestion, err := upgradeFailureDetails(dependencyErr)

	require.ErrorAs(t, err, new(*extensions.DependencyVersionNotFoundError))
	require.Contains(t, suggestion, "azure.ai.inspector")
	require.Contains(t, suggestion, ">=2.0.0")
	require.Contains(t, suggestion, "azure.ai.agents")
}

func TestWrapErrorWithSuggestionUsesTypedSuggestion(t *testing.T) {
	t.Parallel()

	dependencyErr := &extensions.DependencyNotFoundError{
		DependencyId: "azure.ai.inspector",
		ParentId:     "azure.ai.agents",
	}

	wrapped := internal.WrapErrorWithSuggestion(fmt.Errorf("install failed: %w", dependencyErr))

	suggestionErr, ok := errors.AsType[*internal.ErrorWithSuggestion](wrapped)
	require.True(t, ok)
	require.Equal(t, dependencyErr.Suggestion(), suggestionErr.Suggestion)
	require.ErrorIs(t, suggestionErr.Err, dependencyErr)
}

func TestUpgradeOneExtension_InteractiveFailurePreservesRetryFlags(t *testing.T) {
	t.Parallel()

	const registryURL = "https://test.example.com/registry.json"

	mockCtx := mocks.NewMockContext(t.Context())
	manager, sourceManager := createUpgradeTestManager(
		t,
		mockCtx,
		map[string]*extensions.Extension{
			"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "test"},
		},
		registryURL,
		testRegistry(testExtMeta("ext-a", "2.0.0", "test")),
	)

	console := mockinput.NewMockConsole()
	action := &extensionUpgradeAction{
		args: []string{"ext-a"},
		flags: &extensionUpgradeFlags{
			source:  "test",
			version: "3.0.0",
			global:  &internal.GlobalCommandOptions{NoPrompt: true},
		},
		formatter:        &output.NoneFormatter{},
		writer:           &bytes.Buffer{},
		console:          console,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	result := action.upgradeOneExtension(t.Context(), "ext-a", 0, false)

	require.Equal(t, extensions.UpgradeStatusFailed, result.Status)
	require.EqualError(t, result.Error, "extension 'ext-a' version '3.0.0' not available in source 'test'")
	require.Contains(t, result.Suggestion, "azd extension update ext-a --source test --version 2.0.0")

	rendered := strings.Join(console.Output(), "\n")
	require.Contains(t, rendered, result.Error.Error())
	require.Contains(t, rendered, "azd extension update ext-a --source test --version 2.0.0")
	require.NotContains(t, rendered, "Retry with:")
}

func TestDisplayDependencyUpgradeResultsFailedSuggestion(t *testing.T) {
	t.Parallel()

	console := mockinput.NewMockConsole()
	displayDependencyUpgradeResults(
		t.Context(),
		console,
		[]extensions.UpgradeResult{{
			ExtensionId: "azure.ai.inspector",
			Status:      extensions.UpgradeStatusFailed,
			Error:       errors.New("dependency version not found"),
			Suggestion:  "Install or publish a compatible version, then retry.",
		}},
		"  ",
	)

	rendered := strings.Join(console.Output(), "\n")
	require.Contains(t, rendered, "dependency version not found")
	require.Contains(t, rendered, "Install or publish a compatible version, then retry.")
	require.Contains(
		t,
		console.Output(),
		"  "+strings.Repeat(" ", len("(x) Failed: "))+
			"Install or publish a compatible version, then retry.",
	)
}

func TestDisplayDependencyUpgradeResultsChangesAndSkips(t *testing.T) {
	t.Parallel()

	console := mockinput.NewMockConsole()
	displayDependencyUpgradeResults(
		t.Context(),
		console,
		[]extensions.UpgradeResult{
			{
				ExtensionId: "downgraded",
				Status:      extensions.UpgradeStatusUpgraded,
				FromVersion: "2.0.0",
				ToVersion:   "1.0.0",
			},
			{
				ExtensionId: "non-semver",
				Status:      extensions.UpgradeStatusUpgraded,
				FromVersion: "nightly",
				ToVersion:   "dev",
			},
			{
				ExtensionId: "skipped",
				Status:      extensions.UpgradeStatusSkipped,
				SkipReason:  "dependency updates disabled",
				Suggestion:  "Retry without --no-dependency-updates.",
			},
		},
		"  ",
	)

	rendered := strings.Join(console.Output(), "\n")
	require.Contains(t, rendered, "Downgraded downgraded dependency")
	require.Contains(t, rendered, "Updated non-semver dependency")
	require.Contains(t, rendered, "dependency updates disabled")
	require.Contains(t, rendered, "Retry without --no-dependency-updates.")
	require.Contains(
		t,
		console.Output(),
		"  "+strings.Repeat(" ", len("(-) Skipped: "))+
			"Retry without --no-dependency-updates.",
	)
}

func TestDependencyChangeVerb(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fromVersion string
		toVersion   string
		want        string
	}{
		{name: "update", fromVersion: "1.0.0", toVersion: "2.0.0", want: "Updated"},
		{name: "downgrade", fromVersion: "2.0.0", toVersion: "1.0.0", want: "Downgraded"},
		{name: "non-semver", fromVersion: "nightly", toVersion: "dev", want: "Updated"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, dependencyChangeVerb(test.fromVersion, test.toVersion))
		})
	}
}

// createUpgradeTestManager builds a real extensions.Manager backed by an
// in-memory config with the given installed extensions. The mock HTTP
// client serves the registry JSON from registryURL. This follows the
// pattern used in middleware tests.
func createUpgradeTestManager(
	t *testing.T,
	mockCtx *mocks.MockContext,
	installed map[string]*extensions.Extension,
	registryURL string,
	registry extensions.Registry,
) (*extensions.Manager, *extensions.SourceManager) {
	return createUpgradeTestManagerWithOptions(
		t,
		mockCtx,
		installed,
		registryURL,
		registry,
		extensions.ManagerOptions{},
	)
}

func createUpgradeTestManagerWithOptions(
	t *testing.T,
	mockCtx *mocks.MockContext,
	installed map[string]*extensions.Extension,
	registryURL string,
	registry extensions.Registry,
	managerOptions extensions.ManagerOptions,
) (*extensions.Manager, *extensions.SourceManager) {
	t.Helper()

	userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)
	sourceManager := extensions.NewSourceManager(
		mockCtx.Container, userConfigManager, mockCtx.HttpClient,
	)
	lazyRunner := lazy.NewLazy(func() (*extensions.Runner, error) {
		return extensions.NewRunner(exec.NewCommandRunner(nil)), nil
	})

	// Configure source in user config
	cfg, err := userConfigManager.Load()
	require.NoError(t, err)

	err = cfg.Set("extension.sources.test", map[string]any{
		"name":     "test",
		"type":     "url",
		"location": registryURL,
	})
	require.NoError(t, err)

	if installed != nil {
		err = cfg.Set("extension.installed", installed)
		require.NoError(t, err)
	}

	// Mock registry HTTP
	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == registryURL
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(
			request, http.StatusOK, registry,
		)
	})

	manager, err := extensions.NewManagerWithOptions(
		userConfigManager, sourceManager, lazyRunner, mockCtx.HttpClient, managerOptions,
	)
	require.NoError(t, err)

	return manager, sourceManager
}

// ---------------------------------------------------------------------------
// Context cancellation test — verifies Fix 1
// ---------------------------------------------------------------------------

func TestUpgradeAction_ContextCancellation(t *testing.T) {
	t.Parallel()

	const registryURL = "https://test.example.com/registry.json"

	mockCtx := mocks.NewMockContext(context.Background())

	installed := map[string]*extensions.Extension{
		"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "test"},
		"ext-b": {Id: "ext-b", Version: "1.0.0", Source: "test"},
		"ext-c": {Id: "ext-c", Version: "1.0.0", Source: "test"},
	}

	registry := testRegistry(
		testExtMeta("ext-a", "2.0.0", "test"),
		testExtMeta("ext-b", "2.0.0", "test"),
		testExtMeta("ext-c", "2.0.0", "test"),
	)

	manager, sourceManager := createUpgradeTestManager(
		t, mockCtx, installed, registryURL, registry,
	)

	// Cancel context before Run()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var buf bytes.Buffer
	action := newExtensionUpgradeAction(
		nil,
		&extensionUpgradeFlags{
			all:    true,
			global: &internal.GlobalCommandOptions{NoPrompt: true},
		},
		&output.JsonFormatter{},
		&buf,
		mockinput.NewMockConsole(),
		sourceManager,
		manager,
	)

	result, err := action.Run(ctx)
	// All extensions should be marked as failed
	require.Error(t, err)
	require.NotNil(t, result)
	assert.Contains(t, err.Error(), "extensions failed to update")

	// Parse the JSON output to verify all have failed status
	var report struct {
		Extensions []map[string]any `json:"extensions"`
		Summary    struct {
			Total  int `json:"total"`
			Failed int `json:"failed"`
		} `json:"summary"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))

	assert.Equal(t, 3, report.Summary.Total)
	assert.Equal(t, 3, report.Summary.Failed)

	for _, ext := range report.Extensions {
		assert.Equal(t, "failed", ext["status"])
		errMsg, _ := ext["error"].(string)
		assert.Contains(t, errMsg, "context canceled")
	}
}

// ---------------------------------------------------------------------------
// upgradeOneExtension table-driven tests — verifies Fix 2
// ---------------------------------------------------------------------------

func TestUpgradeOneExtension(t *testing.T) {
	t.Parallel()

	const registryURL = "https://test.example.com/registry.json"

	tests := []struct {
		name                   string
		extensionId            string
		installed              map[string]*extensions.Extension
		registry               extensions.Registry
		flags                  extensionUpgradeFlags
		wantStatus             extensions.UpgradeStatus
		wantErr                string
		wantErrSubstr          string
		wantSkipReason         string
		wantFromSourceCategory extensions.SourceCategory
		wantToSourceCategory   extensions.SourceCategory
	}{
		{
			name:        "skip_already_up_to_date",
			extensionId: "ext-a",
			installed: map[string]*extensions.Extension{
				"ext-a": {
					Id:             "ext-a",
					Version:        "1.0.0",
					Source:         "test",
					SourceCategory: extensions.SourceCategoryLocal,
				},
			},
			registry: testRegistry(
				testExtMeta("ext-a", "1.0.0", "test"),
			),
			flags: extensionUpgradeFlags{
				global: &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus:             extensions.UpgradeStatusSkipped,
			wantSkipReason:         "already up to date",
			wantFromSourceCategory: extensions.SourceCategoryLocal,
			wantToSourceCategory:   extensions.SourceCategoryOther,
		},
		{
			name:        "skip_installed_is_newer",
			extensionId: "ext-a",
			installed: map[string]*extensions.Extension{
				"ext-a": {Id: "ext-a", Version: "3.0.0", Source: "test"},
			},
			registry: testRegistry(
				testExtMeta("ext-a", "2.0.0", "test"),
			),
			flags: extensionUpgradeFlags{
				global: &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus:     extensions.UpgradeStatusSkipped,
			wantSkipReason: "installed 3.0.0 is newer than 2.0.0",
		},
		{
			name:        "skipped_delisted_extension",
			extensionId: "missing-ext",
			installed: map[string]*extensions.Extension{
				"missing-ext": {
					Id:             "missing-ext",
					Version:        "1.0.0",
					Source:         "test",
					SourceCategory: extensions.SourceCategoryDev,
				},
			},
			registry: testRegistry(), // empty registry
			flags: extensionUpgradeFlags{
				global: &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus:             extensions.UpgradeStatusSkipped,
			wantSkipReason:         "extension no longer available in any configured registry",
			wantFromSourceCategory: extensions.SourceCategoryDev,
			wantToSourceCategory:   extensions.SourceCategoryDev,
		},
		{
			name:        "failed_no_stored_or_main_source_match",
			extensionId: "ext-a",
			installed: map[string]*extensions.Extension{
				"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "removed-registry"},
			},
			registry: testRegistry(
				testExtMeta("ext-a", "2.0.0", "test"),
			),
			flags: extensionUpgradeFlags{
				all:    true,
				global: &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus: extensions.UpgradeStatusFailed,
			wantErr:    "extension 'ext-a' not available in source 'removed-registry' or the main registry",
		},
		{
			name:        "failed_main_source_only_match_elsewhere",
			extensionId: "ext-a",
			installed: map[string]*extensions.Extension{
				"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "azd"},
			},
			registry: testRegistry(
				testExtMeta("ext-a", "2.0.0", "test"),
			),
			flags: extensionUpgradeFlags{
				all:    true,
				global: &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus: extensions.UpgradeStatusFailed,
			wantErr:    "extension 'ext-a' not available in the main registry",
		},
		{
			name:        "failed_explicit_source_not_found",
			extensionId: "ext-a",
			installed: map[string]*extensions.Extension{
				"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "test"},
			},
			registry: testRegistry(
				testExtMeta("ext-a", "2.0.0", "test"),
			),
			flags: extensionUpgradeFlags{
				source: "missing-source",
				global: &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus: extensions.UpgradeStatusFailed,
			wantErr:    "extension 'ext-a' not found in source 'missing-source'",
		},
		{
			name:        "skip_batch_extension_not_in_explicit_source",
			extensionId: "ext-a",
			installed: map[string]*extensions.Extension{
				"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "test"},
			},
			registry: testRegistry(
				testExtMeta("other-ext", "2.0.0", "test"),
			),
			flags: extensionUpgradeFlags{
				all:    true,
				source: "test",
				global: &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus:     extensions.UpgradeStatusSkipped,
			wantSkipReason: "extension not available in source 'test'",
		},
		{
			name:        "failed_explicit_source_version_not_found",
			extensionId: "ext-a",
			installed: map[string]*extensions.Extension{
				"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "test"},
			},
			registry: testRegistry(
				testExtMeta("ext-a", "2.0.0", "test"),
			),
			flags: extensionUpgradeFlags{
				source:  "test",
				version: "3.0.0",
				global:  &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus: extensions.UpgradeStatusFailed,
			wantErr:    "extension 'ext-a' version '3.0.0' not available in source 'test'",
		},
		{
			name:        "failed_stored_source_version_not_found",
			extensionId: "ext-a",
			installed: map[string]*extensions.Extension{
				"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "test"},
			},
			registry: testRegistry(
				testExtMeta("ext-a", "2.0.0", "test"),
			),
			flags: extensionUpgradeFlags{
				version: "3.0.0",
				global:  &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus: extensions.UpgradeStatusFailed,
			wantErr:    "extension 'ext-a' version '3.0.0' not available in source 'test'",
		},
		{
			name:        "failed_version_and_source_not_found",
			extensionId: "ext-a",
			installed: map[string]*extensions.Extension{
				"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "removed-registry"},
			},
			registry: testRegistry(
				testExtMeta("ext-a", "2.0.0", "test"),
			),
			flags: extensionUpgradeFlags{
				version: "3.0.0",
				global:  &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus: extensions.UpgradeStatusFailed,
			wantErr:    "extension 'ext-a' not available in source 'removed-registry' or the main registry",
		},
		{
			name:        "failed_not_installed",
			extensionId: "not-installed",
			installed:   map[string]*extensions.Extension{},
			registry: testRegistry(
				testExtMeta("not-installed", "1.0.0", "test"),
			),
			flags: extensionUpgradeFlags{
				global: &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus:    extensions.UpgradeStatusFailed,
			wantErrSubstr: "failed to get installed extension",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockCtx := mocks.NewMockContext(context.Background())
			manager, sourceManager := createUpgradeTestManager(
				t, mockCtx, tt.installed, registryURL, tt.registry,
			)

			action := &extensionUpgradeAction{
				args:             []string{tt.extensionId},
				flags:            &tt.flags,
				formatter:        &output.JsonFormatter{},
				writer:           &bytes.Buffer{},
				console:          mockinput.NewMockConsole(),
				sourceManager:    sourceManager,
				extensionManager: manager,
			}

			// Use JSON output to avoid spinner/console issues
			result := action.upgradeOneExtension(
				t.Context(), tt.extensionId, 0, true)

			assert.Equal(t, tt.wantStatus, result.Status)
			assert.Equal(t, tt.extensionId, result.ExtensionId)

			if tt.wantErr != "" {
				require.EqualError(t, result.Error, tt.wantErr)
			}

			if tt.wantErrSubstr != "" {
				require.NotNil(t, result.Error)
				assert.Contains(
					t, result.Error.Error(), tt.wantErrSubstr,
				)
			}

			if tt.wantSkipReason != "" {
				assert.Equal(t, tt.wantSkipReason, result.SkipReason)
			}
			if tt.wantFromSourceCategory != "" {
				assert.Equal(t, tt.wantFromSourceCategory, result.FromSourceCategory)
			}
			if tt.wantToSourceCategory != "" {
				assert.Equal(t, tt.wantToSourceCategory, result.ToSourceCategory)
			}
		})
	}
}

func TestExtensionLifecycleTelemetrySpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previousProvider) })

	t.Run("UnresolvedUpgradeUsesPersistedCategory", func(t *testing.T) {
		const registryURL = "https://private.example/registry.json"
		const sourceName = "private-source"

		mockContext := mocks.NewMockContext(t.Context())
		manager, sourceManager := createUpgradeTestManager(
			t,
			mockContext,
			map[string]*extensions.Extension{
				"missing-ext": {
					Id:             "missing-ext",
					Version:        "1.0.0",
					Source:         sourceName,
					SourceCategory: extensions.SourceCategoryDev,
				},
			},
			registryURL,
			testRegistry(),
		)
		action := &extensionUpgradeAction{
			args: []string{"missing-ext"},
			flags: &extensionUpgradeFlags{
				global: &internal.GlobalCommandOptions{NoPrompt: true},
			},
			formatter:        &output.JsonFormatter{},
			writer:           &bytes.Buffer{},
			console:          mockinput.NewMockConsole(),
			sourceManager:    sourceManager,
			extensionManager: manager,
		}

		result := action.upgradeOneExtension(t.Context(), "missing-ext", 0, true)
		require.Equal(t, extensions.UpgradeStatusSkipped, result.Status)

		span := extensionEndedSpan(t, recorder, events.ExtensionUpdateEvent)
		require.Equal(
			t,
			string(extensions.SourceCategoryDev),
			extensionSpanAttribute(t, span.Attributes(), fields.ExtensionSourceCategory.Key).Value.AsString(),
		)
		for _, attr := range span.Attributes() {
			require.NotContains(t, attr.Value.Emit(), sourceName)
			require.NotContains(t, attr.Value.Emit(), registryURL)
		}
	})

	t.Run("PromotionUsesFixedCategories", func(t *testing.T) {
		emitPromotionEvent(
			t.Context(),
			"test.extension",
			"1.0.0",
			"1.1.0",
			extensions.SourceCategoryDev,
			extensions.SourceCategoryAzd,
		)

		span := extensionEndedSpan(t, recorder, events.ExtensionPromoteEvent)
		require.Equal(
			t,
			string(extensions.SourceCategoryDev),
			extensionSpanAttribute(t, span.Attributes(), fields.ExtensionSourceCategoryFrom.Key).Value.AsString(),
		)
		require.Equal(
			t,
			string(extensions.SourceCategoryAzd),
			extensionSpanAttribute(t, span.Attributes(), fields.ExtensionSourceCategoryTo.Key).Value.AsString(),
		)
	})
}

func TestDisplayPromotionWarning(t *testing.T) {
	t.Parallel()

	console := mockinput.NewMockConsole()
	action := &extensionUpgradeAction{console: console}
	action.displayPromotionWarning(
		t.Context(),
		"Updating test.extension",
		"test.extension",
		"1.0.0",
		"1.1.0",
		"dev",
		"azd",
	)

	require.Len(t, console.SpinnerOps(), 1)
	require.Equal(t, input.StepWarning, console.SpinnerOps()[0].Format)
	rendered := strings.Join(console.Output(), "\n")
	require.Contains(t, rendered, "Updated test.extension")
	require.Contains(t, rendered, "1.0.0")
	require.Contains(t, rendered, "1.1.0")
	require.Contains(t, rendered, "promoted from the dev registry")
	require.Contains(t, rendered, "official azd registry")
	require.Contains(t, rendered, "azd extension install test.extension --source dev")
}

func extensionEndedSpan(
	t *testing.T,
	recorder *tracetest.SpanRecorder,
	name string,
) tracesdk.ReadOnlySpan {
	t.Helper()
	for _, span := range recorder.Ended() {
		if span.Name() == name {
			return span
		}
	}
	require.FailNow(t, "telemetry span not found", "name: %s", name)
	return nil
}

func extensionSpanAttribute(
	t *testing.T,
	attributes []attribute.KeyValue,
	key attribute.Key,
) attribute.KeyValue {
	t.Helper()
	for _, attr := range attributes {
		if attr.Key == key {
			return attr
		}
	}
	require.FailNow(t, "telemetry attribute not found", "key: %s", key)
	return attribute.KeyValue{}
}

// TestUpgradeAction_MixedBatch tests a batch with some skip, some fail.
func TestUpgradeAction_MixedBatch(t *testing.T) {
	t.Parallel()

	const registryURL = "https://test.example.com/registry.json"

	mockCtx := mocks.NewMockContext(context.Background())

	installed := map[string]*extensions.Extension{
		"up-to-date": {Id: "up-to-date", Version: "1.0.0", Source: "test"},
		"newer":      {Id: "newer", Version: "5.0.0", Source: "test"},
		"missing":    {Id: "missing", Version: "1.0.0", Source: "test"},
	}

	registry := testRegistry(
		testExtMeta("up-to-date", "1.0.0", "test"),
		testExtMeta("newer", "2.0.0", "test"),
		// "missing" not in registry
	)

	manager, sourceManager := createUpgradeTestManager(
		t, mockCtx, installed, registryURL, registry,
	)

	var buf bytes.Buffer
	action := newExtensionUpgradeAction(
		nil,
		&extensionUpgradeFlags{
			all:    true,
			global: &internal.GlobalCommandOptions{NoPrompt: true},
		},
		&output.JsonFormatter{},
		&buf,
		mockinput.NewMockConsole(),
		sourceManager,
		manager,
	)

	result, err := action.Run(t.Context())
	// All extensions are skipped (no failures), so no error
	require.NoError(t, err)
	require.NotNil(t, result)

	var report struct {
		Extensions []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			SkipReason string `json:"skipReason,omitempty"`
			Error      string `json:"error,omitempty"`
		} `json:"extensions"`
		Summary struct {
			Total    int `json:"total"`
			Upgraded int `json:"upgraded"`
			Skipped  int `json:"skipped"`
			Promoted int `json:"promoted"`
			Failed   int `json:"failed"`
		} `json:"summary"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))

	assert.Equal(t, 3, report.Summary.Total)
	// "missing" is now skipped (delisted), "newer" and "up-to-date" also skip
	assert.Equal(t, 3, report.Summary.Skipped)
	assert.Equal(t, 0, report.Summary.Failed)
	assert.Equal(t, 0, report.Summary.Upgraded)

	// Verify each extension result
	resultMap := make(map[string]string)
	for _, ext := range report.Extensions {
		resultMap[ext.Name] = ext.Status
	}

	assert.Equal(t, "skipped", resultMap["up-to-date"])
	assert.Equal(t, "skipped", resultMap["newer"])
	assert.Equal(t, "skipped", resultMap["missing"])
}

func TestUpgradeAction_AllWithSourceSkipsExtensionsOutsideSource(t *testing.T) {
	t.Parallel()

	const registryURL = "https://test.example.com/registry.json"

	mockCtx := mocks.NewMockContext(t.Context())
	manager, sourceManager := createUpgradeTestManager(
		t,
		mockCtx,
		map[string]*extensions.Extension{
			"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "test"},
		},
		registryURL,
		testRegistry(testExtMeta("other-ext", "2.0.0", "test")),
	)

	var buf bytes.Buffer
	action := newExtensionUpgradeAction(
		nil,
		&extensionUpgradeFlags{
			all:    true,
			source: "test",
			global: &internal.GlobalCommandOptions{NoPrompt: true},
		},
		&output.JsonFormatter{},
		&buf,
		mockinput.NewMockConsole(),
		sourceManager,
		manager,
	)

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)

	var report struct {
		Extensions []struct {
			Status     string `json:"status"`
			SkipReason string `json:"skipReason"`
		} `json:"extensions"`
		Summary struct {
			Total   int `json:"total"`
			Skipped int `json:"skipped"`
			Failed  int `json:"failed"`
		} `json:"summary"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Equal(t, 1, report.Summary.Total)
	require.Equal(t, 1, report.Summary.Skipped)
	require.Zero(t, report.Summary.Failed)
	require.Equal(t, "skipped", report.Extensions[0].Status)
	require.Equal(t, "extension not available in source 'test'", report.Extensions[0].SkipReason)
}

// ---------------------------------------------------------------------------
// isNetworkError tests
// ---------------------------------------------------------------------------

func TestIsNetworkError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil_error",
			err:  nil,
			want: false,
		},
		{
			name: "regular_error",
			err:  fmt.Errorf("extension not found"),
			want: false,
		},
		{
			name: "dns_error",
			err: &net.DNSError{
				Err:  "no such host",
				Name: "registry.example.com",
			},
			want: true,
		},
		{
			name: "op_error",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: fmt.Errorf("connection refused"),
			},
			want: true,
		},
		{
			name: "wrapped_dns_error",
			err: fmt.Errorf(
				"failed to find extension: %w",
				&net.DNSError{
					Err:  "no such host",
					Name: "test.example.com",
				},
			),
			want: true,
		},
		{
			name: "connection_refused_message",
			err:  fmt.Errorf("dial tcp: connection refused"),
			want: true,
		},
		{
			name: "no_such_host_message",
			err:  fmt.Errorf("lookup test.example.com: no such host"),
			want: true,
		},
		{
			name: "io_timeout_message",
			err:  fmt.Errorf("read tcp: i/o timeout"),
			want: true,
		},
		{
			name: "tls_timeout_message",
			err:  fmt.Errorf("net/http: TLS handshake timeout"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isNetworkError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// Delisted extension edge case tests
// ---------------------------------------------------------------------------

func TestUpgradeOneExtension_DelistedSkipped(t *testing.T) {
	t.Parallel()

	const registryURL = "https://test.example.com/registry.json"

	mockCtx := mocks.NewMockContext(context.Background())

	installed := map[string]*extensions.Extension{
		"delisted-ext": {
			Id: "delisted-ext", Version: "1.0.0", Source: "test",
		},
	}

	// Empty registry — extension no longer listed
	registry := testRegistry()

	manager, sourceManager := createUpgradeTestManager(
		t, mockCtx, installed, registryURL, registry,
	)

	action := &extensionUpgradeAction{
		args: []string{"delisted-ext"},
		flags: &extensionUpgradeFlags{
			global: &internal.GlobalCommandOptions{NoPrompt: true},
		},
		formatter:        &output.JsonFormatter{},
		writer:           &bytes.Buffer{},
		console:          mockinput.NewMockConsole(),
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	result := action.upgradeOneExtension(
		t.Context(), "delisted-ext", 0, true)

	assert.Equal(t, extensions.UpgradeStatusSkipped, result.Status)
	assert.Contains(
		t, result.SkipReason,
		"no longer available",
	)
	assert.Nil(t, result.Error)
}

// ---------------------------------------------------------------------------
// Network failure edge case tests
// ---------------------------------------------------------------------------

// TestUpgradeOneExtension_NetworkFailure_SourceCreation verifies that when
// a network error prevents source creation, the extension is reported as
// skipped (delisted) because FindExtensions returns 0 matches. The source
// manager silently drops sources that fail to create.
func TestUpgradeOneExtension_NetworkFailure_SourceCreation(
	t *testing.T,
) {
	t.Parallel()

	const registryURL = "https://test.example.com/registry.json"

	mockCtx := mocks.NewMockContext(context.Background())

	installed := map[string]*extensions.Extension{
		"net-fail-ext": {
			Id: "net-fail-ext", Version: "1.0.0", Source: "test",
		},
	}

	userConfigManager := config.NewUserConfigManager(
		mockCtx.ConfigManager,
	)
	sourceManager := extensions.NewSourceManager(
		mockCtx.Container, userConfigManager, mockCtx.HttpClient,
	)
	lazyRunner := lazy.NewLazy(
		func() (*extensions.Runner, error) {
			return extensions.NewRunner(exec.NewCommandRunner(nil)), nil
		},
	)

	cfg, err := userConfigManager.Load()
	require.NoError(t, err)

	err = cfg.Set("extension.sources.test", map[string]any{
		"name":     "test",
		"type":     "url",
		"location": registryURL,
	})
	require.NoError(t, err)

	err = cfg.Set("extension.installed", installed)
	require.NoError(t, err)

	// Simulate network failure from HTTP client — source creation
	// will silently drop the source, yielding 0 matches.
	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == registryURL
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return nil, &net.DNSError{
			Err:  "no such host",
			Name: "test.example.com",
		}
	})

	manager, err := extensions.NewManager(
		userConfigManager, sourceManager,
		lazyRunner, mockCtx.HttpClient,
	)
	require.NoError(t, err)

	action := &extensionUpgradeAction{
		args: []string{"net-fail-ext"},
		flags: &extensionUpgradeFlags{
			global: &internal.GlobalCommandOptions{NoPrompt: true},
		},
		formatter:        &output.JsonFormatter{},
		writer:           &bytes.Buffer{},
		console:          mockinput.NewMockConsole(),
		extensionManager: manager,
	}

	result := action.upgradeOneExtension(
		t.Context(), "net-fail-ext", 0, true)

	// Source creation failure means 0 matches → skipped (delisted)
	assert.Equal(t, extensions.UpgradeStatusSkipped, result.Status)
	assert.Contains(
		t, result.SkipReason,
		"no longer available",
	)
}
