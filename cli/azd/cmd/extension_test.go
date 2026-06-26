// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func TestNamespacesConflictCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		ns1         string
		ns2         string
		conflict    bool
		reasonCheck string
	}{
		{
			name:        "same_namespace",
			ns1:         "demo",
			ns2:         "demo",
			conflict:    true,
			reasonCheck: "the same namespace",
		},
		{
			name:        "same_namespace_case_insensitive",
			ns1:         "Demo",
			ns2:         "demo",
			conflict:    true,
			reasonCheck: "the same namespace",
		},
		{
			name:        "ns1_prefix_of_ns2",
			ns1:         "demo",
			ns2:         "demo.commands",
			conflict:    true,
			reasonCheck: "overlapping",
		},
		{
			name:        "ns2_prefix_of_ns1",
			ns1:         "demo.commands.sub",
			ns2:         "demo",
			conflict:    true,
			reasonCheck: "overlapping",
		},
		{
			name:     "no_conflict",
			ns1:      "demo",
			ns2:      "other",
			conflict: false,
		},
		{
			name:     "partial_match_not_conflict",
			ns1:      "demo",
			ns2:      "demolition",
			conflict: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, reason := namespacesConflict(tt.ns1, tt.ns2)
			assert.Equal(t, tt.conflict, got)
			if tt.conflict {
				assert.Contains(t, reason, tt.reasonCheck)
			}
		})
	}
}

func TestExtensionShowItem_Display(t *testing.T) {
	t.Parallel()

	t.Run("basic_display", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		item := &extensionShowItem{
			Id:               "test.extension",
			Name:             "Test Extension",
			Namespace:        "test",
			Description:      "A test extension",
			LatestVersion:    "1.0.0",
			InstalledVersion: "0.9.0",
		}
		err := item.Display(&buf)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "test.extension")
		assert.Contains(t, output, "Test Extension")
		assert.Contains(t, output, "A test extension")
		assert.Contains(t, output, "1.0.0")
		assert.Contains(t, output, "0.9.0")
	})

	t.Run("with_examples", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		item := &extensionShowItem{
			Id:          "test.extension",
			Name:        "Test",
			Description: "desc",
			Examples: []extensions.ExtensionExample{
				{Name: "Example 1", Usage: "azd test run"},
			},
		}
		err := item.Display(&buf)
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "azd test run")
	})

	t.Run("with_tags", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		item := &extensionShowItem{
			Id:          "test.extension",
			Name:        "Test",
			Description: "desc",
			Tags:        []string{"ci", "testing"},
		}
		err := item.Display(&buf)
		require.NoError(t, err)
		assert.Contains(t, buf.String(), "ci")
	})
}

func TestIsJsonOutputFromArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		expected bool
	}{
		{name: "empty", args: []string{}, expected: false},
		{name: "no_output_flag", args: []string{"run"}, expected: false},
		{name: "output_json_space", args: []string{"--output", "json"}, expected: true},
		{name: "output_table", args: []string{"--output", "table"}, expected: false},
		{name: "short_output_json", args: []string{"-o", "json"}, expected: true},
		{name: "output_json_equals", args: []string{"--output=json"}, expected: true},
		{name: "short_output_json_equals", args: []string{"-o=json"}, expected: true},
		{name: "output_flag_at_end", args: []string{"run", "--output"}, expected: false},
		{name: "mixed_args", args: []string{"run", "--all", "--output", "json"}, expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isJsonOutputFromArgs(tt.args)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateVersionCompatibility(t *testing.T) {
	t.Parallel()

	azdVersion, err := semver.NewVersion("1.24.0")
	require.NoError(t, err)

	t.Run("compatible_version", func(t *testing.T) {
		t.Parallel()
		versions := []extensions.ExtensionVersion{
			{Version: "0.1.0", RequiredAzdVersion: ">= 1.23.0"},
		}
		err := validateVersionCompatibility(versions, "0.1.0", "test-ext", azdVersion)
		assert.NoError(t, err)
	})

	t.Run("incompatible_version", func(t *testing.T) {
		t.Parallel()
		versions := []extensions.ExtensionVersion{
			{Version: "0.1.0", RequiredAzdVersion: ">= 2.0.0"},
		}
		err := validateVersionCompatibility(versions, "0.1.0", "test-ext", azdVersion)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "incompatible")
	})

	t.Run("version_not_found", func(t *testing.T) {
		t.Parallel()
		versions := []extensions.ExtensionVersion{
			{Version: "0.1.0", RequiredAzdVersion: ">= 2.0.0"},
		}
		// non-matching version just returns nil
		err := validateVersionCompatibility(versions, "0.2.0", "test-ext", azdVersion)
		assert.NoError(t, err)
	})

	t.Run("no_constraint", func(t *testing.T) {
		t.Parallel()
		versions := []extensions.ExtensionVersion{
			{Version: "0.1.0"}, // no RequiredAzdVersion
		}
		err := validateVersionCompatibility(versions, "0.1.0", "test-ext", azdVersion)
		assert.NoError(t, err)
	})
}

func TestValidateExactVersionFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		wantErr bool
	}{
		{name: "empty", version: ""},
		{name: "latest", version: "latest"},
		{name: "semver", version: "1.2.3"},
		{name: "prerelease", version: "1.2.3-preview.1"},
		{name: "non_semver_version", version: "nightly"},
		{name: "range", version: ">=1.2.3", wantErr: true},
		{name: "tilde", version: "~1.2.0", wantErr: true},
		{name: "wildcard", version: "1.x", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateExactVersionFlag(tt.version)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestResolveCompatibleExtension_NilAzdVersion(t *testing.T) {
	t.Parallel()

	metadata := &extensions.ExtensionMetadata{
		Id: "test-ext",
		Versions: []extensions.ExtensionVersion{
			{Version: "0.1.0"},
		},
	}

	result, compat, err := resolveCompatibleExtension(metadata, "test-ext", "", nil)
	require.NoError(t, err)
	assert.Equal(t, metadata, result)
	assert.Nil(t, compat)
}

func TestResolveCompatibleExtension_SpecificVersion(t *testing.T) {
	t.Parallel()

	azdVersion, err := semver.NewVersion("1.24.0")
	require.NoError(t, err)

	metadata := &extensions.ExtensionMetadata{
		Id: "test-ext",
		Versions: []extensions.ExtensionVersion{
			{Version: "0.1.0", RequiredAzdVersion: ">= 1.23.0"},
		},
	}

	result, compat, err := resolveCompatibleExtension(metadata, "test-ext", "0.1.0", azdVersion)
	require.NoError(t, err)
	assert.Equal(t, metadata, result)
	assert.Nil(t, compat)
}

func TestResolveCompatibleExtension_FilterVersions(t *testing.T) {
	t.Parallel()

	azdVersion, err := semver.NewVersion("1.24.0")
	require.NoError(t, err)

	metadata := &extensions.ExtensionMetadata{
		Id: "test-ext",
		Versions: []extensions.ExtensionVersion{
			{Version: "0.1.0", RequiredAzdVersion: ">= 1.23.0"},
			{Version: "0.2.0", RequiredAzdVersion: ">= 2.0.0"}, // incompatible
		},
	}

	result, compat, err := resolveCompatibleExtension(metadata, "test-ext", "", azdVersion)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, compat)
	// Should have filtered out the incompatible version
	assert.Len(t, result.Versions, 1)
	assert.Equal(t, "0.1.0", result.Versions[0].Version)
}

func TestResolveCompatibleExtension_NoCompatible(t *testing.T) {
	t.Parallel()

	azdVersion, err := semver.NewVersion("1.0.0")
	require.NoError(t, err)

	metadata := &extensions.ExtensionMetadata{
		Id: "test-ext",
		Versions: []extensions.ExtensionVersion{
			{Version: "0.1.0", RequiredAzdVersion: ">= 2.0.0"},
		},
	}

	_, compat, err := resolveCompatibleExtension(metadata, "test-ext", "", azdVersion)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no compatible version")
	require.NotNil(t, compat)
}

func TestResolveCompatibleExtension_AllCompatible(t *testing.T) {
	t.Parallel()

	azdVersion, err := semver.NewVersion("1.24.0")
	require.NoError(t, err)

	metadata := &extensions.ExtensionMetadata{
		Id: "test-ext",
		Versions: []extensions.ExtensionVersion{
			{Version: "0.1.0", RequiredAzdVersion: ">= 1.0.0"},
			{Version: "0.2.0", RequiredAzdVersion: ">= 1.20.0"},
		},
	}

	result, compat, err := resolveCompatibleExtension(metadata, "test-ext", "", azdVersion)
	require.NoError(t, err)
	assert.Equal(t, metadata, result) // same pointer, no filtering needed
	require.NotNil(t, compat)
}

func TestResolveCompatibleExtension_LatestVersion(t *testing.T) {
	t.Parallel()

	azdVersion, err := semver.NewVersion("1.24.0")
	require.NoError(t, err)

	metadata := &extensions.ExtensionMetadata{
		Id: "test-ext",
		Versions: []extensions.ExtensionVersion{
			{Version: "0.1.0"},
		},
	}

	// "latest" should go through the filter path, not the specific version path
	result, _, err := resolveCompatibleExtension(metadata, "test-ext", "latest", azdVersion)
	require.NoError(t, err)
	assert.Equal(t, metadata, result)
}

func TestCurrentAzdSemver(t *testing.T) {
	t.Parallel()

	// In test/dev builds, IsDevVersion() is true, so currentAzdSemver returns nil
	result := currentAzdSemver()
	// We just verify it doesn't panic and returns a consistent result
	// In dev builds this will be nil; in release builds it would be a version
	_ = result
}

func TestDisplayValidationResult(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())

	t.Run("valid_extension", func(t *testing.T) {
		result := &extensions.RegistryValidationResult{
			Valid: true,
			Extensions: []extensions.ExtensionValidationResult{
				{
					Id:            "my.ext",
					Valid:         true,
					LatestVersion: "1.0.0",
				},
			},
		}

		// Should not panic
		displayValidationResult(mockContext.Console, t.Context(), result)
	})

	t.Run("invalid_extension_with_issues", func(t *testing.T) {
		result := &extensions.RegistryValidationResult{
			Valid: false,
			Extensions: []extensions.ExtensionValidationResult{
				{
					Id:    "bad.ext",
					Valid: false,
					Issues: []extensions.ValidationIssue{
						{
							Severity: extensions.ValidationError,
							Message:  "missing artifact",
						},
						{
							Severity: extensions.ValidationWarning,
							Message:  "no checksum",
						},
					},
				},
			},
		}

		displayValidationResult(mockContext.Console, t.Context(), result)
	})

	t.Run("unknown_id", func(t *testing.T) {
		result := &extensions.RegistryValidationResult{
			Valid: true,
			Extensions: []extensions.ExtensionValidationResult{
				{
					Id:    "",
					Valid: true,
				},
			},
		}

		displayValidationResult(mockContext.Console, t.Context(), result)
	})
}

func TestDisplayExtensionUsageAndExamples(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())

	version := &extensions.ExtensionVersion{
		Usage: "azd my-ext [options]",
		Examples: []extensions.ExtensionExample{
			{Name: "basic", Usage: "azd my-ext run"},
			{Name: "verbose", Usage: "azd my-ext run --verbose"},
		},
	}

	// Should not panic
	displayExtensionUsageAndExamples(t.Context(), mockContext.Console, version)
}

func TestDisplayVersionCompatibilityWarning(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	azdVersion, err := semver.NewVersion("1.24.0")
	require.NoError(t, err)

	latestOverall := &extensions.ExtensionVersion{
		Version:            "2.0.0",
		RequiredAzdVersion: ">= 2.0.0",
	}

	latestCompatible := &extensions.ExtensionVersion{
		Version:            "1.5.0",
		RequiredAzdVersion: ">= 1.23.0",
	}

	// Should not panic
	displayVersionCompatibilityWarning(
		t.Context(), mockContext.Console, latestOverall, latestCompatible, azdVersion)
}

func TestDisplayUpgradeSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		results  []extensions.UpgradeResult
		wantMsgs []string
	}{
		{
			name: "all_upgraded",
			results: []extensions.UpgradeResult{
				{Status: extensions.UpgradeStatusUpgraded},
				{Status: extensions.UpgradeStatusUpgraded},
			},
			wantMsgs: []string{
				"2 upgraded",
			},
		},
		{
			name: "mixed_results",
			results: []extensions.UpgradeResult{
				{Status: extensions.UpgradeStatusUpgraded},
				{Status: extensions.UpgradeStatusSkipped},
				{Status: extensions.UpgradeStatusPromoted},
				{Status: extensions.UpgradeStatusFailed},
			},
			wantMsgs: []string{
				"1 upgraded",
				"1 skipped",
				"1 promoted",
				"1 failed",
			},
		},
		{
			name: "failed_shows_retry_suggestion",
			results: []extensions.UpgradeResult{
				{Status: extensions.UpgradeStatusFailed},
			},
			wantMsgs: []string{
				"1 failed",
				"azd extension upgrade <name>",
			},
		},
		{
			name: "all_skipped_no_retry",
			results: []extensions.UpgradeResult{
				{Status: extensions.UpgradeStatusSkipped},
			},
			wantMsgs: []string{
				"1 skipped",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockCtx := mocks.NewMockContext(
				context.Background(),
			)
			displayUpgradeSummary(
				t.Context(), mockCtx.Console, tt.results,
			)
			output := mockCtx.Console.Output()
			var joined strings.Builder
			for _, line := range output {
				joined.WriteString(line + "\n")
			}
			for _, want := range tt.wantMsgs {
				assert.Contains(t, joined.String(), want)
			}
		})
	}
}

func TestDisplayUpgradeSummary_NoRetryWhenNoFailures(
	t *testing.T,
) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(context.Background())
	results := []extensions.UpgradeResult{
		{Status: extensions.UpgradeStatusUpgraded},
		{Status: extensions.UpgradeStatusSkipped},
	}
	displayUpgradeSummary(
		t.Context(), mockCtx.Console, results,
	)
	output := mockCtx.Console.Output()
	var joined strings.Builder
	for _, line := range output {
		joined.WriteString(line + "\n")
	}
	assert.NotContains(t, joined.String(), "Retry")
	assert.NotContains(t, joined.String(), "retry")
}

func TestUpgradeActionResult(t *testing.T) {
	t.Parallel()

	t.Run("all_success_returns_nil_error", func(t *testing.T) {
		t.Parallel()
		results := []extensions.UpgradeResult{
			{Status: extensions.UpgradeStatusUpgraded},
			{Status: extensions.UpgradeStatusSkipped},
			{Status: extensions.UpgradeStatusPromoted},
		}
		actionResult, err := upgradeActionResult(results)
		require.NoError(t, err)
		require.NotNil(t, actionResult)
		assert.Equal(
			t,
			"Extensions upgraded successfully",
			actionResult.Message.Header,
		)
	})

	t.Run(
		"partial_failure_returns_error",
		func(t *testing.T) {
			t.Parallel()
			results := []extensions.UpgradeResult{
				{Status: extensions.UpgradeStatusUpgraded},
				{Status: extensions.UpgradeStatusFailed},
				{Status: extensions.UpgradeStatusFailed},
			}
			actionResult, err := upgradeActionResult(results)
			require.Error(t, err)
			require.NotNil(t, actionResult)
			assert.Contains(
				t, err.Error(),
				"2 of 3 extensions failed to upgrade",
			)
			assert.Contains(
				t, actionResult.Message.Header,
				"2 of 3 extensions failed",
			)
		},
	)

	t.Run(
		"all_failed_returns_error",
		func(t *testing.T) {
			t.Parallel()
			results := []extensions.UpgradeResult{
				{Status: extensions.UpgradeStatusFailed},
			}
			actionResult, err := upgradeActionResult(results)
			require.Error(t, err)
			require.NotNil(t, actionResult)
			assert.Contains(
				t, err.Error(),
				"1 of 1 extensions failed",
			)
		},
	)
}

func TestUpgradeActionResult_EmptyResults(t *testing.T) {
	t.Parallel()
	actionResult, err := upgradeActionResult(nil)
	require.NoError(t, err)
	require.NotNil(t, actionResult)
	assert.Equal(
		t,
		"Extensions upgraded successfully",
		actionResult.Message.Header,
	)
}

func TestExtensionStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		installed  bool
		update     bool
		incompat   bool
		wantStatus string
	}{
		{"not installed", false, false, false, statusNotInstall},
		{"up to date", true, false, false, statusUpToDate},
		{"update available", true, true, false, statusUpdate},
		{"incompatible", true, false, true, statusIncompat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extensionStatus(tt.installed, tt.update, tt.incompat)
			assert.Equal(t, tt.wantStatus, got)
		})
	}
}

func TestExtensionStatusColor(t *testing.T) {
	// Force color output on — fatih/color disables in non-TTY environments.
	originalNoColor := color.NoColor
	color.NoColor = false
	defer func() { color.NoColor = originalNoColor }()

	// Verify no panics and non-empty colored output for each status value.
	for _, s := range []string{
		statusUpToDate, statusUpdate, statusIncompat, statusNotInstall,
	} {
		result := extensionStatusColor(s)
		assert.NotEmpty(t, result, "color function should return non-empty for %q", s)
		assert.Contains(t, result, "\x1b[", "expected ANSI color codes in output for %q", s)
	}
}

// --- currentAzdSemver Tests ---

func Test_CurrentAzdSemver_DevVersion(t *testing.T) {
	// Default dev build returns nil
	v := currentAzdSemver()
	assert.Nil(t, v, "dev build should return nil")
}

func Test_CurrentAzdSemver_ReleaseVersion(t *testing.T) {
	old := internal.Version
	internal.Version = "1.24.3 (commit aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa)"
	defer func() { internal.Version = old }()

	v := currentAzdSemver()
	require.NotNil(t, v)
	assert.Equal(t, uint64(1), v.Major())
	assert.Equal(t, uint64(24), v.Minor())
	assert.Equal(t, uint64(3), v.Patch())
	assert.Equal(t, "", v.Prerelease())
}

func Test_CurrentAzdSemver_PrereleaseStripped(t *testing.T) {
	old := internal.Version
	internal.Version = "1.25.0-beta.1-pr.12345 (commit bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb)"
	defer func() { internal.Version = old }()

	v := currentAzdSemver()
	require.NotNil(t, v)
	// Prerelease tag should be stripped
	assert.Equal(t, "", v.Prerelease())
	assert.Equal(t, uint64(1), v.Major())
	assert.Equal(t, uint64(25), v.Minor())
	assert.Equal(t, uint64(0), v.Patch())
}

// --- selectDistinctExtension Tests ---

func Test_SelectDistinctExtension_NoMatches(t *testing.T) {
	t.Parallel()
	_, err := selectDistinctExtension(
		t.Context(),
		mockinput.NewMockConsole(),
		"test-ext",
		[]*extensions.ExtensionMetadata{},
		&internal.GlobalCommandOptions{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no extensions found")
}

func Test_SelectDistinctExtension_SingleMatch(t *testing.T) {
	t.Parallel()
	meta := &extensions.ExtensionMetadata{Source: "registry"}
	result, err := selectDistinctExtension(
		t.Context(),
		mockinput.NewMockConsole(),
		"test-ext",
		[]*extensions.ExtensionMetadata{meta},
		&internal.GlobalCommandOptions{},
	)
	require.NoError(t, err)
	assert.Equal(t, meta, result)
}

func Test_SelectDistinctExtension_MultipleNoPrompt(t *testing.T) {
	t.Parallel()
	meta1 := &extensions.ExtensionMetadata{Source: "registry1"}
	meta2 := &extensions.ExtensionMetadata{Source: "registry2"}
	_, err := selectDistinctExtension(
		t.Context(),
		mockinput.NewMockConsole(),
		"test-ext",
		[]*extensions.ExtensionMetadata{meta1, meta2},
		&internal.GlobalCommandOptions{NoPrompt: true},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple sources")
}

func Test_DefaultExtensionSourceIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		matches []*extensions.ExtensionMetadata
		want    int
	}{
		{
			name: "DefaultsToAzdSourceWhenPresent",
			matches: []*extensions.ExtensionMetadata{
				{Source: "contoso"},
				{Source: "azd"},
			},
			want: 1,
		},
		{
			name: "DefaultsToFirstSourceWhenAzdMissing",
			matches: []*extensions.ExtensionMetadata{
				{Source: "contoso"},
				{Source: "fabrikam"},
			},
			want: 0,
		},
		{
			name: "MatchesAzdSourceCaseInsensitively",
			matches: []*extensions.ExtensionMetadata{
				{Source: "contoso"},
				{Source: "AZD"},
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, *defaultExtensionSourceIndex(tt.matches))
		})
	}
}

// --- namespacesConflict Tests (additional paths) ---

func Test_NamespacesConflict_SameNamespace(t *testing.T) {
	t.Parallel()
	conflict, _ := namespacesConflict("ai", "ai")
	assert.True(t, conflict)
}

func Test_NamespacesConflict_CaseInsensitive(t *testing.T) {
	t.Parallel()
	conflict, _ := namespacesConflict("AI", "ai")
	assert.True(t, conflict)
}

func Test_NamespacesConflict_PrefixConflict(t *testing.T) {
	t.Parallel()
	conflict, reason := namespacesConflict("ai", "ai.agent")
	assert.True(t, conflict)
	assert.Equal(t, "overlapping namespaces", reason)
}

func Test_NamespacesConflict_ReversePrefixConflict(t *testing.T) {
	t.Parallel()
	conflict, reason := namespacesConflict("ai.agent", "ai")
	assert.True(t, conflict)
	assert.Equal(t, "overlapping namespaces", reason)
}

func Test_NamespacesConflict_NoConflict(t *testing.T) {
	t.Parallel()
	conflict, reason := namespacesConflict("ai", "ml")
	assert.False(t, conflict)
	assert.Equal(t, "", reason)
}

// --- checkNamespaceConflict Tests (additional paths) ---

func Test_CheckNamespaceConflict_EmptyNamespace(t *testing.T) {
	t.Parallel()
	err := checkNamespaceConflict("new-ext", "", map[string]*extensions.Extension{})
	assert.NoError(t, err)
}

func Test_CheckNamespaceConflict_SkipsSelf(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"my-ext": {Namespace: "demo"},
	}
	// Same ID should be skipped (upgrade scenario)
	err := checkNamespaceConflict("my-ext", "demo", installed)
	assert.NoError(t, err)
}

func Test_CheckNamespaceConflict_SkipsEmptyInstalledNamespace(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"other-ext": {Namespace: ""},
	}
	err := checkNamespaceConflict("new-ext", "demo", installed)
	assert.NoError(t, err)
}

func Test_CheckNamespaceConflict_DetectsConflict(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"other-ext": {Namespace: "demo"},
	}
	err := checkNamespaceConflict("new-ext", "demo", installed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicts with installed extension")
}

func Test_NewExtensionListAction(t *testing.T) {
	t.Parallel()
	action := newExtensionListAction(
		&extensionListFlags{},
		&output.JsonFormatter{},
		mockinput.NewMockConsole(),
		&bytes.Buffer{},
		nil, // sourceManager
		nil, // extensionManager
	)
	require.NotNil(t, action)
}

func Test_NewExtensionShowAction(t *testing.T) {
	t.Parallel()
	action := newExtensionShowAction(
		[]string{"test-ext"},
		&extensionShowFlags{},
		mockinput.NewMockConsole(),
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // extensionManager
	)
	require.NotNil(t, action)
}

func Test_NewExtensionInstallAction(t *testing.T) {
	t.Parallel()
	action := newExtensionInstallAction(
		[]string{"test-ext"},
		&extensionInstallFlags{},
		mockinput.NewMockConsole(),
		nil, // extensionManager
		nil, // sourceManager
	)
	require.NotNil(t, action)
}

func Test_NewExtensionUninstallAction(t *testing.T) {
	t.Parallel()
	action := newExtensionUninstallAction(
		[]string{"test-ext"},
		&extensionUninstallFlags{},
		mockinput.NewMockConsole(),
		nil, // extensionManager
	)
	require.NotNil(t, action)
}

func Test_NewExtensionUpgradeAction(t *testing.T) {
	t.Parallel()
	action := newExtensionUpgradeAction(
		[]string{"test-ext"},
		&extensionUpgradeFlags{},
		&output.NoneFormatter{},
		&bytes.Buffer{},
		mockinput.NewMockConsole(),
		nil, // extensionManager
	)
	require.NotNil(t, action)
}

func Test_NewExtensionSourceListAction(t *testing.T) {
	t.Parallel()
	action := newExtensionSourceListAction(
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // sourceManager
	)
	require.NotNil(t, action)
}

func Test_NewExtensionSourceAddAction(t *testing.T) {
	t.Parallel()
	action := newExtensionSourceAddAction(
		&extensionSourceAddFlags{},
		mockinput.NewMockConsole(),
		nil, // sourceManager
		[]string{"my-source"},
	)
	require.NotNil(t, action)
}

func Test_NewExtensionSourceRemoveAction(t *testing.T) {
	t.Parallel()
	action := newExtensionSourceRemoveAction(
		nil, // sourceManager
		mockinput.NewMockConsole(),
		[]string{"my-source"},
	)
	require.NotNil(t, action)
}

func Test_NewExtensionSourceValidateAction(t *testing.T) {
	t.Parallel()
	action := newExtensionSourceValidateAction(
		[]string{"my-source"},
		&extensionSourceValidateFlags{},
		mockinput.NewMockConsole(),
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // sourceManager
	)
	require.NotNil(t, action)
}

func Test_NewAlphaFeatureManagerConfig(t *testing.T) {
	t.Parallel()
	cfg := config.NewEmptyConfig()
	fm := alpha.NewFeaturesManagerWithConfig(cfg)
	require.NotNil(t, fm)
}

func Test_ConsentTypes(t *testing.T) {
	t.Parallel()
	// Verify consent type constants
	require.Equal(t, consent.ActionType("readonly"), consent.ActionReadOnly)
	require.Equal(t, consent.ActionType("any"), consent.ActionAny)
	require.Equal(t, consent.OperationType("tool"), consent.OperationTypeTool)
	require.Equal(t, consent.OperationType("sampling"), consent.OperationTypeSampling)
	require.Equal(t, consent.OperationType("elicitation"), consent.OperationTypeElicitation)
	require.Equal(t, consent.Permission("allow"), consent.PermissionAllow)
	require.Equal(t, consent.Permission("deny"), consent.PermissionDeny)
	require.Equal(t, consent.Permission("prompt"), consent.PermissionPrompt)
	require.Equal(t, consent.Scope("global"), consent.ScopeGlobal)
}

func Test_ConsentTargets(t *testing.T) {
	t.Parallel()
	gt := consent.NewGlobalTarget()
	require.NotEmpty(t, gt)

	st := consent.NewServerTarget("my-server")
	require.NotEmpty(t, st)

	tt := consent.NewToolTarget("my-server", "my-tool")
	require.NotEmpty(t, tt)
}

func Test_ExtensionShowAction_Run_NoArgs(t *testing.T) {
	t.Parallel()
	action := &extensionShowAction{
		args:    []string{},
		flags:   &extensionShowFlags{global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrNoArgsProvided)
}

func Test_ExtensionShowAction_Run_TooManyArgs(t *testing.T) {
	t.Parallel()
	action := &extensionShowAction{
		args:    []string{"ext1", "ext2"},
		flags:   &extensionShowFlags{global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrInvalidFlagCombination)
}

func Test_ExtensionInstallAction_Run_NoArgs(t *testing.T) {
	t.Parallel()
	action := &extensionInstallAction{
		args:    []string{},
		flags:   &extensionInstallFlags{global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrNoArgsProvided)
}

func Test_ExtensionInstallAction_Run_VersionWithMultipleArgs(t *testing.T) {
	t.Parallel()
	action := &extensionInstallAction{
		args:    []string{"ext1", "ext2"},
		flags:   &extensionInstallFlags{version: "1.0.0", global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrInvalidFlagCombination)
}

func Test_ExtensionUninstallAction_Run_ArgsWithAllFlag(t *testing.T) {
	t.Parallel()
	action := &extensionUninstallAction{
		args:    []string{"ext1"},
		flags:   &extensionUninstallFlags{all: true},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrInvalidFlagCombination)
}

func Test_ExtensionUninstallAction_Run_NoArgsNoAll(t *testing.T) {
	t.Parallel()
	action := &extensionUninstallAction{
		args:    []string{},
		flags:   &extensionUninstallFlags{all: false},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrNoArgsProvided)
}

func Test_ExtensionUpgradeAction_Run_ArgsWithAllFlag(t *testing.T) {
	t.Parallel()
	action := &extensionUpgradeAction{
		args:    []string{"ext1"},
		flags:   &extensionUpgradeFlags{all: true, global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrInvalidFlagCombination)
}

func Test_ExtensionUpgradeAction_Run_VersionWithMultipleArgs(t *testing.T) {
	t.Parallel()
	action := &extensionUpgradeAction{
		args:    []string{"ext1", "ext2"},
		flags:   &extensionUpgradeFlags{version: "1.0.0", global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrInvalidFlagCombination)
}

func Test_ExtensionUpgradeAction_Run_NoArgsNoAll(t *testing.T) {
	t.Parallel()
	action := &extensionUpgradeAction{
		args:    []string{},
		flags:   &extensionUpgradeFlags{all: false, global: &internal.GlobalCommandOptions{}},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrNoArgsProvided)
}

func Test_ExtensionSourceValidateAction_Run_NoArgs_Guard(t *testing.T) {
	t.Parallel()
	action := &extensionSourceValidateAction{
		args:    []string{},
		flags:   &extensionSourceValidateFlags{},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrNoArgsProvided)
}

func Test_ExtensionSourceValidateAction_Run_TooManyArgs_Guard(t *testing.T) {
	t.Parallel()
	action := &extensionSourceValidateAction{
		args:    []string{"src1", "src2"},
		flags:   &extensionSourceValidateFlags{},
		console: mockinput.NewMockConsole(),
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	var suggestion *internal.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestion)
	assert.ErrorIs(t, suggestion.Err, internal.ErrInvalidFlagCombination)
}

func Test_NewExtensionListFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newExtensionListFlags(cmd)
	require.NotNil(t, flags)
}

func Test_NewExtensionShowFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newExtensionShowFlags(cmd, global)
	require.NotNil(t, flags)
	assert.Equal(t, global, flags.global)
}

func Test_NewExtensionInstallFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newExtensionInstallFlags(cmd, global)
	require.NotNil(t, flags)
	assert.Equal(t, global, flags.global)
}

func Test_NewExtensionUninstallFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newExtensionUninstallFlags(cmd)
	require.NotNil(t, flags)
}

func Test_NewExtensionUpgradeFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newExtensionUpgradeFlags(cmd, global)
	require.NotNil(t, flags)
	assert.Equal(t, global, flags.global)
}

func Test_NewExtensionSourceAddFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newExtensionSourceAddFlags(cmd)
	require.NotNil(t, flags)
}

func Test_NewExtensionSourceValidateFlags_Constructor(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newExtensionSourceValidateFlags(cmd)
	require.NotNil(t, flags)
}

func Test_ExtensionListItem_Fields(t *testing.T) {
	t.Parallel()
	item := extensionListItem{
		Id:        "ext.test",
		Name:      "Test Extension",
		Version:   "1.0.0",
		Namespace: "test",
		Source:    "default",
	}
	assert.Equal(t, "ext.test", item.Id)
	assert.Equal(t, "Test Extension", item.Name)
}

func Test_ErrorWithSuggestion_Unwrap(t *testing.T) {
	t.Parallel()
	inner := fmt.Errorf("inner error")
	err := &internal.ErrorWithSuggestion{
		Err:        inner,
		Suggestion: "suggestion",
	}
	assert.ErrorIs(t, err, inner)
}

func Test_SelectDistinctExtension_OneMatch(t *testing.T) {
	t.Parallel()
	ext := &extensions.ExtensionMetadata{Source: "default"}
	result, err := selectDistinctExtension(
		t.Context(), mockinput.NewMockConsole(),
		"test-ext", []*extensions.ExtensionMetadata{ext},
		&internal.GlobalCommandOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, ext, result)
}

func Test_ExtensionShowResult_Display(t *testing.T) {
	t.Parallel()
	result := &extensionShowItem{
		Id:          "test-ext",
		Name:        "Test Extension",
		Description: "A test extension",
		Tags:        []string{"test", "demo"},
		Source:      "default",
	}

	var buf bytes.Buffer
	err := result.Display(&buf)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "test-ext")
	require.Contains(t, buf.String(), "Test Extension")
}

func Test_ExtensionShowItem_Display_Minimal(t *testing.T) {
	t.Parallel()
	item := &extensionShowItem{
		Id:          "test.ext",
		Name:        "Test Extension",
		Description: "A test extension",
		Source:      "azd",
		Namespace:   "test",
		Usage:       "azd test",
	}
	buf := &bytes.Buffer{}
	err := item.Display(buf)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "test.ext")
	assert.Contains(t, buf.String(), "Test Extension")
	assert.Contains(t, buf.String(), "azd test")
}

func Test_ExtensionShowItem_Display_AllFields(t *testing.T) {
	t.Parallel()
	item := &extensionShowItem{
		Id:                "full.ext",
		Name:              "Full Extension",
		Description:       "Full desc",
		Source:            "custom-src",
		Namespace:         "full",
		Website:           "https://example.com",
		LatestVersion:     "2.0.0",
		InstalledVersion:  "1.0.0",
		AvailableVersions: []string{"1.0.0", "1.5.0", "2.0.0"},
		Tags:              []string{"tool", "testing"},
		Usage:             "azd full do-thing",
		Capabilities:      []extensions.CapabilityType{"mcp"},
		Providers: []extensions.Provider{
			{Name: "prov1", Type: "host", Description: "Provider 1"},
		},
		Examples: []extensions.ExtensionExample{
			{Usage: "azd full example1"},
			{Usage: "azd full example2"},
		},
	}
	buf := &bytes.Buffer{}
	err := item.Display(buf)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "https://example.com")
	assert.Contains(t, out, "2.0.0")
	assert.Contains(t, out, "1.0.0")
	assert.Contains(t, out, "tool")
	assert.Contains(t, out, "testing")
	assert.Contains(t, out, "mcp")
	assert.Contains(t, out, "prov1")
	assert.Contains(t, out, "Provider 1")
	assert.Contains(t, out, "azd full example1")
	assert.Contains(t, out, "azd full example2")
}

func Test_ExtensionShowItem_Display_NoWebsite(t *testing.T) {
	t.Parallel()
	item := &extensionShowItem{
		Id:          "test.ext",
		Name:        "Test",
		Description: "Desc",
		Source:      "s",
		Namespace:   "n",
		Usage:       "azd test",
	}
	buf := &bytes.Buffer{}
	err := item.Display(buf)
	require.NoError(t, err)
	// Website row should not appear
	assert.NotContains(t, buf.String(), "Website")
}

func Test_ExtensionShowItem_Display_EmptyCapabilities(t *testing.T) {
	t.Parallel()
	item := &extensionShowItem{
		Id: "x", Name: "X", Description: "D", Source: "s", Namespace: "n",
		Usage:        "u",
		Capabilities: []extensions.CapabilityType{},
	}
	buf := &bytes.Buffer{}
	err := item.Display(buf)
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "Capabilities")
}

func Test_ExtensionSourceRemoveAction_NoArgs(t *testing.T) {
	t.Parallel()
	action := &extensionSourceRemoveAction{args: []string{}}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrNoArgsProvided)
}

func Test_ExtensionSourceRemoveAction_TooManyArgs(t *testing.T) {
	t.Parallel()
	action := &extensionSourceRemoveAction{args: []string{"a", "b"}}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrInvalidFlagCombination)
}

func Test_ExtensionSourceValidateAction_NoArgs(t *testing.T) {
	t.Parallel()
	action := &extensionSourceValidateAction{args: []string{}}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrNoArgsProvided)
}

func Test_ExtensionSourceValidateAction_TooManyArgs(t *testing.T) {
	t.Parallel()
	action := &extensionSourceValidateAction{args: []string{"a", "b"}}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, internal.ErrInvalidFlagCombination)
}

// mockUserConfigManager implements config.UserConfigManager for testing
type mockUserConfigManager struct {
	mock.Mock
}

func newTestSourceManager(t *testing.T) (*extensions.SourceManager, *mockUserConfigManager) {
	t.Helper()
	cfgMgr := &mockUserConfigManager{}
	container := ioc.NewNestedContainer(nil)
	sm := extensions.NewSourceManager(container, cfgMgr, nil)
	return sm, cfgMgr
}

func Test_ExtensionSourceListAction_Success(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfg := config.NewEmptyConfig()
	cfg.Set("extension.sources.mysource", map[string]any{
		"name":     "mysource",
		"type":     "url",
		"location": "https://example.com",
	})
	cfgMgr.On("Load").Return(cfg, nil)

	buf := &bytes.Buffer{}
	action := &extensionSourceListAction{
		sourceManager: sm,
		formatter:     &output.JsonFormatter{},
		writer:        buf,
	}
	_, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "mysource")
}

func Test_ExtensionSourceListAction_TableFormat(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfg := config.NewEmptyConfig()
	cfg.Set("extension.sources.test", map[string]any{
		"name":     "test",
		"type":     "file",
		"location": "/tmp/test",
	})
	cfgMgr.On("Load").Return(cfg, nil)

	buf := &bytes.Buffer{}
	action := &extensionSourceListAction{
		sourceManager: sm,
		formatter:     &output.TableFormatter{},
		writer:        buf,
	}
	_, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "test")
}

func Test_ExtensionSourceRemoveAction_Success(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfg := config.NewEmptyConfig()
	cfg.Set("extension.sources.mysource", map[string]any{
		"name":     "mysource",
		"type":     "url",
		"location": "https://example.com",
	})
	cfgMgr.On("Load").Return(cfg, nil)
	cfgMgr.On("Save", mock.Anything).Return(nil)

	console := mockinput.NewMockConsole()
	action := &extensionSourceRemoveAction{
		sourceManager: sm,
		console:       console,
		args:          []string{"mysource"},
	}
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Message.Header, "mysource")
}

func Test_ExtensionSourceRemoveAction_RemoveError(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	// source not found when listing
	cfg := config.NewEmptyConfig()
	cfg.Set("extension.sources.other", map[string]any{
		"name":     "other",
		"type":     "url",
		"location": "https://example.com",
	})
	cfgMgr.On("Load").Return(cfg, nil)

	console := mockinput.NewMockConsole()
	action := &extensionSourceRemoveAction{
		sourceManager: sm,
		console:       console,
		args:          []string{"nonexistent"},
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_ExtensionSourceAddAction_InvalidSourceType(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfgMgr.On("Load").Return(config.NewEmptyConfig(), nil)

	console := mockinput.NewMockConsole()
	action := &extensionSourceAddAction{
		sourceManager: sm,
		console:       console,
		flags:         &extensionSourceAddFlags{name: "bad", location: "somewhere", kind: "badkind"},
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_ExtensionSourceAddAction_EmptyNameError(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfgMgr.On("Load").Return(config.NewEmptyConfig(), nil)

	console := mockinput.NewMockConsole()
	action := &extensionSourceAddAction{
		sourceManager: sm,
		console:       console,
		flags:         &extensionSourceAddFlags{name: "", location: "", kind: "file"},
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_ExtensionSourceListAction_DefaultSource(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfg := config.NewEmptyConfig()
	// No "extension.sources" key → triggers default source creation
	cfgMgr.On("Load").Return(cfg, nil)
	cfgMgr.On("Save", mock.Anything).Return(nil)

	buf := &bytes.Buffer{}
	action := &extensionSourceListAction{
		sourceManager: sm,
		formatter:     &output.JsonFormatter{},
		writer:        buf,
	}
	_, err := action.Run(t.Context())
	require.NoError(t, err)
	// Default source "azd" should appear
	assert.Contains(t, buf.String(), "azd")
}

func Test_ExtensionSourceAddAction_FileNotFound(t *testing.T) {
	t.Parallel()
	sm, cfgMgr := newTestSourceManager(t)
	cfgMgr.On("Load").Return(config.NewEmptyConfig(), nil)

	console := mockinput.NewMockConsole()
	action := &extensionSourceAddAction{
		sourceManager: sm,
		console:       console,
		flags: &extensionSourceAddFlags{
			name:     "local",
			location: "/nonexistent/path/to/registry.json",
			kind:     "file",
		},
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_SelectDistinctExtension_Single(t *testing.T) {
	t.Parallel()
	exts := []*extensions.ExtensionMetadata{
		{Id: "ext.one", DisplayName: "Ext One"},
	}
	console := mockinput.NewMockConsole()
	globalOpts := &internal.GlobalCommandOptions{}
	result, err := selectDistinctExtension(t.Context(), console, "ext.one", exts, globalOpts)
	require.NoError(t, err)
	assert.Equal(t, "ext.one", result.Id)
}

func Test_SelectDistinctExtension_Empty(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	globalOpts := &internal.GlobalCommandOptions{}
	_, err := selectDistinctExtension(t.Context(), console, "ext.missing", nil, globalOpts)
	require.Error(t, err)
}

func Test_CheckForMatchingExtensions_EmptyRegistry(t *testing.T) {
	t.Parallel()
	// Cannot easily mock Source interface for checkForMatchingExtensions
	// without a real implementation. Test is a placeholder.
}

type coverageTestAction struct{}

func (a *coverageTestAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	return nil, nil
}

func Test_CheckNamespaceConflict_NoConflict(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{}
	err := checkNamespaceConflict("new.ext", "foo", installed)
	require.NoError(t, err)
}

func Test_CheckNamespaceConflict_WithConflict(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"existing.ext": {Namespace: "foo"},
	}
	err := checkNamespaceConflict("new.ext", "foo", installed)
	require.Error(t, err)
}

func Test_CheckNamespaceConflict_EmptyNs_NoConflict(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"existing.ext": {Namespace: "foo"},
	}
	err := checkNamespaceConflict("new.ext", "", installed)
	require.NoError(t, err) // empty namespace => no conflict
}

func Test_CheckNamespaceConflict_SkipSelf(t *testing.T) {
	t.Parallel()
	installed := map[string]*extensions.Extension{
		"self.ext": {Namespace: "foo"},
	}
	err := checkNamespaceConflict("self.ext", "foo", installed)
	require.NoError(t, err) // skips self
}
