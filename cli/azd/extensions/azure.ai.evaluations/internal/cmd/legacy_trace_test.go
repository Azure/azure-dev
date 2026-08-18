// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A run reattached by id repeats the data source the last one sent, so an eval
// whose last run predates the preview shape would keep sending the old one for
// good. The old shape carried no agent version, which is the whole reason to
// move off it.
func TestPinReusedTraceWindow_MovesTheLegacyShapeOn(t *testing.T) {
	ds := pinReusedTraceWindow(&eval_api.EvalRunDataSource{
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

// A window with a start and no end means "up to now", so replaying it a week
// later grades a week more than the run it was copied from, and the run after
// that more again. Both ends are written down, whatever shape the window
// arrived in, so the span cannot grow with each reattach.
func TestPinReusedTraceWindow_ClosesAWindowSoItCannotWiden(t *testing.T) {
	cases := []struct {
		name string
		ds   *eval_api.EvalRunDataSource
	}{
		{
			"a legacy source",
			&eval_api.EvalRunDataSource{
				Type:          eval_api.EvalRunDataSourceTypeTraces,
				AgentName:     "support-agent",
				LookbackHours: 24,
			},
		},
		{
			// The shape this extension writes today. Pinning only the legacy
			// one left the current one growing in exactly the way the legacy
			// handling exists to prevent.
			"a source this extension wrote",
			&eval_api.EvalRunDataSource{
				Type: eval_api.EvalRunDataSourceTypeTracePreview,
				TraceSource: &eval_api.TraceSourceFilter{
					Type:      "agent_filter",
					AgentName: "support-agent",
					StartTime: time.Now().Add(-24 * time.Hour).Unix(),
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := pinReusedTraceWindow(tc.ds)

			require.NotNil(t, ds.TraceSource)
			assert.InDelta(t, time.Now().Unix(), ds.TraceSource.EndTime, 60)
			assert.InDelta(t, int64(24*3600), ds.TraceSource.EndTime-ds.TraceSource.StartTime, 60)

			// Reusing the result again changes nothing, so the span cannot
			// creep run after run.
			pinned := *ds.TraceSource
			again := pinReusedTraceWindow(ds)
			assert.Equal(t, pinned, *again.TraceSource)
		})
	}
}

// The old shape had no start bound, so a run that set no lookback was graded
// over whatever the service chose. Carrying it forward with no start would
// widen it to all of history instead.
func TestPinReusedTraceWindow_KeepsTheWindowALegacyRunRanUnder(t *testing.T) {
	ds := pinReusedTraceWindow(&eval_api.EvalRunDataSource{
		Type:      eval_api.EvalRunDataSourceTypeTraces,
		AgentName: "support-agent",
	})

	require.NotNil(t, ds.TraceSource)
	assert.Equal(t, int64(24*7*3600), ds.TraceSource.EndTime-ds.TraceSource.StartTime)
}

// The recorded values come from an older build, from before the bounds existed.
// A lookback past the bound reaches back further than a window may cover, and a
// negative cap is no cap at all: left as zero it is dropped from the request,
// which means the service's own default of a thousand traces -- a bigger and
// costlier run than the one being repeated.
func TestPinReusedTraceWindow_ClampsWhatAnOlderBuildRecorded(t *testing.T) {
	ds := pinReusedTraceWindow(&eval_api.EvalRunDataSource{
		Type:          eval_api.EvalRunDataSourceTypeTraces,
		AgentName:     "support-agent",
		LookbackHours: project.MaxLookbackHours + 1,
		MaxTraces:     -5,
	})

	require.NotNil(t, ds.TraceSource)
	assert.Greater(t, ds.TraceSource.EndTime, ds.TraceSource.StartTime)
	assert.Equal(t, int64(24*7*3600), ds.TraceSource.EndTime-ds.TraceSource.StartTime)
	assert.Equal(t, project.DefaultScaffoldMaxTraces, ds.TraceSource.MaxTraces,
		"a bounded cap, rather than none at all")
}

// An end bound anchors the window it closes, rather than being read alongside a
// start measured from now: a run that ended a month ago covered the week before
// that, not the week before today.
func TestPinReusedTraceWindow_MeasuresBackFromTheEnd(t *testing.T) {
	end := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)

	ds := pinReusedTraceWindow(&eval_api.EvalRunDataSource{
		Type:          eval_api.EvalRunDataSourceTypeTraces,
		AgentName:     "support-agent",
		LookbackHours: 24,
		EndTime:       end.Unix(),
	})

	require.NotNil(t, ds.TraceSource)
	assert.Equal(t, end.Unix(), ds.TraceSource.EndTime)
	assert.Equal(t, end.Add(-24*time.Hour).Unix(), ds.TraceSource.StartTime)
}

// Anything that is not an open trace window is repeated exactly, so a source
// this extension has never heard of is not quietly replaced with one it made up.
func TestPinReusedTraceWindow_LeavesEverythingElseAlone(t *testing.T) {
	assert.Nil(t, pinReusedTraceWindow(nil))

	closed := &eval_api.EvalRunDataSource{
		Type: eval_api.EvalRunDataSourceTypeTracePreview,
		TraceSource: &eval_api.TraceSourceFilter{
			Type: "agent_filter", AgentName: "a", StartTime: 7, EndTime: 8,
		},
	}
	assert.Same(t, closed, pinReusedTraceWindow(closed))

	jsonl := &eval_api.EvalRunDataSource{Type: eval_api.EvalRunDataSourceTypeJSONL}
	assert.Same(t, jsonl, pinReusedTraceWindow(jsonl))
}
