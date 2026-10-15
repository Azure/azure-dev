// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/require"
)

// authoredValues is cfg as the file a user would have written for it.
//
// The structs carry the encoding an eval's fingerprint is computed from in their
// json tags, which is snake_case and must stay as it is, and the authored keys
// in their yaml tags. Marshalling through yaml drops an explicitly empty list the
// tests need to keep, so this goes through json and spells the authored keys the
// way the file does.
func authoredValues(t *testing.T, cfg *project.EvalConfig) map[string]any {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	var values map[string]any
	require.NoError(t, json.Unmarshal(raw, &values))

	rename := func(object any, keys map[string]string) {
		entry, ok := object.(map[string]any)
		if !ok {
			return
		}
		for from, to := range keys {
			if value, present := entry[from]; present {
				entry[to] = value
				delete(entry, from)
			}
		}
	}
	each := func(list any, visit func(entry any)) {
		items, _ := list.([]any)
		for _, item := range items {
			visit(item)
		}
	}
	each(values["evaluators"], func(entry any) {
		rename(entry, map[string]string{
			"display_name": "displayName", "supported_evaluation_levels": "supportedEvaluationLevels",
		})
	})
	each(values["evals"], func(entry any) {
		rename(entry, map[string]string{"evaluation_level": "evaluationLevel", "max_samples": "maxSamples"})
		eval, _ := entry.(map[string]any)
		each(eval["evaluators"], func(ref any) {
			rename(ref, map[string]string{
				"initialization_parameters": "initializationParameters", "data_mapping": "dataMapping",
			})
		})
		rename(eval["source"], map[string]string{
			"lookback_hours": "lookbackHours", "max_traces": "maxTraces", "agent_name": "agentName",
			"agent_version": "agentVersion", "response_ids": "responseIds", "max_turns": "maxTurns",
			"start_time": "startTime", "end_time": "endTime",
		})
		rename(eval["simulation"], map[string]string{"num_conversations": "numConversations", "max_turns": "maxTurns"})
	})
	return values
}
