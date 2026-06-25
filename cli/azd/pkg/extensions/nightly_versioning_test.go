// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Test_UpdateChecker_NightlyVersions verifies that the nightly version scheme
// (X.Y.Z-nightly.<buildId> and X.Y.Z-preview.nightly.<buildId>) produces the
// expected "has update" results through the same semver comparison azd uses at
// runtime. These pin the upgrade behavior we rely on for nightlies.
func Test_UpdateChecker_NightlyVersions(t *testing.T) {
	tests := []struct {
		name       string
		installed  string
		available  []string // available versions in the source (latest is the max semver)
		wantUpdate bool
	}{
		{
			name:       "newer nightly supersedes older nightly",
			installed:  "1.2.3-nightly.100",
			available:  []string{"1.2.3-nightly.100", "1.2.3-nightly.200"},
			wantUpdate: true,
		},
		{
			name:       "same nightly is not an update",
			installed:  "1.2.3-nightly.222",
			available:  []string{"1.2.3-nightly.222"},
			wantUpdate: false,
		},
		{
			name:       "base version bump supersedes older nightly",
			installed:  "1.2.3-nightly.999",
			available:  []string{"1.2.3-nightly.999", "1.2.4-nightly.1"},
			wantUpdate: true,
		},
		{
			name:       "stable release supersedes nightly of same base",
			installed:  "1.2.3-nightly.200",
			available:  []string{"1.2.3-nightly.200", "1.2.3"},
			wantUpdate: true,
		},
		{
			// Documents the prerelease-base caveat: a nightly built off a
			// prerelease base sorts ABOVE the matching stable prerelease, so a
			// user on the stable preview sees the nightly as an update.
			name:       "nightly off preview base outranks stable preview",
			installed:  "1.2.3-preview",
			available:  []string{"1.2.3-preview", "1.2.3-preview.nightly.60"},
			wantUpdate: true,
		},
		{
			name:       "newer preview nightly supersedes older preview nightly",
			installed:  "1.2.3-preview.nightly.50",
			available:  []string{"1.2.3-preview.nightly.50", "1.2.3-preview.nightly.60"},
			wantUpdate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_CONFIG_DIR", t.TempDir())

			cacheManager, err := NewRegistryCacheManager()
			require.NoError(t, err)

			ctx := t.Context()
			sourceName := "nightly"

			versions := make([]ExtensionVersion, 0, len(tt.available))
			for _, v := range tt.available {
				versions = append(versions, ExtensionVersion{Version: v})
			}

			err = cacheManager.Set(ctx, sourceName, []*ExtensionMetadata{
				{
					Id:          "test.extension",
					DisplayName: "Test Extension",
					Versions:    versions,
				},
			})
			require.NoError(t, err)

			updateChecker := NewUpdateChecker(cacheManager)

			result, err := updateChecker.CheckForUpdate(ctx, &Extension{
				Id:          "test.extension",
				DisplayName: "Test Extension",
				Version:     tt.installed,
				Source:      sourceName,
			})
			require.NoError(t, err)
			require.Equal(t, tt.wantUpdate, result.HasUpdate)
		})
	}
}

// Test_ResolveUpgradeSource_NightlyPromotion verifies how a nightly-sourced
// install promotes (or not) to the stable "azd" registry given the chosen
// version strings. Promotion happens only when the stable registry's latest
// version is strictly greater (semver) than the installed nightly's.
func Test_ResolveUpgradeSource_NightlyPromotion(t *testing.T) {
	makeExt := func(source string, versions ...string) *ExtensionMetadata {
		ext := &ExtensionMetadata{Id: "test.extension", Source: source}
		for _, v := range versions {
			ext.Versions = append(ext.Versions, ExtensionVersion{Version: v})
		}
		return ext
	}

	tests := []struct {
		name          string
		nightlyLatest string
		mainLatest    string // empty => extension not in the stable registry
		wantPromotion bool
		wantSource    string
	}{
		{
			name:          "stable release promotes nightly of same base",
			nightlyLatest: "1.2.3-nightly.200",
			mainLatest:    "1.2.3",
			wantPromotion: true,
			wantSource:    MainRegistryName,
		},
		{
			name:          "nightly off preview base stays on nightly (outranks stable preview)",
			nightlyLatest: "1.2.3-preview.nightly.60",
			mainLatest:    "1.2.3-preview",
			wantPromotion: false,
			wantSource:    "nightly",
		},
		{
			name:          "higher stable base promotes preview nightly",
			nightlyLatest: "1.2.3-preview.nightly.60",
			mainLatest:    "1.2.4",
			wantPromotion: true,
			wantSource:    MainRegistryName,
		},
		{
			name:          "no stable entry keeps user on nightly",
			nightlyLatest: "1.2.3-nightly.200",
			mainLatest:    "",
			wantPromotion: false,
			wantSource:    "nightly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installed := &Extension{Id: "test.extension", Source: "nightly"}

			allMatches := []*ExtensionMetadata{makeExt("nightly", tt.nightlyLatest)}
			if tt.mainLatest != "" {
				allMatches = append(allMatches, makeExt(MainRegistryName, tt.mainLatest))
			}

			result := ResolveUpgradeSource(installed, allMatches, "")
			require.NotNil(t, result)
			require.Equal(t, tt.wantPromotion, result.IsPromotion)
			require.Equal(t, tt.wantSource, result.NewSource)
		})
	}
}
