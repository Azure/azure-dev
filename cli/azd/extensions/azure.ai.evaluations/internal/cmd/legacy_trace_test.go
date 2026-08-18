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
	assert.Zero(t, ds.TraceSource.EndTime)
	// Nothing was pinned before, so nothing is pinned now: the upgrade must not
	// invent a version the previous runs were never graded against.
	assert.Empty(t, ds.TraceSource.AgentVersion)
}

// The old shape had no start bound at all: the service applied its own default
// of seven days. Carrying such a run forward with an open start would widen it
// to all of history, grading a different set of traces than the run being
// repeated and making the query far more expensive, with nothing to say so.
func TestUpgradeLegacyTraceSource_KeepsTheWindowItRanUnder(t *testing.T) {
	ds := upgradeLegacyTraceSource(&eval_api.EvalRunDataSource{
		Type:      eval_api.EvalRunDataSourceTypeTraces,
		AgentName: "support-agent",
	})

	require.NotNil(t, ds.TraceSource)
	assert.InDelta(t, time.Now().Add(-24*7*time.Hour).Unix(), ds.TraceSource.StartTime, 60)
	assert.Zero(t, ds.TraceSource.EndTime)
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
