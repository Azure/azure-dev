// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A run reattached by id repeats the data source the last one sent, so an eval
// whose last run predates the preview shape would keep sending the old one for
// good. The old shape carried no agent version, which is the whole reason to
// move off it.
func TestUpgradeLegacyTraceSource_MovesToThePreviewShape(t *testing.T) {
	ds := upgradeLegacyTraceSource(&eval_api.EvalRunDataSource{
		Type:          eval_api.EvalRunDataSourceTypeTraces,
		AgentName:     "support-agent",
		LookbackHours: 24,
		MaxTraces:     500,
	})

	require.NotNil(t, ds.TraceSource)
	assert.Equal(t, eval_api.EvalRunDataSourceTypeTracePreview, ds.Type)
	assert.Equal(t, "agent_filter", ds.TraceSource.Type)
	assert.Equal(t, "support-agent", ds.TraceSource.AgentName)
	assert.Equal(t, 500, ds.TraceSource.MaxTraces)
	assert.InDelta(t, time.Now().Add(-24*time.Hour).Unix(), ds.TraceSource.StartTime, 60)
	// Nothing was pinned before, so nothing is pinned now: the upgrade must not
	// invent a version the previous runs were never graded against.
	assert.Empty(t, ds.TraceSource.AgentVersion)
}

// The old shape said "the last n hours" and was re-read on every run. The new
// one has no lookback, so an upgrade that left the end open would replay the
// same start against a later now on each reattach and grade a wider span than
// the run before it, without limit and without saying so. Both ends are pinned,
// which keeps the length of the window the run was actually graded over.
func TestUpgradeLegacyTraceSource_PinsBothEndsSoItCannotWiden(t *testing.T) {
	ds := upgradeLegacyTraceSource(&eval_api.EvalRunDataSource{
		Type:          eval_api.EvalRunDataSourceTypeTraces,
		AgentName:     "support-agent",
		LookbackHours: 24,
	})

	require.NotNil(t, ds.TraceSource)
	assert.InDelta(t, time.Now().Unix(), ds.TraceSource.EndTime, 60)
	assert.Equal(t, int64(24*3600), ds.TraceSource.EndTime-ds.TraceSource.StartTime)

	// Upgrading the result again is a no-op, so the length cannot creep.
	again := upgradeLegacyTraceSource(ds)
	assert.Same(t, ds, again)
}

// The old shape had no start bound, so a run that set no lookback was graded
// over whatever the service chose. Carrying it forward with no start would
// widen it to all of history instead.
func TestUpgradeLegacyTraceSource_KeepsTheWindowItRanUnder(t *testing.T) {
	ds := upgradeLegacyTraceSource(&eval_api.EvalRunDataSource{
		Type:      eval_api.EvalRunDataSourceTypeTraces,
		AgentName: "support-agent",
	})

	require.NotNil(t, ds.TraceSource)
	assert.Equal(t, int64(24*7*3600), ds.TraceSource.EndTime-ds.TraceSource.StartTime)
}

// The recorded value comes from an older build, from before the bound existed.
// A lookback large enough to overflow the duration it becomes would put the
// start in the future, and the reattached run would read nothing.
func TestUpgradeLegacyTraceSource_ClampsWhatAnOlderBuildRecorded(t *testing.T) {
	ds := upgradeLegacyTraceSource(&eval_api.EvalRunDataSource{
		Type:          eval_api.EvalRunDataSourceTypeTraces,
		AgentName:     "support-agent",
		LookbackHours: 100000000,
		MaxTraces:     -5,
	})

	require.NotNil(t, ds.TraceSource)
	assert.Greater(t, ds.TraceSource.EndTime, ds.TraceSource.StartTime,
		"an overflowed lookback puts the start after the end")
	assert.Equal(t, int64(24*7*3600), ds.TraceSource.EndTime-ds.TraceSource.StartTime)
	assert.Zero(t, ds.TraceSource.MaxTraces, "a negative cap is dropped, not forwarded")
}

// An end bound anchors the window it closes, rather than being read alongside a
// start measured from now: a run that ended a month ago covered the week before
// that, not the week before today.
func TestUpgradeLegacyTraceSource_MeasuresBackFromTheEnd(t *testing.T) {
	end := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)

	ds := upgradeLegacyTraceSource(&eval_api.EvalRunDataSource{
		Type:          eval_api.EvalRunDataSourceTypeTraces,
		AgentName:     "support-agent",
		LookbackHours: 24,
		EndTime:       end.Unix(),
	})

	require.NotNil(t, ds.TraceSource)
	assert.Equal(t, end.Unix(), ds.TraceSource.EndTime)
	assert.Equal(t, end.Add(-24*time.Hour).Unix(), ds.TraceSource.StartTime)
}

// Only the legacy shape is rewritten. Anything else is repeated exactly, so a
// source this extension has never heard of is not quietly replaced with one it
// made up.
func TestUpgradeLegacyTraceSource_LeavesEverythingElseAlone(t *testing.T) {
	assert.Nil(t, upgradeLegacyTraceSource(nil))

	preview := &eval_api.EvalRunDataSource{
		Type:        eval_api.EvalRunDataSourceTypeTracePreview,
		TraceSource: &eval_api.TraceSourceFilter{Type: "agent_filter", AgentName: "a", StartTime: 7},
	}
	assert.Same(t, preview, upgradeLegacyTraceSource(preview))

	jsonl := &eval_api.EvalRunDataSource{Type: eval_api.EvalRunDataSourceTypeJSONL}
	assert.Same(t, jsonl, upgradeLegacyTraceSource(jsonl))
}
