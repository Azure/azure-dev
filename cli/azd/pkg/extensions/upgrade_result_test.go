// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpgradeStatus_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status UpgradeStatus
		want   string
	}{
		{UpgradeStatusUpgraded, "upgraded"},
		{UpgradeStatusSkipped, "skipped"},
		{UpgradeStatusPromoted, "promoted"},
		{UpgradeStatusFailed, "failed"},
		{UpgradeStatus(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.status.String())
		})
	}
}

func TestUpgradeResult_MarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("upgraded_result", func(t *testing.T) {
		t.Parallel()
		r := UpgradeResult{
			ExtensionId: "ai",
			FromVersion: "0.1.0",
			ToVersion:   "0.2.0",
			FromSource:  "azd",
			ToSource:    "azd",
			Status:      UpgradeStatusUpgraded,
		}

		data, err := json.Marshal(r)
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(data, &parsed))

		assert.Equal(t, "ai", parsed["name"])
		assert.Equal(t, "upgraded", parsed["status"])
		assert.Equal(t, "0.1.0", parsed["fromVersion"])
		assert.Equal(t, "0.2.0", parsed["toVersion"])
		assert.Equal(t, "azd", parsed["fromSource"])
		assert.Equal(t, "azd", parsed["toSource"])
		// error and skipReason should be omitted
		_, hasError := parsed["error"]
		assert.False(t, hasError)
		_, hasSkip := parsed["skipReason"]
		assert.False(t, hasSkip)
	})

	t.Run("failed_result_includes_error", func(t *testing.T) {
		t.Parallel()
		r := UpgradeResult{
			ExtensionId: "broken",
			FromVersion: "1.0.0",
			FromSource:  "dev",
			Status:      UpgradeStatusFailed,
			Error:       errors.New("network timeout"),
		}

		data, err := json.Marshal(r)
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(data, &parsed))

		assert.Equal(t, "broken", parsed["name"])
		assert.Equal(t, "failed", parsed["status"])
		assert.Equal(t, "network timeout", parsed["error"])
		// toVersion should be omitted when empty
		_, hasTo := parsed["toVersion"]
		assert.False(t, hasTo)
	})

	t.Run("skipped_result_includes_reason", func(t *testing.T) {
		t.Parallel()
		r := UpgradeResult{
			ExtensionId: "tools",
			FromVersion: "2.0.0",
			FromSource:  "azd",
			ToSource:    "azd",
			Status:      UpgradeStatusSkipped,
			SkipReason:  "already up to date",
		}

		data, err := json.Marshal(r)
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(data, &parsed))

		assert.Equal(t, "tools", parsed["name"])
		assert.Equal(t, "skipped", parsed["status"])
		assert.Equal(t, "already up to date", parsed["skipReason"])
	})

	t.Run("promoted_result", func(t *testing.T) {
		t.Parallel()
		r := UpgradeResult{
			ExtensionId: "terraform",
			FromVersion: "0.9.0",
			ToVersion:   "1.0.0",
			FromSource:  "dev",
			ToSource:    "azd",
			Status:      UpgradeStatusPromoted,
		}

		data, err := json.Marshal(r)
		require.NoError(t, err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(data, &parsed))

		assert.Equal(t, "terraform", parsed["name"])
		assert.Equal(t, "promoted", parsed["status"])
		assert.Equal(t, "dev", parsed["fromSource"])
		assert.Equal(t, "azd", parsed["toSource"])
	})
}

func TestNewUpgradeSummary(t *testing.T) {
	t.Parallel()

	t.Run("empty_results", func(t *testing.T) {
		t.Parallel()
		s := NewUpgradeSummary(nil)
		assert.Equal(t, 0, s.Total)
		assert.Equal(t, 0, s.Upgraded)
		assert.Equal(t, 0, s.Skipped)
		assert.Equal(t, 0, s.Promoted)
		assert.Equal(t, 0, s.Failed)
	})

	t.Run("mixed_results", func(t *testing.T) {
		t.Parallel()
		results := []UpgradeResult{
			{Status: UpgradeStatusUpgraded},
			{Status: UpgradeStatusUpgraded},
			{Status: UpgradeStatusSkipped},
			{Status: UpgradeStatusPromoted},
			{Status: UpgradeStatusFailed},
			{Status: UpgradeStatusFailed},
		}
		s := NewUpgradeSummary(results)
		assert.Equal(t, 6, s.Total)
		assert.Equal(t, 2, s.Upgraded)
		assert.Equal(t, 1, s.Skipped)
		assert.Equal(t, 1, s.Promoted)
		assert.Equal(t, 2, s.Failed)
	})

	t.Run("all_upgraded", func(t *testing.T) {
		t.Parallel()
		results := []UpgradeResult{
			{Status: UpgradeStatusUpgraded},
			{Status: UpgradeStatusUpgraded},
			{Status: UpgradeStatusUpgraded},
		}
		s := NewUpgradeSummary(results)
		assert.Equal(t, 3, s.Total)
		assert.Equal(t, 3, s.Upgraded)
		assert.Equal(t, 0, s.Failed)
	})

	t.Run("all_failed", func(t *testing.T) {
		t.Parallel()
		results := []UpgradeResult{
			{Status: UpgradeStatusFailed},
			{Status: UpgradeStatusFailed},
		}
		s := NewUpgradeSummary(results)
		assert.Equal(t, 2, s.Total)
		assert.Equal(t, 0, s.Upgraded)
		assert.Equal(t, 2, s.Failed)
	})

	t.Run("all_skipped", func(t *testing.T) {
		t.Parallel()
		results := []UpgradeResult{
			{Status: UpgradeStatusSkipped},
			{Status: UpgradeStatusSkipped},
		}
		s := NewUpgradeSummary(results)
		assert.Equal(t, 2, s.Total)
		assert.Equal(t, 2, s.Skipped)
		assert.Equal(t, 0, s.Failed)
	})
}

func TestUpgradeReport_MarshalJSON(t *testing.T) {
	t.Parallel()

	results := []UpgradeResult{
		{
			ExtensionId: "ai",
			FromVersion: "0.1.0",
			ToVersion:   "0.2.0",
			FromSource:  "azd",
			ToSource:    "azd",
			Status:      UpgradeStatusUpgraded,
		},
		{
			ExtensionId: "contoso",
			FromVersion: "1.0.0",
			FromSource:  "dev",
			Status:      UpgradeStatusFailed,
			Error:       errors.New("network timeout"),
		},
		{
			ExtensionId: "tools",
			FromVersion: "2.0.0",
			FromSource:  "azd",
			ToSource:    "azd",
			Status:      UpgradeStatusSkipped,
			SkipReason:  "already up to date",
		},
	}

	report := UpgradeReport{
		Extensions: results,
		Summary:    NewUpgradeSummary(results),
	}

	data, err := json.Marshal(report)
	require.NoError(t, err)

	var parsed struct {
		Extensions []map[string]any `json:"extensions"`
		Summary    struct {
			Total    int `json:"total"`
			Upgraded int `json:"upgraded"`
			Skipped  int `json:"skipped"`
			Promoted int `json:"promoted"`
			Failed   int `json:"failed"`
		} `json:"summary"`
	}
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Len(t, parsed.Extensions, 3)
	assert.Equal(t, 3, parsed.Summary.Total)
	assert.Equal(t, 1, parsed.Summary.Upgraded)
	assert.Equal(t, 1, parsed.Summary.Skipped)
	assert.Equal(t, 0, parsed.Summary.Promoted)
	assert.Equal(t, 1, parsed.Summary.Failed)

	// Verify individual extension entries
	assert.Equal(t, "ai", parsed.Extensions[0]["name"])
	assert.Equal(t, "upgraded", parsed.Extensions[0]["status"])
	assert.Equal(t, "contoso", parsed.Extensions[1]["name"])
	assert.Equal(t, "failed", parsed.Extensions[1]["status"])
	assert.Equal(t, "network timeout", parsed.Extensions[1]["error"])
	assert.Equal(t, "tools", parsed.Extensions[2]["name"])
	assert.Equal(t, "skipped", parsed.Extensions[2]["status"])
}
