// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import "encoding/json"

// UpgradeStatus represents the outcome of a single extension upgrade attempt.
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
	// ToSource is the registry source used for the upgrade.
	ToSource string
	// Status is the outcome of the upgrade attempt.
	Status UpgradeStatus
	// Error is the error value if Status is UpgradeStatusFailed, nil otherwise.
	Error error
	// SkipReason describes why the extension was skipped (e.g., "already up to date").
	SkipReason string
}

// upgradeResultJSON is the JSON-serializable representation of UpgradeResult.
type upgradeResultJSON struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	FromVersion string `json:"fromVersion,omitempty"`
	ToVersion   string `json:"toVersion,omitempty"`
	FromSource  string `json:"fromSource,omitempty"`
	ToSource    string `json:"toSource,omitempty"`
	SkipReason  string `json:"skipReason,omitempty"`
	Error       string `json:"error,omitempty"`
}

// MarshalJSON implements json.Marshaler for clean JSON output.
func (r UpgradeResult) MarshalJSON() ([]byte, error) {
	j := upgradeResultJSON{
		Name:        r.ExtensionId,
		Status:      r.Status.String(),
		FromVersion: r.FromVersion,
		ToVersion:   r.ToVersion,
		FromSource:  r.FromSource,
		ToSource:    r.ToSource,
		SkipReason:  r.SkipReason,
	}
	if r.Error != nil {
		j.Error = r.Error.Error()
	}
	return json.Marshal(j)
}

// UpgradeSummary holds aggregate counts from a batch upgrade operation.
type UpgradeSummary struct {
	Total    int `json:"total"`
	Upgraded int `json:"upgraded"`
	Skipped  int `json:"skipped"`
	Promoted int `json:"promoted"`
	Failed   int `json:"failed"`
}

// NewUpgradeSummary computes aggregate counts from a slice of UpgradeResult.
func NewUpgradeSummary(results []UpgradeResult) UpgradeSummary {
	s := UpgradeSummary{Total: len(results)}
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
	}
	return s
}

// UpgradeReport is the top-level JSON structure for batch upgrade output.
type UpgradeReport struct {
	Extensions []UpgradeResult `json:"extensions"`
	Summary    UpgradeSummary  `json:"summary"`
}
