// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// UpgradeStatus represents the outcome of a single extension upgrade attempt.
// The String() values ("upgraded", "skipped", "promoted", "failed") are
// serialized to JSON output and form a public API contract.
// Changing these values is a breaking change.
type UpgradeStatus int

const (
	// UpgradeStatusUpgraded indicates the extension was successfully upgraded.
	UpgradeStatusUpgraded UpgradeStatus = iota
	// UpgradeStatusSkipped indicates the extension was skipped (already up to date or newer).
	UpgradeStatusSkipped
	// UpgradeStatusPromoted indicates the extension was migrated from a non-main registry.
	UpgradeStatusPromoted
	// UpgradeStatusFailed indicates the extension upgrade failed.
	UpgradeStatusFailed
)

// String returns the lowercase display name for an UpgradeStatus.
func (s UpgradeStatus) String() string {
	switch s {
	case UpgradeStatusUpgraded:
		return "upgraded"
	case UpgradeStatusSkipped:
		return "skipped"
	case UpgradeStatusPromoted:
		return "promoted"
	case UpgradeStatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// UpgradeResult captures the outcome of a single extension upgrade attempt.
// It is used for both interactive display and structured JSON output.
type UpgradeResult struct {
	// ExtensionId is the extension identifier.
	ExtensionId string
	// FromVersion is the installed version before the upgrade attempt.
	FromVersion string
	// ToVersion is the version after upgrade (empty if skipped or failed).
	ToVersion string
	// FromSource is the registry source before the upgrade.
	FromSource string
	// FromSourceCategory is the privacy-safe category before the upgrade.
	FromSourceCategory SourceCategory
	// ToSource is the registry source used for the upgrade.
	ToSource string
	// ToSourceCategory is the privacy-safe category selected for the upgrade.
	ToSourceCategory SourceCategory
	// Status is the outcome of the upgrade attempt.
	Status UpgradeStatus
	// Error is the error value if Status is UpgradeStatusFailed, nil otherwise.
	Error error
	// SkipReason describes why the extension was skipped (e.g., "already up to date").
	SkipReason string
	// Suggestion is an optional actionable hint shown alongside SkipReason or Error
	// in interactive output. Not serialized to JSON; may contain ANSI formatting.
	Suggestion string
	// Warnings describes problems the upgrade caused without failing, such as an explicitly
	// requested version that does not satisfy an installed extension's constraint.
	Warnings []string
	// DependencyUpgrades captures dependency reconciliation results, including
	// skipped upgrades and failed metadata writes when dependency updates are disabled.
	DependencyUpgrades []UpgradeResult
}

// DependencyUpgradeError preserves causes from failed dependencies, including nested results.
func DependencyUpgradeError(results []UpgradeResult) error {
	var failures []error
	for _, result := range results {
		if result.Status == UpgradeStatusFailed && result.Error != nil {
			failures = append(failures, fmt.Errorf("dependency %s: %w", result.ExtensionId, result.Error))
		}
		if err := DependencyUpgradeError(result.DependencyUpgrades); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// upgradeResultJSON is the JSON serialization format for UpgradeResult.
// Field names are part of the public --output json contract.
// Changes to field names or removal of fields is a breaking change.
type upgradeResultJSON struct {
	Name               string          `json:"name"`
	Status             string          `json:"status"`
	FromVersion        string          `json:"fromVersion,omitempty"`
	ToVersion          string          `json:"toVersion,omitempty"`
	FromSource         string          `json:"fromSource,omitempty"`
	ToSource           string          `json:"toSource,omitempty"`
	SkipReason         string          `json:"skipReason,omitempty"`
	Error              string          `json:"error,omitempty"`
	Warnings           []string        `json:"warnings,omitempty"`
	DependencyUpgrades []UpgradeResult `json:"dependencyUpgrades,omitempty"`
}

// MarshalJSON serializes UpgradeResult to JSON for --output json.
// Error messages are included as-is because azd errors are user-facing
// by design (via ErrorWithSuggestion and error_suggestions.yaml pipeline).
func (r UpgradeResult) MarshalJSON() ([]byte, error) {
	j := upgradeResultJSON{
		Name:               r.ExtensionId,
		Status:             r.Status.String(),
		FromVersion:        r.FromVersion,
		ToVersion:          r.ToVersion,
		FromSource:         r.FromSource,
		ToSource:           r.ToSource,
		SkipReason:         r.SkipReason,
		Warnings:           r.Warnings,
		DependencyUpgrades: r.DependencyUpgrades,
	}
	if r.Error != nil {
		j.Error = r.Error.Error()
	}
	return json.Marshal(j)
}

// UpgradeSummary holds aggregate counts from a batch upgrade operation.
// DependencyUpgradesByStatus is for internal UI and telemetry counters. Dependency counters count
// each dependency once per status, even when several parents report it.
type UpgradeSummary struct {
	Total                      int                   `json:"total"`
	Upgraded                   int                   `json:"upgraded"`
	Skipped                    int                   `json:"skipped"`
	Promoted                   int                   `json:"promoted"`
	Failed                     int                   `json:"failed"`
	DependencyUpgrades         int                   `json:"dependencyUpgrades,omitempty"`
	DependencyUpgradesByStatus map[UpgradeStatus]int `json:"-"`
}

// HasFailures reports whether a top-level extension or any dependency failed.
func (s UpgradeSummary) HasFailures() bool {
	return s.Failed > 0 || s.DependencyUpgradesByStatus[UpgradeStatusFailed] > 0
}

// NewUpgradeSummary computes aggregate counts from a slice of UpgradeResult.
// Top-level status counters exclude dependency upgrades.
func NewUpgradeSummary(results []UpgradeResult) UpgradeSummary {
	s := UpgradeSummary{
		Total:                      len(results),
		DependencyUpgradesByStatus: map[UpgradeStatus]int{},
	}
	dependencies := map[UpgradeStatus]map[string]struct{}{}
	for i := range results {
		switch results[i].Status {
		case UpgradeStatusUpgraded:
			s.Upgraded++
		case UpgradeStatusSkipped:
			s.Skipped++
		case UpgradeStatusPromoted:
			s.Promoted++
		case UpgradeStatusFailed:
			s.Failed++
		}
		collectDependenciesByStatus(results[i].DependencyUpgrades, dependencies)
	}
	for status, ids := range dependencies {
		s.DependencyUpgradesByStatus[status] = len(ids)
		s.DependencyUpgrades += len(ids)
	}
	return s
}

// collectDependenciesByStatus records recursive dependency ids by status.
func collectDependenciesByStatus(results []UpgradeResult, dependencies map[UpgradeStatus]map[string]struct{}) {
	for i := range results {
		status := results[i].Status
		if dependencies[status] == nil {
			dependencies[status] = map[string]struct{}{}
		}
		dependencies[status][strings.ToLower(results[i].ExtensionId)] = struct{}{}
		collectDependenciesByStatus(results[i].DependencyUpgrades, dependencies)
	}
}

// CountDependencyUpgrades returns the recursive count of dependency upgrade entries.
func CountDependencyUpgrades(results []UpgradeResult) int {
	n := 0
	for i := range results {
		n++
		n += CountDependencyUpgrades(results[i].DependencyUpgrades)
	}
	return n
}

// UpgradeReport is the top-level JSON structure for batch upgrade output.
type UpgradeReport struct {
	Extensions []UpgradeResult `json:"extensions"`
	Summary    UpgradeSummary  `json:"summary"`
}
