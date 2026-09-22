// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func conversationItem(itemID string, conversationID any, status string) eval_api.OutputItem {
	return eval_api.OutputItem{
		ID: itemID, Status: status,
		DataSourceItem: map[string]any{"id": conversationID},
		Results: []eval_api.OutputResult{
			{Name: "quality", Score: 0, Passed: new(false)},
		},
	}
}

func TestObservedConversationOutputDeduplicatesIDsWithoutInventingStatus(t *testing.T) {
	items := []eval_api.OutputItem{
		conversationItem("1", "conv_completed", "completed"),
		conversationItem("2", "conv_completed", "completed"),
		conversationItem("3", "conv_failed", "failed"),
		conversationItem("4", "conv_errored", "errored"),
		conversationItem("5", "conv_error", "error"),
		conversationItem("6", "conv_mixed", "completed"),
		conversationItem("7", "conv_mixed", "failed"),
		conversationItem("8", "conv_mixed", "completed"),
		conversationItem("9", "conv_unknown", "new_service_state"),
		conversationItem("10", "conv_absent", ""),
		conversationItem("11", "conv_skipped", "skipped"),
		conversationItem("12", "conv_running", "running"),
		conversationItem("13", "", "completed"),
		conversationItem("14", " ", "completed"),
		conversationItem("15", nil, "completed"),
		conversationItem("16", 42, "completed"),
		{ID: "17", Status: "completed"},
	}
	assert.Equal(t, &conversationOutputSummary{
		complete: true, rows: 17, identified: 9,
		completed: 1, failed: 1, errored: 2, other: 5, unidentified: 5,
	}, summarizeConversationOutput(items))

	var out bytes.Buffer
	renderConversationOutput(&out, summarizeConversationOutput(items))
	assert.Contains(t, out.String(), "Across all 17 returned output rows")
	assert.Contains(t, out.String(), "Identified conversation IDs    9")
	assert.Contains(t, out.String(), "IDs with completed output      1")
	assert.Contains(t, out.String(), "Rows without conversation ID   5")
	assert.Contains(t, out.String(), "not a generation count, conversation-completion count, or quality verdict")
	assert.NotContains(t, out.String(), "conv_completed", "only counts are printed, not transcript identities")
}

func TestObservedConversationOutputDoesNotInferIdentityOrTurns(t *testing.T) {
	item := conversationItem("output_id_is_not_conversation_id", nil, "completed")
	item.DataSourceItem["test_case_id"] = "seed_scenario"
	item.DataSourceItem["messages"] = []any{
		map[string]any{"role": "system"},
		map[string]any{"role": "user"},
		map[string]any{"role": "assistant"},
		map[string]any{"role": "tool"},
	}
	assert.Equal(t, &conversationOutputSummary{
		complete: true, rows: 1, unidentified: 1,
	}, summarizeConversationOutput([]eval_api.OutputItem{item}))
	assert.Equal(t, &conversationOutputSummary{complete: true}, summarizeConversationOutput(nil))
}

func TestRunOutputObservationRequiresSuccessfulCompleteFetch(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail bool
	}{
		{"all pages returned", false},
		{"later page failed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Query().Get("after") {
				case "":
					assert.NoError(t, json.NewEncoder(w).Encode(eval_api.OutputItemList{
						Data: []eval_api.OutputItem{
							conversationItem("1", "conv_1", "completed"),
							conversationItem("2", "conv_1", "completed"),
						},
						HasMore: true, LastID: "2",
					}))
				case "2":
					if tc.fail {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					assert.NoError(t, json.NewEncoder(w).Encode(eval_api.OutputItemList{
						Data: []eval_api.OutputItem{conversationItem("3", "conv_2", "failed")},
					}))
				default:
					t.Errorf("unexpected page cursor: %q", r.URL.Query().Get("after"))
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(srv.Close)
			run := simulationReportingRun()
			before, err := json.Marshal(run)
			require.NoError(t, err)
			summary := evalContextFor(srv).runOutputSummary(t.Context(), run.EvalID, run)
			require.NotNil(t, summary)
			require.NotNil(t, summary.conversations)
			assert.Equal(t, 2, calls, "reuse the same page walk used for mean scores")
			var out bytes.Buffer
			require.NoError(t, renderRun(&out, run, summary))
			if tc.fail {
				assert.False(t, summary.conversations.complete)
				assert.Contains(t, out.String(), "the complete output listing could not be read")
				assert.NotContains(t, out.String(), "Identified conversation IDs")
				assert.Nil(t, summary.means)
			} else {
				assert.Equal(t, &conversationOutputSummary{
					complete: true, rows: 3, identified: 2, completed: 1, failed: 1,
				}, summary.conversations)
				assert.Equal(t, 0.0, summary.means["quality"])
				assert.Contains(t, out.String(), "Conversations generated  not reported")
				assert.Contains(t, out.String(), "Conversation turns       not reported")
				assert.Contains(t, out.String(), "OBSERVED CONVERSATION OUTPUT")
			}
			after, err := json.Marshal(run)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "observations must not alter the service run or JSON")
		})
	}
}

func TestObservedConversationOutputIsNotDerivedFromPagedOrFilteredListings(t *testing.T) {
	run := simulationReportingRun()
	items := []eval_api.OutputItem{conversationItem("1", "conv_1", "completed")}
	for _, failedOnly := range []bool{false, true} {
		var out bytes.Buffer
		require.NoError(t, renderResults(&out, run.EvalID, run, items, failedOnly))
		assert.NotContains(t, out.String(), "OBSERVED CONVERSATION OUTPUT")
	}
	var out bytes.Buffer
	require.NoError(t, renderRunDetail(&out, run))
	assert.NotContains(t, out.String(), "OBSERVED CONVERSATION OUTPUT",
		"run show has not fetched output rows")
	out.Reset()
	require.NoError(t, renderRun(&out, finishedRun(), &runOutputSummary{
		conversations: summarizeConversationOutput(items),
	}))
	assert.NotContains(t, out.String(), "OBSERVED CONVERSATION OUTPUT", "other run modes keep their existing output")
}
