// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/require"
)

func Test_VersionIsCompatible(t *testing.T) {
	tests := []struct {
		name               string
		requiredAzdVersion string
		azdVersion         string
		expectCompatible   bool
	}{
		{
			name:               "no min version set",
			requiredAzdVersion: "",
			azdVersion:         "1.0.0",
			expectCompatible:   true,
		},
		{
			name:               "azd version meets minimum",
			requiredAzdVersion: ">= 1.24.0",
			azdVersion:         "1.24.0",
			expectCompatible:   true,
		},
		{
			name:               "azd version exceeds minimum",
			requiredAzdVersion: ">= 1.24.0",
			azdVersion:         "1.25.0",
			expectCompatible:   true,
		},
		{
			name:               "azd version below minimum",
			requiredAzdVersion: ">= 1.24.0",
			azdVersion:         "1.23.0",
			expectCompatible:   false,
		},
		{
			name:               "invalid constraint is compatible",
			requiredAzdVersion: "invalid",
			azdVersion:         "1.0.0",
			expectCompatible:   true,
		},
		{
			name:               "major version mismatch",
			requiredAzdVersion: ">= 2.0.0",
			azdVersion:         "1.99.99",
			expectCompatible:   false,
		},
		{
			name:               "caret constraint compatible",
			requiredAzdVersion: "^1.24.0",
			azdVersion:         "1.25.0",
			expectCompatible:   true,
		},
		{
			name:               "caret constraint incompatible major",
			requiredAzdVersion: "^1.24.0",
			azdVersion:         "2.0.0",
			expectCompatible:   false,
		},
		{
			name:               "tilde constraint compatible",
			requiredAzdVersion: "~1.24.0",
			azdVersion:         "1.24.5",
			expectCompatible:   true,
		},
		{
			name:               "tilde constraint incompatible minor",
			requiredAzdVersion: "~1.24.0",
			azdVersion:         "1.25.0",
			expectCompatible:   false,
		},
		{
			name:               "range constraint compatible",
			requiredAzdVersion: ">= 1.20.0, < 2.0.0",
			azdVersion:         "1.25.0",
			expectCompatible:   true,
		},
		{
			name:               "range constraint incompatible",
			requiredAzdVersion: ">= 1.20.0, < 2.0.0",
			azdVersion:         "2.0.0",
			expectCompatible:   false,
		},
		{
			name:               "strict greater than compatible",
			requiredAzdVersion: "> 1.24.0",
			azdVersion:         "1.24.1",
			expectCompatible:   true,
		},
		{
			name:               "strict greater than excludes exact version",
			requiredAzdVersion: "> 1.24.0",
			azdVersion:         "1.24.0",
			expectCompatible:   false,
		},
		{
			name:               "strict greater than below",
			requiredAzdVersion: "> 1.24.0",
			azdVersion:         "1.23.0",
			expectCompatible:   false,
		},
		{
			name:               "less than compatible",
			requiredAzdVersion: "< 2.0.0",
			azdVersion:         "1.99.0",
			expectCompatible:   true,
		},
		{
			name:               "less than excludes exact version",
			requiredAzdVersion: "< 2.0.0",
			azdVersion:         "2.0.0",
			expectCompatible:   false,
		},
		{
			name:               "less than incompatible above",
			requiredAzdVersion: "< 2.0.0",
			azdVersion:         "2.1.0",
			expectCompatible:   false,
		},
		{
			name:               "strict greater than and less than range compatible",
			requiredAzdVersion: "> 1.20.0, < 2.0.0",
			azdVersion:         "1.25.0",
			expectCompatible:   true,
		},
		{
			name:               "strict greater than and less than range at lower bound",
			requiredAzdVersion: "> 1.20.0, < 2.0.0",
			azdVersion:         "1.20.0",
			expectCompatible:   false,
		},
		{
			name:               "strict greater than and less than range at upper bound",
			requiredAzdVersion: "> 1.20.0, < 2.0.0",
			azdVersion:         "2.0.0",
			expectCompatible:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extVersion := &ExtensionVersion{
				RequiredAzdVersion: tt.requiredAzdVersion,
			}
			azdVersion := semver.MustParse(tt.azdVersion)
			result := VersionIsCompatible(extVersion, azdVersion)
			require.Equal(t, tt.expectCompatible, result)
		})
	}
}

func Test_VersionIsCompatible_PrereleaseFormats(t *testing.T) {
	// These tests verify that semver constraint checking works correctly with
	// prerelease version formats used by PR and daily builds.
	// In practice, currentAzdSemver() strips prerelease tags before passing to these functions,
	// because semver constraints exclude prerelease versions by default.
	tests := []struct {
		name               string
		requiredAzdVersion string
		rawVersion         string
		strippedVersion    string
		strippedCompat     bool
	}{
		{
			name:               "PR build stripped version satisfies constraint",
			requiredAzdVersion: ">= 1.23.0",
			rawVersion:         "1.24.0-beta.1-pr.5861630",
			strippedVersion:    "1.24.0",
			strippedCompat:     true,
		},
		{
			name:               "PR build stripped version does not satisfy constraint",
			requiredAzdVersion: ">= 1.25.0",
			rawVersion:         "1.24.0-beta.1-pr.5861630",
			strippedVersion:    "1.24.0",
			strippedCompat:     false,
		},
		{
			name:               "daily build stripped version satisfies constraint",
			requiredAzdVersion: ">= 1.23.0",
			rawVersion:         "1.23.4-daily.5857181",
			strippedVersion:    "1.23.4",
			strippedCompat:     true,
		},
		{
			name:               "daily build stripped version does not satisfy constraint",
			requiredAzdVersion: ">= 1.24.0",
			rawVersion:         "1.23.4-daily.5857181",
			strippedVersion:    "1.23.4",
			strippedCompat:     false,
		},
		{
			name:               "PR build stripped version satisfies exact minimum",
			requiredAzdVersion: ">= 1.24.0",
			rawVersion:         "1.24.0-beta.1-pr.5861630",
			strippedVersion:    "1.24.0",
			strippedCompat:     true,
		},
		{
			name:               "daily build stripped version with caret constraint",
			requiredAzdVersion: "^1.23.0",
			rawVersion:         "1.23.4-daily.5857181",
			strippedVersion:    "1.23.4",
			strippedCompat:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extVersion := &ExtensionVersion{
				RequiredAzdVersion: tt.requiredAzdVersion,
			}

			// Stripped version comparison (simulates what currentAzdSemver does)
			strippedAzdVersion := semver.MustParse(tt.strippedVersion)
			strippedResult := VersionIsCompatible(extVersion, strippedAzdVersion)
			require.Equal(t, tt.strippedCompat, strippedResult, "stripped version check for raw %s", tt.rawVersion)
		})
	}
}

func Test_FilterCompatibleVersions(t *testing.T) {
	t.Run("all versions compatible", func(t *testing.T) {
		versions := []ExtensionVersion{
			{Version: "1.0.0"},
			{Version: "1.1.0"},
			{Version: "2.0.0"},
		}
		azdVersion := semver.MustParse("1.25.0")
		result := FilterCompatibleVersions(versions, azdVersion)

		require.Len(t, result.Compatible, 3)
		require.Equal(t, "2.0.0", result.LatestOverall.Version)
		require.Equal(t, "2.0.0", result.LatestCompatible.Version)
		require.False(t, result.HasNewerIncompatible)
	})

	t.Run("some versions incompatible", func(t *testing.T) {
		versions := []ExtensionVersion{
			{Version: "1.0.0"},
			{Version: "1.1.0", RequiredAzdVersion: ">= 1.20.0"},
			{Version: "2.0.0", RequiredAzdVersion: ">= 1.25.0"},
		}
		azdVersion := semver.MustParse("1.22.0")
		result := FilterCompatibleVersions(versions, azdVersion)

		require.Len(t, result.Compatible, 2)
		require.Equal(t, "1.0.0", result.Compatible[0].Version)
		require.Equal(t, "1.1.0", result.Compatible[1].Version)
		require.Equal(t, "2.0.0", result.LatestOverall.Version)
		require.Equal(t, "1.1.0", result.LatestCompatible.Version)
		require.True(t, result.HasNewerIncompatible)
	})

	t.Run("no compatible versions", func(t *testing.T) {
		versions := []ExtensionVersion{
			{Version: "1.0.0", RequiredAzdVersion: ">= 1.25.0"},
			{Version: "2.0.0", RequiredAzdVersion: ">= 1.26.0"},
		}
		azdVersion := semver.MustParse("1.20.0")
		result := FilterCompatibleVersions(versions, azdVersion)

		require.Len(t, result.Compatible, 0)
		require.Equal(t, "2.0.0", result.LatestOverall.Version)
		require.Nil(t, result.LatestCompatible)
		require.True(t, result.HasNewerIncompatible)
	})

	t.Run("empty versions list", func(t *testing.T) {
		versions := []ExtensionVersion{}
		azdVersion := semver.MustParse("1.25.0")
		result := FilterCompatibleVersions(versions, azdVersion)

		require.Len(t, result.Compatible, 0)
		require.Nil(t, result.LatestOverall)
		require.Nil(t, result.LatestCompatible)
		require.False(t, result.HasNewerIncompatible)
	})

	t.Run("latest version has min azd but earlier ones do not", func(t *testing.T) {
		versions := []ExtensionVersion{
			{Version: "1.0.0"},
			{Version: "1.1.0"},
			{Version: "2.0.0", RequiredAzdVersion: ">= 2.0.0"},
		}
		azdVersion := semver.MustParse("1.25.0")
		result := FilterCompatibleVersions(versions, azdVersion)

		require.Len(t, result.Compatible, 2)
		require.Equal(t, "1.1.0", result.LatestCompatible.Version)
		require.Equal(t, "2.0.0", result.LatestOverall.Version)
		require.True(t, result.HasNewerIncompatible)
	})

	t.Run("PR build version filters correctly", func(t *testing.T) {
		versions := []ExtensionVersion{
			{Version: "1.0.0"},
			{Version: "1.1.0", RequiredAzdVersion: ">= 1.23.0"},
			{Version: "2.0.0", RequiredAzdVersion: ">= 1.25.0"},
		}
		// Simulate a PR build "1.24.0-beta.1-pr.5861630" with prerelease stripped to "1.24.0"
		azdVersion := semver.MustParse("1.24.0")
		result := FilterCompatibleVersions(versions, azdVersion)

		require.Len(t, result.Compatible, 2)
		require.Equal(t, "1.0.0", result.Compatible[0].Version)
		require.Equal(t, "1.1.0", result.Compatible[1].Version)
		require.Equal(t, "1.1.0", result.LatestCompatible.Version)
		require.True(t, result.HasNewerIncompatible)
	})

	t.Run("daily build version filters correctly", func(t *testing.T) {
		versions := []ExtensionVersion{
			{Version: "1.0.0"},
			{Version: "1.1.0", RequiredAzdVersion: ">= 1.23.0"},
			{Version: "2.0.0", RequiredAzdVersion: ">= 1.25.0"},
		}
		// Simulate a daily build "1.23.4-daily.5857181" with prerelease stripped to "1.23.4"
		azdVersion := semver.MustParse("1.23.4")
		result := FilterCompatibleVersions(versions, azdVersion)

		require.Len(t, result.Compatible, 2)
		require.Equal(t, "1.1.0", result.LatestCompatible.Version)
		require.True(t, result.HasNewerIncompatible)
	})

	t.Run("descending order returns correct latest overall", func(t *testing.T) {
		// Registry returns versions newest-first (descending), e.g. [0.1.1, 0.1.0, 0.0.5, 0.0.2]
		versions := []ExtensionVersion{
			{Version: "0.1.1"},
			{Version: "0.1.0"},
			{Version: "0.0.5"},
			{Version: "0.0.2"},
		}
		azdVersion := semver.MustParse("1.25.0")
		result := FilterCompatibleVersions(versions, azdVersion)

		require.Equal(t, "0.1.1", result.LatestOverall.Version)
		require.Equal(t, "0.1.1", result.LatestCompatible.Version)
		require.False(t, result.HasNewerIncompatible)
	})

	t.Run("descending order with incompatible newest version", func(t *testing.T) {
		// Registry returns versions newest-first; the newest is incompatible
		versions := []ExtensionVersion{
			{Version: "2.0.0", RequiredAzdVersion: ">= 2.0.0"},
			{Version: "1.1.0"},
			{Version: "1.0.0"},
		}
		azdVersion := semver.MustParse("1.25.0")
		result := FilterCompatibleVersions(versions, azdVersion)

		require.Equal(t, "2.0.0", result.LatestOverall.Version)
		require.Equal(t, "1.1.0", result.LatestCompatible.Version)
		require.True(t, result.HasNewerIncompatible)
	})
}

func Test_LatestVersion(t *testing.T) {
	t.Run("nil on empty slice", func(t *testing.T) {
		require.Nil(t, LatestVersion([]ExtensionVersion{}))
	})

	t.Run("single element", func(t *testing.T) {
		versions := []ExtensionVersion{{Version: "1.0.0"}}
		result := LatestVersion(versions)
		require.Equal(t, "1.0.0", result.Version)
	})

	t.Run("ascending order", func(t *testing.T) {
		versions := []ExtensionVersion{
			{Version: "0.0.2"},
			{Version: "0.0.5"},
			{Version: "0.1.0"},
			{Version: "0.1.1"},
		}
		require.Equal(t, "0.1.1", LatestVersion(versions).Version)
	})

	t.Run("descending order (registry sort order)", func(t *testing.T) {
		versions := []ExtensionVersion{
			{Version: "0.1.1"},
			{Version: "0.1.0"},
			{Version: "0.0.5"},
			{Version: "0.0.2"},
		}
		require.Equal(t, "0.1.1", LatestVersion(versions).Version)
	})
}
