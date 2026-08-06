// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
)

// Scenario 3 answers "did my change help?" by reading two rows of `run list`,
// which only works if a row carries when it ran and how it scored. The columns
// were RUN ID / NAME / STATUS / RESULTS, so the question the scenario exists to
// answer could not be.
func TestRunListColumnsMatchTheScenario(t *testing.T) {
	counts := &eval_api.EvalRunResultCounts{Total: 15, Passed: 14, Failed: 1}

	assert.Equal(t, "15", sampleCount(counts),
		"a rate over 15 samples and one over 200 are not the same claim")
	assert.Equal(t, "93.3%", runPassRate(counts),
		"the scenario compares 80.0% against 93.3%, so the row has to carry the rate")
}

// The rate is the gate's arithmetic: passed over total, with errored and
// skipped inside the total. A row a reader gates on must not disagree with the
// gate that acts on it.
func TestRunListPassRateAgreesWithTheGate(t *testing.T) {
	counts := &eval_api.EvalRunResultCounts{Total: 3, Passed: 2, Errored: 1}

	assert.Equal(t, "66.7%", runPassRate(counts),
		"an errored row is not a pass, here or in the gate")

	g, err := parseGate("pass-rate=0.8")
	assert.NoError(t, err)
	assert.NotEmpty(t, g.breach(counts),
		"the same counts that read 66.7% must breach an 80% threshold")
}

// A run that has not scored yet has no rate to show. An empty cell says that;
// "0.0%" would say the run failed.
func TestRunListOmitsARateItCannotCompute(t *testing.T) {
	assert.Empty(t, runPassRate(nil))
	assert.Empty(t, runPassRate(&eval_api.EvalRunResultCounts{}))
	assert.Empty(t, sampleCount(nil))
}

// Timestamps are RFC3339 in UTC, whichever shape the service sent. The service
// answers with epoch seconds on some routes and a string on others, and a list
// that renders both would not sort.
func TestRunListTimestampsAreRFC3339UTC(t *testing.T) {
	assert.Equal(t, "2026-08-01T09:15:22Z", timestampString(float64(1785575722)))
	assert.Equal(t, "2026-08-01T09:15:22Z", timestampString(int64(1785575722)))
	assert.Equal(t, "2026-08-01T09:15:22Z", timestampString("2026-08-01T09:15:22Z"))
	assert.Empty(t, timestampString(nil))
}
