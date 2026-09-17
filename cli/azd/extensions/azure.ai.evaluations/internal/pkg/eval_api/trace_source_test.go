// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The service reads the window and the cap from inside trace_source and
// ignores them beside the data source type. Verified live: a run submitted with
// them at the top level came back with the nested ones null, so the caller's
// window was dropped without a word -- the same defect the legacy
// azure_ai_traces shape has, moved one level in.
func TestTracePreviewNestsEverythingInsideTheFilter(t *testing.T) {
	ds := NewTracePreviewDataSource(
		"support-agent", "2",
		time.Unix(1785542400, 0), time.Unix(1785628800, 0),
		20,
	)

	raw, err := json.Marshal(ds)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	assert.Equal(t, "azure_ai_trace_data_source_preview", got["type"])
	assert.NotContains(t, got, "start_time", "the service ignores it here")
	assert.NotContains(t, got, "end_time", "the service ignores it here")
	assert.NotContains(t, got, "max_traces", "the service ignores it here")

	filter, ok := got["trace_source"].(map[string]any)
	require.True(t, ok, "trace_source has to be an object")
	assert.Equal(t, "agent_filter", filter["type"])
	assert.Equal(t, "support-agent", filter["agent_name"])
	assert.Equal(t, "2", filter["agent_version"])
	assert.EqualValues(t, 1785542400, filter["start_time"])
	assert.EqualValues(t, 1785628800, filter["end_time"])
	assert.EqualValues(t, 20, filter["max_traces"])
}

// An unpinned version and an open window are omitted rather than sent as zero,
// which the service would read as a bound of 1970.
func TestTracePreviewOmitsWhatWasNotAskedFor(t *testing.T) {
	ds := NewTracePreviewDataSource("support-agent", "", time.Time{}, time.Time{}, 0)

	raw, err := json.Marshal(ds)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	filter, ok := got["trace_source"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "support-agent", filter["agent_name"])
	assert.NotContains(t, filter, "agent_version")
	assert.NotContains(t, filter, "start_time")
	assert.NotContains(t, filter, "end_time")
	assert.NotContains(t, filter, "max_traces")
}
