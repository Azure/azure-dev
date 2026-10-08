// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A skip and a failure to run are different things, and the service says which.
//
// Both used to decode to "not judged" and be counted in one bucket, so a row
// the service deliberately declined to grade was reported as an infrastructure
// error -- and the reader was sent to retry a run that had nothing to retry.
func TestSkippedIsNotErrored(t *testing.T) {
	assert.Equal(t, ResultSkipped, OutputResult{Name: "relevance", Status: "skipped"}.Outcome(),
		"the service said it skipped this one")
	assert.Equal(t, ResultSkipped, OutputResult{Name: "relevance", Label: "skipped"}.Outcome(),
		"and says the same thing through label on the evaluators that use it")

	errored := OutputResult{Name: "relevance", Status: "error"}
	assert.Equal(t, ResultErrored, errored.Outcome(),
		"an evaluator that failed to run is not one that declined to")
	assert.Equal(t, ResultErrored, OutputResult{Name: "relevance", Status: "errored"}.Outcome(),
		"spelled either way")
}

// A recorded call failure outranks whatever verdict came with it.
//
// The service sends `passed: false` alongside a sample error, because a call
// that never produced a judgement did not produce a passing one either. Reading
// the boolean first named the evaluator as the thing that judged badly rather
// than the thing that never ran.
func TestASampleErrorOutranksTheVerdict(t *testing.T) {
	r := OutputResult{
		Name:   "relevance",
		Passed: new(false),
		Sample: &OutputSample{Error: &SampleError{Code: "FAILED_EXECUTION", Message: "boom"}},
	}
	assert.Equal(t, ResultErrored, r.Outcome())
	assert.Equal(t, "FAILED_EXECUTION", r.SampleError().Code,
		"and the code is reachable, because it is what the reader came for")
}

// An empty sample error is not one. The service sends the envelope either way.
func TestAnEmptySampleErrorIsNotAFailure(t *testing.T) {
	r := OutputResult{Name: "relevance", Passed: new(true), Sample: &OutputSample{Error: &SampleError{}}}
	assert.Nil(t, r.SampleError(), "no code and no message is nothing recorded")
	assert.Equal(t, ResultPassed, r.Outcome(),
		"so the verdict it did send is what the result amounts to")
}

// Verdicts arrive as a boolean or as a label, and both have to read the same.
func TestAVerdictIsReadFromEitherField(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   OutputResult
		want string
	}{
		{"passed bool", OutputResult{Passed: new(true)}, ResultPassed},
		{"failed bool", OutputResult{Passed: new(false)}, ResultFailed},
		{"pass label", OutputResult{Label: "pass"}, ResultPassed},
		{"passed label", OutputResult{Label: "passed"}, ResultPassed},
		{"fail label", OutputResult{Label: "fail"}, ResultFailed},
		{"failed label", OutputResult{Label: "failed"}, ResultFailed},
		{"upper and padded", OutputResult{Label: "  FAIL "}, ResultFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.in.Outcome())
		})
	}
}

// A result that arrived with nothing at all is errored, not passed.
//
// The evaluator was asked and answered with no status, no label and no verdict.
// Falling through to "passed" would have counted a missing answer as a good
// one, which is the one reading the numbers must never allow.
func TestAResultCarryingNothingIsErrored(t *testing.T) {
	assert.Equal(t, ResultErrored, OutputResult{Name: "relevance"}.Outcome())
	assert.Nil(t, OutputResult{Name: "relevance"}.SampleError(),
		"and it has no recorded failure to explain it, which is itself the finding")
}
