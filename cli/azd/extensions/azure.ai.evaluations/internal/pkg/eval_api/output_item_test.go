// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --failed-only is where someone looks to find out what went wrong, so a row
// that errored badly enough to carry no verdict at all has to appear there. It
// used to answer false and be hidden.
func TestARowWithNoVerdictCountsAsFailed(t *testing.T) {
	assert.True(t, OutputItem{ID: "item_1", Status: "errored"}.Failed(),
		"nothing graded this row, so nothing passed it")
	assert.True(t, OutputItem{ID: "item_2", Results: []OutputResult{}}.Failed(),
		"an empty result set is the same absence")
	assert.True(t,
		OutputItem{ID: "item_3", Results: []OutputResult{{Name: "relevance"}}}.Failed(),
		"a result the evaluator never judged is that absence one level down")
}

// The ordinary cases have to keep answering as they did.
func TestFailedReadsEveryVerdict(t *testing.T) {
	passing := OutputItem{Results: []OutputResult{
		{Name: "relevance", Passed: new(true)},
		{Name: "coherence", Passed: new(true)},
	}}
	assert.False(t, passing.Failed(), "every evaluator passed it")

	mixed := OutputItem{Results: []OutputResult{
		{Name: "relevance", Passed: new(true)},
		{Name: "coherence", Passed: new(false)},
	}}
	assert.True(t, mixed.Failed(), "one failing evaluator is enough")
}
