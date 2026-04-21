// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
) *extensions.Manager {
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

	manager, err := extensions.NewManager(
		userConfigManager, sourceManager, lazyRunner, mockCtx.HttpClient,
	)
	require.NoError(t, err)

	return manager
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

	manager := createUpgradeTestManager(
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
		manager,
	)

	result, err := action.Run(ctx)
	// All extensions should be marked as failed
	require.Error(t, err)
	require.NotNil(t, result)
	assert.Contains(t, err.Error(), "extensions failed to upgrade")

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
		name           string
		extensionId    string
		installed      map[string]*extensions.Extension
		registry       extensions.Registry
		flags          extensionUpgradeFlags
		wantStatus     extensions.UpgradeStatus
		wantErrSubstr  string
		wantSkipReason string
	}{
		{
			name:        "skip_already_up_to_date",
			extensionId: "ext-a",
			installed: map[string]*extensions.Extension{
				"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "test"},
			},
			registry: testRegistry(
				testExtMeta("ext-a", "1.0.0", "test"),
			),
			flags: extensionUpgradeFlags{
				global: &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus:     extensions.UpgradeStatusSkipped,
			wantSkipReason: "already up to date",
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
			name:        "failed_not_found_in_registry",
			extensionId: "missing-ext",
			installed: map[string]*extensions.Extension{
				"missing-ext": {Id: "missing-ext", Version: "1.0.0", Source: "test"},
			},
			registry: testRegistry(), // empty registry
			flags: extensionUpgradeFlags{
				global: &internal.GlobalCommandOptions{NoPrompt: true},
			},
			wantStatus:    extensions.UpgradeStatusFailed,
			wantErrSubstr: "not found in any configured registry",
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
			manager := createUpgradeTestManager(
				t, mockCtx, tt.installed, registryURL, tt.registry,
			)

			action := &extensionUpgradeAction{
				args:             []string{tt.extensionId},
				flags:            &tt.flags,
				formatter:        &output.JsonFormatter{},
				writer:           &bytes.Buffer{},
				console:          mockinput.NewMockConsole(),
				extensionManager: manager,
			}

			// Use JSON output to avoid spinner/console issues
			result := action.upgradeOneExtension(
				t.Context(), tt.extensionId, 0, nil, true,
			)

			assert.Equal(t, tt.wantStatus, result.Status)
			assert.Equal(t, tt.extensionId, result.ExtensionId)

			if tt.wantErrSubstr != "" {
				require.NotNil(t, result.Error)
				assert.Contains(
					t, result.Error.Error(), tt.wantErrSubstr,
				)
			}

			if tt.wantSkipReason != "" {
				assert.Equal(t, tt.wantSkipReason, result.SkipReason)
			}
		})
	}
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

	manager := createUpgradeTestManager(
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
		manager,
	)

	result, err := action.Run(t.Context())
	// At least one failure ("missing") so we expect an error
	require.Error(t, err)
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
	// "missing" should fail, "newer" and "up-to-date" should skip
	assert.Equal(t, 2, report.Summary.Skipped)
	assert.Equal(t, 1, report.Summary.Failed)
	assert.Equal(t, 0, report.Summary.Upgraded)

	// Verify each extension result
	resultMap := make(map[string]string)
	for _, ext := range report.Extensions {
		resultMap[ext.Name] = ext.Status
	}

	assert.Equal(t, "skipped", resultMap["up-to-date"])
	assert.Equal(t, "skipped", resultMap["newer"])
	assert.Equal(t, "failed", resultMap["missing"])
}
