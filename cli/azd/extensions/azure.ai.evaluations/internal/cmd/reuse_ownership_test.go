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

// What the previous run sent is history. A caller that logs it, or emits it
// under -o json, has to see what was recorded rather than what this run decided
// to send instead.
func TestPinReusedTraceWindow_DoesNotTouchWhatItWasGiven(t *testing.T) {
	recorded := &eval_api.EvalRunDataSource{
		Type: eval_api.EvalRunDataSourceTypeTracePreview,
		TraceSource: &eval_api.TraceSourceFilter{
			Type:      "agent_filter",
			AgentName: "support-agent",
			StartTime: time.Now().Add(-24 * time.Hour).Unix(),
		},
	}
	before := *recorded.TraceSource

	pinned := pinReusedTraceWindow(recorded)

	require.NotSame(t, recorded, pinned)
	assert.Equal(t, before, *recorded.TraceSource, "the recorded source is unchanged")
	assert.NotZero(t, pinned.TraceSource.EndTime)
}

// A window with no start says "everything", which is what it said when it was
// recorded. Closing it would freeze a declaration that never asked to be
// bounded, and each reattach would then grade a staler span than the last.
func TestPinReusedTraceWindow_LeavesAnUnboundedWindowUnbounded(t *testing.T) {
	open := &eval_api.EvalRunDataSource{
		Type: eval_api.EvalRunDataSourceTypeTracePreview,
		TraceSource: &eval_api.TraceSourceFilter{
			Type: "agent_filter", AgentName: "support-agent",
		},
	}

	assert.Same(t, open, pinReusedTraceWindow(open))
	assert.Zero(t, open.TraceSource.EndTime)
	assert.Zero(t, open.TraceSource.StartTime)
}

// A recorded end early enough to put the start at or before the epoch would
// send a bound the wire drops, or a negative one, which is what a declaration
// is refused for. The length of the window is kept and reached back from now.
func TestPinReusedTraceWindow_KeepsAReattachedStartOutOfThePreEpoch(t *testing.T) {
	ds := pinReusedTraceWindow(&eval_api.EvalRunDataSource{
		Type:          eval_api.EvalRunDataSourceTypeTraces,
		AgentName:     "support-agent",
		LookbackHours: 24,
		EndTime:       3600,
	})

	require.NotNil(t, ds.TraceSource)
	assert.Positive(t, ds.TraceSource.StartTime)
	assert.Equal(t, int64(24*3600), ds.TraceSource.EndTime-ds.TraceSource.StartTime)
}
