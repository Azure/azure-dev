// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/require"
)

func TestInsightTerminalStates(t *testing.T) {
	for _, state := range []string{"", "NotStarted", "Running", "InProgress", "Queued"} {
		require.False(t, (&eval_api.Insight{State: state}).Terminal(), "%q is not terminal", state)
	}
	for _, state := range []string{"Succeeded", "Failed", "Cancelled"} {
		require.True(t, (&eval_api.Insight{State: state}).Terminal(), "%q is terminal", state)
	}
	require.True(t, (&eval_api.Insight{State: "Succeeded"}).Succeeded())
	require.False(t, (&eval_api.Insight{State: "Failed"}).Succeeded())
}

// The rendered table is how a reader decides whether a change helped, so the
// delta carries its sign and the effect classification is not dropped.
func TestRenderComparisonShowsSignedDeltaAndEffect(t *testing.T) {
	insight := &eval_api.Insight{
		State: "Succeeded",
		Result: &eval_api.InsightResult{
			Method: "PairedTTest",
			Comparisons: []eval_api.MetricComparison{{
				Metric:             "task_adherence",
				BaselineRunSummary: &eval_api.RunSummary{RunID: "base", Average: 0.75},
				CompareItems: []eval_api.CompareItem{{
					TreatmentRunSummary: &eval_api.RunSummary{RunID: "treat", Average: 0.5},
					DeltaEstimate:       -0.25,
					PValue:              0.39,
					TreatmentEffect:     "TooFewSamples",
				}},
			}},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, renderComparison(&buf, insight))
	out := buf.String()

	require.Contains(t, out, "PairedTTest", "the statistical method qualifies the result")
	require.Contains(t, out, "task_adherence")
	require.Contains(t, out, "-0.250", "a regression must read as negative")
	require.Contains(t, out, "0.390")
	require.Contains(t, out, "TooFewSamples",
		"an inconclusive result must not look like a finding")
}

// A positive delta reads as an improvement rather than an unsigned number.
func TestRenderComparisonSignsImprovements(t *testing.T) {
	insight := &eval_api.Insight{
		State: "Succeeded",
		Result: &eval_api.InsightResult{
			Comparisons: []eval_api.MetricComparison{{
				Metric:             "similarity",
				BaselineRunSummary: &eval_api.RunSummary{Average: 0.5},
				CompareItems: []eval_api.CompareItem{{
					TreatmentRunSummary: &eval_api.RunSummary{RunID: "t", Average: 0.8},
					DeltaEstimate:       0.3,
				}},
			}},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, renderComparison(&buf, insight))
	require.Contains(t, buf.String(), "+0.300")
}

func TestRenderComparisonHandlesEmptyResult(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, renderComparison(&buf, &eval_api.Insight{State: "Succeeded"}))
	require.Contains(t, buf.String(), "no metrics")
}
