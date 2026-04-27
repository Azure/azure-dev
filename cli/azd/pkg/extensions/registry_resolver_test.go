// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveUpgradeSource(t *testing.T) {
	// Helper to build ExtensionMetadata with a source and versions
	makeExt := func(source string, versions ...string) *ExtensionMetadata {
		ext := &ExtensionMetadata{
			Id:     "test-ext",
			Source: source,
		}
		for _, v := range versions {
			ext.Versions = append(ext.Versions, ExtensionVersion{Version: v})
		}
		return ext
	}

	tests := []struct {
		name          string
		installed     *Extension
		allMatches    []*ExtensionMetadata
		flagSource    string
		wantNil       bool
		wantSource    string
		wantPromotion bool
		wantOldSource string
		wantNewSource string
	}{
		{
			name:       "no matches returns nil",
			installed:  &Extension{Id: "test-ext", Source: "azd"},
			allMatches: []*ExtensionMetadata{},
			wantNil:    true,
		},
		{
			name:      "explicit --source flag selects that source",
			installed: &Extension{Id: "test-ext", Source: "dev"},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "1.0.0"),
				makeExt("dev", "2.0.0-beta.1"),
			},
			flagSource:    "dev",
			wantSource:    "dev",
			wantPromotion: false,
			wantOldSource: "dev",
			wantNewSource: "dev",
		},
		{
			name:      "explicit --source flag not found returns nil",
			installed: &Extension{Id: "test-ext", Source: "azd"},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "1.0.0"),
			},
			flagSource: "custom-registry",
			wantNil:    true,
		},
		{
			name:      "explicit --source overrides promotion",
			installed: &Extension{Id: "test-ext", Source: "dev"},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "1.0.0"),
				makeExt("dev", "0.9.0"),
			},
			flagSource:    "dev",
			wantSource:    "dev",
			wantPromotion: false,
			wantOldSource: "dev",
			wantNewSource: "dev",
		},
		{
			name:      "stored source used when available",
			installed: &Extension{Id: "test-ext", Source: "custom"},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "1.0.0"),
				makeExt("custom", "1.0.0"),
			},
			wantSource:    "custom",
			wantPromotion: false,
			wantOldSource: "custom",
			wantNewSource: "custom",
		},
		{
			name:      "stored source azd stays on azd",
			installed: &Extension{Id: "test-ext", Source: "azd"},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "2.0.0"),
				makeExt("dev", "3.0.0-beta.1"),
			},
			wantSource:    "azd",
			wantPromotion: false,
			wantOldSource: "azd",
			wantNewSource: "azd",
		},
		{
			name:      "promotion dev to main when main version > dev version",
			installed: &Extension{Id: "test-ext", Source: "dev"},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "1.0.0"),
				makeExt("dev", "0.9.0"),
			},
			wantSource:    "azd",
			wantPromotion: true,
			wantOldSource: "dev",
			wantNewSource: "azd",
		},
		{
			name:      "no promotion when versions equal (source-sticky)",
			installed: &Extension{Id: "test-ext", Source: "dev"},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "1.0.0"),
				makeExt("dev", "1.0.0"),
			},
			wantSource:    "dev",
			wantPromotion: false,
			wantOldSource: "dev",
			wantNewSource: "dev",
		},
		{
			name: "no promotion when dev has newer version " +
				"than main (pre-release stays on dev)",
			installed: &Extension{Id: "test-ext", Source: "dev"},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "1.0.0"),
				makeExt("dev", "2.0.0-beta.1"),
			},
			wantSource:    "dev",
			wantPromotion: false,
			wantOldSource: "dev",
			wantNewSource: "dev",
		},
		{
			name:      "promotion when stored source has no match (removed from dev)",
			installed: &Extension{Id: "test-ext", Source: "dev"},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "1.0.0"),
			},
			wantSource:    "azd",
			wantPromotion: true,
			wantOldSource: "dev",
			wantNewSource: "azd",
		},
		{
			name:      "empty stored source treated as main registry",
			installed: &Extension{Id: "test-ext", Source: ""},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "1.0.0"),
				makeExt("dev", "2.0.0"),
			},
			wantSource:    "azd",
			wantPromotion: false,
			wantOldSource: "",
			wantNewSource: "azd",
		},
		{
			name:      "returns nil when no stored/main match (no silent fallback)",
			installed: &Extension{Id: "test-ext", Source: "removed-registry"},
			allMatches: []*ExtensionMetadata{
				makeExt("custom-a", "1.0.0"),
				makeExt("custom-b", "1.0.0"),
			},
			wantNil: true,
		},
		{
			name:      "custom source stays custom (no promotion to main)",
			installed: &Extension{Id: "test-ext", Source: "enterprise"},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "1.0.0"),
				makeExt("enterprise", "1.0.0"),
			},
			wantSource:    "enterprise",
			wantPromotion: false,
			wantOldSource: "enterprise",
			wantNewSource: "enterprise",
		},
		{
			name: "custom source promotes to main when main version " +
				"is higher and stored source has no match",
			installed: &Extension{Id: "test-ext", Source: "enterprise"},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "2.0.0"),
			},
			wantSource:    "azd",
			wantPromotion: true,
			wantOldSource: "enterprise",
			wantNewSource: "azd",
		},
		{
			name:      "case insensitive source matching",
			installed: &Extension{Id: "test-ext", Source: "AZD"},
			allMatches: []*ExtensionMetadata{
				makeExt("azd", "1.0.0"),
			},
			wantSource:    "azd",
			wantPromotion: false,
			wantOldSource: "AZD",
			wantNewSource: "azd",
		},
		{
			name:      "single match from stored source",
			installed: &Extension{Id: "test-ext", Source: "dev"},
			allMatches: []*ExtensionMetadata{
				makeExt("dev", "1.5.0"),
			},
			wantSource:    "dev",
			wantPromotion: false,
			wantOldSource: "dev",
			wantNewSource: "dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolveUpgradeSource(tt.installed, tt.allMatches, tt.flagSource)

			if tt.wantNil {
				require.Nil(t, result, "expected nil result")
				return
			}

			require.NotNil(t, result, "expected non-nil result")
			require.Equal(t, tt.wantSource, result.Extension.Source)
			require.Equal(t, tt.wantPromotion, result.IsPromotion)
			require.Equal(t, tt.wantOldSource, result.OldSource)
			require.Equal(t, tt.wantNewSource, result.NewSource)
		})
	}
}

func TestShouldPromote(t *testing.T) {
	makeExt := func(source string, versions ...string) *ExtensionMetadata {
		ext := &ExtensionMetadata{Id: "test-ext", Source: source}
		for _, v := range versions {
			ext.Versions = append(ext.Versions, ExtensionVersion{Version: v})
		}
		return ext
	}

	tests := []struct {
		name        string
		storedMatch *ExtensionMetadata
		mainMatch   *ExtensionMetadata
		want        bool
	}{
		{
			name:        "nil main match",
			storedMatch: makeExt("dev", "1.0.0"),
			mainMatch:   nil,
			want:        false,
		},
		{
			name:        "nil stored match promotes",
			storedMatch: nil,
			mainMatch:   makeExt("azd", "1.0.0"),
			want:        true,
		},
		{
			name:        "main newer promotes",
			storedMatch: makeExt("dev", "0.9.0"),
			mainMatch:   makeExt("azd", "1.0.0"),
			want:        true,
		},
		{
			name:        "versions equal stays on stored (source-sticky)",
			storedMatch: makeExt("dev", "1.0.0"),
			mainMatch:   makeExt("azd", "1.0.0"),
			want:        false,
		},
		{
			name:        "stored newer does not promote",
			storedMatch: makeExt("dev", "2.0.0-beta.1"),
			mainMatch:   makeExt("azd", "1.0.0"),
			want:        false,
		},
		{
			name:        "main has no versions does not promote",
			storedMatch: makeExt("dev", "1.0.0"),
			mainMatch: &ExtensionMetadata{
				Id: "test-ext", Source: "azd", Versions: []ExtensionVersion{},
			},
			want: false,
		},
		{
			name: "stored has no versions promotes",
			storedMatch: &ExtensionMetadata{
				Id: "test-ext", Source: "dev", Versions: []ExtensionVersion{},
			},
			mainMatch: makeExt("azd", "1.0.0"),
			want:      true,
		},
		{
			name:        "main has multiple versions picks latest",
			storedMatch: makeExt("dev", "1.5.0"),
			mainMatch:   makeExt("azd", "1.0.0", "2.0.0"),
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldPromote(tt.storedMatch, tt.mainMatch)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFindMatchBySource(t *testing.T) {
	matches := []*ExtensionMetadata{
		{Id: "ext1", Source: "azd"},
		{Id: "ext2", Source: "dev"},
		{Id: "ext3", Source: "custom"},
	}

	tests := []struct {
		name   string
		source string
		wantID string
		wantOk bool
	}{
		{"find azd", "azd", "ext1", true},
		{"find dev", "dev", "ext2", true},
		{"find custom", "custom", "ext3", true},
		{"not found", "missing", "", false},
		{"case insensitive", "AZD", "ext1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findMatchBySource(matches, tt.source)
			if tt.wantOk {
				require.NotNil(t, result)
				require.Equal(t, tt.wantID, result.Id)
			} else {
				require.Nil(t, result)
			}
		})
	}
}
