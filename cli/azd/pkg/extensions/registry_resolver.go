// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"strings"

	"github.com/Masterminds/semver/v3"
)

const (
	// MainRegistryName is the well-known name of the official azd extension registry.
	// This is the default promotion target for extensions migrating from dev/preview registries.
	MainRegistryName = "azd"
)

// ResolveResult holds the outcome of resolving which registry source to use for an extension upgrade.
type ResolveResult struct {
	// Extension is the selected ExtensionMetadata from the resolved source.
	Extension *ExtensionMetadata
	// IsPromotion is true when the extension is being migrated from a non-main source
	// to the main registry (e.g., dev → azd).
	IsPromotion bool
	// OldSource is the source the extension was installed from (before resolution).
	OldSource string
	// NewSource is the source selected by the resolver for the upgrade.
	NewSource string
}

// ResolveUpgradeSource determines which registry source to use for upgrading an installed extension.
//
// It implements a priority chain:
//  1. Explicit source flag — if flagSource is non-empty, only matches from that source are considered.
//  2. Stored source — prefer matches from the extension's persisted Source field.
//  3. Main registry fallback — if the stored source has no match, fall back to the main "azd" registry.
//
// If no step in the priority chain produces a match, nil is returned so the caller
// can report a meaningful error.
//
// Promotion detection: when the installed extension's stored source is a non-main registry
// (e.g., "dev") and the extension is also available in the main "azd" registry, this is a
// promotion event. Promotion is ONE-WAY only (non-main → main). If the user explicitly passes
// a --source flag, promotion is skipped.
//
// Parameters:
//   - installed: the currently installed extension (provides Id and stored Source)
//   - allMatches: extension metadata from all configured sources (result of FindExtensions with no source filter)
//   - flagSource: the value of the --source flag, or "" if not specified
func ResolveUpgradeSource(
	installed *Extension,
	allMatches []*ExtensionMetadata,
	flagSource string,
) *ResolveResult {
	if len(allMatches) == 0 {
		return nil
	}

	storedSource := installed.Source
	// Treat empty/missing stored source as the main registry
	if storedSource == "" {
		storedSource = MainRegistryName
	}

	// Priority 1: Explicit --source flag
	if flagSource != "" {
		if match := findMatchBySource(allMatches, flagSource); match != nil {
			return &ResolveResult{
				Extension: match,
				OldSource: installed.Source,
				NewSource: match.Source,
			}
		}
		// Flag source specified but no match found — return nil so caller can report error
		return nil
	}

	// Priority 2: Stored source
	storedMatch := findMatchBySource(allMatches, storedSource)

	// Check for promotion: stored source is non-main AND main registry has a match
	if !strings.EqualFold(storedSource, MainRegistryName) {
		mainMatch := findMatchBySource(allMatches, MainRegistryName)
		if mainMatch != nil {
			// Promotion is only valid if the main registry version is not a downgrade.
			// If the stored source also has a match, compare their latest versions.
			if shouldPromote(storedMatch, mainMatch) {
				return &ResolveResult{
					Extension:   mainMatch,
					IsPromotion: true,
					OldSource:   installed.Source,
					NewSource:   mainMatch.Source,
				}
			}
		}

		// No promotion — use stored source match if available
		if storedMatch != nil {
			return &ResolveResult{
				Extension: storedMatch,
				OldSource: installed.Source,
				NewSource: storedMatch.Source,
			}
		}

		// Stored source has no match — fall through to main fallback
		if mainMatch != nil {
			return &ResolveResult{
				Extension:   mainMatch,
				IsPromotion: true,
				OldSource:   installed.Source,
				NewSource:   mainMatch.Source,
			}
		}
	}

	// Priority 2 (continued): stored source match for main registry
	if storedMatch != nil {
		return &ResolveResult{
			Extension: storedMatch,
			OldSource: installed.Source,
			NewSource: storedMatch.Source,
		}
	}

	// Priority 3: Main registry fallback (when stored source was main but not found)
	mainMatch := findMatchBySource(allMatches, MainRegistryName)
	if mainMatch != nil {
		return &ResolveResult{
			Extension: mainMatch,
			OldSource: installed.Source,
			NewSource: mainMatch.Source,
		}
	}

	// No match found in any priority chain step — the extension exists in other sources
	// but none matched the stored source or the main registry. Return nil so the caller
	// can report a meaningful error.
	return nil
}

// shouldPromote determines whether a promotion from a non-main source to the main registry
// should occur. Promotion happens when:
//   - The stored source has no match at all (extension removed from dev), OR
//   - The main registry's latest version is strictly greater than the stored source's latest
//     version (the extension has advanced in the main registry beyond the non-main source)
//
// When both sources have the same latest version, the stored source is preferred
// to keep the extension "sticky" to its original source.
func shouldPromote(storedMatch, mainMatch *ExtensionMetadata) bool {
	if mainMatch == nil {
		return false
	}

	// If stored source has no match, always promote
	if storedMatch == nil {
		return true
	}

	storedLatest := LatestVersion(storedMatch.Versions)
	mainLatest := LatestVersion(mainMatch.Versions)

	// If main registry has no parseable versions, don't promote
	if mainLatest == nil {
		return false
	}
	// If stored source has no parseable versions, promote to main (which has versions)
	if storedLatest == nil {
		return true
	}

	storedSemver, err := semver.NewVersion(storedLatest.Version)
	if err != nil {
		return true
	}

	mainSemver, err := semver.NewVersion(mainLatest.Version)
	if err != nil {
		return false
	}

	// Promote only if main version is strictly greater than stored version.
	// Equal versions keep the extension on its stored source (source-sticky).
	return mainSemver.GreaterThan(storedSemver)
}

// findMatchBySource returns the first ExtensionMetadata matching the given source name.
func findMatchBySource(matches []*ExtensionMetadata, source string) *ExtensionMetadata {
	for _, m := range matches {
		if strings.EqualFold(m.Source, source) {
			return m
		}
	}
	return nil
}
