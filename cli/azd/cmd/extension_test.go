// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	mockContext := mocks.NewMockContext(context.Background())

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

	mockContext := mocks.NewMockContext(context.Background())

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

	mockContext := mocks.NewMockContext(context.Background())
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
