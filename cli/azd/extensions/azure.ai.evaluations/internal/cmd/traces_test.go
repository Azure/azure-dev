// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A window is only sent when one was asked for; the service defaults it
// otherwise.
func TestNewTracesDataSource_OmitsAnUnsetWindow(t *testing.T) {
	ds := eval_api.NewTracesDataSource("support-agent", 0, time.Time{}, 0)
	assert.Equal(t, eval_api.EvalRunDataSourceTypeTraces, ds.Type)
	assert.Equal(t, "support-agent", ds.AgentName)

	raw, err := json.Marshal(ds)
	require.NoError(t, err)
	body := string(raw)
	assert.NotContains(t, body, "lookback_hours")
	assert.NotContains(t, body, "end_time")
	assert.NotContains(t, body, "max_traces")
	assert.NotContains(t, body, "input_messages", "traces carry no template")
}

// The service reads `lookback_hours` and has no start bound. Sending a
// start_time is accepted and dropped, which silently leaves the default seven
// days in place, so the window has to travel as hours.
func TestNewTracesDataSource_SendsAWindowTheServiceReads(t *testing.T) {
	ds := eval_api.NewTracesDataSource("support-agent", 30*24, time.Time{}, 25)
	assert.Equal(t, 720, ds.LookbackHours)
	assert.Equal(t, 25, ds.MaxTraces)

	raw, err := json.Marshal(ds)
	require.NoError(t, err)
	body := string(raw)
	assert.Contains(t, body, `"lookback_hours":720`)
	assert.NotContains(t, body, "start_time",
		"the service drops start_time and falls back to its default window")
}

// The reason a run failed is the only actionable part of the response, so it
// has to survive into the output.
func TestRunFailureMessage(t *testing.T) {
	var run eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{
      "id": "evalrun_x", "status": "failed",
      "error": { "code": "UserError", "message": "  No trace data found for agent_name 'a'.  " }
    }`), &run))
	assert.Equal(t, "No trace data found for agent_name 'a'.", run.Failure())

	// The field is present and null-valued on success, so presence alone
	// must not read as failure.
	var ok eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{
      "id": "evalrun_y", "status": "completed",
      "error": { "code": null, "message": null }
    }`), &ok))
	assert.Empty(t, ok.Failure())

	var absent eval_api.OpenAIEvalRun
	require.NoError(t, json.Unmarshal([]byte(`{"id":"evalrun_z","status":"completed"}`), &absent))
	assert.Empty(t, absent.Failure())

	var nilRun *eval_api.OpenAIEvalRun
	assert.Empty(t, nilRun.Failure())
}

// The ids travel as ordinary JSONL rows with a mapping pointing at the field
// that holds each one; that is how the service finds the chat history.
func TestNewResponsesDataSource(t *testing.T) {
	ds := eval_api.NewResponsesDataSource([]string{"resp_a", "resp_b"}, 10)
	assert.Equal(t, eval_api.EvalRunDataSourceTypeResponses, ds.Type)
	require.NotNil(t, ds.ItemGenerationParams)
	assert.Equal(t, "response_retrieval", ds.ItemGenerationParams.Type)
	assert.Equal(t, 10, ds.ItemGenerationParams.MaxNumTurns)
	assert.Equal(t,
		map[string]string{"response_id": "{{item.response_id}}"},
		ds.ItemGenerationParams.DataMapping)

	raw, err := json.Marshal(ds)
	require.NoError(t, err)
	body := string(raw)
	assert.Contains(t, body, `"response_id":"resp_a"`)
	assert.Contains(t, body, `"response_id":"resp_b"`)
	assert.NotContains(t, body, "agent_name", "responses carry no agent")
}

// An unset turn limit is left to the service rather than sent as zero.
func TestNewResponsesDataSource_OmitsAnUnsetTurnLimit(t *testing.T) {
	ds := eval_api.NewResponsesDataSource([]string{"resp_a"}, 0)
	raw, err := json.Marshal(ds)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "max_num_turns")
}

func TestRenderRun_ShowsTheFailureReason(t *testing.T) {
	var buf bytes.Buffer
	run := &eval_api.OpenAIEvalRun{
		ID:     "evalrun_x",
		Status: "failed",
		Error:  &eval_api.JobError{Code: "UserError", Message: "No trace data found."},
	}
	require.NoError(t, renderRun(&buf, run))
	assert.Contains(t, buf.String(), "failed")
	assert.Contains(t, buf.String(), "No trace data found.")

	var clean bytes.Buffer
	require.NoError(t, renderRun(&clean, &eval_api.OpenAIEvalRun{ID: "evalrun_y", Status: "completed"}))
	assert.NotContains(t, clean.String(), "  \n", "a successful run gains no blank reason line")
}
