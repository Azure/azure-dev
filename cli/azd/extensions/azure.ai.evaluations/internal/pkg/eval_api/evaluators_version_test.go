// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Evaluator versions are integers rendered as strings, so a lexical compare
// ranks "9" above "15". The live service already has evaluators at version 15
// and 17, so this is not hypothetical.
func TestPickLatestEvaluatorVersionIsNumeric(t *testing.T) {
	cases := []struct {
		name     string
		versions []string
		want     string
	}{
		{"single", []string{"1"}, "1"},
		{"ascending", []string{"1", "2", "3"}, "3"},
		{"unordered", []string{"3", "1", "2"}, "3"},
		{"double digits beat single", []string{"9", "15"}, "15"},
		{"realistic", []string{"1", "9", "10", "17", "2"}, "17"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := make([]EvaluatorSummary, 0, len(tc.versions))
			for _, v := range tc.versions {
				entries = append(entries, EvaluatorSummary{Name: "e", Version: v})
			}
			require.Equal(t, tc.want, pickLatestVersion(entries))
		})
	}
}

// A non-numeric version is only used when nothing numeric exists, so one odd
// entry cannot mask the real latest.
func TestPickLatestEvaluatorVersionHandlesNonNumeric(t *testing.T) {
	require.Equal(t, "2", pickLatestVersion([]EvaluatorSummary{
		{Version: "draft"}, {Version: "1"}, {Version: "2"},
	}))
	require.Equal(t, "draft", pickLatestVersion([]EvaluatorSummary{{Version: "draft"}}))
	require.Equal(t, "", pickLatestVersion(nil))
}
