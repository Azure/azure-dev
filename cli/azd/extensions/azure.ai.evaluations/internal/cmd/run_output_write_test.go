// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The export is one document holding the run and every row beneath it.
//
// It used to be three formats that were not serializations of one another: CSV
// and JSONL emitted a per-evaluator summary of passed and failed only, while
// JSON emitted the run object. None of them carried the evaluated rows, so a
// run with errored or skipped results exported numbers that did not add up to
// it, and the per-sample reasons existed in no format at all.
func TestExportCarriesTheRunAndItsItems(t *testing.T) {
	doc := exportDocument{
		Run: json.RawMessage(`{"id":"evalrun_abc","status":"completed",` +
			`"result_counts":{"total":3,"passed":1,"failed":1,"errored":1,"skipped":0}}`),
		Items: []json.RawMessage{
			json.RawMessage(`{"id":"1","status":"completed"}`),
			json.RawMessage(`{"id":"2","status":"completed"}`),
		},
	}

	var buf bytes.Buffer
	require.NoError(t, writeExport(&buf, doc))

	var out struct {
		Run struct {
			ID           string `json:"id"`
			ResultCounts struct {
				Errored int `json:"errored"`
				Skipped int `json:"skipped"`
			} `json:"result_counts"`
		} `json:"run"`
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out),
		"the export has to be one parseable document:\n%s", buf.String())

	assert.Equal(t, "evalrun_abc", out.Run.ID)
	assert.Equal(t, 1, out.Run.ResultCounts.Errored,
		"errored is the count the flat formats dropped")
	assert.Len(t, out.Items, 2, "the rows are the part no format used to carry")
}

// Unknown service fields survive the round trip.
//
// The typed models decode what the CLI renders, so exporting through them
// silently dropped job logs, per-model usage, durations and anything the
// service added after this client was written. An export is the machine path,
// so it hands back what arrived.
func TestExportPreservesFieldsTheModelsDoNotDecode(t *testing.T) {
	doc := exportDocument{
		Run: json.RawMessage(`{"id":"evalrun_abc","duration":"3m08s",` +
			`"per_model_usage":[{"model_name":"gpt-4o","total_tokens":812}],` +
			`"properties":{"job_logs":"https://example/logs"}}`),
		Items: []json.RawMessage{
			json.RawMessage(`{"id":"1","datasource_item":{"nested":{"deep":[1,2,3]}}}`),
		},
	}

	var buf bytes.Buffer
	require.NoError(t, writeExport(&buf, doc))
	text := buf.String()

	for _, field := range []string{"duration", "per_model_usage", "job_logs", "datasource_item", "deep"} {
		assert.Containsf(t, text, field,
			"a field the models do not decode was dropped by the export: %s", field)
	}
}

// A run that produced no rows still exports a document with an empty list, so a
// consumer can iterate it without a nil check and can tell an empty run from a
// failed export.
func TestExportOfARunWithNoItems(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeExport(&buf, exportDocument{
		Run:   json.RawMessage(`{"id":"evalrun_empty","status":"failed"}`),
		Items: []json.RawMessage{},
	}))

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	items, ok := out["items"].([]any)
	require.True(t, ok, "items must be a list even when the run graded nothing")
	assert.Empty(t, items)
}

// JSON is the only format, and the help has to say so rather than offering
// spellings the command will refuse.
func TestExportOffersOneFormat(t *testing.T) {
	assert.Equal(t, "json", formatJSON)

	usage := find(t, "run output export").Flags().Lookup("format")
	require.NotNil(t, usage)
	assert.Equal(t, formatJSON, usage.DefValue)
	assert.Contains(t, usage.Usage, formatJSON)

	// The flat formats are gone deliberately: they exported a different object,
	// not a different encoding of this one.
	for _, dropped := range []string{"csv", "jsonl"} {
		assert.NotContainsf(t, usage.Usage, dropped,
			"--format still advertises %q", dropped)
	}
}
