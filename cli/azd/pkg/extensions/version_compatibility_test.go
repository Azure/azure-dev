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
		name             string
		minAzdVersion    string
		azdVersion       string
		expectCompatible bool
	}{
		{
			name:             "no min version set",
			minAzdVersion:    "",
			azdVersion:       "1.0.0",
			expectCompatible: true,
		},
		{
			name:             "azd version meets minimum",
			minAzdVersion:    "1.24.0",
			azdVersion:       "1.24.0",
			expectCompatible: true,
		},
		{
			name:             "azd version exceeds minimum",
			minAzdVersion:    "1.24.0",
			azdVersion:       "1.25.0",
			expectCompatible: true,
		},
		{
			name:             "azd version below minimum",
			minAzdVersion:    "1.24.0",
			azdVersion:       "1.23.0",
			expectCompatible: false,
		},
		{
			name:             "invalid min version is compatible",
			minAzdVersion:    "invalid",
			azdVersion:       "1.0.0",
			expectCompatible: true,
		},
		{
			name:             "azd prerelease below minimum",
			minAzdVersion:    "1.24.0",
			azdVersion:       "1.24.0-pr.123",
			expectCompatible: false,
		},
		{
			name:             "major version mismatch",
			minAzdVersion:    "2.0.0",
			azdVersion:       "1.99.99",
			expectCompatible: false,
		},
		{
			name:             "PR build with stripped prerelease meets minimum",
			minAzdVersion:    "1.24.0",
			azdVersion:       "1.24.0",
			expectCompatible: true,
		},
		{
			name:             "daily build base version below minimum",
			minAzdVersion:    "1.24.0",
			azdVersion:       "1.23.4",
			expectCompatible: false,
		},
		{
			name:             "daily build base version meets minimum",
			minAzdVersion:    "1.23.0",
			azdVersion:       "1.23.4",
			expectCompatible: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extVersion := &ExtensionVersion{
				MinAzdVersion: tt.minAzdVersion,
			}
			azdVersion := semver.MustParse(tt.azdVersion)
			result := VersionIsCompatible(extVersion, azdVersion)
			require.Equal(t, tt.expectCompatible, result)
		})
	}
}

func Test_VersionIsCompatible_PrereleaseFormats(t *testing.T) {
	// These tests verify the raw semver comparison behavior with prerelease versions.
	// In practice, currentAzdSemver() strips prerelease tags before passing to these functions,
	// so PR/daily builds are compared using their base version.
	tests := []struct {
		name            string
		minAzdVersion   string
		rawVersion      string
		strippedVersion string
		rawCompatible   bool
		strippedCompat  bool
	}{
		{
			name:            "PR build raw version is below minimum",
			minAzdVersion:   "1.24.0",
			rawVersion:      "1.24.0-beta.1-pr.5861630",
			strippedVersion: "1.24.0",
			rawCompatible:   false,
			strippedCompat:  true,
		},
		{
			name:            "daily build raw version is below minimum",
			minAzdVersion:   "1.24.0",
			rawVersion:      "1.23.4-daily.5857181",
			strippedVersion: "1.23.4",
			rawCompatible:   false,
			strippedCompat:  false,
		},
		{
			name:            "daily build of same major.minor meets minimum",
			minAzdVersion:   "1.23.0",
			rawVersion:      "1.23.4-daily.5857181",
			strippedVersion: "1.23.4",
			rawCompatible:   true,
			strippedCompat:  true,
		},
		{
			name:            "PR build exceeds minimum",
			minAzdVersion:   "1.23.0",
			rawVersion:      "1.24.0-beta.1-pr.5861630",
			strippedVersion: "1.24.0",
			rawCompatible:   true,
			strippedCompat:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extVersion := &ExtensionVersion{
				MinAzdVersion: tt.minAzdVersion,
			}

			// Raw prerelease version comparison
			rawAzdVersion := semver.MustParse(tt.rawVersion)
			rawResult := VersionIsCompatible(extVersion, rawAzdVersion)
			require.Equal(t, tt.rawCompatible, rawResult, "raw version check")

			// Stripped version comparison (simulates what currentAzdSemver does)
			strippedAzdVersion := semver.MustParse(tt.strippedVersion)
			strippedResult := VersionIsCompatible(extVersion, strippedAzdVersion)
			require.Equal(t, tt.strippedCompat, strippedResult, "stripped version check")
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
			{Version: "1.1.0", MinAzdVersion: "1.20.0"},
			{Version: "2.0.0", MinAzdVersion: "1.25.0"},
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
			{Version: "1.0.0", MinAzdVersion: "1.25.0"},
			{Version: "2.0.0", MinAzdVersion: "1.26.0"},
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
			{Version: "2.0.0", MinAzdVersion: "2.0.0"},
		}
		azdVersion := semver.MustParse("1.25.0")
		result := FilterCompatibleVersions(versions, azdVersion)

		require.Len(t, result.Compatible, 2)
		require.Equal(t, "1.1.0", result.LatestCompatible.Version)
		require.Equal(t, "2.0.0", result.LatestOverall.Version)
		require.True(t, result.HasNewerIncompatible)
	})

	t.Run("PR build stripped version filters correctly", func(t *testing.T) {
		versions := []ExtensionVersion{
			{Version: "1.0.0"},
			{Version: "1.1.0", MinAzdVersion: "1.23.0"},
			{Version: "2.0.0", MinAzdVersion: "1.25.0"},
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

	t.Run("daily build stripped version filters correctly", func(t *testing.T) {
		versions := []ExtensionVersion{
			{Version: "1.0.0"},
			{Version: "1.1.0", MinAzdVersion: "1.23.0"},
			{Version: "2.0.0", MinAzdVersion: "1.25.0"},
		}
		// Simulate a daily build "1.23.4-daily.5857181" with prerelease stripped to "1.23.4"
		azdVersion := semver.MustParse("1.23.4")
		result := FilterCompatibleVersions(versions, azdVersion)

		require.Len(t, result.Compatible, 2)
		require.Equal(t, "1.1.0", result.LatestCompatible.Version)
		require.True(t, result.HasNewerIncompatible)
	})
}
